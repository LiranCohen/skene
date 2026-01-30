package project

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
)

func TestRunStatus(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		events []event.Event
		want   RunStatusResult
	}{
		{
			name:   "empty events returns pending",
			events: []event.Event{},
			want: RunStatusResult{
				Status: StatusPending,
			},
		},
		{
			name: "workflow started only returns running",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
			},
			want: RunStatusResult{
				WorkflowName: "test-workflow",
				Version:      "1.0",
				Status:       StatusRunning,
				StartedAt:    &baseTime,
			},
		},
		{
			name: "workflow completed returns completed",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowCompleted,
					Timestamp: baseTime.Add(5 * time.Minute),
				},
			},
			want: RunStatusResult{
				WorkflowName: "test-workflow",
				Version:      "1.0",
				Status:       StatusCompleted,
				StartedAt:    &baseTime,
				CompletedAt:  ptrTime(baseTime.Add(5 * time.Minute)),
				DurationMs:   ptrInt64(300000),
			},
		},
		{
			name: "workflow failed returns failed",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowFailed,
					Timestamp: baseTime.Add(2 * time.Minute),
					Data:      mustJSON(event.WorkflowFailedData{Error: "something went wrong"}),
				},
			},
			want: RunStatusResult{
				WorkflowName: "test-workflow",
				Version:      "1.0",
				Status:       StatusFailed,
				StartedAt:    &baseTime,
				CompletedAt:  ptrTime(baseTime.Add(2 * time.Minute)),
				DurationMs:   ptrInt64(120000),
				Error:        ptrString("something went wrong"),
			},
		},
		{
			name: "workflow cancelled returns cancelled",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowCancelled,
					Timestamp: baseTime.Add(1 * time.Minute),
					Data:      mustJSON(event.WorkflowCancelledData{Reason: "user requested"}),
				},
			},
			want: RunStatusResult{
				WorkflowName: "test-workflow",
				Version:      "1.0",
				Status:       StatusCancelled,
				StartedAt:    &baseTime,
				CompletedAt:  ptrTime(baseTime.Add(1 * time.Minute)),
				DurationMs:   ptrInt64(60000),
				Error:        ptrString("user requested"),
			},
		},
		{
			name: "signal waiting returns waiting",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
				{
					Type:      event.EventSignalWaiting,
					StepName:  "approval",
					Timestamp: baseTime.Add(1 * time.Minute),
					Data:      mustJSON(event.SignalWaitingData{SignalName: "user-approval"}),
				},
			},
			want: RunStatusResult{
				WorkflowName:     "test-workflow",
				Version:          "1.0",
				Status:           StatusWaiting,
				StartedAt:        &baseTime,
				WaitingForSignal: ptrString("user-approval"),
			},
		},
		{
			name: "signal received resumes running",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1.0"}),
				},
				{
					Type:      event.EventSignalWaiting,
					StepName:  "approval",
					Timestamp: baseTime.Add(1 * time.Minute),
					Data:      mustJSON(event.SignalWaitingData{SignalName: "user-approval"}),
				},
				{
					Type:      event.EventSignalReceived,
					StepName:  "approval",
					Timestamp: baseTime.Add(2 * time.Minute),
					Data:      mustJSON(event.SignalReceivedData{SignalName: "user-approval"}),
				},
			},
			want: RunStatusResult{
				WorkflowName: "test-workflow",
				Version:      "1.0",
				Status:       StatusRunning,
				StartedAt:    &baseTime,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RunStatus(tt.events)
			if got.WorkflowName != tt.want.WorkflowName {
				t.Errorf("WorkflowName = %q, want %q", got.WorkflowName, tt.want.WorkflowName)
			}
			if got.Version != tt.want.Version {
				t.Errorf("Version = %q, want %q", got.Version, tt.want.Version)
			}
			if got.Status != tt.want.Status {
				t.Errorf("Status = %q, want %q", got.Status, tt.want.Status)
			}
			if !timeEqual(got.StartedAt, tt.want.StartedAt) {
				t.Errorf("StartedAt = %v, want %v", got.StartedAt, tt.want.StartedAt)
			}
			if !timeEqual(got.CompletedAt, tt.want.CompletedAt) {
				t.Errorf("CompletedAt = %v, want %v", got.CompletedAt, tt.want.CompletedAt)
			}
			if !int64PtrEqual(got.DurationMs, tt.want.DurationMs) {
				t.Errorf("DurationMs = %v, want %v", ptrVal(got.DurationMs), ptrVal(tt.want.DurationMs))
			}
			if !stringPtrEqual(got.Error, tt.want.Error) {
				t.Errorf("Error = %v, want %v", ptrValStr(got.Error), ptrValStr(tt.want.Error))
			}
			if !stringPtrEqual(got.WaitingForSignal, tt.want.WaitingForSignal) {
				t.Errorf("WaitingForSignal = %v, want %v", ptrValStr(got.WaitingForSignal), ptrValStr(tt.want.WaitingForSignal))
			}
		})
	}
}

