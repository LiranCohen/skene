# Phase 4: Parallel Execution

## Goal
Implement parallel execution of independent steps (steps without dependencies between them) in the replay engine.

## Primary Design Document
- `docs/design/04-CHORUS.md`

## Related Documents
- `docs/design/03-REPLAY.md` (replay engine)
- `docs/design/02-WORKFLOW-MODEL.md` (workflow model)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement/Extend

### Parallel Execution in Replayer (`workflow/replay.go`)

Extend the Replayer to execute independent steps concurrently:

```go
// findReadySteps returns steps whose dependencies are all complete
func (r *Replayer) findReadySteps(completed map[string]bool) []StepNode

// executeParallel runs multiple steps concurrently
func (r *Replayer) executeParallel(ctx context.Context, steps []StepNode) ([]stepResult, error)

// stepResult captures the outcome of parallel step execution
type stepResult struct {
    stepName string
    output   any
    err      error
    events   []event.Event
}
```

### Parallel Execution Loop

```go
func (r *Replayer) replayParallel(ctx context.Context) (*ReplayOutput, error) {
    completed := make(map[string]bool)

    // Mark already-completed steps from history
    for _, stepName := range r.history.CompletedSteps() {
        completed[stepName] = true
    }

    for {
        ready := r.findReadySteps(completed)
        if len(ready) == 0 {
            break
        }

        results, err := r.executeParallel(ctx, ready)
        if err != nil {
            return nil, err
        }

        for _, result := range results {
            if result.err != nil {
                // Handle failure
                return r.handleStepFailure(result)
            }
            completed[result.stepName] = true
            // Collect events
        }
    }

    return r.buildOutput(completed)
}
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/replay.go` | Extended with parallel execution |

---

## Acceptance Criteria

### Parallel Execution
- [ ] Steps without dependencies execute concurrently
- [ ] Steps with dependencies wait for all deps to complete
- [ ] goroutines are properly managed (sync.WaitGroup)
- [ ] Context cancellation propagates to all goroutines
- [ ] First failure cancels remaining steps (fail-fast)
- [ ] Events from parallel steps are collected
- [ ] Events have correct sequence numbers (serialized)

### Failure Modes
- [ ] FailFast: cancel siblings on first failure (default)
- [ ] All parallel events recorded before returning error
- [ ] Completed steps' outputs preserved

### Tests
- [ ] Two independent steps run in parallel
- [ ] Three+ independent steps run in parallel
- [ ] Mixed: some parallel, some sequential
- [ ] One parallel step fails, others cancelled
- [ ] All parallel steps complete successfully
- [ ] Event ordering is deterministic
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### Parallel Execution Tests
- Two steps with no dependencies run concurrently
- Step with dependency waits for dependency
- Diamond pattern: A -> (B, C) -> D
- Verify concurrent execution (use timing or channels)

### Failure Tests
- One of three parallel steps fails
- Verify other steps are cancelled
- Verify completed step outputs preserved
- Verify failure events recorded

### Event Ordering Tests
- Parallel steps produce events with sequential numbers
- Event order is deterministic across replays
- Events are serialized correctly despite parallel execution

### Integration Tests
- Complex workflow with mixed parallel/sequential
- Replay partial execution (some parallel steps done)
- Full replay from completed history

---

## Dependencies
- Phase 1: Events & Storage
- Phase 2: Replay Engine
- Phase 3: Typed Step Model

---

## Implementation Notes

### Sequence Number Assignment
Events from parallel steps must have sequential sequence numbers. Options:
1. Collect all events, then assign sequences (recommended)
2. Use mutex-protected counter during parallel execution

### Goroutine Management
- Use errgroup or sync.WaitGroup
- Ensure all goroutines complete before returning
- Propagate context cancellation

### Event Collection
- Each goroutine produces events into a channel or slice
- Main goroutine collects and orders events
- Assign sequence numbers after collection

---

## Code Quality Reminders (from CLAUDE.md)
- Use channels for coordination, mutexes for state
- Document concurrency requirements in comments
- Table-driven tests
- Test behavior under race detector
