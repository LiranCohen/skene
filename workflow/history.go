// Package workflow provides the core workflow types and execution engine for Skene.
package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lirancohen/skene/event"
)

// History provides efficient queries over workflow events.
// It indexes events by type and step name for fast lookup of outputs,
// branch decisions, and signal states.
type History struct {
	runID         string
	events        []event.Event
	workflowInput json.RawMessage
	outputs       map[string]json.RawMessage // stepName -> output
	fatalErrors   map[string]error           // stepName -> error (will_retry=false)
	branches      map[string]string          // branchName -> choice
	signals       map[string]*SignalState    // signalName -> state
	cancelled     bool
	completed     bool
	lastSequence  int64

	// Map operation indexes for O(1) lookup
	mapResults map[int64]json.RawMessage // mapIndex -> results JSON
	mapErrors  map[int64]*MapError       // mapIndex -> error info

	// Child operation indexes for O(1) lookup
	childOutputs map[string]json.RawMessage // childRunID -> output
	childErrors  map[string]string          // childRunID -> error message
}

// MapError holds information about a failed map operation.
type MapError struct {
	FailedIndex int
	Error       string
}

// SignalState tracks the status of a signal wait point.
type SignalState struct {
	Waiting   bool            // signal.waiting recorded
	Received  bool            // signal.received recorded
	Timeout   bool            // signal.timeout recorded
	Payload   json.RawMessage // from signal.received
	TimeoutAt time.Time       // from signal.waiting
}

// NewHistory creates a History from events.
// It indexes events by type and step name for efficient queries.
func NewHistory(runID string, events []event.Event) *History {
	h := &History{
		runID:        runID,
		events:       events,
		outputs:      make(map[string]json.RawMessage),
		fatalErrors:  make(map[string]error),
		branches:     make(map[string]string),
		signals:      make(map[string]*SignalState),
		mapResults:   make(map[int64]json.RawMessage),
		mapErrors:    make(map[int64]*MapError),
		childOutputs: make(map[string]json.RawMessage),
		childErrors:  make(map[string]string),
	}
	h.index()
	return h
}

// index processes all events and builds lookup indexes.
func (h *History) index() {
	for _, e := range h.events {
		if e.Sequence > h.lastSequence {
			h.lastSequence = e.Sequence
		}

		switch e.Type {
		case event.EventWorkflowStarted:
			var data event.WorkflowStartedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.workflowInput = data.Input
			}

		case event.EventWorkflowCompleted:
			h.completed = true

		case event.EventWorkflowCancelled:
			h.cancelled = true

		case event.EventStepCompleted:
			if e.StepName != "" && e.Output != nil {
				h.outputs[e.StepName] = e.Output
			}

		case event.EventStepFailed:
			if e.StepName != "" {
				var data event.StepFailedData
				if err := json.Unmarshal(e.Data, &data); err == nil {
					if !data.WillRetry {
						h.fatalErrors[e.StepName] = errors.New(data.Error)
					}
				}
			}

		case event.EventBranchEvaluated:
			var data event.BranchEvaluatedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.branches[data.BranchName] = data.Choice
			}

		case event.EventSignalWaiting:
			var data event.SignalWaitingData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				state := h.getOrCreateSignalState(data.SignalName)
				state.Waiting = true
				state.TimeoutAt = data.TimeoutAt
			}

		case event.EventSignalReceived:
			var data event.SignalReceivedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				state := h.getOrCreateSignalState(data.SignalName)
				state.Received = true
				state.Payload = data.Payload
			}

		case event.EventSignalTimeout:
			var data event.SignalTimeoutData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				state := h.getOrCreateSignalState(data.SignalName)
				state.Timeout = true
			}

		case event.EventMapCompleted:
			var data event.MapCompletedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.mapResults[data.MapIndex] = data.Results
			}

		case event.EventMapFailed:
			var data event.MapFailedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.mapErrors[data.MapIndex] = &MapError{
					FailedIndex: data.FailedIndex,
					Error:       data.Error,
				}
			}

		case event.EventChildCompleted:
			var data event.ChildCompletedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.childOutputs[data.ChildRunID] = data.Output
			}

		case event.EventChildFailed:
			var data event.ChildFailedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				h.childErrors[data.ChildRunID] = data.Error
			}
		}
	}
}