// Helper functions

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func ptrInt64(i int64) *int64 {
	return &i
}

func ptrString(s string) *string {
	return &s
}

func timeEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func stringPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func ptrVal(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func ptrValStr(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestStepCounts(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		events []event.Event
		want   map[string]StepCountResult
	}{
		{
			name:   "empty events returns empty map",
			events: []event.Event{},
			want:   map[string]StepCountResult{},
		},
		{
			name: "single step started only shows running",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
			},
			want: map[string]StepCountResult{
				"validate": {StepName: "validate", Total: 1, Running: 1},
			},
		},
		{
			name: "single step completed",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
			},
			want: map[string]StepCountResult{
				"validate": {StepName: "validate", Total: 1, Completed: 1},
			},
		},
		{
			name: "step failed without retry",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime.Add(50 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "declined", Attempt: 1, WillRetry: false}),
				},
			},
			want: map[string]StepCountResult{
				"charge": {StepName: "charge", Total: 1, Failed: 1},
			},
		},
		{
			name: "step retry counts both attempts",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime.Add(50 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "timeout", Attempt: 1, WillRetry: true}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime.Add(1 * time.Second),
					Data:      mustJSON(event.StepStartedData{Attempt: 2}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "charge",
					Timestamp: baseTime.Add(1*time.Second + 100*time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
			},
			want: map[string]StepCountResult{
				"charge": {StepName: "charge", Total: 2, Completed: 1, Failed: 1},
			},
		},
		{
			name: "multiple steps in workflow",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime.Add(200 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "charge",
					Timestamp: baseTime.Add(400 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "ship",
					Timestamp: baseTime.Add(500 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
			},
			want: map[string]StepCountResult{
				"validate": {StepName: "validate", Total: 1, Completed: 1},
				"charge":   {StepName: "charge", Total: 1, Completed: 1},
				"ship":     {StepName: "ship", Total: 1, Running: 1},
			},
		},
		{
			name: "multiple failures before success",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "flaky",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "flaky",
					Timestamp: baseTime.Add(10 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "err1", Attempt: 1, WillRetry: true}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "flaky",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 2}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "flaky",
					Timestamp: baseTime.Add(110 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "err2", Attempt: 2, WillRetry: true}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "flaky",
					Timestamp: baseTime.Add(200 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 3}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "flaky",
					Timestamp: baseTime.Add(300 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
			},
			want: map[string]StepCountResult{
				"flaky": {StepName: "flaky", Total: 3, Completed: 1, Failed: 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StepCounts(tt.events)

			if len(got) != len(tt.want) {
				t.Fatalf("StepCounts() returned %d steps, want %d", len(got), len(tt.want))
			}

			for stepName, wantResult := range tt.want {
				gotResult, ok := got[stepName]
				if !ok {
					t.Errorf("step %q not found in result", stepName)
					continue
				}
				if gotResult.StepName != wantResult.StepName {
					t.Errorf("step %q: StepName = %q, want %q", stepName, gotResult.StepName, wantResult.StepName)
				}
				if gotResult.Total != wantResult.Total {
					t.Errorf("step %q: Total = %d, want %d", stepName, gotResult.Total, wantResult.Total)
				}
				if gotResult.Completed != wantResult.Completed {
					t.Errorf("step %q: Completed = %d, want %d", stepName, gotResult.Completed, wantResult.Completed)
				}
				if gotResult.Failed != wantResult.Failed {
					t.Errorf("step %q: Failed = %d, want %d", stepName, gotResult.Failed, wantResult.Failed)
				}
				if gotResult.Running != wantResult.Running {
					t.Errorf("step %q: Running = %d, want %d", stepName, gotResult.Running, wantResult.Running)
				}
			}
		})
	}
}

func TestChildWorkflows(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		events []event.Event
		want   []ChildWorkflowResult
	}{
		{
			name:   "empty events returns empty slice",
			events: []event.Event{},
			want:   nil,
		},
		{
			name: "single child spawned only shows running",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "process-payment"}),
				},
			},
			want: []ChildWorkflowResult{
				{
					ChildRunID:   "child-1",
					WorkflowName: "process-payment",
					Status:       ChildStatusRunning,
					StartedAt:    baseTime,
				},
			},
		},
		{
			name: "child completes successfully",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "process-payment"}),
				},
				{
					Type:      event.EventChildCompleted,
					Timestamp: baseTime.Add(5 * time.Minute),
					Data:      mustJSON(event.ChildCompletedData{ChildRunID: "child-1"}),
				},
			},
			want: []ChildWorkflowResult{
				{
					ChildRunID:   "child-1",
					WorkflowName: "process-payment",
					Status:       ChildStatusCompleted,
					StartedAt:    baseTime,
					CompletedAt:  ptrTime(baseTime.Add(5 * time.Minute)),
					DurationMs:   ptrInt64(300000),
				},
			},
		},
		{
			name: "child fails with error",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "process-payment"}),
				},
				{
					Type:      event.EventChildFailed,
					Timestamp: baseTime.Add(2 * time.Minute),
					Data:      mustJSON(event.ChildFailedData{ChildRunID: "child-1", Error: "payment declined"}),
				},
			},
			want: []ChildWorkflowResult{
				{
					ChildRunID:   "child-1",
					WorkflowName: "process-payment",
					Status:       ChildStatusFailed,
					StartedAt:    baseTime,
					CompletedAt:  ptrTime(baseTime.Add(2 * time.Minute)),
					DurationMs:   ptrInt64(120000),
					Error:        ptrString("payment declined"),
				},
			},
		},
		{
			name: "multiple children with mixed outcomes",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "validate-order"}),
				},
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime.Add(1 * time.Second),
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-2", WorkflowName: "process-payment"}),
				},
				{
					Type:      event.EventChildCompleted,
					Timestamp: baseTime.Add(30 * time.Second),
					Data:      mustJSON(event.ChildCompletedData{ChildRunID: "child-1"}),
				},
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime.Add(31 * time.Second),
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-3", WorkflowName: "ship-order"}),
				},
				{
					Type:      event.EventChildFailed,
					Timestamp: baseTime.Add(1 * time.Minute),
					Data:      mustJSON(event.ChildFailedData{ChildRunID: "child-2", Error: "card declined"}),
				},
			},
			want: []ChildWorkflowResult{
				{
					ChildRunID:   "child-1",
					WorkflowName: "validate-order",
					Status:       ChildStatusCompleted,
					StartedAt:    baseTime,
					CompletedAt:  ptrTime(baseTime.Add(30 * time.Second)),
					DurationMs:   ptrInt64(30000),
				},
				{
					ChildRunID:   "child-2",
					WorkflowName: "process-payment",
					Status:       ChildStatusFailed,
					StartedAt:    baseTime.Add(1 * time.Second),
					CompletedAt:  ptrTime(baseTime.Add(1 * time.Minute)),
					DurationMs:   ptrInt64(59000),
					Error:        ptrString("card declined"),
				},
				{
					ChildRunID:   "child-3",
					WorkflowName: "ship-order",
					Status:       ChildStatusRunning,
					StartedAt:    baseTime.Add(31 * time.Second),
				},
			},
		},
		{
			name: "ignores unmatched completion events",
			events: []event.Event{
				{
					Type:      event.EventChildCompleted,
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildCompletedData{ChildRunID: "unknown-child"}),
				},
				{
					Type:      event.EventChildSpawned,
					Timestamp: baseTime.Add(1 * time.Second),
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "real-workflow"}),
				},
			},
			want: []ChildWorkflowResult{
				{
					ChildRunID:   "child-1",
					WorkflowName: "real-workflow",
					Status:       ChildStatusRunning,
					StartedAt:    baseTime.Add(1 * time.Second),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChildWorkflows(tt.events)

			if len(got) != len(tt.want) {
				t.Fatalf("ChildWorkflows() returned %d children, want %d", len(got), len(tt.want))
			}

			for i := range got {
				if got[i].ChildRunID != tt.want[i].ChildRunID {
					t.Errorf("child[%d].ChildRunID = %q, want %q", i, got[i].ChildRunID, tt.want[i].ChildRunID)
				}
				if got[i].WorkflowName != tt.want[i].WorkflowName {
					t.Errorf("child[%d].WorkflowName = %q, want %q", i, got[i].WorkflowName, tt.want[i].WorkflowName)
				}
				if got[i].Status != tt.want[i].Status {
					t.Errorf("child[%d].Status = %q, want %q", i, got[i].Status, tt.want[i].Status)
				}
				if !got[i].StartedAt.Equal(tt.want[i].StartedAt) {
					t.Errorf("child[%d].StartedAt = %v, want %v", i, got[i].StartedAt, tt.want[i].StartedAt)
				}
				if !timeEqual(got[i].CompletedAt, tt.want[i].CompletedAt) {
					t.Errorf("child[%d].CompletedAt = %v, want %v", i, got[i].CompletedAt, tt.want[i].CompletedAt)
				}
				if !int64PtrEqual(got[i].DurationMs, tt.want[i].DurationMs) {
					t.Errorf("child[%d].DurationMs = %v, want %v", i, ptrVal(got[i].DurationMs), ptrVal(tt.want[i].DurationMs))
				}
				if !stringPtrEqual(got[i].Error, tt.want[i].Error) {
					t.Errorf("child[%d].Error = %v, want %v", i, ptrValStr(got[i].Error), ptrValStr(tt.want[i].Error))
				}
			}
		})
	}
}

