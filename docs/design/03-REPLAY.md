# Feature: Replay Engine

## Overview

The replay engine executes workflows by walking the static DAG, skipping completed steps (restoring outputs from events), and executing pending steps. This is the core of the event-sourced execution model.

---

## Decisions

### DAG Walking

**Decision:** Walk the workflow DAG in topological order, executing ready steps.

**Rationale:**
- Workflows are static DAGs of typed step handles
- Structure is known at compile time - can determine execution order
- A step is "ready" when all its dependencies have completed
- Steps without dependencies can run immediately (in parallel)

```go
func replayDAG(ctx context.Context, dag *DAG, history *History) error {
    for _, step := range dag.TopologicalOrder() {
        if history.HasCompleted(step.Name) {
            continue // Output already in history
        }
        if !allDependenciesComplete(step, history) {
            continue // Not ready yet
        }
        output, err := executeStep(ctx, step, history)
        if err != nil {
            return err
        }
        history.RecordOutput(step.Name, output)
    }
    return nil
}
```

### Output Restoration from Events

**Decision:** Restore step outputs by unmarshaling from `step.completed` events.

**Rationale:**
- No need to re-execute completed steps
- Outputs are typed - deserialize to the correct type
- Simple JSON unmarshal to `Step[T]`'s type parameter

### Event Generation During Replay

**Decision:** Replayer generates new events as it executes, returned in `ReplayOutput.NewEvents`.

**Rationale:**
- Caller is responsible for persisting events (enables batching, transactions)
- Replayer stays pure (no I/O except step execution)
- Events have auto-incremented sequence numbers

### Cancellation Checking at Step Boundaries

**Decision:** Check for workflow cancellation before executing each step.

**Rationale:**
- Cancellation is recorded as `workflow.cancelled` event
- Checking at boundaries allows graceful exit
- Steps in progress complete, but no new steps start

```go
func replayStep(ctx context.Context, step StepNode, history *History) error {
    if history.IsCancelled() {
        return ErrWorkflowCancelled
    }
    // ... continue
}
```

### Context Population

**Decision:** Populate `workflow.Context` with all completed step outputs before executing each step.

**Rationale:**
- Step functions call `Step.Output(ctx)` to read prior outputs
- All dependencies are guaranteed complete (topological order)
- Outputs are pre-loaded and type-safe

```go
func executeStep(ctx context.Context, step StepNode, history *History) (any, error) {
    // Build workflow context with access to all prior outputs
    wctx := newWorkflowContext(ctx, history)

    // Execute the step function
    return step.Execute(wctx)
}
```

### Replayer Configuration

```go
type ReplayerConfig struct {
    Workflow     *WorkflowDef  // Static workflow definition (DAG)
    History      *History      // Events to replay (nil = fresh execution)
    RunID        string        // Workflow run ID
    Input        any           // Workflow input (typed by caller)
    Logger       Logger        // Logging
}
```

### ReplayOutput Structure

```go
type ReplayOutput struct {
    Result        ReplayResult      // Completed, Waiting, Failed
    FinalOutput   any               // Output of final step (when completed)
    Error         error             // On failure
    WaitingSignal string            // On waiting for signal
    NewEvents     []Event           // Generated during replay
    NextSequence  int64             // For next event append
}
```

---

## Replay Logic by Component

### Step Execution

1. Check if step is in history (`step.completed`) - load output, return
2. Check if step failed (`step.failed` with `will_retry=false`) - return error
3. Verify all dependencies are complete
4. Build workflow context with prior outputs
5. Execute step function with timeout
6. On success: append `step.completed` with output
7. On failure: append `step.failed`, apply retry policy or return error

```go
func replayStep(ctx context.Context, step StepNode, history *History) (any, error) {
    // 1. Already completed?
    if output, ok := history.GetOutput(step.Name); ok {
        return output, nil
    }

    // 2. Previously failed without retry?
    if err := history.GetFatalError(step.Name); err != nil {
        return nil, err
    }

    // 3-4. Build context with dependencies
    wctx := buildContext(ctx, step.Dependencies, history)

    // 5-7. Execute
    output, err := step.Execute(wctx)
    if err != nil {
        // Handle retry or return error
        return nil, err
    }
    return output, nil
}
```

### Parallel Execution

Steps without dependencies between them can run in parallel:

1. Identify "ready" steps (all dependencies complete, not yet executed)
2. Execute ready steps concurrently
3. Record outputs as each completes
4. Repeat until no more steps are ready

```go
func replayParallel(ctx context.Context, dag *DAG, history *History) error {
    for {
        ready := findReadySteps(dag, history)
        if len(ready) == 0 {
            break
        }

        var wg sync.WaitGroup
        for _, step := range ready {
            wg.Add(1)
            go func(s StepNode) {
                defer wg.Done()
                output, err := replayStep(ctx, s, history)
                // Record output or error
            }(step)
        }
        wg.Wait()
    }
    return nil
}
```

### Branch (Conditional)

1. Check `branch.evaluated` - use recorded choice (deterministic!)
2. If not evaluated: run selector, record choice
3. Append `branch.evaluated` with selected branch name
4. Execute only the selected branch's step

**Critical:** Always use recorded branch on replay, never re-evaluate selector.

```go
func replayBranch(ctx context.Context, branch BranchNode, history *History) error {
    // Use recorded decision for determinism
    if choice, ok := history.GetBranchChoice(branch.Name); ok {
        return replayStep(ctx, branch.Cases[choice], history)
    }

    // First execution: evaluate selector
    wctx := buildContext(ctx, branch.Dependencies, history)
    choice := branch.Select(wctx)

    // Record for future replay
    appendEvent(BranchEvaluatedEvent{Branch: branch.Name, Choice: choice})

    return replayStep(ctx, branch.Cases[choice], history)
}
```

