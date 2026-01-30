# Feature: Chorus (Parallel Execution)

## Overview

Chorus executes multiple branches in parallel. This is Phase 3 of the implementation - the replay placeholder exists but executes sequentially.

---

## Decisions

### No Special Merge Function

**Decision:** The step after Chorus processes results. No explicit merge callback.

**Rationale:**
- Mirrors Temporal's pattern where code after `future.Get()` processes results
- Keeps API simple: `Chorus{Steps: []Workflow}`
- "Merge" is just another Act that reads the parallel results

```go
Scene[OrderState]{
    Steps: []Workflow[OrderState]{
        // Parallel execution
        Chorus[OrderState]{
            Name: "checks",
            Steps: []Workflow[OrderState]{
                Act{Name: "check-inventory", Run: checkInventory},
                Act{Name: "check-fraud", Run: checkFraud},
            },
        },
        // "Merge" is just the next step
        Act{Name: "evaluate", Run: func(ctx, state, deps) error {
            if !state.InventoryResult.Available {
                return errors.New("out of stock")
            }
            return nil
        }},
    },
}
```

### Branches Write to Distinct State Fields

**Decision:** Each parallel branch must write to its own field in state.

**Rationale:**
- Prevents race conditions (no locking needed)
- State mutation model preserved
- Convention enforced by runtime conflict detection

```go
type OrderState struct {
    // Each branch owns its field
    InventoryResult *InventoryResult  // Branch A writes here
    FraudResult     *FraudResult      // Branch B writes here
}
```

### Conflict Detection via JSON Diff

**Decision:** Detect conflicts by comparing JSON serializations before/after.

**Rationale:**
- Works for any serializable state
- No reflection complexity
- Runtime error if same field modified by multiple branches

```go
// Error: chorus conflict: field "Score" modified by multiple branches
```

### State Merging Strategy

**Decision:** Apply each branch's changes to a cloned base state.

**Algorithm:**
1. Clone state before Chorus
2. For each completed branch:
   - Deserialize branch's StateAfter
   - Find fields that differ from base
   - If field already modified by another branch: error
   - Copy changed fields to result
3. Result is merged state

### Event Model

```
ChorusStarted(branches: [A, B, C])
  ChorusBranchStarted(A)
  ChorusBranchStarted(B)
  ChorusBranchStarted(C)
  ChorusBranchCompleted(B, state_after: {...})   // Order non-deterministic
  ChorusBranchCompleted(A, state_after: {...})
  ChorusBranchCompleted(C, state_after: {...})
ChorusCompleted(state_after: {merged})
```

### Failure Modes

**Decision:** Support three failure modes.

```go
type ChorusFailureMode int

const (
    FailFast      // Default: cancel siblings on first failure
    FailAfterAll  // Wait for all, then fail
    PartialSuccess // Continue with successes (use carefully)
)
```

### Cancelled Branch Events

**Decision:** Record `chorus.branch.cancelled` in FailFast mode.

**Rationale:**
- Audit trail shows what was cancelled and why
- Distinguishes "failed" from "never got to run"

### MaxWorkers

**Decision:** `MaxWorkers` limits concurrent goroutines for pending branches only.

**Rationale:**
- During replay, completed branches just restore state (no goroutines)
- Only pending branches need actual execution
- Respects resource constraints during partial replay

### Chorus Timeout

**Decision:** Chorus-level timeout applies to entire parallel execution.

**Rationale:**
- Per-branch timeouts use Act's timeout/retry policy
- Chorus timeout is a wall-clock limit on the whole operation
- If exceeded, cancel all pending branches

---

## Replay Logic

```go
func replayChorus(ctx, chorus, state, history) error {
    // 1. Check completion
    if history.HasCompleted(chorus.Path()) {
        return restoreState(...)
    }

    // 2. Check failure
    if history.HasFailed(chorus.Path()) {
        return reconstructError(...)
    }

    // 3. Record started if needed
    if !history.Has(chorus.Path(), EventChorusStarted) {
        appendEvent(EventChorusStarted, ...)
    }

    // 4. Find completed branches
    completedBranches := map[int]json.RawMessage{}
    for _, e := range history.FindAll(path, EventChorusBranchCompleted) {
        completedBranches[e.BranchIndex] = e.StateAfter
    }

    // 5. Execute pending branches in parallel
    pendingIndices := findPending(chorus.Steps, completedBranches)
    if len(pendingIndices) > 0 {
        results := executeBranchesParallel(ctx, chorus, state, pendingIndices)
        // Record results as events
        // Handle failures based on FailureMode
    }

    // 6. Merge states
    mergedState, err := mergeStates(state, completedBranches)
    if err != nil {
        return err // Conflict
    }

    // 7. Record completion
    appendEvent(EventChorusCompleted, mergedState)
    return nil
}
```

