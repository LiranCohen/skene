package event

import (
	"encoding/json"
	"time"
)

// WorkflowStartedData is the payload for workflow.started events.
type WorkflowStartedData struct {
	WorkflowName string          `json:"workflow_name"`
	Version      string          `json:"version"`
	Input        json.RawMessage `json:"input"`
}

// WorkflowCompletedData is the payload for workflow.completed events.
type WorkflowCompletedData struct {
	Output json.RawMessage `json:"output,omitempty"`
}

// WorkflowFailedData is the payload for workflow.failed events.
type WorkflowFailedData struct {
	Error string `json:"error"`
}

// WorkflowCancelledData is the payload for workflow.cancelled events.
type WorkflowCancelledData struct {
	Reason string `json:"reason"`
}

// StepStartedData is the payload for step.started events.
type StepStartedData struct {
	Attempt    int    `json:"attempt,omitempty"`
	BranchName string `json:"branch_name,omitempty"` // Parent branch name (if executing a branch case)
	CaseValue  string `json:"case_value,omitempty"`  // Which case was chosen (if executing a branch case)
}

// StepCompletedData is the payload for step.completed events.
type StepCompletedData struct {
	Duration   time.Duration `json:"duration_ns"`
	BranchName string        `json:"branch_name,omitempty"` // Parent branch name (if executing a branch case)
	CaseValue  string        `json:"case_value,omitempty"`  // Which case was chosen (if executing a branch case)
}

// StepFailedData is the payload for step.failed events.
type StepFailedData struct {
	Error      string `json:"error"`
	Attempt    int    `json:"attempt"`
	WillRetry  bool   `json:"will_retry"`
	BranchName string `json:"branch_name,omitempty"` // Parent branch name (if executing a branch case)
	CaseValue  string `json:"case_value,omitempty"`  // Which case was chosen (if executing a branch case)
}

// BranchEvaluatedData is the payload for branch.evaluated events.
type BranchEvaluatedData struct {
	BranchName string `json:"branch_name"`
	Choice     string `json:"choice"`
}

// SignalWaitingData is the payload for signal.waiting events.
type SignalWaitingData struct {
	SignalName string    `json:"signal_name"`
	TimeoutAt  time.Time `json:"timeout_at,omitempty"`
}

// SignalReceivedData is the payload for signal.received events.
type SignalReceivedData struct {
	SignalName string          `json:"signal_name"`
	Payload    json.RawMessage `json:"payload,omitempty"`
}

// SignalTimeoutData is the payload for signal.timeout events.
type SignalTimeoutData struct {
	SignalName string `json:"signal_name"`
}

// ChildSpawnedData is the payload for child.spawned events.
type ChildSpawnedData struct {
	ChildRunID   string          `json:"child_run_id"`
	WorkflowName string          `json:"workflow_name"`
	Input        json.RawMessage `json:"input"`
}

// ChildCompletedData is the payload for child.completed events.
type ChildCompletedData struct {
	ChildRunID string          `json:"child_run_id"`
	Output     json.RawMessage `json:"output,omitempty"`
}

// ChildFailedData is the payload for child.failed events.
type ChildFailedData struct {
	ChildRunID string `json:"child_run_id"`
	Error      string `json:"error"`
}

// MapStartedData is the payload for map.started events.
type MapStartedData struct {
	MapIndex  int64 `json:"map_index"`
	ItemCount int   `json:"item_count"`
}

// MapCompletedData is the payload for map.completed events.
type MapCompletedData struct {
	MapIndex int64           `json:"map_index"`
	Results  json.RawMessage `json:"results"`
}

// MapFailedData is the payload for map.failed events.
type MapFailedData struct {
	MapIndex    int64  `json:"map_index"`
	FailedIndex int    `json:"failed_index"`
	Error       string `json:"error"`
}

// CompensationStartedData is the payload for compensation.started events.
type CompensationStartedData struct {
	TriggerStep string   `json:"trigger_step"`
	StepsToUndo []string `json:"steps_to_undo"`
}

// CompensationCompletedData is the payload for compensation.completed events.
type CompensationCompletedData struct {
	StepsCompensated []string `json:"steps_compensated"`
}

// CompensationFailedData is the payload for compensation.failed events.
type CompensationFailedData struct {
	FailedStep string `json:"failed_step"`
	Error      string `json:"error"`
}

// SnapshotData is the payload for snapshot events.
type SnapshotData struct {
	State json.RawMessage `json:"state"`
}
