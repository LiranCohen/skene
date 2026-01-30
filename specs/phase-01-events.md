# Phase 1: Events & Storage

## Goal
Implement the event system and in-memory storage layer as the foundation for Skene's event-sourced workflow framework.

## Primary Design Document
- `docs/design/01-EVENTS.md`

## Related Documents
- `docs/design/00-ARCHITECTURE.md` (package structure)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Event Struct (`event/event.go`)

```go
type Event struct {
    ID         string            // UUID
    RunID      string            // Workflow run identifier
    Sequence   int64             // Strict ordering (1, 2, 3, ...)
    Version    int               // Schema version for forward compatibility
    Type       EventType         // Event classification
    StepName   string            // Step identifier: "validate", "charge"
    Data       json.RawMessage   // Type-specific payload
    Output     json.RawMessage   // Step output (completion events only)
    Timestamp  time.Time
    Metadata   map[string]string // Trace IDs, correlation IDs, etc.
}
```

### EventType Constants (`event/event.go`)

```go
type EventType string

const (
    // Workflow lifecycle
    EventWorkflowStarted   EventType = "workflow.started"
    EventWorkflowCompleted EventType = "workflow.completed"
    EventWorkflowFailed    EventType = "workflow.failed"
    EventWorkflowCancelled EventType = "workflow.cancelled"

    // Step lifecycle
    EventStepStarted   EventType = "step.started"
    EventStepCompleted EventType = "step.completed"
    EventStepFailed    EventType = "step.failed"

    // Branch
    EventBranchEvaluated EventType = "branch.evaluated"

    // Signal
    EventSignalWaiting  EventType = "signal.waiting"
    EventSignalReceived EventType = "signal.received"
    EventSignalTimeout  EventType = "signal.timeout"

    // Child workflows
    EventChildSpawned   EventType = "child.spawned"
    EventChildCompleted EventType = "child.completed"
    EventChildFailed    EventType = "child.failed"

    // Map (fan-out)
    EventMapStarted   EventType = "map.started"
    EventMapCompleted EventType = "map.completed"
    EventMapFailed    EventType = "map.failed"

    // Compensation
    EventCompensationStarted   EventType = "compensation.started"
    EventCompensationCompleted EventType = "compensation.completed"
    EventCompensationFailed    EventType = "compensation.failed"

    // Snapshot
    EventSnapshot EventType = "snapshot"
)
```

### EventStore Interface (`event/store.go`)

```go
type EventStore interface {
    Append(ctx context.Context, event Event) error
    AppendBatch(ctx context.Context, events []Event) error
    Load(ctx context.Context, runID string) ([]Event, error)
    LoadSince(ctx context.Context, runID string, afterSequence int64) ([]Event, error)
    GetLastSequence(ctx context.Context, runID string) (int64, error)
}
```

### Event Data Structs (`event/data.go`)

Payload structs for each event type:

