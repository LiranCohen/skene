package river

import (
	"encoding/json"
	"time"
)

// RunStatus represents the current state of a workflow execution.
type RunStatus string

const (
	// RunStatusPending indicates the workflow has been created but not yet started.
	RunStatusPending RunStatus = "pending"

	// RunStatusRunning indicates the workflow is actively executing.
	RunStatusRunning RunStatus = "running"

	// RunStatusCompleted indicates the workflow finished successfully.
	RunStatusCompleted RunStatus = "completed"

	// RunStatusFailed indicates the workflow terminated with an error.
	RunStatusFailed RunStatus = "failed"

	// RunStatusCancelled indicates the workflow was explicitly cancelled.
	RunStatusCancelled RunStatus = "cancelled"

	// RunStatusWaiting indicates the workflow is waiting for a signal.
	RunStatusWaiting RunStatus = "waiting"
)

// IsTerminal returns true if the status represents a final state.
func (s RunStatus) IsTerminal() bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}

// String returns the string representation of the status.
func (s RunStatus) String() string {
	return string(s)
}

// Run represents a workflow execution with full details.
type Run struct {
	// ID is the unique identifier for this run.
	ID string

	// WorkflowName identifies the workflow definition.
	WorkflowName string

	// Version is the workflow definition version.
	Version string

	// Status is the current execution state.
	Status RunStatus

	// Input is the workflow input as JSON.
	Input json.RawMessage

	// Output is the workflow result as JSON (nil if not completed).
	Output json.RawMessage

	// Error contains the error message if the workflow failed.
	Error string

	// StartedAt is when the workflow started.
	StartedAt time.Time

	// CompletedAt is when the workflow finished (nil if still running).
	CompletedAt *time.Time
}

// RunSummary is a lightweight representation of a run for list operations.
type RunSummary struct {
	// ID is the unique identifier for this run.
	ID string

	// WorkflowName identifies the workflow definition.
	WorkflowName string

	// Status is the current execution state.
	Status RunStatus

	// StartedAt is when the workflow started.
	StartedAt time.Time
}

// StartOptions configures workflow start behavior.
type StartOptions struct {
	// RunID is an optional custom run ID. If empty, a UUID is generated.
	RunID string

	// Priority is the job priority (lower values = higher priority).
	Priority int

	// Metadata is optional key-value pairs associated with the run.
	Metadata map[string]string
}

// RunFilter specifies criteria for listing runs.
type RunFilter struct {
	// WorkflowName filters by workflow type. Empty matches all.
	WorkflowName string

	// Status filters by run status. Empty matches all.
	Status RunStatus

	// Limit is the maximum number of results to return.
	// Zero or negative means no limit.
	Limit int

	// Offset is the number of results to skip for pagination.
	Offset int
}
