// Package project provides pure projection functions that transform event
// histories into dashboard-friendly data structures.
//
// All functions in this package are pure: they take []event.Event as input
// and return derived structures. They do not perform I/O or have side effects.
//
// This package exists to support dashboard UIs without polluting Skene's core
// EventStore interface. Following Rob Pike's principle: "The bigger the
// interface, the weaker the abstraction."
package project

import (
	"encoding/json"
	"time"

	"github.com/lirancohen/skene/event"
)

// Status represents the current state of a workflow run.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusWaiting   Status = "waiting"
)

// RunStatusResult contains the projected status of a workflow run.
type RunStatusResult struct {
	WorkflowName     string
	Version          string
	Status           Status
	StartedAt        *time.Time
	CompletedAt      *time.Time
	DurationMs       *int64
	Error            *string
	WaitingForSignal *string
}

// RunStatus projects the current status from a workflow's event history.
// Returns pending if no events, running if started but not completed,
// and the appropriate terminal status for completed/failed/cancelled.
func RunStatus(events []event.Event) RunStatusResult {
	if len(events) == 0 {
		return RunStatusResult{Status: StatusPending}
	}

	result := RunStatusResult{
		Status: StatusRunning,
	}

	var waitingSignal string

	for _, e := range events {
		switch e.Type {
		case event.EventWorkflowStarted:
			var data event.WorkflowStartedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				result.WorkflowName = data.WorkflowName
				result.Version = data.Version
			}
			ts := e.Timestamp
			result.StartedAt = &ts

		case event.EventWorkflowCompleted:
			result.Status = StatusCompleted
			ts := e.Timestamp
			result.CompletedAt = &ts
			result.DurationMs = calcDuration(result.StartedAt, &ts)

		case event.EventWorkflowFailed:
			result.Status = StatusFailed
			ts := e.Timestamp
			result.CompletedAt = &ts
			result.DurationMs = calcDuration(result.StartedAt, &ts)
			var data event.WorkflowFailedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				result.Error = &data.Error
			}

		case event.EventWorkflowCancelled:
			result.Status = StatusCancelled
			ts := e.Timestamp
			result.CompletedAt = &ts
			result.DurationMs = calcDuration(result.StartedAt, &ts)
			var data event.WorkflowCancelledData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				result.Error = &data.Reason
			}

		case event.EventSignalWaiting:
			var data event.SignalWaitingData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				waitingSignal = data.SignalName
			}

		case event.EventSignalReceived, event.EventSignalTimeout:
			waitingSignal = ""
		}
	}

	// If we're still waiting for a signal at the end, mark as waiting
	if waitingSignal != "" && result.Status == StatusRunning {
		result.Status = StatusWaiting
		result.WaitingForSignal = &waitingSignal
	}

	return result
}

func calcDuration(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	ms := end.Sub(*start).Milliseconds()
	return &ms
}

// InvocationStatus represents the outcome of a step invocation.
type InvocationStatus string

const (
	InvocationStarted   InvocationStatus = "started"
	InvocationCompleted InvocationStatus = "completed"
	InvocationFailed    InvocationStatus = "failed"
	InvocationRetrying  InvocationStatus = "retrying"
)

// StepInvocation represents a single execution attempt of a workflow step.
type StepInvocation struct {
	StepName    string
	Attempt     int
	Status      InvocationStatus
	StartedAt   time.Time
	CompletedAt *time.Time
	DurationMs  *int64
	Error       *string
}

// pendingInvocation tracks an in-progress step for correlation.
type pendingInvocation struct {
	stepName  string
	attempt   int
	startedAt time.Time
	index     int // position in result slice
}

