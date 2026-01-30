# Phase 8: Runner Integration (River)

## Goal
Implement the Runner that integrates the replay engine with River job queue for durable workflow execution.

## Primary Design Document
- `docs/design/07-RUNNER.md`

## Related Documents
- `docs/design/00-ARCHITECTURE.md` (Runner interface)
- `docs/design/06-SIGNALS.md` (Signal delivery, timeouts)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Runner (`river/runner.go`)

```go
// Runner manages workflow execution with River job queue
type Runner interface {
    // Lifecycle
    Start(ctx context.Context) error
    Stop(ctx context.Context) error

    // Workflow operations
    StartWorkflow(ctx context.Context, name string, input json.RawMessage) (string, error)
    StartWorkflowTx(ctx context.Context, tx pgx.Tx, name string, input json.RawMessage, opts StartOptions) (string, error)
    SendSignal(ctx context.Context, runID, signalName string, payload json.RawMessage) error
    CancelWorkflow(ctx context.Context, runID, reason string) error

    // Queries
    GetRun(ctx context.Context, runID string) (*Run, error)
    ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error)
    GetHistory(ctx context.Context, runID string) ([]event.Event, error)
}

// StartOptions configures workflow start
type StartOptions struct {
    RunID    string // Optional custom run ID
    Priority int    // Job priority
    Metadata map[string]string
}

// Run represents a workflow execution
type Run struct {
    ID           string
    WorkflowName string
    Version      int
    Status       RunStatus
    Input        json.RawMessage
    Output       json.RawMessage
    Error        string
    StartedAt    time.Time
    CompletedAt  *time.Time
}

// RunStatus represents workflow state
type RunStatus string

const (
    RunStatusPending   RunStatus = "pending"
    RunStatusRunning   RunStatus = "running"
    RunStatusCompleted RunStatus = "completed"
    RunStatusFailed    RunStatus = "failed"
    RunStatusCancelled RunStatus = "cancelled"
    RunStatusWaiting   RunStatus = "waiting"
)

// RunFilter for ListRuns queries
type RunFilter struct {
    WorkflowName string
    Status       RunStatus
    Limit        int
    Offset       int
}

// RunSummary is a lightweight run representation
type RunSummary struct {
    ID           string
    WorkflowName string
    Status       RunStatus
    StartedAt    time.Time
}
```

### Runner Configuration (`river/config.go`)

```go
// Config configures the Runner
type Config struct {
    Pool       *pgxpool.Pool
    EventStore event.EventStore
    Registry   *Registry
    Logger     Logger

    // Optional
    Workers    int           // Default: runtime.NumCPU()
    JobTimeout time.Duration // Default: 30s
}

// NewRunner creates a new Runner
func NewRunner(config Config) (*runner, error)
```

### Job Types (`river/jobs.go`)

```go
// WorkflowJobArgs for main execution jobs
type WorkflowJobArgs struct {
    RunID        string `json:"run_id"`
    WorkflowName string `json:"workflow_name"`
    Version      int    `json:"version"`
}

// Implement river.JobArgs
func (WorkflowJobArgs) Kind() string { return "skene.workflow" }

// SignalTimeoutJobArgs for signal timeout handling
type SignalTimeoutJobArgs struct {
    RunID      string `json:"run_id"`
    SignalName string `json:"signal_name"`
}

func (SignalTimeoutJobArgs) Kind() string { return "skene.signal_timeout" }
```

### Workflow Registry

