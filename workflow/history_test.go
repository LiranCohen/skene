package workflow

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
)

// Helper to create events with minimal boilerplate
func makeEvent(seq int64, typ event.EventType, stepName string, data any, output any) event.Event {
	e := event.Event{
		ID:        "evt",
		RunID:     "run-123",
		Sequence:  seq,
		Version:   1,
		Type:      typ,
		StepName:  stepName,
		Timestamp: time.Now(),
	}
	if data != nil {
		e.Data, _ = json.Marshal(data)
	}
	if output != nil {
		e.Output, _ = json.Marshal(output)
	}
	return e
}

func TestNewHistory_EmptyEvents(t *testing.T) {
	h := NewHistory("run-123", nil)

	if h.RunID() != "run-123" {
		t.Errorf("RunID() = %q, want %q", h.RunID(), "run-123")
	}
	if h.LastSequence() != 0 {
		t.Errorf("LastSequence() = %d, want 0", h.LastSequence())
	}
	if h.IsCompleted() {
		t.Error("IsCompleted() = true, want false for empty history")
	}
	if h.IsCancelled() {
		t.Error("IsCancelled() = true, want false for empty history")
	}
	if h.GetWorkflowInput() != nil {
		t.Errorf("GetWorkflowInput() = %v, want nil", h.GetWorkflowInput())
	}
	if h.HasCompleted("any-step") {
		t.Error("HasCompleted() = true for non-existent step")
	}
	if _, ok := h.GetOutput("any-step"); ok {
		t.Error("GetOutput() returned ok=true for non-existent step")
	}
	if h.GetFatalError("any-step") != nil {
		t.Error("GetFatalError() returned error for non-existent step")
	}
	if _, ok := h.GetBranchChoice("any-branch"); ok {
		t.Error("GetBranchChoice() returned ok=true for non-existent branch")
	}
	if h.GetSignalState("any-signal") != nil {
		t.Error("GetSignalState() returned non-nil for non-existent signal")
	}
}

func TestHistory_WorkflowStarted(t *testing.T) {
	input := map[string]string{"order_id": "123"}
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "order",
			Version:      "1",
			Input:        mustMarshal(input),
		}, nil),
	}

	h := NewHistory("run-123", events)

	if h.GetWorkflowInput() == nil {
		t.Fatal("GetWorkflowInput() = nil, want non-nil")
	}

	var gotInput map[string]string
	if err := json.Unmarshal(h.GetWorkflowInput(), &gotInput); err != nil {
		t.Fatalf("Failed to unmarshal input: %v", err)
	}
	if gotInput["order_id"] != "123" {
		t.Errorf("Input order_id = %q, want %q", gotInput["order_id"], "123")
	}
}

func TestHistory_StepCompleted(t *testing.T) {
	tests := []struct {
		name       string
		stepName   string
		output     any
		wantOutput any
	}{
		{
			name:       "simple output",
			stepName:   "validate",
			output:     map[string]bool{"valid": true},
			wantOutput: map[string]bool{"valid": true},
		},
		{
			name:       "nested output",
			stepName:   "charge",
			output:     map[string]any{"amount": 99.99, "currency": "USD"},
			wantOutput: map[string]any{"amount": 99.99, "currency": "USD"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []event.Event{
				makeEvent(1, event.EventStepCompleted, tt.stepName, nil, tt.output),
			}

			h := NewHistory("run-123", events)

			if !h.HasCompleted(tt.stepName) {
				t.Errorf("HasCompleted(%q) = false, want true", tt.stepName)
			}

			output, ok := h.GetOutput(tt.stepName)
			if !ok {
				t.Fatalf("GetOutput(%q) returned ok=false", tt.stepName)
			}
			if output == nil {
				t.Fatal("GetOutput() returned nil output")
			}

			// Verify output content
			var got map[string]any
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("Failed to unmarshal output: %v", err)
			}
		})
	}
}

func TestHistory_StepFailed(t *testing.T) {
	tests := []struct {
		name      string
		stepName  string
		error     string
		willRetry bool
		wantFatal bool
	}{
		{
			name:      "retryable failure is not fatal",
			stepName:  "charge",
			error:     "timeout",
			willRetry: true,
			wantFatal: false,
		},
		{
			name:      "non-retryable failure is fatal",
			stepName:  "validate",
			error:     "invalid order",
			willRetry: false,
			wantFatal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []event.Event{
				makeEvent(1, event.EventStepFailed, tt.stepName, event.StepFailedData{
					Error:     tt.error,
					Attempt:   1,
					WillRetry: tt.willRetry,
				}, nil),
			}

			h := NewHistory("run-123", events)

			err := h.GetFatalError(tt.stepName)
			if tt.wantFatal {
				if err == nil {
					t.Errorf("GetFatalError(%q) = nil, want error", tt.stepName)
				} else if err.Error() != tt.error {
					t.Errorf("GetFatalError() error = %q, want %q", err.Error(), tt.error)
				}
			} else {
				if err != nil {
					t.Errorf("GetFatalError(%q) = %v, want nil (retryable)", tt.stepName, err)
				}
			}
		})
	}
}

