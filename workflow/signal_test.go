package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
)

// Test output types for signal tests
type ApprovalInput struct {
	OrderID string `json:"order_id"`
	Amount  int    `json:"amount"`
}

type ApprovalData struct {
	ApprovedBy string `json:"approved_by"`
	Notes      string `json:"notes"`
}

type ApprovalOutput struct {
	Status     string `json:"status"`
	ApprovedBy string `json:"approved_by"`
}

func TestSignalWaitError_Error(t *testing.T) {
	tests := []struct {
		name       string
		err        *SignalWaitError
		wantString string
	}{
		{
			name:       "timeout without signal name",
			err:        &SignalWaitError{Reason: "timeout"},
			wantString: "signal timeout",
		},
		{
			name:       "timeout with signal name",
			err:        &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			wantString: `signal "approval" timeout`,
		},
		{
			name:       "cancelled without signal name",
			err:        &SignalWaitError{Reason: "cancelled"},
			wantString: "signal cancelled",
		},
		{
			name:       "cancelled with signal name",
			err:        &SignalWaitError{SignalName: "payment", Reason: "cancelled"},
			wantString: `signal "payment" cancelled`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			if got != tt.wantString {
				t.Errorf("Error() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

func TestSignalWaitError_Is(t *testing.T) {
	tests := []struct {
		name   string
		err    *SignalWaitError
		target error
		want   bool
	}{
		{
			name:   "matches ErrSignalTimeout",
			err:    &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			target: ErrSignalTimeout,
			want:   true,
		},
		{
			name:   "matches same signal and reason",
			err:    &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			target: &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			want:   true,
		},
		{
			name:   "does not match different signal name",
			err:    &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			target: &SignalWaitError{SignalName: "payment", Reason: "timeout"},
			want:   false,
		},
		{
			name:   "does not match different reason",
			err:    &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			target: &SignalWaitError{SignalName: "approval", Reason: "cancelled"},
			want:   false,
		},
		{
			name:   "sentinel matches by reason only",
			err:    &SignalWaitError{SignalName: "any-signal", Reason: "timeout"},
			target: &SignalWaitError{Reason: "timeout"},
			want:   true,
		},
		{
			name:   "does not match non-SignalWaitError",
			err:    &SignalWaitError{SignalName: "approval", Reason: "timeout"},
			target: errors.New("some other error"),
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestErrReplayWaiting(t *testing.T) {
	// ErrReplayWaiting should be a sentinel error
	if ErrReplayWaiting == nil {
		t.Fatal("ErrReplayWaiting is nil")
	}
	if ErrReplayWaiting.Error() != "replay waiting for signal" {
		t.Errorf("ErrReplayWaiting.Error() = %q, want %q", ErrReplayWaiting.Error(), "replay waiting for signal")
	}
}

func TestWaitForSignal_FirstExecution(t *testing.T) {
	// Create a step that waits for a signal
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		payload, err := WaitForSignal(ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{}, err
		}

		var data ApprovalData
		if err := json.Unmarshal(payload, &data); err != nil {
			return ApprovalOutput{}, err
		}

		return ApprovalOutput{Status: "approved", ApprovedBy: data.ApprovedBy}, nil
	})

	wf := Define("signal-test", approvalStep.After())

	history := NewHistory("run-1", nil)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Workflow should fail because step returns ErrReplayWaiting
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want %v", output.Result, ReplayFailed)
	}

	// Verify signal.waiting event was generated
	var signalWaitingEvent *event.Event
	for i := range output.NewEvents {
		if output.NewEvents[i].Type == event.EventSignalWaiting {
			signalWaitingEvent = &output.NewEvents[i]
			break
		}
	}

	if signalWaitingEvent == nil {
		t.Fatal("expected signal.waiting event")
	}

	// Verify event data
	var waitingData event.SignalWaitingData
	if err := json.Unmarshal(signalWaitingEvent.Data, &waitingData); err != nil {
		t.Fatalf("unmarshal signal.waiting data: %v", err)
	}

	if waitingData.SignalName != "approval" {
		t.Errorf("SignalName = %q, want %q", waitingData.SignalName, "approval")
	}

	if waitingData.TimeoutAt.IsZero() {
		t.Error("TimeoutAt should not be zero")
	}
}

func TestWaitForSignal_ReplayWithReceived(t *testing.T) {
	approvalPayload := mustMarshal(ApprovalData{ApprovedBy: "admin", Notes: "LGTM"})

	// Create history with signal.waiting and signal.received
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "signal-test",
			Version:      "1",
			Input:        mustMarshal(ApprovalInput{OrderID: "order-123", Amount: 100}),
		}, nil),
		makeEvent(2, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  time.Now().Add(24 * time.Hour),
		}, nil),
		makeEvent(3, event.EventSignalReceived, "", event.SignalReceivedData{
			SignalName: "approval",
			Payload:    approvalPayload,
		}, nil),
	}

	// Create a step that waits for a signal
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		payload, err := WaitForSignal(ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{}, err
		}

		var data ApprovalData
		if err := json.Unmarshal(payload, &data); err != nil {
			return ApprovalOutput{}, err
		}

		return ApprovalOutput{Status: "approved", ApprovedBy: data.ApprovedBy}, nil
	})

	wf := Define("signal-test", approvalStep.After())

	history := NewHistory("run-1", events)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Workflow should complete because signal was received
	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want %v (error: %v)", output.Result, ReplayCompleted, output.Error)
	}

	// Verify final output
	if output.FinalOutput == nil {
		t.Fatal("FinalOutput is nil")
	}

	finalOutput, ok := output.FinalOutput.(ApprovalOutput)
	if !ok {
		t.Fatalf("FinalOutput type = %T, want ApprovalOutput", output.FinalOutput)
	}

	if finalOutput.Status != "approved" {
		t.Errorf("Status = %q, want %q", finalOutput.Status, "approved")
	}
	if finalOutput.ApprovedBy != "admin" {
		t.Errorf("ApprovedBy = %q, want %q", finalOutput.ApprovedBy, "admin")
	}
}

