# Feature: Saga (Compensation)

## Overview

Saga pattern provides automatic compensation (rollback) when a step in a saga-enabled Scene fails. This is Phase 4 of the implementation.

---

## Decisions

### Saga is a Scene Property

**Decision:** Enable saga via `Scene{Saga: true}`.

**Rationale:**
- Natural fit: Scenes are sequential, compensation is reverse sequential
- No new primitive needed
- Opt-in per Scene

```go
Scene[OrderState]{
    Name: "process-order",
    Saga: true,
    Steps: []Workflow[OrderState]{
        Act{Name: "reserve-inventory", Compensate: releaseInventory},
        Act{Name: "charge-payment", Compensate: refundPayment},
        Act{Name: "create-shipment", Compensate: cancelShipment},
    },
}
```

### Compensate is an Act Property

**Decision:** Compensation functions defined on Act via `Compensate` field.

**Rationale:**
- Compensation is step-specific (each step knows how to undo itself)
- Optional - not all steps need compensation
- Same signature as Run function

```go
Act[OrderState]{
    Name: "charge-payment",
    Run: func(ctx, state, deps) error {
        // charge the card
    },
    Compensate: func(ctx, state, deps) error {
        // refund the charge
    },
}
```

### Reverse Order Compensation

**Decision:** Compensate in reverse order of completion.

**Rationale:**
- Natural undo order (LIFO)
- Later steps may depend on earlier steps
- Standard saga pattern

```
Forward:  A → B → C → D (fails)
Backward: C.Compensate → B.Compensate → A.Compensate
```

### Only Compensate Completed Steps

**Decision:** Only steps that completed successfully are compensated.

**Rationale:**
- Failed step didn't succeed, nothing to undo
- Steps after the failure never ran

### Event Model

```
SceneStarted(saga: true)
  ActStarted(A)
  ActCompleted(A)
  ActStarted(B)
  ActCompleted(B)
  ActStarted(C)
  ActFailed(C)
SceneFailed(failed_at_step: 2)
CompensationStarted(steps_to_undo: [B, A])
  CompensationStepStarted(B)
  CompensationStepCompleted(B)
  CompensationStepStarted(A)
  CompensationStepCompleted(A)
CompensationCompleted
```

### Compensation Failures

**Decision:** If compensation fails, return `SagaError` with both original and compensation errors.

**Rationale:**
- Can't silently ignore compensation failure
- Caller needs to know both what failed and that cleanup failed
- May require manual intervention

```go
type SagaError struct {
    OriginalError     error  // The step failure that triggered compensation
    CompensationError error  // The compensation failure
}
```

### Continue on Compensation Failure

**Decision:** Continue compensating other steps even if one fails.

**Rationale:**
- Best effort cleanup
- Stopping at first compensation failure leaves more mess
- All failures reported in final error

---

## Open Questions

### 1. Compensation Retry Policy

**Question:** Should compensation steps have retry policies?

**Context:**
- Compensation might fail due to transient errors
- Retrying could succeed
- But infinite retries could block workflow

**Options:**
- A: No retries (fail fast)
- B: Use same retry policy as forward step
- C: Separate compensation retry policy
- D: Fixed retry policy (e.g., 3 attempts)

**Leaning toward:** D - fixed conservative retry. Compensation should be reliable.

### 2. Compensation Timeout

**Question:** Should compensation have a timeout?

**Context:**
- Compensation that hangs blocks workflow termination
- But timeout too short might cause partial cleanup

**Options:**
- A: No timeout (rely on context cancellation)
- B: Same timeout as forward step
- C: Configurable compensation timeout
- D: Fixed timeout (e.g., 30 seconds)

**Leaning toward:** B - use forward step's timeout as default.

### 3. Nested Saga Scenes

**Question:** How do nested Saga Scenes work?

**Context:**
```go
Scene{Saga: true, Steps: [
    Scene{Saga: true, Steps: [A, B]},  // Inner saga
    C,
]}
```
- If C fails, should inner saga's compensation run?
- Inner saga already completed (no failure there)