```go
// Registry stores workflow definitions
type Registry struct {
    workflows map[string]*workflow.WorkflowDef
}

// NewRegistry creates a new Registry
func NewRegistry() *Registry

// Register adds a workflow definition
func (r *Registry) Register(def *workflow.WorkflowDef)

// Get retrieves a workflow by name
func (r *Registry) Get(name string) (*workflow.WorkflowDef, error)
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `river/runner.go` | Runner implementation |
| `river/config.go` | Configuration |
| `river/jobs.go` | Job types and workers |
| `river/queries.go` | GetRun, ListRuns, GetHistory |
| `river/signals.go` | SendSignal, timeout handling |
| `river/registry.go` | Workflow registry |

---

## Acceptance Criteria

### Lifecycle
- [ ] Start initializes River client and workers
- [ ] Stop gracefully shuts down (configurable timeout)
- [ ] Workers process jobs concurrently

### StartWorkflow
- [ ] Generates UUID for RunID if not provided
- [ ] Appends workflow.started event
- [ ] Inserts WorkflowJob into River
- [ ] Returns RunID
- [ ] StartWorkflowTx uses provided transaction

### Job Execution
- [ ] Load events from EventStore
- [ ] Create Replayer with workflow definition
- [ ] Execute replay
- [ ] Append new events atomically
- [ ] Insert next job if more work needed
- [ ] All in single transaction

### SendSignal
- [ ] Validates signal is waiting
- [ ] Appends signal.received event
- [ ] Inserts resume job
- [ ] Idempotent (sending twice is no-op)

### CancelWorkflow
- [ ] Validates workflow is running/waiting
- [ ] Appends workflow.cancelled event
- [ ] Cancels pending River jobs

### Signal Timeouts
- [ ] SignalTimeoutJob scheduled when signal.waiting
- [ ] Job checks if signal already received
- [ ] Appends signal.timeout if not received
- [ ] Resumes workflow with timeout

### Queries
- [ ] GetRun returns full Run from events
- [ ] ListRuns returns summaries with filtering
- [ ] GetHistory returns all events

### Tests
- [ ] Start/Stop lifecycle
- [ ] StartWorkflow creates run
- [ ] Workflow completes successfully
- [ ] Workflow fails, events recorded
- [ ] SendSignal resumes workflow
- [ ] Signal timeout triggers
- [ ] CancelWorkflow stops execution
- [ ] Query APIs return correct data
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### Lifecycle Tests
- Start runner, stop runner
- Start with invalid config returns error
- Stop with in-flight jobs waits gracefully

### Workflow Execution Tests
- Simple workflow completes
- Multi-step workflow completes
- Workflow with parallel steps
- Workflow fails, error recorded
- Workflow cancelled

### Signal Tests
- Workflow waits for signal
- SendSignal resumes workflow
- Signal timeout fires
- Signal sent after timeout (no-op)
- Signal sent twice (idempotent)

### Transaction Tests
- Events and job committed atomically
- Transaction rollback on failure
- No orphaned jobs

### Query Tests
- GetRun for running workflow
- GetRun for completed workflow
- ListRuns with filter
- GetHistory returns events in order

---

## Dependencies
- Phase 1: Events & Storage
- Phase 2: Replay Engine
- Phase 3: Typed Step Model
- Phase 4: Parallel Execution
- Phase 5: Child Workflows
- Phase 6: Branching
- Phase 7: Signals

---

## Implementation Notes

### Transaction Pattern

```go
func (r *runner) executeJob(ctx context.Context, job *river.Job[WorkflowJobArgs]) error {
    tx, err := r.pool.Begin(ctx)
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx)

    // Load events
    events, err := r.eventStore.LoadTx(ctx, tx, job.Args.RunID)
    if err != nil {
        return err
    }

    // Replay
    history := workflow.NewHistory(job.Args.RunID, events)
    def, _ := r.registry.Get(job.Args.WorkflowName)
    replayer := workflow.NewReplayer(workflow.ReplayerConfig{
        Workflow: def,
        History:  history,
        RunID:    job.Args.RunID,
    })

    output, err := replayer.Replay(ctx)
    if err != nil {
        return err
    }

    // Append new events
    if err := r.eventStore.AppendBatchTx(ctx, tx, output.NewEvents); err != nil {
        return err
    }

    // Schedule next job if needed
    if output.Result == workflow.ReplaySuspended {
        if _, err := r.client.InsertTx(ctx, tx, WorkflowJobArgs{
            RunID:        job.Args.RunID,
            WorkflowName: job.Args.WorkflowName,
            Version:      job.Args.Version,
        }, nil); err != nil {
            return err
        }
    }

    // Schedule signal timeouts
    for _, sig := range output.WaitingForSignals {
        if _, err := r.client.InsertTx(ctx, tx, SignalTimeoutJobArgs{
            RunID:      job.Args.RunID,
            SignalName: sig.SignalName,
        }, &river.InsertOpts{
            ScheduledAt: sig.TimeoutAt,
        }); err != nil {
            return err
        }
    }

    return tx.Commit(ctx)
}
```

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors, don't panic
- Wrap errors with context
- Use testcontainers for integration tests
- Table-driven tests