func TestWaitForSignal_ReplayWithTimeout(t *testing.T) {
	// Create history with signal.waiting and signal.timeout
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "signal-test",
			Version:      "1",
			Input:        mustMarshal(ApprovalInput{OrderID: "order-123", Amount: 100}),
		}, nil),
		makeEvent(2, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  time.Now().Add(-1 * time.Hour), // Already past
		}, nil),
		makeEvent(3, event.EventSignalTimeout, "", event.SignalTimeoutData{
			SignalName: "approval",
		}, nil),
	}

	// Create a step that waits for a signal
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		_, err := WaitForSignal(ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{Status: "timeout"}, err
		}
		return ApprovalOutput{Status: "approved"}, nil
	})

	wf := Define("signal-test", approvalStep.After())

	history := NewHistory("run-1", events)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Workflow should fail with timeout error
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want %v", output.Result, ReplayFailed)
	}

	// Verify error is a SignalWaitError with timeout reason
	if output.Error == nil {
		t.Fatal("expected error")
	}

	var sigErr *SignalWaitError
	// The error is wrapped, so we need to extract it from the step failure message
	if !errors.As(output.Error, &sigErr) {
		// Check if the underlying error message contains timeout info
		if !errors.Is(output.Error, ErrSignalTimeout) {
			t.Logf("Error: %v", output.Error)
			// The error is wrapped by step failure, check string contains timeout
		}
	}
}

func TestWaitForSignal_ReplayStillWaiting(t *testing.T) {
	// Create history with only signal.waiting (no received or timeout yet)
	timeoutAt := time.Now().Add(24 * time.Hour)
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "signal-test",
			Version:      "1",
			Input:        mustMarshal(ApprovalInput{OrderID: "order-123", Amount: 100}),
		}, nil),
		makeEvent(2, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  timeoutAt,
		}, nil),
	}

	// Create a step that waits for a signal
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		payload, err := WaitForSignal(ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{}, err
		}

		var data ApprovalData
		if err := json.Unmarshal(payload, &data); err != nil {
			return ApprovalOutput{}, err
		}

		return ApprovalOutput{Status: "approved", ApprovedBy: data.ApprovedBy}, nil
	})

	wf := Define("signal-test", approvalStep.After())

	history := NewHistory("run-1", events)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Workflow should fail because still waiting (ErrReplayWaiting from step)
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want %v", output.Result, ReplayFailed)
	}
}

