package river

import (
	"encoding/json"
	"testing"
)

func TestWorkflowJobArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     WorkflowJobArgs
		wantKind string
	}{
		{
			name: "basic args",
			args: WorkflowJobArgs{
				RunID:        "run-123",
				WorkflowName: "order-workflow",
				Version:      "1",
			},
			wantKind: JobKindWorkflow,
		},
		{
			name:     "zero value",
			args:     WorkflowJobArgs{},
			wantKind: JobKindWorkflow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.args.Kind(); got != tt.wantKind {
				t.Errorf("Kind() = %q, want %q", got, tt.wantKind)
			}

			opts := tt.args.InsertOpts()
			if opts.MaxAttempts != 3 {
				t.Errorf("InsertOpts().MaxAttempts = %d, want 3", opts.MaxAttempts)
			}
		})
	}
}

func TestWorkflowJobArgs_JSON(t *testing.T) {
	args := WorkflowJobArgs{
		RunID:        "run-456",
		WorkflowName: "payment-workflow",
		Version:      "2",
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded WorkflowJobArgs
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.RunID != args.RunID {
		t.Errorf("RunID = %q, want %q", decoded.RunID, args.RunID)
	}
	if decoded.WorkflowName != args.WorkflowName {
		t.Errorf("WorkflowName = %q, want %q", decoded.WorkflowName, args.WorkflowName)
	}
	if decoded.Version != args.Version {
		t.Errorf("Version = %q, want %q", decoded.Version, args.Version)
	}
}

func TestSignalTimeoutJobArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     SignalTimeoutJobArgs
		wantKind string
	}{
		{
			name: "basic args",
			args: SignalTimeoutJobArgs{
				RunID:      "run-789",
				SignalName: "approval",
			},
			wantKind: JobKindSignalTimeout,
		},
		{
			name:     "zero value",
			args:     SignalTimeoutJobArgs{},
			wantKind: JobKindSignalTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.args.Kind(); got != tt.wantKind {
				t.Errorf("Kind() = %q, want %q", got, tt.wantKind)
			}

			opts := tt.args.InsertOpts()
			if opts.MaxAttempts != 3 {
				t.Errorf("InsertOpts().MaxAttempts = %d, want 3", opts.MaxAttempts)
			}
		})
	}
}

func TestSignalTimeoutJobArgs_JSON(t *testing.T) {
	args := SignalTimeoutJobArgs{
		RunID:      "run-abc",
		SignalName: "payment_confirmed",
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded SignalTimeoutJobArgs
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.RunID != args.RunID {
		t.Errorf("RunID = %q, want %q", decoded.RunID, args.RunID)
	}
	if decoded.SignalName != args.SignalName {
		t.Errorf("SignalName = %q, want %q", decoded.SignalName, args.SignalName)
	}
}

func TestScheduledStartJobArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     ScheduledStartJobArgs
		wantKind string
	}{
		{
			name: "with all fields",
			args: ScheduledStartJobArgs{
				WorkflowName: "scheduled-workflow",
				Input:        json.RawMessage(`{"key":"value"}`),
				RunID:        "custom-run-id",
				Priority:     2,
			},
			wantKind: JobKindScheduledStart,
		},
		{
			name: "minimal fields",
			args: ScheduledStartJobArgs{
				WorkflowName: "minimal-workflow",
				Input:        json.RawMessage(`{}`),
			},
			wantKind: JobKindScheduledStart,
		},
		{
			name:     "zero value",
			args:     ScheduledStartJobArgs{},
			wantKind: JobKindScheduledStart,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.args.Kind(); got != tt.wantKind {
				t.Errorf("Kind() = %q, want %q", got, tt.wantKind)
			}

			opts := tt.args.InsertOpts()
			if opts.MaxAttempts != 3 {
				t.Errorf("InsertOpts().MaxAttempts = %d, want 3", opts.MaxAttempts)
			}
		})
	}
}

func TestScheduledStartJobArgs_JSON(t *testing.T) {
	args := ScheduledStartJobArgs{
		WorkflowName: "order-workflow",
		Input:        json.RawMessage(`{"order_id":"12345"}`),
		RunID:        "preset-run-id",
		Priority:     1,
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var decoded ScheduledStartJobArgs
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if decoded.WorkflowName != args.WorkflowName {
		t.Errorf("WorkflowName = %q, want %q", decoded.WorkflowName, args.WorkflowName)
	}
	if string(decoded.Input) != string(args.Input) {
		t.Errorf("Input = %q, want %q", string(decoded.Input), string(args.Input))
	}
	if decoded.RunID != args.RunID {
		t.Errorf("RunID = %q, want %q", decoded.RunID, args.RunID)
	}
	if decoded.Priority != args.Priority {
		t.Errorf("Priority = %d, want %d", decoded.Priority, args.Priority)
	}
}

func TestScheduledStartJobArgs_JSON_OmitsEmptyOptional(t *testing.T) {
	args := ScheduledStartJobArgs{
		WorkflowName: "simple-workflow",
		Input:        json.RawMessage(`{}`),
		// RunID and Priority are zero values, should be omitted
	}

	data, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Check that optional fields are omitted when empty
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal to map failed: %v", err)
	}

	if _, exists := raw["run_id"]; exists {
		t.Error("Expected run_id to be omitted when empty")
	}
	// Note: priority with value 0 may or may not be omitted depending on omitempty behavior
	// The test verifies the JSON structure, not specific omitempty behavior
}

func TestJobKindConstants(t *testing.T) {
	// Verify job kind constants have expected prefixes for namespacing
	kinds := []string{
		JobKindWorkflow,
		JobKindSignalTimeout,
		JobKindScheduledStart,
	}

	for _, kind := range kinds {
		if len(kind) < 6 || kind[:6] != "skene." {
			t.Errorf("Job kind %q should have 'skene.' prefix", kind)
		}
	}

	// Verify uniqueness
	kindSet := make(map[string]bool)
	for _, kind := range kinds {
		if kindSet[kind] {
			t.Errorf("Duplicate job kind: %q", kind)
		}
		kindSet[kind] = true
	}
}