func TestHistory_BranchEvaluated(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
		choice     string
	}{
		{
			name:       "approval branch",
			branchName: "approval-check",
			choice:     "approved",
		},
		{
			name:       "fraud branch",
			branchName: "fraud-check",
			choice:     "low-risk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := []event.Event{
				makeEvent(1, event.EventBranchEvaluated, "", event.BranchEvaluatedData{
					BranchName: tt.branchName,
					Choice:     tt.choice,
				}, nil),
			}

			h := NewHistory("run-123", events)

			choice, ok := h.GetBranchChoice(tt.branchName)
			if !ok {
				t.Errorf("GetBranchChoice(%q) returned ok=false", tt.branchName)
			}
			if choice != tt.choice {
				t.Errorf("GetBranchChoice(%q) = %q, want %q", tt.branchName, choice, tt.choice)
			}
		})
	}
}

func TestHistory_SignalEvents(t *testing.T) {
	tests := []struct {
		name        string
		events      []event.Event
		signalName  string
		wantWaiting bool
		wantRecv    bool
		wantTimeout bool
		wantPayload string
	}{
		{
			name: "signal waiting",
			events: []event.Event{
				makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
					SignalName: "approval",
				}, nil),
			},
			signalName:  "approval",
			wantWaiting: true,
			wantRecv:    false,
			wantTimeout: false,
		},
		{
			name: "signal received",
			events: []event.Event{
				makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
					SignalName: "approval",
				}, nil),
				makeEvent(2, event.EventSignalReceived, "", event.SignalReceivedData{
					SignalName: "approval",
					Payload:    mustMarshal(map[string]string{"approved_by": "admin"}),
				}, nil),
			},
			signalName:  "approval",
			wantWaiting: true,
			wantRecv:    true,
			wantTimeout: false,
			wantPayload: `{"approved_by":"admin"}`,
		},
		{
			name: "signal timeout",
			events: []event.Event{
				makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
					SignalName: "approval",
				}, nil),
				makeEvent(2, event.EventSignalTimeout, "", event.SignalTimeoutData{
					SignalName: "approval",
				}, nil),
			},
			signalName:  "approval",
			wantWaiting: true,
			wantRecv:    false,
			wantTimeout: true,
		},
		{
			name:        "unknown signal returns nil",
			events:      []event.Event{},
			signalName:  "unknown",
			wantWaiting: false,
			wantRecv:    false,
			wantTimeout: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHistory("run-123", tt.events)

			state := h.GetSignalState(tt.signalName)

			if len(tt.events) == 0 {
				if state != nil {
					t.Error("GetSignalState() returned non-nil for unknown signal")
				}
				return
			}

			if state == nil {
				t.Fatal("GetSignalState() = nil, want non-nil")
			}

			if state.Waiting != tt.wantWaiting {
				t.Errorf("Waiting = %v, want %v", state.Waiting, tt.wantWaiting)
			}
			if state.Received != tt.wantRecv {
				t.Errorf("Received = %v, want %v", state.Received, tt.wantRecv)
			}
			if state.Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %v, want %v", state.Timeout, tt.wantTimeout)
			}
			if tt.wantPayload != "" {
				if string(state.Payload) != tt.wantPayload {
					t.Errorf("Payload = %q, want %q", state.Payload, tt.wantPayload)
				}
			}
		})
	}
}

func TestHistory_WorkflowCompleted(t *testing.T) {
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "order",
			Version:      "1",
		}, nil),
		makeEvent(2, event.EventStepCompleted, "validate", nil, map[string]bool{"valid": true}),
		makeEvent(3, event.EventWorkflowCompleted, "", event.WorkflowCompletedData{}, nil),
	}

	h := NewHistory("run-123", events)

	if !h.IsCompleted() {
		t.Error("IsCompleted() = false, want true")
	}
	if h.IsCancelled() {
		t.Error("IsCancelled() = true, want false")
	}
}

func TestHistory_WorkflowCancelled(t *testing.T) {
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "order",
			Version:      "1",
		}, nil),
		makeEvent(2, event.EventWorkflowCancelled, "", event.WorkflowCancelledData{
			Reason: "user requested",
		}, nil),
	}

	h := NewHistory("run-123", events)

	if h.IsCompleted() {
		t.Error("IsCompleted() = true, want false")
	}
	if !h.IsCancelled() {
		t.Error("IsCancelled() = false, want true")
	}
}