**Options:**
- A: Don't compensate completed inner sagas
- B: Compensate inner saga as a unit (it has its own compensation events)
- C: Recursively compensate inner steps
- D: Disallow nested sagas

**Leaning toward:** A - inner saga completed successfully, nothing to undo.

### 4. Chorus Inside Saga

**Question:** How to compensate a Chorus that ran inside a saga Scene?

**Context:**
```go
Scene{Saga: true, Steps: [
    Chorus{Steps: [A, B, C]},  // All completed
    D,                          // Fails
]}
```

**Options:**
- A: Compensate Chorus branches in parallel (they ran parallel)
- B: Compensate in declaration order (simpler)
- C: Compensate in reverse completion order (mirrors execution)

**Leaning toward:** A - parallel compensation mirrors parallel execution.

### 5. Partial Chorus Compensation

**Question:** If Chorus failed with PartialSuccess, how to compensate?

**Context:**
- Chorus had 3 branches, 2 succeeded, 1 failed
- Scene continues (PartialSuccess mode)
- Later step fails, triggering compensation

**Options:**
- A: Only compensate the 2 successful branches
- B: Attempt to compensate all 3 (failed one is no-op)
- C: Don't allow PartialSuccess in Saga Scenes

**Leaning toward:** A - only compensate what succeeded.

### 6. Compensation Observability

**Question:** How much detail to capture in compensation events?

**Context:**
- Currently: started/completed/failed events
- Could capture: duration, attempts, state changes

**Options:**
- A: Minimal (started/completed/failed)
- B: Include duration and attempt count
- C: Include state changes (StateBefore/StateAfter)

**Leaning toward:** B - duration and attempts are useful for debugging.

### 7. Manual Compensation Trigger

**Question:** Should users be able to trigger compensation manually?

**Context:**
- Workflow failed but wasn't in saga mode
- User wants to compensate after the fact
- Or compensation failed and user wants to retry

**Options:**
- A: Not supported (design workflows correctly)
- B: `runner.CompensateRun(runID, stepPath)` API
- C: `runner.RetryCompensation(runID)` API

**Leaning toward:** A initially. Add B/C if real need emerges.

### 8. Compensation State

**Question:** What state does compensation receive?

**Context:**
- Forward: A modifies state, B reads A's changes
- Compensation: B.Compensate runs, then A.Compensate
- What state does A.Compensate see?

**Options:**
- A: Final state at failure (includes B's changes)
- B: State right after A completed (from A's StateAfter)
- C: Accumulated state (each compensation can modify)

**Leaning toward:** A - compensation sees current state, which includes all changes. This matches typical saga implementations.

### 9. Idempotent Compensation

**Question:** Should we enforce/encourage idempotent compensation?

**Context:**
- Compensation might run multiple times (retries, replays)
- Non-idempotent compensation could cause double-refunds, etc.

**Options:**
- A: Documentation only (user's responsibility)
- B: Provide helpers (idempotency keys, status checks)
- C: Track compensation execution in state

**Leaning toward:** A - documentation. Idempotency is always the user's responsibility for side effects.

### 10. Compensation for Signals

**Question:** Can a Signal step have compensation?

**Context:**
- Signal waits for external input
- If later step fails, should signal "undo" the approval?
- Doesn't make semantic sense in most cases

**Options:**
- A: Signals cannot have compensation
- B: Allow compensation (user decides if meaningful)
- C: Allow but document it's unusual

**Leaning toward:** B - allow but unusual. Sometimes sending a "cancelled" notification makes sense.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Event types | Complete |
| Event data structs | Complete |
| Saga: true on Scene | Defined (not wired) |
| Compensate on Act | Defined (not wired) |
| Compensation replay | Not implemented |
| Reverse traversal | Not implemented |
| Compensation in Chorus | Not implemented |
| SagaError type | Defined |
| CompensationReport | Defined |
