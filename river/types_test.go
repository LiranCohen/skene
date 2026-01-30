package river

import (
	"testing"
)

func TestRunStatus_IsTerminal(t *testing.T) {
	tests := []struct {
		status   RunStatus
		terminal bool
	}{
		{RunStatusPending, false},
		{RunStatusRunning, false},
		{RunStatusWaiting, false},
		{RunStatusCompleted, true},
		{RunStatusFailed, true},
		{RunStatusCancelled, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsTerminal(); got != tt.terminal {
				t.Errorf("RunStatus(%q).IsTerminal() = %v, want %v", tt.status, got, tt.terminal)
			}
		})
	}
}

func TestRunStatus_String(t *testing.T) {
	tests := []struct {
		status RunStatus
		want   string
	}{
		{RunStatusPending, "pending"},
		{RunStatusRunning, "running"},
		{RunStatusCompleted, "completed"},
		{RunStatusFailed, "failed"},
		{RunStatusCancelled, "cancelled"},
		{RunStatusWaiting, "waiting"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("RunStatus.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartOptions_ZeroValue(t *testing.T) {
	var opts StartOptions

	if opts.RunID != "" {
		t.Error("expected RunID to be empty by default")
	}
	if opts.Priority != 0 {
		t.Error("expected Priority to be 0 by default")
	}
	if opts.Metadata != nil {
		t.Error("expected Metadata to be nil by default")
	}
}

func TestRunFilter_ZeroValue(t *testing.T) {
	var filter RunFilter

	if filter.WorkflowName != "" {
		t.Error("expected WorkflowName to be empty by default")
	}
	if filter.Status != "" {
		t.Error("expected Status to be empty by default")
	}
	if filter.Limit != 0 {
		t.Error("expected Limit to be 0 by default")
	}
	if filter.Offset != 0 {
		t.Error("expected Offset to be 0 by default")
	}
}