// getOrCreateSignalState returns the signal state for a signal name,
// creating it if it doesn't exist.
func (h *History) getOrCreateSignalState(signalName string) *SignalState {
	state, ok := h.signals[signalName]
	if !ok {
		state = &SignalState{}
		h.signals[signalName] = state
	}
	return state
}

// RunID returns the workflow run ID.
func (h *History) RunID() string {
	return h.runID
}

// Events returns the raw events slice.
func (h *History) Events() []event.Event {
	return h.events
}

// HasCompleted returns true if the step has a step.completed event.
func (h *History) HasCompleted(stepName string) bool {
	_, ok := h.outputs[stepName]
	return ok
}

// GetOutput returns the output for a completed step.
// Returns nil, false if the step hasn't completed.
func (h *History) GetOutput(stepName string) (json.RawMessage, bool) {
	output, ok := h.outputs[stepName]
	return output, ok
}

// GetTypedOutput deserializes the output for a step to the given type.
// Returns an error if the step hasn't completed or deserialization fails.
func GetTypedOutput[T any](h *History, stepName string) (T, error) {
	var zero T
	output, ok := h.outputs[stepName]
	if !ok {
		return zero, fmt.Errorf("step %q has not completed", stepName)
	}
	var result T
	if err := json.Unmarshal(output, &result); err != nil {
		return zero, fmt.Errorf("unmarshal output for step %q: %w", stepName, err)
	}
	return result, nil
}

// GetBranchChoice returns the recorded branch decision.
// Returns "", false if the branch hasn't been evaluated.
func (h *History) GetBranchChoice(branchName string) (string, bool) {
	choice, ok := h.branches[branchName]
	return choice, ok
}

// GetFatalError returns the error if a step failed without retry.
// Returns nil if the step succeeded, hasn't run, or will be retried.
func (h *History) GetFatalError(stepName string) error {
	return h.fatalErrors[stepName]
}

// IsCancelled returns true if the workflow was cancelled.
func (h *History) IsCancelled() bool {
	return h.cancelled
}

// IsCompleted returns true if the workflow completed successfully.
func (h *History) IsCompleted() bool {
	return h.completed
}

// GetWorkflowInput returns the workflow input from the workflow.started event.
// Returns nil if no workflow.started event exists.
func (h *History) GetWorkflowInput() json.RawMessage {
	return h.workflowInput
}

// LastSequence returns the highest sequence number in the history.
// Returns 0 if there are no events.
func (h *History) LastSequence() int64 {
	return h.lastSequence
}

// GetSignalState returns the state of a signal.
// Returns nil if the signal hasn't been used.
func (h *History) GetSignalState(signalName string) *SignalState {
	return h.signals[signalName]
}

// GetMapResults returns the results for a completed map operation.
// Returns nil, false if the map hasn't completed.
func (h *History) GetMapResults(mapIndex int64) (json.RawMessage, bool) {
	results, ok := h.mapResults[mapIndex]
	return results, ok
}

// GetMapError returns the error info for a failed map operation.
// Returns nil if the map didn't fail.
func (h *History) GetMapError(mapIndex int64) *MapError {
	return h.mapErrors[mapIndex]
}

// GetChildOutput returns the output for a completed child workflow.
// Returns nil, false if the child hasn't completed.
func (h *History) GetChildOutput(childRunID string) (json.RawMessage, bool) {
	output, ok := h.childOutputs[childRunID]
	return output, ok
}

// GetChildError returns the error message for a failed child workflow.
// Returns "", false if the child didn't fail.
func (h *History) GetChildError(childRunID string) (string, bool) {
	errMsg, ok := h.childErrors[childRunID]
	return errMsg, ok
}
