# Skene Dashboard Support - Design Document

> **Note:** This is supplementary documentation. The actual ralph prompt is inline
> in `skene-dashboard.yml`. This document provides extended rationale and examples.

## Objective

Create dashboard support for Skene **without polluting the core**.

Skene is an event-sourced workflow execution engine. Its job is:
1. Execute workflows deterministically
2. Record events for crash recovery
3. Provide type-safe step composition

What it's NOT:
- A query engine for dashboards
- An application metadata store
- A logging framework

## The Rob Pike Approach

> "The bigger the interface, the weaker the abstraction."

Instead of adding dashboard methods to Skene's core, we create:

1. **`project/` package** - Pure functions that transform `[]event.Event` into dashboard-friendly structures
2. **`query/` package** - Optional interfaces that stores CAN implement (but don't have to)
3. **Documentation** on using `Event.Metadata` for application-specific data

## What We're Building

### project/ Package

Pure projection functions. No interfaces. No side effects.

```go
package project

// Transform events into step execution history
func StepInvocations(events []event.Event) []StepInvocation

// Extract child workflow information from events
func ChildWorkflows(events []event.Event) []ChildInfo

// Count completed and failed steps
func StepCounts(events []event.Event) StepCounts

// Determine workflow status from events
func WorkflowStatus(events []event.Event) Status

// Build a timeline for visualization
func Timeline(events []event.Event) []TimelineEntry
```

Why pure functions?
- Same input always produces same output
- Test with table-driven tests, no mocks
- Compose them: `Timeline(events)` can use `StepInvocations(events)` internally

### query/ Package (Optional)

Interfaces that stores can optionally implement:

```go
package query

// RunLister provides paginated run queries
// Stores that don't implement this can still work -
// dashboards just call EventStore.Load() and filter in-memory
type RunLister interface {
    ListRuns(ctx context.Context, opts ListOptions) ([]RunSummary, error)
}

type ListOptions struct {
    WorkflowName string
    Status       Status
    Limit        int
    Offset       int
}
```

### Entity Correlation (Convention, Not Code)

Dashboards often need to link workflows to entities (users, orders, etc.).

**Don't add EntityID to the core.** Use the existing `Event.Metadata`:

```go
// When creating events, applications add their own metadata
event.Metadata["entity_type"] = "order"
event.Metadata["entity_id"] = orderID.String()
event.Metadata["trace_id"] = traceID
```

Document this convention. Let applications handle their own metadata.

### Real-time Updates (Already Solved)

The gaps document asks for "UI events for SSE".

**This already exists.** `EventStore.LoadSince(ctx, runID, afterSequence)` is polling.

```go
// Dashboard polls for updates
func pollForUpdates(ctx context.Context, store EventStore, runID string, lastSeq int64) ([]event.Event, int64) {
    events, _ := store.LoadSince(ctx, runID, lastSeq)
    if len(events) > 0 {
        return events, events[len(events)-1].Sequence
    }
    return nil, lastSeq
}
```

Polling is simpler than callbacks. No channel leaks. Works everywhere.

### Structured Logging (Out of Scope)

**Logs are not events.** Don't add them to the event model.

| Events | Logs |
|--------|------|
| State transitions | Debug context |
| Must be durable | Can be ephemeral |
| Replayed on recovery | Never replayed |
| Small, finite | Large, verbose |

If steps need to log, they use Go's `slog` or a custom logger. Where those logs go is the application's concern.

## What We're NOT Building

1. **Batch methods on EventStore** - Implementation detail. Stores can add their own.
2. **EntityID/EntityType fields** - Application metadata. Use Event.Metadata.
3. **Logging in events** - Separate concern. Use slog.
4. **Real-time callbacks** - Polling via LoadSince is enough.
5. **Retry from step** - Complex. Maybe later with a Fork primitive.

## Success Criteria

1. `project/` package exists with pure projection functions
2. `query/` package exists with small, optional interfaces
3. All functions have table-driven tests
4. Core packages (`event/`, `workflow/`) are NOT modified (except docs)
5. `go test ./...` passes
6. Documentation explains the philosophy and usage

## The Philosophy

> "Skene's job is to execute workflows and record events.
>  Querying, projecting, and displaying those events is someone else's job."

Keep the core small. Let composition handle the rest.