// StepInvocations projects step execution history from workflow events.
// Each invocation represents one attempt of a step, with timing and outcome.
// Invocations are returned in chronological order (by start time).
func StepInvocations(events []event.Event) []StepInvocation {
	// Map key: "stepName:attempt" for correlating started/completed/failed
	pending := make(map[string]*pendingInvocation)
	var result []StepInvocation

	for _, e := range events {
		switch e.Type {
		case event.EventStepStarted:
			var data event.StepStartedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				data.Attempt = 1 // default to 1 if not specified
			}
			if data.Attempt == 0 {
				data.Attempt = 1
			}

			inv := StepInvocation{
				StepName:  e.StepName,
				Attempt:   data.Attempt,
				Status:    InvocationStarted,
				StartedAt: e.Timestamp,
			}
			result = append(result, inv)

			key := stepAttemptKey(e.StepName, data.Attempt)
			pending[key] = &pendingInvocation{
				stepName:  e.StepName,
				attempt:   data.Attempt,
				startedAt: e.Timestamp,
				index:     len(result) - 1,
			}

		case event.EventStepCompleted:
			var data event.StepCompletedData
			_ = json.Unmarshal(e.Data, &data)

			// Find the pending invocation - try current attempt first, then look for any
			attempt := findPendingAttempt(pending, e.StepName)
			if attempt == 0 {
				continue // No matching started event
			}

			key := stepAttemptKey(e.StepName, attempt)
			if p, ok := pending[key]; ok {
				ts := e.Timestamp
				result[p.index].Status = InvocationCompleted
				result[p.index].CompletedAt = &ts
				result[p.index].DurationMs = calcDuration(&p.startedAt, &ts)
				delete(pending, key)
			}

		case event.EventStepFailed:
			var data event.StepFailedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}

			key := stepAttemptKey(e.StepName, data.Attempt)
			if p, ok := pending[key]; ok {
				ts := e.Timestamp
				if data.WillRetry {
					result[p.index].Status = InvocationRetrying
				} else {
					result[p.index].Status = InvocationFailed
				}
				result[p.index].CompletedAt = &ts
				result[p.index].DurationMs = calcDuration(&p.startedAt, &ts)
				result[p.index].Error = &data.Error
				delete(pending, key)
			}
		}
	}

	return result
}

func stepAttemptKey(stepName string, attempt int) string {
	return stepName + ":" + string(rune('0'+attempt))
}

func findPendingAttempt(pending map[string]*pendingInvocation, stepName string) int {
	// Look for any pending invocation for this step
	for _, p := range pending {
		if p.stepName == stepName {
			return p.attempt
		}
	}
	return 0
}

// StepCountResult contains aggregate counts for a single step across all attempts.
type StepCountResult struct {
	StepName  string
	Total     int // Total invocations (attempts)
	Completed int // Successfully completed
	Failed    int // Failed (final, no retry)
	Running   int // Currently running
}

// StepCounts projects step execution counts from workflow events.
// Returns a map keyed by step name with aggregate counts for each step.
func StepCounts(events []event.Event) map[string]StepCountResult {
	result := make(map[string]StepCountResult)
	// Track running attempts: stepName:attempt -> true
	running := make(map[string]bool)

	for _, e := range events {
		switch e.Type {
		case event.EventStepStarted:
			var data event.StepStartedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				data.Attempt = 1
			}
			if data.Attempt == 0 {
				data.Attempt = 1
			}

			counts := result[e.StepName]
			counts.StepName = e.StepName
			counts.Total++
			counts.Running++
			result[e.StepName] = counts

			key := stepAttemptKey(e.StepName, data.Attempt)
			running[key] = true

		case event.EventStepCompleted:
			// Find the pending attempt for this step
			attempt := findRunningAttempt(running, e.StepName)
			if attempt == 0 {
				continue
			}

			key := stepAttemptKey(e.StepName, attempt)
			if running[key] {
				counts := result[e.StepName]
				counts.Running--
				counts.Completed++
				result[e.StepName] = counts
				delete(running, key)
			}

		case event.EventStepFailed:
			var data event.StepFailedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}

			key := stepAttemptKey(e.StepName, data.Attempt)
			if running[key] {
				counts := result[e.StepName]
				counts.Running--
				counts.Failed++
				result[e.StepName] = counts
				delete(running, key)
			}
		}
	}

	return result
}

