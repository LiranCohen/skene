# Feature: Runner (River Integration)

## Overview

The Runner is the main execution engine that integrates the replay engine with River job queue. This is Phase 5 of the implementation - the largest phase.

---

## Decisions

### River as Job Scheduler

**Decision:** River handles scheduling, retries, worker distribution, and transactions.

**Rationale:**
- Battle-tested PostgreSQL-based job queue
- Native transaction support (same DB as events)
- Built-in scheduled jobs for timeouts

### Same Transaction for Events + Jobs

**Decision:** Event append, job completion, and next job insertion happen atomically.

**Rationale:**
- No lost events (job completes but events weren't written)
- No duplicate execution (events written but job not completed)
- No orphaned jobs (next job only exists if events recorded)

```go
func executeJob(ctx, job) error {
    tx, _ := pool.Begin(ctx)
    defer tx.Rollback(ctx)

    events := eventStore.LoadTx(tx, runID)
    output := replayer.Replay(ctx, input)

    eventStore.AppendBatchTx(tx, runID, output.NewEvents)

    if hasMoreWork {
        riverClient.InsertTx(tx, nextJob)
    }

    return tx.Commit(ctx)
}
```

### Minimal Job Arguments

**Decision:** Jobs carry only RunID, WorkflowName, and Version.

**Rationale:**
- State reconstructed from events
- Jobs are cheap and recoverable
- Version needed to look up workflow definition

```go
type WorkflowJobArgs struct {
    RunID        string
    WorkflowName string
    Version      int
}
```

### Multi-Step Execution Per Job

**Decision:** Single job executes multiple synchronous steps until async boundary.

**Rationale:**
- Reduces job scheduling overhead
- Simple workflows may complete in one job
- Async boundaries: Signal, Chorus (when parallel execution needed)

```go
func executeJob(...) {
    for {
        step := findNextStep(...)
        if step.IsAsync() {
            break  // Stop at async boundary
        }
        executeStep(...)
    }
}
```

### Signal Timeouts via Scheduled Jobs

**Decision:** When signal.waiting is recorded, schedule SignalTimeoutJob at timeout time.

**Rationale:**
- No polling required
- Precise timing
- Scales with workflow count

### Workflow Cancellation

**Decision:** `CancelWorkflow(runID, reason)` appends event and cancels pending jobs.

**Flow:**
1. Check workflow is running
2. Append `workflow.cancelled` event
3. Cancel pending River jobs for this run
4. Return success

### Runner API Surface

```go
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
    GetHistory(ctx context.Context, runID string) ([]Event, error)

    // Batch operations
    BatchStart(ctx context.Context, requests []WorkflowStart) ([]string, error)
    BatchSendSignal(ctx context.Context, requests []SignalRequest) error

    // Scheduling
    ScheduleOnce(ctx context.Context, name string, input json.RawMessage, at time.Time) error
    ScheduleIn(ctx context.Context, name string, input json.RawMessage, delay time.Duration) error
}
```

---

## Job Types

### WorkflowJob

Main execution job. Loads events, replays, executes pending steps.

```go
type WorkflowJobArgs struct {
    RunID        string `json:"run_id"`
    WorkflowName string `json:"workflow_name"`
    Version      int    `json:"version"`
}
```

### SignalTimeoutJob

Checks if signal timed out, resumes workflow if so.

```go
type SignalTimeoutJobArgs struct {
    RunID      string `json:"run_id"`
    SignalPath string `json:"signal_path"`
}
```

### ScheduledStartJob

Starts a workflow at a scheduled time.

```go
type ScheduledStartJobArgs struct {
    WorkflowName string          `json:"workflow_name"`
    Input        json.RawMessage `json:"input"`
}
```

---

## Open Questions

### 1. Worker Affinity

**Question:** Should all jobs for a workflow run on the same worker?

**Context:**
- Currently: any worker picks up any job
- Affinity could improve cache locality
- But limits scalability and fault tolerance

**Options:**
- A: No affinity (current - any worker)
- B: Prefer same worker (soft affinity)
- C: Require same worker (hard affinity)

**Leaning toward:** A - no affinity. Stateless workers are simpler.

### 2. Job Priority

**Question:** Should workflows have priority levels?

**Context:**
- Some workflows are time-sensitive
- River supports job priorities
- Could affect fairness

**Options:**
- A: Single priority (FIFO)
- B: High/Normal/Low priorities
- C: Configurable per workflow
- D: Per-run priority in StartOptions

**Leaning toward:** D - per-run priority for flexibility.

### 3. Rate Limiting

**Question:** Should the runner support rate limiting?

**Context:**
- Workflows might overwhelm downstream systems
- Per-workflow-type limits useful
- River doesn't have native rate limiting

**Options:**
- A: No rate limiting (user's responsibility)
- B: Global rate limit on step execution
- C: Per-workflow-type rate limits
- D: External rate limiter integration

**Leaning toward:** A - handle at application level or in step logic.

### 4. Concurrent Run Limits

**Question:** Should we limit concurrent runs of same workflow type?

**Context:**
- Prevent runaway workflow creation
- Resource protection

**Options:**
- A: No limit
- B: Configurable per workflow type
- C: Global limit across all types
- D: Use River's unique jobs feature

**Leaning toward:** B - per-type limits are useful.

### 5. Job Retry Policy

**Question:** What retry policy for workflow jobs?

**Context:**
- Step failure is handled by Act's RetryPolicy
- Job failure (infra errors) needs River retry
- Different from step-level retry

**Options:**
- A: River defaults (24 attempts over days)
- B: Short retry (3 attempts, fast backoff)
- C: Configurable per workflow
- D: No retry (rely on step-level idempotency)

**Leaning toward:** B - short retry for infra issues, step retry for logic.

### 6. Workflow Execution Timeout

**Question:** Should workflows have overall execution timeouts?

**Context:**
- Individual steps have timeouts
- But workflow could run forever (Signal waits)
- Some workflows need total deadline

**Options:**
- A: No overall timeout (current)
- B: Optional RunTimeout on Root
- C: Configurable in StartOptions
- D: Check at step boundaries

**Leaning toward:** C - optional in StartOptions.

### 7. Dead Letter Queue

**Question:** What happens to permanently failed jobs?

**Context:**
- Job fails all retries
- Workflow is stuck
- Need visibility and recovery

**Options:**
- A: Mark workflow as failed, done
- B: Move to DLQ for manual inspection
- C: Alert/webhook on permanent failure
- D: All of the above

**Leaning toward:** D - multiple signals for failed workflows.

### 8. Graceful Shutdown

**Question:** How to handle in-flight jobs during shutdown?

**Context:**
- Runner.Stop() called
- Jobs are executing
- Need clean shutdown

**Options:**
- A: Wait for all jobs to complete (could take forever)
- B: Wait with timeout, then cancel
- C: Let River handle (jobs become available for other workers)
- D: Configurable shutdown behavior

**Leaning toward:** B with reasonable timeout (30s).

### 9. Metrics Collection

**Question:** What metrics should the runner expose?

**Context:**
- Observability for production
- Performance tuning
- Alerting

**Options:**
- A: None (user instruments at app level)
- B: Basic: workflow started/completed/failed counts
- C: Detailed: step durations, queue depths, retry counts
- D: Pluggable metrics interface

**Leaning toward:** D - pluggable interface with default implementations.

### 10. Event Store Injection

**Question:** Should event store be injected or constructed?

**Context:**
- Runner needs EventStore
- Also needs same connection pool as River

**Options:**
- A: Inject EventStore (user constructs)
- B: Construct from pool (runner creates pgstore)
- C: Inject pool, runner creates store internally
- D: Accept either interface or pool

**Leaning toward:** A - inject EventStore for flexibility.

### 11. Transaction Isolation

**Question:** What isolation level for job execution?

**Context:**
- Load events, execute, append events
- Another job might be running for same workflow (shouldn't happen, but...)
- Sequence conflict is safety net

**Options:**
- A: Read Committed (default, relies on sequence check)
- B: Serializable (prevents all races)
- C: Repeatable Read (middle ground)

**Leaning toward:** A - Read Committed with sequence conflicts is sufficient.

### 12. Execution Context

**Question:** What context information should be available during execution?

**Context:**
- RunID, WorkflowName, Version are obvious
- StepPath, Attempt for logging
- What else?

**Existing:** `RunContext`, `StepContext`, `ExecutionContext`

**Open:** Should we add `IsReplaying` to context?

**Leaning toward:** Yes - helps hooks and logging distinguish replay from execution.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Runner struct | Not implemented |
| Start/Stop lifecycle | Not implemented |
| StartWorkflow | Not implemented |
| StartWorkflowTx | Not implemented |
| Job execution handler | Not implemented |
| Multi-step execution | Not implemented |
| SendSignal | Not implemented |
| CancelWorkflow | Not implemented |
| SignalTimeoutJob | Not implemented |
| GetRun | Not implemented |
| ListRuns | Not implemented |
| GetHistory | Not implemented |
| BatchStart | Not implemented |
| BatchSendSignal | Not implemented |
| ScheduleOnce/In | Not implemented |
