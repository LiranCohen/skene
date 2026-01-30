package river

import "encoding/json"

// Job kind constants for River job registration.
const (
	// JobKindWorkflow is the kind for main workflow execution jobs.
	JobKindWorkflow = "skene.workflow"

	// JobKindSignalTimeout is the kind for signal timeout jobs.
	JobKindSignalTimeout = "skene.signal_timeout"

	// JobKindScheduledStart is the kind for scheduled workflow start jobs.
	JobKindScheduledStart = "skene.scheduled_start"
)

// WorkflowJobArgs contains arguments for the main workflow execution job.
// This job loads events, replays the workflow, and executes pending steps.
type WorkflowJobArgs struct {
	// RunID is the unique identifier for this workflow run.
	RunID string `json:"run_id"`

	// WorkflowName identifies the workflow definition to execute.
	WorkflowName string `json:"workflow_name"`

	// Version is the workflow definition version.
	Version string `json:"version"`
}

// Kind implements river.JobArgs.
func (WorkflowJobArgs) Kind() string {
	return JobKindWorkflow
}

// InsertOpts implements river.JobArgsWithInsertOpts to provide default options.
// The returned options can be overridden when inserting the job.
func (WorkflowJobArgs) InsertOpts() InsertOpts {
	return InsertOpts{
		MaxAttempts: 3,
	}
}

// SignalTimeoutJobArgs contains arguments for signal timeout handling.
// This job checks if a signal has been received and handles timeouts.
type SignalTimeoutJobArgs struct {
	// RunID is the workflow run waiting for the signal.
	RunID string `json:"run_id"`

	// SignalName is the name of the signal being waited for.
	SignalName string `json:"signal_name"`
}

// Kind implements river.JobArgs.
func (SignalTimeoutJobArgs) Kind() string {
	return JobKindSignalTimeout
}

// InsertOpts implements river.JobArgsWithInsertOpts.
func (SignalTimeoutJobArgs) InsertOpts() InsertOpts {
	return InsertOpts{
		MaxAttempts: 3,
	}
}

// ScheduledStartJobArgs contains arguments for starting a workflow at a scheduled time.
type ScheduledStartJobArgs struct {
	// WorkflowName identifies the workflow to start.
	WorkflowName string `json:"workflow_name"`

	// Input is the workflow input as JSON.
	Input json.RawMessage `json:"input"`

	// RunID is an optional custom run ID. If empty, a UUID is generated.
	RunID string `json:"run_id,omitempty"`

	// Priority is the job priority for the workflow execution.
	Priority int `json:"priority,omitempty"`
}

// Kind implements river.JobArgs.
func (ScheduledStartJobArgs) Kind() string {
	return JobKindScheduledStart
}

// InsertOpts implements river.JobArgsWithInsertOpts.
func (ScheduledStartJobArgs) InsertOpts() InsertOpts {
	return InsertOpts{
		MaxAttempts: 3,
	}
}

// InsertOpts mirrors River's InsertOpts for job configuration.
// This allows our job args to specify default insert options without
// importing River directly in this file.
type InsertOpts struct {
	// MaxAttempts is the maximum number of attempts for this job.
	// If not set, River's default (24) is used.
	MaxAttempts int

	// Priority is the job priority. Lower values are higher priority.
	// If not set, River's default (1) is used.
	Priority int

	// Queue is the queue to insert the job into.
	// If not set, River's default queue is used.
	Queue string
}
