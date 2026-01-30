// Package event provides the event types and storage interfaces for Skene's
// event-sourced workflow execution model.
package event

import (
	"encoding/json"
	"time"
)

// EventType classifies events in the workflow execution lifecycle.
type EventType string

const (
	// Workflow lifecycle events
	EventWorkflowStarted   EventType = "workflow.started"
	EventWorkflowCompleted EventType = "workflow.completed"
	EventWorkflowFailed    EventType = "workflow.failed"
	EventWorkflowCancelled EventType = "workflow.cancelled"

	// Step lifecycle events
	EventStepStarted   EventType = "step.started"
	EventStepCompleted EventType = "step.completed"
	EventStepFailed    EventType = "step.failed"

	// Branch events
	EventBranchEvaluated EventType = "branch.evaluated"

	// Signal events
	EventSignalWaiting  EventType = "signal.waiting"
	EventSignalReceived EventType = "signal.received"
	EventSignalTimeout  EventType = "signal.timeout"

	// Child workflow events
	EventChildSpawned   EventType = "child.spawned"
	EventChildCompleted EventType = "child.completed"
	EventChildFailed    EventType = "child.failed"

	// Map (fan-out) events
	EventMapStarted   EventType = "map.started"
	EventMapCompleted EventType = "map.completed"
	EventMapFailed    EventType = "map.failed"

	// Compensation events
	EventCompensationStarted   EventType = "compensation.started"
	EventCompensationCompleted EventType = "compensation.completed"
	EventCompensationFailed    EventType = "compensation.failed"

	// Snapshot event
	EventSnapshot EventType = "snapshot"
)

// Event represents a single event in a workflow's execution history.
// Events are the source of truth for workflow execution and enable
// crash recovery through replay.
type Event struct {
	// ID is the unique identifier for this event (UUID).
	ID string `json:"id"`

	// RunID identifies the workflow run this event belongs to.
	RunID string `json:"run_id"`

	// Sequence provides strict ordering within a run (1, 2, 3, ...).
	// Sequences are gapless and monotonically increasing.
	Sequence int64 `json:"sequence"`

	// Version is the schema version for forward compatibility.
	Version int `json:"version"`

	// Type classifies the event (e.g., "step.completed").
	Type EventType `json:"type"`

	// StepName identifies the step this event relates to.
	// Empty for workflow-level events.
	StepName string `json:"step_name,omitempty"`

	// Data contains the type-specific event payload.
	Data json.RawMessage `json:"data,omitempty"`

	// Output contains the step output (completion events only).
	Output json.RawMessage `json:"output,omitempty"`

	// Timestamp records when the event was created.
	Timestamp time.Time `json:"timestamp"`

	// Metadata holds additional context like trace IDs and correlation IDs.
	Metadata map[string]string `json:"metadata,omitempty"`
}
