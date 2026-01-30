# Feature: Events & Storage

## Overview

Events are the source of truth for workflow execution. This document covers the event model, storage format, and store interfaces.

---

## Decisions

### Event Structure

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

### Event Types

| Category | Events |
|----------|--------|
| Workflow | `workflow.started`, `workflow.completed`, `workflow.failed`, `workflow.cancelled` |
| Step | `step.started`, `step.completed`, `step.failed` |
| Branch | `branch.evaluated` |
| Signal | `signal.waiting`, `signal.received`, `signal.timeout` |
| Child | `child.spawned`, `child.completed`, `child.failed` |
| Map | `map.started`, `map.completed`, `map.failed` |
| Compensation | `compensation.started`, `compensation.completed`, `compensation.failed` |
| Snapshot | `snapshot` |

### Storage Format: JSON

**Decision:** Use JSON for event data.

**Rationale:**
- Human-readable, debuggable
- Easy to query with PostgreSQL JSONB
- Sufficient performance for workflow use cases
- Can optimize to binary format later if needed

### Output on Completion Events Only

**Decision:** Store step `Output` only on `step.completed` events.

**Rationale:**
- Enables retrieval of any step's output via `Step.Output(ctx)`
- Outputs are typed - JSON deserializes to the step's declared type
- Failed events don't have output (step didn't produce one)

### Sequence Numbers for Ordering

**Decision:** Use monotonically increasing sequence numbers within a run.

**Rationale:**
- Simple, unambiguous ordering
- Enables optimistic concurrency (append fails if sequence != expected)
- No need for vector clocks or timestamps for ordering

### Version Field for Schema Evolution

**Decision:** Include `Version` field on every event.

**Rationale:**
- Forward compatibility when event schema changes
- Old events can still be parsed
- Defensive deserialization: ignore unknown fields, handle missing with defaults

### StepName as Simple String

**Decision:** Use simple step names like `"validate"` or `"charge"`.

**Rationale:**
- With DAG model, steps are flat (not nested trees)
- Simple lookup by name
- Matches step handle declaration: `Step[T]("validate", fn)`
- Child workflows have their own event streams with different RunID

### Output Size Guidance

**Decision:** Keep step outputs under 100KB.

**Rationale:**
- Large outputs increase storage costs and replay time
- For large data, store references (IDs, URLs) not data itself
- This is guidance, not enforced

---

## Store Interfaces

### EventStore (Core)

```go
type EventStore interface {
    Append(ctx context.Context, event Event) error
    AppendBatch(ctx context.Context, events []Event) error
    Load(ctx context.Context, runID string) ([]Event, error)
    LoadSince(ctx context.Context, runID string, afterSequence int64) ([]Event, error)
    GetLastSequence(ctx context.Context, runID string) (int64, error)
}
```

### TxEventStore (Transactional)

```go
type TxEventStore interface {
    EventStore
    AppendTx(ctx context.Context, tx pgx.Tx, event Event) error
    AppendBatchTx(ctx context.Context, tx pgx.Tx, events []Event) error
    LoadTx(ctx context.Context, tx pgx.Tx, runID string) ([]Event, error)
    LoadSinceTx(ctx context.Context, tx pgx.Tx, runID string, afterSequence int64) ([]Event, error)
}
```

**Why TxEventStore?** Enables atomic operations with River jobs:
- Append events + complete job + insert next job = single transaction
- Prevents lost events or orphaned jobs

### SnapshotStore

```go
type SnapshotStore interface {
    SaveSnapshot(ctx context.Context, runID string, sequence int64, state json.RawMessage) error
    LoadLatestSnapshot(ctx context.Context, runID string) (*Snapshot, error)
}
```

---

## Implementations

### PostgreSQL (Production)

- Uses `pgxpool.Pool` for connection pooling
- Uses `pgx.Tx` for transactions
- Optimistic concurrency via `(run_id, sequence)` unique constraint
- Batch operations using `pgx.Batch`

### Memory (Testing)

- Thread-safe map with mutex
- Validates sequence numbers
- No persistence

---

## Open Questions

### 1. Event Compression

**Question:** Should we compress event data for storage?

**Context:**
- JSON is verbose
- StateAfter can be large (up to 100KB guidance)
- Compression adds CPU overhead

**Options:**
- A: No compression (simple, debuggable)
- B: Compress StateAfter only (biggest wins)
- C: Compress entire event (maximum savings)
- D: Make it configurable

**Leaning toward:** A (no compression) initially, add later if needed.

### 2. Event Archival

**Question:** Should old events be archived to cold storage?

**Context:**
- Event history grows unbounded
- PostgreSQL performance degrades with huge tables
- Some workflows need long-term audit trails

**Options:**
- A: Keep all events in PostgreSQL forever
- B: Archive to S3/GCS after retention period
- C: Partition by time, drop old partitions
- D: Separate hot (recent) and cold (archived) stores

**Leaning toward:** A initially. Add archival in Phase 6 if needed.

### 3. Event Immutability Enforcement

**Question:** How strictly do we enforce event immutability?

**Context:**
- Events should never be modified after write
- But bugs happen, migrations may be needed
- GDPR may require data deletion

**Options:**
- A: Database-level enforcement (no UPDATE/DELETE permissions)
- B: Application-level enforcement only
- C: Soft deletes (mark as deleted, don't remove)
- D: Allow modifications with audit trail

**Leaning toward:** B with logging. True immutability is a goal, not absolute.

### 4. Cross-Run Event Queries

**Question:** Should we support querying events across multiple runs?

**Context:**
- "Find all workflows that failed at step X"
- "Show all runs for customer Y"
- Useful for debugging and analytics

**Options:**
- A: Not supported (query individual runs only)
- B: Basic filtering by type, time range
- C: Full query capability with indexes
- D: Export to analytics system for complex queries

**Leaning toward:** B with good indexes. Complex analytics via export.

### 5. Event Metadata Schema

**Question:** What should be standardized in the Metadata field?

**Context:**
- Trace IDs for distributed tracing
- Correlation IDs for request tracking
- User IDs for audit
- Currently unstructured map[string]string

**Options:**
- A: Keep unstructured, document conventions
- B: Define standard fields (traceId, correlationId, userId)
- C: Typed metadata struct with optional extension map

**Leaning toward:** B - define conventions, don't enforce.

### 6. Binary Event IDs

**Question:** Should Event IDs use UUID or ULID?

**Context:**
- UUIDs are random, don't sort by time
- ULIDs are time-sortable, slightly larger
- Currently using UUIDs

**Options:**
- A: Keep UUIDs (simple, universal)
- B: Switch to ULIDs (time-sortable)
- C: Use sequence as primary, UUID for external reference

**Leaning toward:** A - sequence is our ordering mechanism, UUID is fine for identity.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Event struct | Needs update for new model |
| Event types | Needs update for typed steps |
| Event data structs | Needs update |
| EventStore interface | Complete |
| TxEventStore interface | Complete |
| PostgreSQL implementation | Complete |
| Memory implementation | Complete |
| SnapshotStore interface | Needs update for outputs |
| Snapshot implementation | Needs update |

**Note:** Existing implementations work but need schema updates for the typed step model (Output instead of StateAfter, StepName instead of StepPath, new event types).
