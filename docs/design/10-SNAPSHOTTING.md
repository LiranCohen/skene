# Feature: Snapshotting

## Overview

Snapshots provide periodic checkpoints for fast workflow resume. Instead of replaying all events from the beginning, load snapshot + events since snapshot.

---

## Decisions

### Inline Snapshot Creation

**Decision:** Create snapshots during job execution, not as background job.

**Rationale:**
- Simpler architecture (no separate snapshot job)
- Guaranteed consistency (snapshot in same transaction)
- Predictable timing

```go
func appendEvents(tx, runID, events, state) error {
    eventStore.AppendBatchTx(tx, runID, events)

    eventCount := getEventCount(tx, runID)
    if eventCount % snapshotInterval == 0 {
        appendSnapshot(tx, runID, state)
    }
    return nil
}
```

### Snapshot Interval

**Decision:** Default snapshot every 100 events (configurable).

**Rationale:**
- 100 events is typical for medium workflows
- Balances storage vs replay performance
- Configurable for specific needs

### Snapshot Contains Only State

**Decision:** Snapshot stores state, not tree position.

**Rationale:**
- Tree position is implicit in events
- Replay from snapshot walks tree, checking events
- Simpler snapshot format

```go
type Snapshot struct {
    RunID     string
    Sequence  int64           // Event sequence at snapshot
    State     json.RawMessage
    CreatedAt time.Time
}
```

### Replay with Snapshot

**Algorithm:**
1. Load latest snapshot for run
2. Load events since snapshot sequence
3. Create replayer with snapshot state as InitialState
4. Replay walks tree, skipping completed steps (events exist)
5. Execute pending steps

```go
func resumeWorkflow(ctx, runID) error {
    snapshot, _ := snapshotStore.LoadLatest(ctx, runID)

    var events []Event
    var initialState *S

    if snapshot != nil {
        events, _ = eventStore.LoadSince(ctx, runID, snapshot.Sequence)
        json.Unmarshal(snapshot.State, &initialState)
    } else {
        events, _ = eventStore.Load(ctx, runID)
        // initialState remains nil, Init will be called
    }

    replayer, _ := NewReplayer(ReplayerConfig{
        History:      NewHistory(events),
        InitialState: initialState,
        ...
    })

    return replayer.Replay(ctx)
}
```

### Snapshot as Event Type

**Decision:** Snapshots are stored as events with type `snapshot`.

**Rationale:**
- Single storage mechanism
- Events and snapshots interleaved naturally
- Sequence numbers provide ordering

```go
Event{
    Type: EventSnapshot,
    Data: SnapshotData{State: stateJSON},
}
```

---

## Open Questions

### 1. Snapshot Storage Separation

**Question:** Should snapshots be in separate storage from events?

**Context:**
- Currently: snapshots as events in same table
- Separate table: different retention policies
- Separate store: different storage backends

**Options:**
- A: Same table as events (current)
- B: Separate table, same database
- C: Separate store (e.g., S3)

**Leaning toward:** B - separate table allows different retention/indexing.

### 2. Snapshot Compaction

**Question:** Should we compact/prune old snapshots?

**Context:**
- Multiple snapshots accumulate
- Only latest is needed for resume
- Storage growth concern

**Options:**
- A: Keep all snapshots (audit trail)
- B: Keep only latest N snapshots
- C: Keep latest + time-based sampling
- D: Keep only latest

**Leaning toward:** D - keep only latest. Events provide history.

### 3. Snapshot on Signal Waiting

**Question:** Should we snapshot when workflow starts waiting for signal?

**Context:**
- Long signal waits (days, weeks)
- Current position is natural snapshot point
- Ensures fast resume when signal arrives

**Options:**
- A: No special handling (rely on interval)
- B: Always snapshot on signal.waiting
- C: Snapshot on signal.waiting if > N events since last

**Leaning toward:** B - signal waiting is a natural checkpoint.

### 4. Manual Snapshot Trigger

**Question:** Should users be able to trigger snapshots?

**Context:**
- "I know this step is expensive, snapshot after it"
- API: `deps.Checkpoint()` or similar

**Options:**
- A: No manual snapshots (automatic only)
- B: `deps.Checkpoint()` method
- C: `Checkpoint: true` on step definition

**Leaning toward:** B - explicit checkpoint method is clear.

### 5. Snapshot Validation

**Question:** Should we validate snapshots before using?

**Context:**
- Snapshot state might be corrupted
- Workflow definition might have changed
- Validation adds overhead

**Options:**
- A: No validation (trust storage)
- B: Checksum validation
- C: Schema validation
- D: Full validation (deserialize + verify)

**Leaning toward:** A - trust storage, rely on replay to catch issues.

### 6. Snapshot Size Limits

**Question:** Should we limit snapshot size?

**Context:**
- State size guidance: 100KB
- Large states slow down snapshots
- Storage costs

**Options:**
- A: No limit (guidance only)
- B: Warn above threshold
- C: Error above threshold
- D: Compress large snapshots

**Leaning toward:** B - warn but allow large snapshots.

### 7. Partial Replay Optimization

**Question:** Can we skip more replay with smarter snapshots?

**Context:**
- Snapshot at sequence 100
- Event 101 is scene.completed for "main"
- Could skip replaying "main" entirely

**Options:**
- A: Always walk tree (current - simple)
- B: Store tree position in snapshot
- C: Store completed step paths in snapshot
- D: Derive from events (smarter History)

**Leaning toward:** A - tree walk is fast enough. D is interesting for later.

### 8. Snapshot Recovery

**Question:** What if snapshot is corrupted/missing?

**Context:**
- Snapshot table corruption
- Accidental deletion
- Need to recover

**Options:**
- A: Fall back to full event replay (always works)
- B: Rebuild snapshots from events (batch job)
- C: Alert on missing snapshot
- D: A + C

**Leaning toward:** D - fall back gracefully, alert for investigation.

### 9. Concurrent Snapshot Access

**Question:** Can multiple workers create snapshots for same run?

**Context:**
- Unlikely but possible (race condition)
- Duplicate snapshots waste storage
- Last one wins?

**Options:**
- A: Allow duplicates (harmless)
- B: Unique constraint (run_id, sequence)
- C: Check before creating

**Leaning toward:** B - unique constraint prevents waste.

### 10. Snapshot Metrics

**Question:** What snapshot metrics to track?

**Context:**
- Snapshot frequency
- Snapshot size
- Replay performance with/without snapshot

**Options:**
- A: None
- B: Count and size
- C: Count, size, and replay speedup
- D: Full histogram of sizes

**Leaning toward:** B - count and size are sufficient.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Snapshot type | Complete |
| SnapshotData struct | Complete |
| SnapshotStore interface | Complete |
| PostgreSQL snapshot storage | Complete |
| Memory snapshot storage | Complete |
| Snapshot creation in replay | Not implemented |
| Snapshot interval config | Not implemented |
| LoadSince for resume | Not implemented |
| Snapshot compaction | Not implemented |