func TestHistory_LastSequence(t *testing.T) {
	tests := []struct {
		name         string
		events       []event.Event
		wantSequence int64
	}{
		{
			name:         "empty events",
			events:       nil,
			wantSequence: 0,
		},
		{
			name: "single event",
			events: []event.Event{
				makeEvent(5, event.EventWorkflowStarted, "", nil, nil),
			},
			wantSequence: 5,
		},
		{
			name: "multiple events in order",
			events: []event.Event{
				makeEvent(1, event.EventWorkflowStarted, "", nil, nil),
				makeEvent(2, event.EventStepCompleted, "validate", nil, nil),
				makeEvent(3, event.EventStepCompleted, "charge", nil, nil),
			},
			wantSequence: 3,
		},
		{
			name: "events out of order",
			events: []event.Event{
				makeEvent(3, event.EventWorkflowStarted, "", nil, nil),
				makeEvent(1, event.EventStepCompleted, "validate", nil, nil),
				makeEvent(5, event.EventStepCompleted, "charge", nil, nil),
			},
			wantSequence: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHistory("run-123", tt.events)
			if h.LastSequence() != tt.wantSequence {
				t.Errorf("LastSequence() = %d, want %d", h.LastSequence(), tt.wantSequence)
			}
		})
	}
}

func TestGetTypedOutput(t *testing.T) {
	type ValidateOutput struct {
		Valid  bool    `json:"valid"`
		Amount float64 `json:"amount"`
	}

	events := []event.Event{
		makeEvent(1, event.EventStepCompleted, "validate", nil, ValidateOutput{
			Valid:  true,
			Amount: 99.99,
		}),
	}

	h := NewHistory("run-123", events)

	t.Run("successful deserialization", func(t *testing.T) {
		output, err := GetTypedOutput[ValidateOutput](h, "validate")
		if err != nil {
			t.Fatalf("GetTypedOutput() error = %v", err)
		}
		if !output.Valid {
			t.Error("output.Valid = false, want true")
		}
		if output.Amount != 99.99 {
			t.Errorf("output.Amount = %v, want 99.99", output.Amount)
		}
	})

	t.Run("step not completed", func(t *testing.T) {
		_, err := GetTypedOutput[ValidateOutput](h, "unknown-step")
		if err == nil {
			t.Error("GetTypedOutput() for unknown step returned nil error")
		}
	})

	t.Run("type mismatch", func(t *testing.T) {
		type DifferentType struct {
			SomeField int `json:"some_field"`
		}
		// This should succeed since JSON unmarshaling is lenient,
		// but the fields won't match
		output, err := GetTypedOutput[DifferentType](h, "validate")
		if err != nil {
			t.Fatalf("GetTypedOutput() error = %v", err)
		}
		// The field won't be populated (zero value)
		if output.SomeField != 0 {
			t.Errorf("output.SomeField = %d, want 0", output.SomeField)
		}
	})
}

func TestHistory_MultipleSteps(t *testing.T) {
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "order",
			Version:      "1",
			Input:        mustMarshal(map[string]string{"order_id": "123"}),
		}, nil),
		makeEvent(2, event.EventStepCompleted, "validate", nil, map[string]bool{"valid": true}),
		makeEvent(3, event.EventStepCompleted, "fraud", nil, map[string]string{"risk": "low"}),
		makeEvent(4, event.EventStepFailed, "charge", event.StepFailedData{
			Error:     "timeout",
			Attempt:   1,
			WillRetry: true,
		}, nil),
		makeEvent(5, event.EventStepCompleted, "charge", nil, map[string]string{"txn_id": "txn-456"}),
	}

	h := NewHistory("run-123", events)

	// All steps should have completed
	if !h.HasCompleted("validate") {
		t.Error("validate should be completed")
	}
	if !h.HasCompleted("fraud") {
		t.Error("fraud should be completed")
	}
	if !h.HasCompleted("charge") {
		t.Error("charge should be completed (after retry)")
	}

	// Retryable failure should not be fatal
	if h.GetFatalError("charge") != nil {
		t.Error("charge should not have fatal error (retry succeeded)")
	}

	// Verify outputs
	validateOutput, ok := h.GetOutput("validate")
	if !ok {
		t.Fatal("GetOutput(validate) returned ok=false")
	}
	var valid map[string]bool
	if err := json.Unmarshal(validateOutput, &valid); err != nil {
		t.Fatalf("failed to unmarshal validate output: %v", err)
	}
	if !valid["valid"] {
		t.Error("validate output should have valid=true")
	}
}

func TestHistory_Events(t *testing.T) {
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", nil, nil),
		makeEvent(2, event.EventStepCompleted, "validate", nil, nil),
	}

	h := NewHistory("run-123", events)

	got := h.Events()
	if len(got) != 2 {
		t.Errorf("Events() returned %d events, want 2", len(got))
	}
}

// mustMarshal is a test helper that marshals to JSON or panics
func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
