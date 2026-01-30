# Feature: Queries & Observability

## Overview

Query APIs allow inspecting workflow state, history, and metrics. This is Phase 6 of the implementation.

---

## Decisions

### GetRun Reconstructs from Events

**Decision:** `GetRun` loads events and reconstructs run status.

**Rationale:**
- Events are source of truth
- No separate "runs" table needed
- Status is derived: pending events → running, completion event → completed, etc.

```go
type Run struct {
    RunID           string
    WorkflowName    string
    WorkflowVersion int
    Status          RunStatus      // Running, Completed, Failed, Cancelled, Waiting
    StartedAt       time.Time
    CompletedAt     *time.Time
    Error           string         // If failed
    WaitingSignal   string         // If waiting
    CurrentState    json.RawMessage
}

type RunStatus string
const (
    RunStatusRunning   RunStatus = "running"
    RunStatusCompleted RunStatus = "completed"
    RunStatusFailed    RunStatus = "failed"
    RunStatusCancelled RunStatus = "cancelled"
    RunStatusWaiting   RunStatus = "waiting"
)
```

### GetHistory Returns Raw Events

**Decision:** `GetHistory(runID)` returns the raw event stream.

**Rationale:**
- Full visibility into execution
- Enables debugging and audit
- Clients can filter/process as needed

### ListRuns with Filters

**Decision:** `ListRuns(filter)` supports common query patterns.

```go
type RunFilter struct {
    WorkflowName    string
    Status          RunStatus
    StartedAfter    *time.Time
    StartedBefore   *time.Time
    Tags            []string
    Limit           int
    Offset          int
}
```

### Time-Travel via GetStateAt

**Decision:** `GetStateAt(runID, sequence)` returns state at a specific point.

**Rationale:**
- Debugging: "what was state when step X ran?"
- Audit: "what was the order total at approval time?"
- Derived from events up to sequence

---

## Query APIs

### Core Queries

```go
// GetRun returns the current state of a workflow run.
GetRun(ctx context.Context, runID string) (*Run, error)

// ListRuns returns runs matching the filter.
ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error)

// GetHistory returns all events for a run.
GetHistory(ctx context.Context, runID string) ([]Event, error)
```

### Time-Travel Queries

```go
// GetStateAt returns the state at a specific event sequence.
GetStateAt(ctx context.Context, runID string, sequence int64) (json.RawMessage, error)

// GetStateAtTime returns the state at a specific timestamp.
GetStateAtTime(ctx context.Context, runID string, t time.Time) (json.RawMessage, error)
```

### Signal Queries

```go
// ListWaitingSignals returns signals waiting for delivery.
ListWaitingSignals(ctx context.Context, filter SignalFilter) ([]WaitingSignal, error)

type SignalFilter struct {
    RunID      string
    SignalName string
    TimeoutBefore *time.Time
}

type WaitingSignal struct {
    RunID      string
    SignalName string
    StepPath   string
    WaitingSince time.Time
    TimeoutAt    *time.Time
}
```

---

## Metrics

### Metrics Interface

**Decision:** Pluggable metrics interface for observability.

```go
type Metrics interface {
    // Workflow lifecycle
    WorkflowStarted(name string, tags map[string]string)
    WorkflowCompleted(name string, duration time.Duration, tags map[string]string)
    WorkflowFailed(name string, duration time.Duration, error string, tags map[string]string)

    // Step execution
    StepStarted(workflowName, stepPath string)
    StepCompleted(workflowName, stepPath string, duration time.Duration, attempt int)
    StepFailed(workflowName, stepPath string, error string, attempt int)

    // Signals
    SignalWaiting(workflowName, signalName string)
    SignalReceived(workflowName, signalName string, waitDuration time.Duration)
    SignalTimeout(workflowName, signalName string, waitDuration time.Duration)

    // Queue metrics
    JobsQueued(workflowName string, count int)
    JobsProcessing(count int)
}
```

### Derived Metrics from Events

**Decision:** Metrics can be derived by processing events.

**Rationale:**
- Events contain all timing information
- Post-hoc analysis possible
- Real-time metrics via event processing

---

## Open Questions

### 1. Runs Table vs Event-Only

