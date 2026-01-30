# Phase 2: Replay Engine & History

## Goal
Implement the replay engine that walks the workflow DAG, restores completed step outputs from events, and executes pending steps.

## Primary Design Document
- `docs/design/03-REPLAY.md`

## Related Documents
- `docs/design/00-ARCHITECTURE.md` (package structure)
- `docs/design/01-EVENTS.md` (event model)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### History Helper (`workflow/history.go`)

```go
// History provides efficient queries over workflow events
type History struct {
    runID    string
    events   []event.Event
    outputs  map[string]json.RawMessage // stepName -> output
    branches map[string]string          // branchName -> choice
    signals  map[string]*SignalState    // signalName -> state
}

// NewHistory creates a History from events
func NewHistory(runID string, events []event.Event) *History

// RunID returns the workflow run ID
func (h *History) RunID() string

// HasCompleted returns true if step has a step.completed event
func (h *History) HasCompleted(stepName string) bool

// GetOutput returns the output for a completed step
func (h *History) GetOutput(stepName string) (json.RawMessage, bool)

// GetTypedOutput deserializes output to the given type
func GetTypedOutput[T any](h *History, stepName string) (T, error)

// GetBranchChoice returns the recorded branch decision
func (h *History) GetBranchChoice(branchName string) (string, bool)

// GetFatalError returns the error if step failed without retry
func (h *History) GetFatalError(stepName string) error

// IsCancelled returns true if workflow was cancelled
func (h *History) IsCancelled() bool

// IsCompleted returns true if workflow completed
func (h *History) IsCompleted() bool

// GetWorkflowInput returns the workflow input from workflow.started event
func (h *History) GetWorkflowInput() json.RawMessage

// LastSequence returns the highest sequence number
func (h *History) LastSequence() int64

// SignalState tracks signal status
type SignalState struct {
    Waiting  bool
    Received bool
    Timeout  bool
    Payload  json.RawMessage
}

// GetSignalState returns the state of a signal
func (h *History) GetSignalState(signalName string) *SignalState
```

### Replayer (`workflow/replay.go`)

```go
// ReplayerConfig configures the replay engine
type ReplayerConfig struct {
    Workflow *WorkflowDef  // Static workflow definition (DAG)
    History  *History      // Events to replay (nil = fresh execution)
    RunID    string        // Workflow run ID
    Input    any           // Workflow input (typed by caller)
    Logger   Logger        // Logging
}

// ReplayResult indicates replay outcome
type ReplayResult int

const (
    ReplayCompleted ReplayResult = iota // Workflow finished
    ReplayWaiting                       // Waiting for signal
    ReplayFailed                        // Workflow failed
    ReplaySuspended                     // More work needed (scheduling)
)

// ReplayOutput contains the result of replay
type ReplayOutput struct {
    Result        ReplayResult      // Outcome
    FinalOutput   any               // Output of final step (when completed)
    Error         error             // On failure
    WaitingSignal string            // On waiting for signal
    NewEvents     []event.Event     // Generated during replay
    NextSequence  int64             // For next event append
}

// Replayer executes workflows via event sourcing
type Replayer struct {
    config ReplayerConfig
}

// NewReplayer creates a new Replayer
func NewReplayer(config ReplayerConfig) *Replayer

// Replay executes the workflow, returning new events
func (r *Replayer) Replay(ctx context.Context) (*ReplayOutput, error)
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/history.go` | History helper for querying events |
| `workflow/replay.go` | Replay engine implementation |

---

## Acceptance Criteria

### History
- [ ] NewHistory indexes events by type and step name
- [ ] HasCompleted returns true only for step.completed events
- [ ] GetOutput returns JSON output for completed steps
- [ ] GetTypedOutput deserializes to correct type
- [ ] GetBranchChoice returns recorded decision
- [ ] GetFatalError returns error for failed steps (will_retry=false)
- [ ] IsCancelled checks for workflow.cancelled event
- [ ] IsCompleted checks for workflow.completed event
- [ ] GetSignalState tracks waiting/received/timeout

### Replayer
- [ ] Walks DAG in topological order
- [ ] Skips completed steps (restores output from history)
- [ ] Executes pending steps with workflow context
- [ ] Generates new events for executed steps
- [ ] Checks for cancellation before each step
- [ ] Returns ReplayWaiting when signal encountered
- [ ] Returns ReplayCompleted when all steps done
- [ ] Returns ReplayFailed on step failure

### Tests
- [ ] Table-driven tests for History methods
- [ ] Replay of empty history (fresh workflow)
- [ ] Replay with partial history (resume)
- [ ] Replay with completed workflow (no-op)
- [ ] Replay with cancelled workflow (returns error)
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### History Tests
- Empty history returns correct defaults
- History with workflow.started has correct input
- History with step.completed returns output
- History with step.failed (will_retry=true) not fatal
- History with step.failed (will_retry=false) is fatal
- History with branch.evaluated returns choice
- History with signal events tracks state correctly
- Cancelled workflow is detected

### Replayer Tests
- Fresh execution generates workflow.started event
- Completed steps are skipped (output from history)
- Pending steps are executed
- Step failure generates step.failed event
- Sequential steps execute in order
- Events have correct sequence numbers
- Cancellation is checked at step boundaries

---

## Dependencies
- Phase 1: Events & Storage

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors, don't panic
- Wrap errors with context
- Table-driven tests
- Test behavior, not implementation