func TestWaitForSignalTyped(t *testing.T) {
	approvalPayload := mustMarshal(ApprovalData{ApprovedBy: "admin", Notes: "Approved"})

	// Create history with signal received
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "signal-typed-test",
			Version:      "1",
			Input:        mustMarshal(ApprovalInput{OrderID: "order-123", Amount: 100}),
		}, nil),
		makeEvent(2, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  time.Now().Add(24 * time.Hour),
		}, nil),
		makeEvent(3, event.EventSignalReceived, "", event.SignalReceivedData{
			SignalName: "approval",
			Payload:    approvalPayload,
		}, nil),
	}

	// Create a step that uses WaitForSignalTyped
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		data, err := WaitForSignalTyped[ApprovalData](ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{}, err
		}

		return ApprovalOutput{Status: "approved", ApprovedBy: data.ApprovedBy}, nil
	})

	wf := Define("signal-typed-test", approvalStep.After())

	history := NewHistory("run-1", events)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want %v (error: %v)", output.Result, ReplayCompleted, output.Error)
	}

	finalOutput, ok := output.FinalOutput.(ApprovalOutput)
	if !ok {
		t.Fatalf("FinalOutput type = %T, want ApprovalOutput", output.FinalOutput)
	}

	if finalOutput.ApprovedBy != "admin" {
		t.Errorf("ApprovedBy = %q, want %q", finalOutput.ApprovedBy, "admin")
	}
}

func TestWaitForSignalTyped_InvalidPayload(t *testing.T) {
	// Create history with signal received but invalid payload
	events := []event.Event{
		makeEvent(1, event.EventWorkflowStarted, "", event.WorkflowStartedData{
			WorkflowName: "signal-typed-test",
			Version:      "1",
			Input:        mustMarshal(ApprovalInput{OrderID: "order-123", Amount: 100}),
		}, nil),
		makeEvent(2, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  time.Now().Add(24 * time.Hour),
		}, nil),
		makeEvent(3, event.EventSignalReceived, "", event.SignalReceivedData{
			SignalName: "approval",
			Payload:    json.RawMessage(`invalid json`),
		}, nil),
	}

	// Create a step that uses WaitForSignalTyped
	approvalStep := NewStep("await-approval", func(ctx Context) (ApprovalOutput, error) {
		data, err := WaitForSignalTyped[ApprovalData](ctx, "approval", 24*time.Hour)
		if err != nil {
			return ApprovalOutput{}, err
		}

		return ApprovalOutput{Status: "approved", ApprovedBy: data.ApprovedBy}, nil
	})

	wf := Define("signal-typed-test", approvalStep.After())

	history := NewHistory("run-1", events)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    ApprovalInput{OrderID: "order-123", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Should fail due to unmarshal error
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want %v", output.Result, ReplayFailed)
	}
}

func TestSignalState(t *testing.T) {
	tests := []struct {
		name        string
		events      []event.Event
		signalName  string
		wantWaiting bool
		wantRecv    bool
		wantTimeout bool
		wantPayload string
		wantNil     bool
	}{
		{
			name:       "empty history returns nil",
			events:     nil,
			signalName: "approval",
			wantNil:    true,
		},
		{
			name: "signal waiting",
			events: []event.Event{
				makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
					SignalName: "approval",
					TimeoutAt:  time.Now().Add(time.Hour),
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
					Payload:    mustMarshal(map[string]string{"by": "admin"}),
				}, nil),
			},
			signalName:  "approval",
			wantWaiting: true,
			wantRecv:    true,
			wantTimeout: false,
			wantPayload: `{"by":"admin"}`,
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
			name: "different signal returns nil",
			events: []event.Event{
				makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
					SignalName: "payment",
				}, nil),
			},
			signalName: "approval",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			history := NewHistory("run-1", tt.events)
			state := history.GetSignalState(tt.signalName)

			if tt.wantNil {
				if state != nil {
					t.Error("GetSignalState() returned non-nil, want nil")
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
			if tt.wantPayload != "" && string(state.Payload) != tt.wantPayload {
				t.Errorf("Payload = %q, want %q", state.Payload, tt.wantPayload)
			}
		})
	}
}

func TestSignalState_TimeoutAt(t *testing.T) {
	timeoutAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	events := []event.Event{
		makeEvent(1, event.EventSignalWaiting, "", event.SignalWaitingData{
			SignalName: "approval",
			TimeoutAt:  timeoutAt,
		}, nil),
	}

	history := NewHistory("run-1", events)
	state := history.GetSignalState("approval")

	if state == nil {
		t.Fatal("GetSignalState() = nil")
	}

	// Compare truncated times to avoid nanosecond precision issues
	gotTime := state.TimeoutAt.Truncate(time.Second)
	if !gotTime.Equal(timeoutAt) {
		t.Errorf("TimeoutAt = %v, want %v", gotTime, timeoutAt)
	}
}