---

## Open Questions

### 1. Conflict Detection Granularity

**Question:** Should we detect conflicts at field level or deeper?

**Context:**
- Currently: "field X modified by multiple branches"
- What about nested fields? `state.Order.Items[0].Quantity`?

**Options:**
- A: Top-level fields only (simple)
- B: Full path comparison (complex, thorough)
- C: Configurable depth

**Leaning toward:** A - top-level is sufficient. Deeper conflicts are rare.

### 2. Branch Ordering in Merge

**Question:** In what order should branch states be applied?

**Context:**
- If branches wrote to different fields, order doesn't matter
- If conflict detection is perfect, order is irrelevant
- But edge cases exist (nil vs empty slice, etc.)

**Options:**
- A: Declaration order (deterministic)
- B: Completion order (reflects reality)
- C: Doesn't matter (conflict detection handles it)

**Leaning toward:** A - declaration order for determinism.

### 3. Nested Chorus

**Question:** Can a Chorus branch contain another Chorus?

**Context:**
- Technically possible with current tree model
- Complicates state merging (nested merge operations)
- May not have practical use cases

**Options:**
- A: Allow (full flexibility)
- B: Disallow at validation time (simpler)
- C: Allow but document complexity

**Leaning toward:** A - allow it. Tree model handles it naturally.

### 4. Partial Success Semantics

**Question:** What exactly does PartialSuccess mean?

**Context:**
- "Continue with successful branches"
- But what state is passed to next step?
- What if a required branch failed?

**Options:**
- A: Merge only successful branch states, continue
- B: Merge successful + record failure, let next step decide
- C: Require explicit handling in next step

**Leaning toward:** A with clear documentation that failed branches don't contribute state.

### 5. Collect Pattern vs Next Step

**Question:** Should we keep the existing `Collect` callback or remove it?

**Context:**
- Current Chorus has `Collect func(ctx, state, outputs) error`
- New model says "next step is the merge"
- Collect was for thread-safe output collection

**Options:**
- A: Remove Collect (next step does merge)
- B: Keep Collect as optional convenience
- C: Keep Collect but deprecate

**Leaning toward:** A - remove for simplicity. State fields replace Collect.

### 6. Branch Retry Policy

**Question:** Should branches have independent retry policies?

**Context:**
- A branch is typically an Act (has retry policy)
- But branch could be a Scene (multiple steps)
- Should the whole branch retry, or just failed step?

**Options:**
- A: Use Act's retry policy (natural)
- B: Chorus-level branch retry (e.g., retry entire branch 3 times)
- C: No branch-level retry, only step-level

**Leaning toward:** A - Act's policy applies. Branch-level is over-engineering.

### 7. Event Ordering Across Branches

**Question:** How do we handle non-deterministic completion order?

**Context:**
- Branches complete in different order each run
- Event sequence numbers are global to run
- Replay must handle any ordering

**Options:**
- A: Events interleaved (current - natural)
- B: Group by branch (gather all branch events together)
- C: Two-phase: all starts, then all completions

**Leaning toward:** A - natural interleaving. History helper handles lookup.

### 8. State Cloning

**Question:** How do we clone state before Chorus for comparison?

**Context:**
- Need "before" state to detect changes
- JSON round-trip is safe but slow
- Reflection-based clone is complex

**Options:**
- A: JSON marshal/unmarshal (safe, simple)
- B: Require Clone method on state
- C: Use reflection with careful handling

**Leaning toward:** A - JSON clone. Performance is acceptable for workflow state sizes.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Event types | Complete |
| Event data structs | Complete |
| History queries | Complete |
| Replay logic | Placeholder (sequential) |
| Parallel execution | Not implemented |
| State merging | Not implemented |
| Conflict detection | Not implemented |
| Failure modes | Not implemented |
| MaxWorkers | Not implemented |