func findRunningAttempt(running map[string]bool, stepName string) int {
	// Look for any running attempt for this step
	for key := range running {
		// Parse the key to extract stepName
		// Key format is "stepName:N" where N is a single digit
		if len(key) > len(stepName)+1 && key[:len(stepName)] == stepName && key[len(stepName)] == ':' {
			// Extract the attempt number (single digit)
			return int(key[len(stepName)+1] - '0')
		}
	}
	return 0
}

// ChildStatus represents the current state of a child workflow.
type ChildStatus string

const (
	ChildStatusRunning   ChildStatus = "running"
	ChildStatusCompleted ChildStatus = "completed"
	ChildStatusFailed    ChildStatus = "failed"
)

// ChildWorkflowResult represents a spawned child workflow and its outcome.
type ChildWorkflowResult struct {
	ChildRunID   string
	WorkflowName string
	Status       ChildStatus
	StartedAt    time.Time
	CompletedAt  *time.Time
	DurationMs   *int64
	Error        *string
}

// TimelineEventType categorizes events in the timeline.
type TimelineEventType string

const (
	TimelineWorkflowStarted   TimelineEventType = "workflow_started"
	TimelineWorkflowCompleted TimelineEventType = "workflow_completed"
	TimelineWorkflowFailed    TimelineEventType = "workflow_failed"
	TimelineWorkflowCancelled TimelineEventType = "workflow_cancelled"
	TimelineStepStarted       TimelineEventType = "step_started"
	TimelineStepCompleted     TimelineEventType = "step_completed"
	TimelineStepFailed        TimelineEventType = "step_failed"
	TimelineSignalWaiting     TimelineEventType = "signal_waiting"
	TimelineSignalReceived    TimelineEventType = "signal_received"
	TimelineSignalTimeout     TimelineEventType = "signal_timeout"
	TimelineChildSpawned      TimelineEventType = "child_spawned"
	TimelineChildCompleted    TimelineEventType = "child_completed"
	TimelineChildFailed       TimelineEventType = "child_failed"
)

// TimelineEntry represents a single event in the workflow timeline.
// Used by dashboards to render a chronological view of workflow execution.
type TimelineEntry struct {
	Timestamp time.Time
	Type      TimelineEventType
	StepName  string  // For step events
	Attempt   int     // For step events with retries
	Message   string  // Human-readable description
	Error     *string // Error message for failure events
	Metadata  map[string]string
}