func TestTimeline(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		events []event.Event
		want   []TimelineEntry
	}{
		{
			name:   "empty events returns empty slice",
			events: []event.Event{},
			want:   nil,
		},
		{
			name: "workflow started",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "order-process", Version: "1.0"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineWorkflowStarted,
					Message:   "Workflow order-process started",
				},
			},
		},
		{
			name: "workflow completed",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "order-process", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowCompleted,
					Timestamp: baseTime.Add(5 * time.Minute),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineWorkflowStarted,
					Message:   "Workflow order-process started",
				},
				{
					Timestamp: baseTime.Add(5 * time.Minute),
					Type:      TimelineWorkflowCompleted,
					Message:   "Workflow completed",
				},
			},
		},
		{
			name: "workflow failed",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "order-process", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowFailed,
					Timestamp: baseTime.Add(2 * time.Minute),
					Data:      mustJSON(event.WorkflowFailedData{Error: "payment declined"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineWorkflowStarted,
					Message:   "Workflow order-process started",
				},
				{
					Timestamp: baseTime.Add(2 * time.Minute),
					Type:      TimelineWorkflowFailed,
					Message:   "Workflow failed",
					Error:     ptrString("payment declined"),
				},
			},
		},
		{
			name: "workflow cancelled",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "order-process", Version: "1.0"}),
				},
				{
					Type:      event.EventWorkflowCancelled,
					Timestamp: baseTime.Add(1 * time.Minute),
					Data:      mustJSON(event.WorkflowCancelledData{Reason: "user requested"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineWorkflowStarted,
					Message:   "Workflow order-process started",
				},
				{
					Timestamp: baseTime.Add(1 * time.Minute),
					Type:      TimelineWorkflowCancelled,
					Message:   "Workflow cancelled: user requested",
				},
			},
		},
		{
			name: "step lifecycle",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineStepStarted,
					StepName:  "validate",
					Attempt:   1,
					Message:   "Step validate started",
				},
				{
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Type:      TimelineStepCompleted,
					StepName:  "validate",
					Message:   "Step validate completed",
				},
			},
		},
		{
			name: "step with retry shows attempt number",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 2}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineStepStarted,
					StepName:  "charge",
					Attempt:   2,
					Message:   "Step charge started (attempt 2)",
				},
			},
		},
		{
			name: "step failed with retry",
			events: []event.Event{
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepFailedData{Error: "timeout", Attempt: 1, WillRetry: true}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineStepFailed,
					StepName:  "charge",
					Attempt:   1,
					Message:   "Step charge failed (will retry)",
					Error:     ptrString("timeout"),
				},
			},
		},
		{
			name: "step failed without retry",
			events: []event.Event{
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepFailedData{Error: "declined", Attempt: 1, WillRetry: false}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineStepFailed,
					StepName:  "charge",
					Attempt:   1,
					Message:   "Step charge failed",
					Error:     ptrString("declined"),
				},
			},
		},
		{
			name: "signal events",
			events: []event.Event{
				{
					Type:      event.EventSignalWaiting,
					StepName:  "approval",
					Timestamp: baseTime,
					Data:      mustJSON(event.SignalWaitingData{SignalName: "user-approval"}),
				},
				{
					Type:      event.EventSignalReceived,
					StepName:  "approval",
					Timestamp: baseTime.Add(5 * time.Minute),
					Data:      mustJSON(event.SignalReceivedData{SignalName: "user-approval"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineSignalWaiting,
					StepName:  "approval",
					Message:   "Waiting for signal: user-approval",
				},
				{
					Timestamp: baseTime.Add(5 * time.Minute),
					Type:      TimelineSignalReceived,
					StepName:  "approval",
					Message:   "Signal received: user-approval",
				},
			},
		},
		{
			name: "signal timeout",
			events: []event.Event{
				{
					Type:      event.EventSignalTimeout,
					StepName:  "approval",
					Timestamp: baseTime,
					Data:      mustJSON(event.SignalTimeoutData{SignalName: "user-approval"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineSignalTimeout,
					StepName:  "approval",
					Message:   "Signal timeout: user-approval",
				},
			},
		},
		{
			name: "child workflow events",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					StepName:  "process",
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "payment-flow"}),
				},
				{
					Type:      event.EventChildCompleted,
					StepName:  "process",
					Timestamp: baseTime.Add(30 * time.Second),
					Data:      mustJSON(event.ChildCompletedData{ChildRunID: "child-1"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineChildSpawned,
					StepName:  "process",
					Message:   "Child workflow payment-flow spawned",
					Metadata:  map[string]string{"child_run_id": "child-1"},
				},
				{
					Timestamp: baseTime.Add(30 * time.Second),
					Type:      TimelineChildCompleted,
					StepName:  "process",
					Message:   "Child workflow completed",
					Metadata:  map[string]string{"child_run_id": "child-1"},
				},
			},
		},
		{
			name: "child workflow failed",
			events: []event.Event{
				{
					Type:      event.EventChildSpawned,
					StepName:  "process",
					Timestamp: baseTime,
					Data:      mustJSON(event.ChildSpawnedData{ChildRunID: "child-1", WorkflowName: "payment-flow"}),
				},
				{
					Type:      event.EventChildFailed,
					StepName:  "process",
					Timestamp: baseTime.Add(10 * time.Second),
					Data:      mustJSON(event.ChildFailedData{ChildRunID: "child-1", Error: "card declined"}),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineChildSpawned,
					StepName:  "process",
					Message:   "Child workflow payment-flow spawned",
					Metadata:  map[string]string{"child_run_id": "child-1"},
				},
				{
					Timestamp: baseTime.Add(10 * time.Second),
					Type:      TimelineChildFailed,
					StepName:  "process",
					Message:   "Child workflow failed",
					Error:     ptrString("card declined"),
					Metadata:  map[string]string{"child_run_id": "child-1"},
				},
			},
		},
		{
			name: "full workflow timeline",
			events: []event.Event{
				{
					Type:      event.EventWorkflowStarted,
					Timestamp: baseTime,
					Data:      mustJSON(event.WorkflowStartedData{WorkflowName: "order-process", Version: "1.0"}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime.Add(10 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime.Add(110 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "charge",
					Timestamp: baseTime.Add(200 * time.Millisecond),
				},
				{
					Type:      event.EventWorkflowCompleted,
					Timestamp: baseTime.Add(210 * time.Millisecond),
				},
			},
			want: []TimelineEntry{
				{
					Timestamp: baseTime,
					Type:      TimelineWorkflowStarted,
					Message:   "Workflow order-process started",
				},
				{
					Timestamp: baseTime.Add(10 * time.Millisecond),
					Type:      TimelineStepStarted,
					StepName:  "validate",
					Attempt:   1,
					Message:   "Step validate started",
				},
				{
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Type:      TimelineStepCompleted,
					StepName:  "validate",
					Message:   "Step validate completed",
				},
				{
					Timestamp: baseTime.Add(110 * time.Millisecond),
					Type:      TimelineStepStarted,
					StepName:  "charge",
					Attempt:   1,
					Message:   "Step charge started",
				},
				{
					Timestamp: baseTime.Add(200 * time.Millisecond),
					Type:      TimelineStepCompleted,
					StepName:  "charge",
					Message:   "Step charge completed",
				},
				{
					Timestamp: baseTime.Add(210 * time.Millisecond),
					Type:      TimelineWorkflowCompleted,
					Message:   "Workflow completed",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Timeline(tt.events)

			if len(got) != len(tt.want) {
				t.Fatalf("Timeline() returned %d entries, want %d", len(got), len(tt.want))
			}

			for i := range got {
				if !got[i].Timestamp.Equal(tt.want[i].Timestamp) {
					t.Errorf("entry[%d].Timestamp = %v, want %v", i, got[i].Timestamp, tt.want[i].Timestamp)
				}
				if got[i].Type != tt.want[i].Type {
					t.Errorf("entry[%d].Type = %q, want %q", i, got[i].Type, tt.want[i].Type)
				}
				if got[i].StepName != tt.want[i].StepName {
					t.Errorf("entry[%d].StepName = %q, want %q", i, got[i].StepName, tt.want[i].StepName)
				}
				if got[i].Attempt != tt.want[i].Attempt {
					t.Errorf("entry[%d].Attempt = %d, want %d", i, got[i].Attempt, tt.want[i].Attempt)
				}
				if got[i].Message != tt.want[i].Message {
					t.Errorf("entry[%d].Message = %q, want %q", i, got[i].Message, tt.want[i].Message)
				}
				if !stringPtrEqual(got[i].Error, tt.want[i].Error) {
					t.Errorf("entry[%d].Error = %v, want %v", i, ptrValStr(got[i].Error), ptrValStr(tt.want[i].Error))
				}
				if !metadataEqual(got[i].Metadata, tt.want[i].Metadata) {
					t.Errorf("entry[%d].Metadata = %v, want %v", i, got[i].Metadata, tt.want[i].Metadata)
				}
			}
		})
	}
}

func metadataEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return len(a) == 0 && len(b) == 0
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestStepInvocations(t *testing.T) {
	baseTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		events []event.Event
		want   []StepInvocation
	}{
		{
			name:   "empty events returns empty slice",
			events: []event.Event{},
			want:   nil,
		},
		{
			name: "single step started only",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
			},
			want: []StepInvocation{
				{
					StepName:  "validate",
					Attempt:   1,
					Status:    InvocationStarted,
					StartedAt: baseTime,
				},
			},
		},
		{
			name: "single step completes",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{Duration: 100 * time.Millisecond}),
				},
			},
			want: []StepInvocation{
				{
					StepName:    "validate",
					Attempt:     1,
					Status:      InvocationCompleted,
					StartedAt:   baseTime,
					CompletedAt: ptrTime(baseTime.Add(100 * time.Millisecond)),
					DurationMs:  ptrInt64(100),
				},
			},
		},
		{
			name: "step fails without retry",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime.Add(50 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "payment declined", Attempt: 1, WillRetry: false}),
				},
			},
			want: []StepInvocation{
				{
					StepName:    "charge",
					Attempt:     1,
					Status:      InvocationFailed,
					StartedAt:   baseTime,
					CompletedAt: ptrTime(baseTime.Add(50 * time.Millisecond)),
					DurationMs:  ptrInt64(50),
					Error:       ptrString("payment declined"),
				},
			},
		},
		{
			name: "step fails then retries successfully",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepFailed,
					StepName:  "charge",
					Timestamp: baseTime.Add(50 * time.Millisecond),
					Data:      mustJSON(event.StepFailedData{Error: "timeout", Attempt: 1, WillRetry: true}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime.Add(1 * time.Second),
					Data:      mustJSON(event.StepStartedData{Attempt: 2}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "charge",
					Timestamp: baseTime.Add(1*time.Second + 100*time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{Duration: 100 * time.Millisecond}),
				},
			},
			want: []StepInvocation{
				{
					StepName:    "charge",
					Attempt:     1,
					Status:      InvocationRetrying,
					StartedAt:   baseTime,
					CompletedAt: ptrTime(baseTime.Add(50 * time.Millisecond)),
					DurationMs:  ptrInt64(50),
					Error:       ptrString("timeout"),
				},
				{
					StepName:    "charge",
					Attempt:     2,
					Status:      InvocationCompleted,
					StartedAt:   baseTime.Add(1 * time.Second),
					CompletedAt: ptrTime(baseTime.Add(1*time.Second + 100*time.Millisecond)),
					DurationMs:  ptrInt64(100),
				},
			},
		},
		{
			name: "multiple steps in workflow",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "charge",
					Timestamp: baseTime.Add(200 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "charge",
					Timestamp: baseTime.Add(400 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
				{
					Type:      event.EventStepStarted,
					StepName:  "ship",
					Timestamp: baseTime.Add(500 * time.Millisecond),
					Data:      mustJSON(event.StepStartedData{Attempt: 1}),
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "ship",
					Timestamp: baseTime.Add(700 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
			},
			want: []StepInvocation{
				{
					StepName:    "validate",
					Attempt:     1,
					Status:      InvocationCompleted,
					StartedAt:   baseTime,
					CompletedAt: ptrTime(baseTime.Add(100 * time.Millisecond)),
					DurationMs:  ptrInt64(100),
				},
				{
					StepName:    "charge",
					Attempt:     1,
					Status:      InvocationCompleted,
					StartedAt:   baseTime.Add(200 * time.Millisecond),
					CompletedAt: ptrTime(baseTime.Add(400 * time.Millisecond)),
					DurationMs:  ptrInt64(200),
				},
				{
					StepName:    "ship",
					Attempt:     1,
					Status:      InvocationCompleted,
					StartedAt:   baseTime.Add(500 * time.Millisecond),
					CompletedAt: ptrTime(baseTime.Add(700 * time.Millisecond)),
					DurationMs:  ptrInt64(200),
				},
			},
		},
		{
			name: "default attempt to 1 if not specified",
			events: []event.Event{
				{
					Type:      event.EventStepStarted,
					StepName:  "validate",
					Timestamp: baseTime,
					Data:      mustJSON(event.StepStartedData{}), // Attempt defaults to 0 in JSON
				},
				{
					Type:      event.EventStepCompleted,
					StepName:  "validate",
					Timestamp: baseTime.Add(100 * time.Millisecond),
					Data:      mustJSON(event.StepCompletedData{}),
				},
			},
			want: []StepInvocation{
				{
					StepName:    "validate",
					Attempt:     1,
					Status:      InvocationCompleted,
					StartedAt:   baseTime,
					CompletedAt: ptrTime(baseTime.Add(100 * time.Millisecond)),
					DurationMs:  ptrInt64(100),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StepInvocations(tt.events)

			if len(got) != len(tt.want) {
				t.Fatalf("StepInvocations() returned %d invocations, want %d", len(got), len(tt.want))
			}

			for i := range got {
				if got[i].StepName != tt.want[i].StepName {
					t.Errorf("invocation[%d].StepName = %q, want %q", i, got[i].StepName, tt.want[i].StepName)
				}
				if got[i].Attempt != tt.want[i].Attempt {
					t.Errorf("invocation[%d].Attempt = %d, want %d", i, got[i].Attempt, tt.want[i].Attempt)
				}
				if got[i].Status != tt.want[i].Status {
					t.Errorf("invocation[%d].Status = %q, want %q", i, got[i].Status, tt.want[i].Status)
				}
				if !got[i].StartedAt.Equal(tt.want[i].StartedAt) {
					t.Errorf("invocation[%d].StartedAt = %v, want %v", i, got[i].StartedAt, tt.want[i].StartedAt)
				}
				if !timeEqual(got[i].CompletedAt, tt.want[i].CompletedAt) {
					t.Errorf("invocation[%d].CompletedAt = %v, want %v", i, got[i].CompletedAt, tt.want[i].CompletedAt)
				}
				if !int64PtrEqual(got[i].DurationMs, tt.want[i].DurationMs) {
					t.Errorf("invocation[%d].DurationMs = %v, want %v", i, ptrVal(got[i].DurationMs), ptrVal(tt.want[i].DurationMs))
				}
				if !stringPtrEqual(got[i].Error, tt.want[i].Error) {
					t.Errorf("invocation[%d].Error = %v, want %v", i, ptrValStr(got[i].Error), ptrValStr(tt.want[i].Error))
				}
			}
		})
	}
}
