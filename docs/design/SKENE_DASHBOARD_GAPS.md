# Skene Dashboard Integration

This document describes how to integrate dashboards with Skene's event-sourced workflow engine.

## Philosophy

> "The bigger the interface, the weaker the abstraction." — Rob Pike

Skene's core is an event-sourced execution engine. Dashboard support is provided through **composition, not modification**:

- **Core (`event/`, `workflow/`)** — Small, focused interfaces. Does one thing well.
- **Projections (`project/`)** — Pure functions that transform `[]event.Event` into dashboard views.
- **Query interfaces (`query/`)** — Optional interfaces stores can implement for optimized queries.

This separation keeps the core clean while enabling rich dashboard functionality.

---

## Solution Summary

| Original Gap | Solution | Location |
|--------------|----------|----------|
| Run queries with pagination | `query.RunFilter`, `query.RunCounter` | `query/query.go` |
| Step invocations (history, timing) | `project.StepInvocations()` | `project/project.go` |
| Step counts (batch query) | `project.StepCounts()` | `project/project.go` |
| Step logs (structured logging) | Out of scope — use `slog` | N/A |
| Child workflow support | `project.ChildWorkflows()`, `query.ChildQuerier` | `project/`, `query/` |
| Entity correlation | `Event.Metadata` convention | See below |
| UI events (real-time updates) | `EventStore.LoadSince()` | Already exists |
| Retry from step / skip step | Future: Fork primitive | Not implemented |

---

## Usage Examples

### Projecting Run Status

```go
import "github.com/lirancohen/skene/project"

events, _ := store.Load(ctx, runID)
status := project.RunStatus(events)

fmt.Printf("Workflow: %s, Status: %s\n", status.WorkflowName, status.Status)
if status.Error != nil {
    fmt.Printf("Error: %s\n", *status.Error)
}
```

### Projecting Step Invocations

```go
invocations := project.StepInvocations(events)

for _, inv := range invocations {
    fmt.Printf("Step: %s, Attempt: %d, Status: %s\n",
        inv.StepName, inv.Attempt, inv.Status)
    if inv.DurationMs != nil {
        fmt.Printf("  Duration: %dms\n", *inv.DurationMs)
    }
}
```

### Projecting Step Counts

```go
counts := project.StepCounts(events)

for stepName, c := range counts {
    fmt.Printf("%s: %d completed, %d failed, %d running\n",
        stepName, c.Completed, c.Failed, c.Running)
}
```

### Projecting Child Workflows

```go
children := project.ChildWorkflows(events)

for _, child := range children {
    fmt.Printf("Child: %s (%s) - %s\n",
        child.ChildRunID, child.WorkflowName, child.Status)
}
```

### Building a Timeline

```go
timeline := project.Timeline(events)

for _, entry := range timeline {
    fmt.Printf("[%s] %s: %s\n",
        entry.Timestamp.Format(time.RFC3339), entry.Type, entry.Message)
}
```

### Using Optional Query Interfaces

```go
// Check if store supports counting (for pagination)
if counter, ok := store.(query.RunCounter); ok {
    total, err := counter.CountRuns(ctx, query.RunFilter{
        WorkflowType: "order-processing",
        Status:       "completed",
    })
    // Use total for pagination UI
}

// Check if store supports entity queries
if querier, ok := store.(query.EntityQuerier); ok {
    runIDs, err := querier.QueryByEntity(ctx, "user", "user-123")
    // Load events for each runID
}

// Check if store supports parent/child queries
if querier, ok := store.(query.ChildQuerier); ok {
    childIDs, err := querier.QueryChildren(ctx, parentRunID)
}
```

---

## Entity Correlation via Metadata

Skene does not add `EntityID` or `EntityType` fields to the `Event` struct. Instead, use the existing `Metadata` field with conventional keys:

```go
// When starting a workflow, include entity metadata
event := event.Event{
    Type:     event.EventWorkflowStarted,
    RunID:    runID,
    Metadata: map[string]string{
        "entity_type": "order",
        "entity_id":   "order-12345",
    },
    // ...
}
```

Dashboard code can extract this from the first event:

```go
events, _ := store.Load(ctx, runID)
if len(events) > 0 {
    entityType := events[0].Metadata["entity_type"]
    entityID := events[0].Metadata["entity_id"]
}
```

For optimized queries, implement `query.EntityQuerier` in your store.

---

## Real-Time Updates

Use `EventStore.LoadSince()` for polling-based real-time updates:

```go
// Dashboard polls every second
lastSeq := int64(0)
for {
    events, err := store.LoadSince(ctx, runID, lastSeq)
    if err != nil {
        // Handle error
    }

    for _, e := range events {
        sendToClient(e) // SSE, WebSocket, etc.
        lastSeq = e.Sequence
    }

    time.Sleep(time.Second)
}
```

This is simpler and more reliable than callbacks.

---

## What We Didn't Do (And Why)

### Did not add batch methods to EventStore

The core `EventStore` interface stays minimal:
- `Append(ctx, event) error`
- `Load(ctx, runID) ([]Event, error)`
- `LoadSince(ctx, runID, afterSeq) ([]Event, error)`

Batch operations like "get step counts for 100 workflows" are dashboard concerns. Dashboards can:
1. Load events and use projection functions (simple, works everywhere)
2. Type-assert to optional `query.*` interfaces (optimized, store-specific)

### Did not add entity fields to Event

Entity correlation is **application metadata**, not core workflow data. The `Metadata map[string]string` field handles this cleanly without changing the schema.

### Did not add logging to events

Logging and event sourcing are separate concerns:
- **Events** — Source of truth for workflow state. Enables replay.
- **Logs** — Debug/observability. Use structured logging (`slog`).

Mixing them would bloat the event store and complicate replay.

### Did not add callbacks for real-time

Polling with `LoadSince()` is simpler:
- No callback registration lifecycle
- No delivery guarantees to manage
- Dashboard controls its own polling interval
- Works across process boundaries

---

## Future: Retry from Step

The "retry from step" feature requires a **fork primitive**:

```go
// Conceptual API (not implemented)
newRunID := workflow.Fork(ctx, runID, fromStep)
```

This would:
1. Load events up to `fromStep`
2. Start a new run with those events as history
3. Resume execution from `fromStep`

This is complex and requires careful design. Current workaround: retry the entire workflow.