// Timeline projects a chronological sequence of significant events for dashboard display.
// Returns entries in timestamp order (oldest first).
func Timeline(events []event.Event) []TimelineEntry {
	var result []TimelineEntry

	for _, e := range events {
		var entry *TimelineEntry

		switch e.Type {
		case event.EventWorkflowStarted:
			var data event.WorkflowStartedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineWorkflowStarted,
				Message:   "Workflow " + data.WorkflowName + " started",
			}

		case event.EventWorkflowCompleted:
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineWorkflowCompleted,
				Message:   "Workflow completed",
			}

		case event.EventWorkflowFailed:
			var data event.WorkflowFailedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineWorkflowFailed,
				Message:   "Workflow failed",
				Error:     &data.Error,
			}

		case event.EventWorkflowCancelled:
			var data event.WorkflowCancelledData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineWorkflowCancelled,
				Message:   "Workflow cancelled: " + data.Reason,
			}

		case event.EventStepStarted:
			var data event.StepStartedData
			_ = json.Unmarshal(e.Data, &data)
			attempt := data.Attempt
			if attempt == 0 {
				attempt = 1
			}
			msg := "Step " + e.StepName + " started"
			if attempt > 1 {
				msg = "Step " + e.StepName + " started (attempt " + itoa(attempt) + ")"
			}
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineStepStarted,
				StepName:  e.StepName,
				Attempt:   attempt,
				Message:   msg,
			}

		case event.EventStepCompleted:
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineStepCompleted,
				StepName:  e.StepName,
				Message:   "Step " + e.StepName + " completed",
			}

		case event.EventStepFailed:
			var data event.StepFailedData
			_ = json.Unmarshal(e.Data, &data)
			msg := "Step " + e.StepName + " failed"
			if data.WillRetry {
				msg += " (will retry)"
			}
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineStepFailed,
				StepName:  e.StepName,
				Attempt:   data.Attempt,
				Message:   msg,
				Error:     &data.Error,
			}

		case event.EventSignalWaiting:
			var data event.SignalWaitingData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineSignalWaiting,
				StepName:  e.StepName,
				Message:   "Waiting for signal: " + data.SignalName,
			}

		case event.EventSignalReceived:
			var data event.SignalReceivedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineSignalReceived,
				StepName:  e.StepName,
				Message:   "Signal received: " + data.SignalName,
			}

		case event.EventSignalTimeout:
			var data event.SignalTimeoutData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineSignalTimeout,
				StepName:  e.StepName,
				Message:   "Signal timeout: " + data.SignalName,
			}

		case event.EventChildSpawned:
			var data event.ChildSpawnedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineChildSpawned,
				StepName:  e.StepName,
				Message:   "Child workflow " + data.WorkflowName + " spawned",
				Metadata:  map[string]string{"child_run_id": data.ChildRunID},
			}

		case event.EventChildCompleted:
			var data event.ChildCompletedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineChildCompleted,
				StepName:  e.StepName,
				Message:   "Child workflow completed",
				Metadata:  map[string]string{"child_run_id": data.ChildRunID},
			}

		case event.EventChildFailed:
			var data event.ChildFailedData
			_ = json.Unmarshal(e.Data, &data)
			entry = &TimelineEntry{
				Timestamp: e.Timestamp,
				Type:      TimelineChildFailed,
				StepName:  e.StepName,
				Message:   "Child workflow failed",
				Error:     &data.Error,
				Metadata:  map[string]string{"child_run_id": data.ChildRunID},
			}
		}

		if entry != nil {
			result = append(result, *entry)
		}
	}

	return result
}

// itoa converts an int to a string (simple, avoids strconv import).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// ChildWorkflows projects child workflow relationships from event history.
// Returns a slice of child workflows spawned by this workflow run.
func ChildWorkflows(events []event.Event) []ChildWorkflowResult {
	// Track pending children by ChildRunID
	pending := make(map[string]int) // ChildRunID -> index in result
	var result []ChildWorkflowResult

	for _, e := range events {
		switch e.Type {
		case event.EventChildSpawned:
			var data event.ChildSpawnedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}

			child := ChildWorkflowResult{
				ChildRunID:   data.ChildRunID,
				WorkflowName: data.WorkflowName,
				Status:       ChildStatusRunning,
				StartedAt:    e.Timestamp,
			}
			result = append(result, child)
			pending[data.ChildRunID] = len(result) - 1

		case event.EventChildCompleted:
			var data event.ChildCompletedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}

			if idx, ok := pending[data.ChildRunID]; ok {
				ts := e.Timestamp
				result[idx].Status = ChildStatusCompleted
				result[idx].CompletedAt = &ts
				result[idx].DurationMs = calcDuration(&result[idx].StartedAt, &ts)
				delete(pending, data.ChildRunID)
			}

		case event.EventChildFailed:
			var data event.ChildFailedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				continue
			}

			if idx, ok := pending[data.ChildRunID]; ok {
				ts := e.Timestamp
				result[idx].Status = ChildStatusFailed
				result[idx].CompletedAt = &ts
				result[idx].DurationMs = calcDuration(&result[idx].StartedAt, &ts)
				result[idx].Error = &data.Error
				delete(pending, data.ChildRunID)
			}
		}
	}

	return result
}