### Signal (Wait Point)

1. Check `signal.received` - load payload, continue
2. Check `signal.timeout` - return timeout error
3. Check `signal.waiting` - return waiting marker
4. If not in history: append `signal.waiting`, return waiting marker

Signal wait points are implemented within step functions:

```go
func awaitApproval(ctx workflow.Context) (*ApprovalOutput, error) {
    // This blocks until signal received or timeout
    payload, err := workflow.WaitForSignal(ctx, "approval", 24*time.Hour)
    if err != nil {
        return nil, err // Timeout or cancellation
    }

    var approval ApprovalData
    json.Unmarshal(payload, &approval)
    return &ApprovalOutput{ApprovedBy: approval.ApprovedBy}, nil
}
```

### Child Workflows (Map)

1. Check `map.completed` - load aggregated results, return
2. Check each `child.completed` - skip completed children
3. Spawn jobs for remaining children
4. Wait for all children to complete
5. Aggregate results
6. Append `map.completed` with results

```go
func replayMap(ctx context.Context, items []any, childWorkflow *WorkflowDef, history *History) ([]any, error) {
    results := make([]any, len(items))

    for i, item := range items {
        childID := fmt.Sprintf("%s-child-%d", history.RunID, i)

        if output, ok := history.GetChildOutput(childID); ok {
            results[i] = output
            continue
        }

        // Spawn child workflow
        output, err := spawnChild(ctx, childWorkflow, item, childID)
        if err != nil {
            return nil, err
        }
        results[i] = output
    }

    return results, nil
}
```

---

## Open Questions

### 1. Replay Hooks

**Question:** Should hooks fire during replay?

**Context:**
- Hooks are used for logging, metrics, tracing
- Re-firing hooks on replay could produce duplicate metrics
- But some hooks (logging) might be useful during replay

**Options:**
- A: No hooks during replay
- B: All hooks fire with `IsReplaying: true` flag
- C: Separate hook interface for replay-aware hooks
- D: Configurable per hook

**Current Decision:** B (hooks fire with `IsReplaying: true`)

**Open:** Is this the right choice? Should metrics be suppressed?

### 2. Partial Replay for Debugging

**Question:** Should we support "replay up to step X" for debugging?

**Context:**
- Useful for debugging: "show me outputs after step 3"
- Could enable "what-if" scenarios
- Adds complexity to replay engine

**Options:**
- A: Not supported (always replay to current position)
- B: Support `ReplayUntil(stepName)` option
- C: Implement via GetOutputAt query

**Leaning toward:** C - implement as query, not replay option.

### 3. Output Caching During Replay

**Question:** Should we cache deserialized outputs during replay?

**Context:**
- Same output might be read by multiple dependent steps
- Deserializing from JSON each time is wasteful
- But caching adds memory overhead

**Options:**
- A: No caching (deserialize on each `Step.Output(ctx)` call)
- B: Cache in workflow context (deserialize once per step per replay)
- C: Cache in history (persistent across replays)

**Decision:** B - cache in context for duration of replay.

### 4. Concurrent Replay Safety

**Question:** Can multiple replayers run for the same workflow concurrently?

**Context:**
- Signal delivery might trigger replay
- Timeout handling might trigger replay
- Need to prevent duplicate execution

**Options:**
- A: Rely on sequence number conflicts (optimistic)
- B: Distributed lock before replay
- C: Single-worker-per-run affinity in River

**Current approach:** A (sequence conflicts). Is this sufficient?

### 5. Error Reconstruction Fidelity

**Question:** How much error detail should be preserved in events?

**Context:**
- Currently store error.Error() string
- Loses type information, stack traces
- But errors may contain sensitive info

**Options:**
- A: String only (current)
- B: Structured error with type, message, retryable flag
- C: Full serialization including wrapped errors
- D: Error code + message

**Leaning toward:** B - structured but not full serialization.

### 6. Step Timeout Handling

**Question:** How should step-level timeouts work during replay?

**Context:**
- Step has 30s timeout
- On original execution, completed in 10s
- On replay, step is skipped (output loaded from history)

**Options:**
- A: Timeouts don't apply to replayed (skipped) steps
- B: Original duration recorded but not enforced on replay
- C: Timeout applies only to pending steps

**Decision:** A - skipped steps don't consume timeout.

### 7. Input Capture for Auditing

**Question:** Should we capture the inputs to each step explicitly?

**Context:**
- With explicit I/O, we know exactly what each step reads
- Inputs are the outputs of prior steps (already captured)
- Explicit input capture would help auditing ("what did this step see?")

**Options:**
- A: No explicit input capture (derive from prior step outputs)
- B: Capture step inputs in `step.started` event
- C: Capture only workflow input, derive rest

**Decision:** A - with explicit I/O model, inputs are determinable from prior outputs.

### 8. Type Safety Across Serialization

**Question:** How to ensure type safety when deserializing step outputs?

**Context:**
- Step outputs are serialized to JSON in events
- On replay, we deserialize back to the declared type
- Type mismatches (e.g., workflow version change) could cause issues

**Options:**
- A: Trust the type declaration (panic on mismatch)
- B: Validate schema on deserialization
- C: Store type information in events for validation

**Leaning toward:** A for simplicity. Workflow versioning handles incompatible changes.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| DAG construction | Not started |
| Topological sort | Not started |
| Step execution | Not started |
| Output restoration | Not started |
| Context population | Not started |
| Parallel execution | Not started |
| Branch replay | Not started |
| Signal wait | Not started |
| Child workflows | Not started |
| History helper | Complete (needs update) |
| Cancellation checking | Complete (needs update) |