**Question:** Should we maintain a separate runs table?

**Context:**
- Currently: derive run status from events
- Runs table: faster queries, but data duplication
- Event-only: simpler, but queries scan events

**Options:**
- A: Event-only (current plan)
- B: Runs table as materialized view
- C: Runs table updated on workflow start/complete
- D: Runs table as read model (async projection)

**Leaning toward:** D - async projection for query performance.

### 2. Pagination for GetHistory

**Question:** Should GetHistory support pagination?

**Context:**
- Long workflows have many events
- Loading all events could be expensive
- But most use cases need full history

**Options:**
- A: No pagination (load all)
- B: Optional pagination (limit/offset)
- C: Cursor-based pagination (after_sequence)
- D: Stream interface for large histories

**Leaning toward:** C - cursor-based is efficient for event streams.

### 3. Event Filtering

**Question:** Should GetHistory support filtering?

**Context:**
- "Show me only act events"
- "Show me events for step X"
- Reduces data transfer

**Options:**
- A: No filtering (return all, client filters)
- B: Type filter (only certain event types)
- C: Path filter (only certain step paths)
- D: Full query language

**Leaning toward:** B + C - type and path filters cover most needs.

### 4. Real-Time Subscriptions

**Question:** Should we support subscribing to workflow events?

**Context:**
- UI wants live updates
- Monitoring wants event stream
- Webhooks want notifications

**Options:**
- A: Not supported (poll GetRun)
- B: WebSocket subscription per run
- C: Event stream (SSE) for run
- D: Webhook callbacks on state changes

**Leaning toward:** D initially, add B/C for real-time UIs later.

### 5. Aggregate Queries

**Question:** Should we support aggregate queries?

**Context:**
- "How many workflows completed today?"
- "Average duration by workflow type"
- Analytics use cases

**Options:**
- A: Not supported (export to analytics system)
- B: Basic aggregates (counts, averages)
- C: Full OLAP queries

**Leaning toward:** B - basic aggregates, complex analytics via export.

### 6. State Search

**Question:** Should we support searching by state content?

**Context:**
- "Find all orders for customer X"
- State is JSON, searchable via JSONB
- But state is in events, not dedicated column

**Options:**
- A: Not supported (use application-level tracking)
- B: Search final state only
- C: Search any state snapshot
- D: Index specific state fields

**Leaning toward:** A - application tracks relationships, not workflow engine.

### 7. Retention Queries

**Question:** Should we support querying archived events?

**Context:**
- Old events might be archived (if we implement archival)
- Need to query historical data
- Performance vs completeness trade-off

**Options:**
- A: Only query hot storage
- B: Transparent archive access (slow but complete)
- C: Explicit archive queries (user knows it's slow)

**Leaning toward:** C - explicit is better than magic slowness.

### 8. GraphQL API

**Question:** Should we provide a GraphQL API?

**Context:**
- REST is simpler
- GraphQL allows flexible queries
- UI developers often prefer GraphQL

**Options:**
- A: REST only
- B: GraphQL only
- C: Both
- D: GraphQL as optional layer

**Leaning toward:** A initially, D if demand exists.

### 9. Metrics Timing

**Question:** When are metrics emitted - at event time or query time?

**Context:**
- Event time: real-time, requires event processing
- Query time: derived on demand, no real-time

**Options:**
- A: Event time (hooks emit metrics)
- B: Query time (GetRun computes metrics)
- C: Both (real-time + derived)

**Leaning toward:** A - hooks emit metrics at event time.

### 10. Step-Level Metrics

**Question:** What step metrics are most valuable?

**Context:**
- Duration per step
- Retry count per step
- Failure rate per step

**Options:**
- A: Duration only
- B: Duration + retries
- C: Duration + retries + success/failure rate
- D: All above + custom step metrics

**Leaning toward:** C - standard suite, custom via hooks.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| GetRun | Not implemented |
| ListRuns | Not implemented |
| GetHistory | Not implemented |
| GetStateAt | Not implemented |
| GetStateAtTime | Not implemented |
| ListWaitingSignals | Partially (history query exists) |
| Metrics interface | Not implemented |
| Runs projection | Not implemented |