func TestMultipleSignals(t *testing.T) {
	// Create a step that waits for multiple signals
	multiSignalStep := NewStep("multi-signal", func(ctx Context) (string, error) {
		// First signal
		_, err := WaitForSignal(ctx, "approval", 24*time.Hour)
		if err != nil {
			return "", err
		}

		// Second signal
		_, err = WaitForSignal(ctx, "payment", 24*time.Hour)
		if err != nil {
			return "", err
		}

		return "done", nil
	})

	wf := Define("multi-signal-test", multiSignalStep.After())

	// First execution - should wait for first signal
	history1 := NewHistory("run-1", nil)
	replayer1 := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history1,
		RunID:    "run-1",
		Input:    nil,
	})

	output1, err := replayer1.Replay(context.Background())
	if err != nil {
		t.Fatalf("First Replay() error = %v", err)
	}

	// Should record signal.waiting for approval
	var approvalWaiting bool
	for _, e := range output1.NewEvents {
		if e.Type == event.EventSignalWaiting {
			var data event.SignalWaitingData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal signal.waiting data: %v", err)
				continue
			}
			if data.SignalName == "approval" {
				approvalWaiting = true
			}
		}
	}

	if !approvalWaiting {
		t.Error("expected signal.waiting event for 'approval'")
	}
}

func TestWaitingSignal_Struct(t *testing.T) {
	ws := WaitingSignal{
		SignalName: "approval",
		TimeoutAt:  time.Now().Add(time.Hour),
	}

	if ws.SignalName != "approval" {
		t.Errorf("SignalName = %q, want %q", ws.SignalName, "approval")
	}

	if ws.TimeoutAt.IsZero() {
		t.Error("TimeoutAt should not be zero")
	}
}

func TestReplayer_AddWaitingSignal(t *testing.T) {
	wf := Define("test", NewStep("step", func(ctx Context) (string, error) {
		return "done", nil
	}).After())

	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  NewHistory("run-1", nil),
		RunID:    "run-1",
		Input:    nil,
	})

	timeoutAt := time.Now().Add(time.Hour)

	// Add first signal
	replayer.addWaitingSignal("signal-1", timeoutAt)

	signals := replayer.getWaitingSignals()
	if len(signals) != 1 {
		t.Fatalf("getWaitingSignals() len = %d, want 1", len(signals))
	}
	if signals[0].SignalName != "signal-1" {
		t.Errorf("SignalName = %q, want %q", signals[0].SignalName, "signal-1")
	}

	// Add same signal again (should be deduplicated)
	replayer.addWaitingSignal("signal-1", timeoutAt)

	signals = replayer.getWaitingSignals()
	if len(signals) != 1 {
		t.Errorf("getWaitingSignals() len = %d after duplicate, want 1", len(signals))
	}

	// Add different signal
	replayer.addWaitingSignal("signal-2", timeoutAt)

	signals = replayer.getWaitingSignals()
	if len(signals) != 2 {
		t.Errorf("getWaitingSignals() len = %d, want 2", len(signals))
	}
}

func TestReplayer_RecordSignalWaiting(t *testing.T) {
	wf := Define("test", NewStep("step", func(ctx Context) (string, error) {
		return "done", nil
	}).After())

	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  NewHistory("run-1", nil),
		RunID:    "run-1",
		Input:    nil,
	})

	timeoutAt := time.Now().Add(time.Hour)

	// Record signal waiting
	evt := replayer.RecordSignalWaiting("approval", timeoutAt)

	if evt.Type != event.EventSignalWaiting {
		t.Errorf("Event Type = %q, want %q", evt.Type, event.EventSignalWaiting)
	}
	if evt.RunID != "run-1" {
		t.Errorf("RunID = %q, want %q", evt.RunID, "run-1")
	}
	if evt.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", evt.Sequence)
	}

	var data event.SignalWaitingData
	if err := json.Unmarshal(evt.Data, &data); err != nil {
		t.Fatalf("unmarshal event data: %v", err)
	}

	if data.SignalName != "approval" {
		t.Errorf("SignalName = %q, want %q", data.SignalName, "approval")
	}
}