```go
// WorkflowStartedData for workflow.started events
type WorkflowStartedData struct {
    WorkflowName string          `json:"workflow_name"`
    Version      int             `json:"version"`
    Input        json.RawMessage `json:"input"`
}

// WorkflowCompletedData for workflow.completed events
type WorkflowCompletedData struct {
    Output json.RawMessage `json:"output,omitempty"`
}

// WorkflowFailedData for workflow.failed events
type WorkflowFailedData struct {
    Error string `json:"error"`
}

// WorkflowCancelledData for workflow.cancelled events
type WorkflowCancelledData struct {
    Reason string `json:"reason"`
}

// StepStartedData for step.started events
type StepStartedData struct {
    Attempt int `json:"attempt,omitempty"`
}

// StepCompletedData for step.completed events
type StepCompletedData struct {
    Duration time.Duration `json:"duration_ns"`
}

// StepFailedData for step.failed events
type StepFailedData struct {
    Error     string `json:"error"`
    Attempt   int    `json:"attempt"`
    WillRetry bool   `json:"will_retry"`
}

// BranchEvaluatedData for branch.evaluated events
type BranchEvaluatedData struct {
    BranchName string `json:"branch_name"`
    Choice     string `json:"choice"`
}

// SignalWaitingData for signal.waiting events
type SignalWaitingData struct {
    SignalName string    `json:"signal_name"`
    TimeoutAt  time.Time `json:"timeout_at,omitempty"`
}

// SignalReceivedData for signal.received events
type SignalReceivedData struct {
    SignalName string          `json:"signal_name"`
    Payload    json.RawMessage `json:"payload,omitempty"`
}

// SignalTimeoutData for signal.timeout events
type SignalTimeoutData struct {
    SignalName string `json:"signal_name"`
}

// ChildSpawnedData for child.spawned events
type ChildSpawnedData struct {
    ChildRunID   string          `json:"child_run_id"`
    WorkflowName string          `json:"workflow_name"`
    Input        json.RawMessage `json:"input"`
}

// ChildCompletedData for child.completed events
type ChildCompletedData struct {
    ChildRunID string          `json:"child_run_id"`
    Output     json.RawMessage `json:"output,omitempty"`
}

// ChildFailedData for child.failed events
type ChildFailedData struct {
    ChildRunID string `json:"child_run_id"`
    Error      string `json:"error"`
}

// MapStartedData for map.started events
type MapStartedData struct {
    ItemCount int `json:"item_count"`
}

// MapCompletedData for map.completed events
type MapCompletedData struct {
    Results json.RawMessage `json:"results"`
}

// MapFailedData for map.failed events
type MapFailedData struct {
    FailedIndex int    `json:"failed_index"`
    Error       string `json:"error"`
}

// CompensationStartedData for compensation.started events
type CompensationStartedData struct {
    TriggerStep string   `json:"trigger_step"`
    StepsToUndo []string `json:"steps_to_undo"`
}

// CompensationCompletedData for compensation.completed events
type CompensationCompletedData struct {
    StepsCompensated []string `json:"steps_compensated"`
}

// CompensationFailedData for compensation.failed events
type CompensationFailedData struct {
    FailedStep string `json:"failed_step"`
    Error      string `json:"error"`
}
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `event/event.go` | Event struct, EventType constants |
| `event/data.go` | Event payload structs |
| `event/store.go` | EventStore interface |
| `event/memory/store.go` | In-memory EventStore implementation |

---

## Acceptance Criteria

- [ ] Event struct matches design doc exactly
- [ ] All event types defined as constants
- [ ] EventStore interface has: Append, AppendBatch, Load, LoadSince, GetLastSequence
- [ ] In-memory store is thread-safe (uses sync.RWMutex)
- [ ] Sequence validation: gapless, monotonically increasing
- [ ] Append fails if sequence != lastSequence + 1
- [ ] AppendBatch is atomic (all-or-nothing)
- [ ] Load returns events sorted by sequence
- [ ] Table-driven tests with edge cases
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes
- [ ] `go vet ./...` passes
- [ ] `gofmt -l .` returns no output

---

## Test Scenarios

### Event Creation Tests
- Create event with all fields populated
- Verify JSON serialization/deserialization roundtrip
- Test each event type constant

### EventStore Tests
- Append single event, load it back
- Append event with wrong sequence (should fail)
- Append event with duplicate ID (should fail)
- AppendBatch with valid sequence range
- AppendBatch with invalid sequence (atomic rollback)
- Load empty run (returns empty slice)
- Load non-existent run (returns empty slice)
- LoadSince returns events after given sequence
- GetLastSequence for empty run returns 0
- GetLastSequence for run with events returns correct value
- Concurrent appends to different runs succeed
- Concurrent appends to same run with correct sequences succeed

### Memory Store Specific
- Thread-safety under concurrent reads/writes
- Events sorted by sequence on Load

---

## Dependencies
None - this is the foundation phase.

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors, don't panic
- Wrap errors with context: `fmt.Errorf("context: %w", err)`
- Table-driven tests for comprehensive coverage
- Test behavior, not implementation
