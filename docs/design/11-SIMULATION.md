# Feature: Simulation & Counterfactuals

## Overview

Event sourcing enables "what-if" scenarios: replay a workflow with modified inputs to explore alternative outcomes without affecting production data.

---

## Core Capabilities

### 1. Replay from Any Point

Given a workflow run, reconstruct state at any event sequence:

```go
// Get state at specific point
state := runner.GetStateAt(ctx, runID, sequence)
```

### 2. Modified Input Replay

Re-run from a specific point with modified state:

```go
// Load events up to step N-1
// Modify state
// Replay from step N with modified state
```

### 3. Branch Exploration

For Cue steps, explore "what if we took the other branch":

```go
// Events show cue.evaluated with branch "express-shipping"
// Simulate: what if we selected "standard-shipping"?
```

---

## Decisions

### Simulation is Read-Only

**Decision:** Simulation does not write to production event store.

**Rationale:**
- Production data immutable
- Simulation results are ephemeral
- Use in-memory store for simulation events

### Simulation Uses Same Replayer

**Decision:** Simulation uses the same Replayer, just with modified inputs.

**Rationale:**
- Consistent execution semantics
- No separate simulation engine
- Easy to compare real vs simulated

### No Special Simulation API Initially

**Decision:** Start without explicit simulation API. Users construct Replayer manually.

**Rationale:**
- Flexibility for different use cases
- API can be added later based on patterns
- Reduces initial scope

```go
// User constructs simulation
memStore := memstore.New()
events, _ := runner.GetHistory(ctx, runID)
history := NewHistory(events[:eventN])  // Up to step N-1

// Modify state
modifiedState := state
modifiedState.Amount = 200  // What if amount was higher?

replayer, _ := NewReplayer(ReplayerConfig{
    Workflow:     workflow,
    History:      history,
    InitialState: &modifiedState,
    Deps:         simulationDeps,  // Use simulation services
})

output, _ := replayer.Replay(ctx, nil)
// output.NewEvents contains simulated events
```

---

## Open Questions

### 1. Simulation API

**Question:** Should we provide explicit simulation API?

**Context:**
- Manual Replayer construction is verbose
- Common patterns could be wrapped
- API makes simulation more discoverable

**Options:**
- A: No API (manual construction)
- B: Simple: `runner.Simulate(runID, fromSeq, modifiedState)`
- C: Full: `runner.CreateSimulation(runID).FromStep(path).WithState(s).Run()`
- D: Separate SimulationRunner

**Leaning toward:** B - simple API covers 80% of use cases.

### 2. External Service Handling

**Question:** How to handle external services in simulation?

**Context:**
- Real workflow called PaymentAPI
- Simulation shouldn't charge again
- Need mock or record/replay

**Options:**
- A: User provides simulation Deps with mocks
- B: Record/replay: use recorded responses
- C: Hybrid: record responses, allow override
- D: Automatic mocking (dangerous)

**Leaning toward:** C - record responses as default, allow mocks.

### 3. Recorded Responses

**Question:** Should we record external call responses in events?

**Context:**
- Events capture state changes
- Don't capture what external API returned
- Useful for simulation and debugging

**Options:**
- A: Don't record (state captures what matters)
- B: Record in step metadata
- C: Separate "call log" table
- D: Optional recording per step

**Leaning toward:** A initially. B/D for future enhancement.

### 4. Simulation Storage

**Question:** Where to store simulation results?

**Context:**
- Simulations produce events
- Don't want to pollute production
- Might want to persist for comparison

**Options:**
- A: Memory only (ephemeral)
- B: Separate simulation database
- C: Same database, flagged as simulation
- D: User provides store

**Leaning toward:** D - user provides store. Memory for one-off, persistent for comparison.

### 5. Branch Override

**Question:** How to simulate different Cue branch selection?

**Context:**
- Cue recorded "selected branch A"
- Want to simulate "what if branch B"
- Need to override recorded decision

**Options:**
- A: Modify events before replay (hacky)
- B: Simulation config with branch overrides
- C: Provide state that would select B (natural)
- D: ReplayOption to ignore recorded decisions

**Leaning toward:** C - modify state so selector returns desired branch.

### 6. Simulation Depth

**Question:** Should simulation execute real step logic or just simulate state?

**Context:**
- Full execution: run actual step code (slow, side effects risk)
- State simulation: just apply expected state changes (fast, approximate)

**Options:**
- A: Always full execution (with mock services)
- B: Allow state-only simulation (user provides expected changes)
- C: Configurable per step
- D: Infer from step type (Act = execute, others = skip)

**Leaning toward:** A - full execution with proper mocking is most accurate.

### 7. Comparison Tools

**Question:** Should we provide tools to compare real vs simulated?

**Context:**
- "Show me what would have been different"
- State diff at each step
- Event sequence comparison

**Options:**
- A: No tools (user diffs manually)
- B: State diff utility
- C: Full execution comparison report
- D: Visual diff (web UI)

**Leaning toward:** B - state diff utility is useful and simple.

### 8. Simulation from Failure

**Question:** Can we simulate "what if step X hadn't failed"?

**Context:**
- Workflow failed at step X
- Want to see what would happen if X succeeded
- Need to provide success state

**Options:**
- A: Not supported (failure is terminal)
- B: Load events before failure, provide success state, continue
- C: Special "override failure" mode

**Leaning toward:** B - load events before failure, simulate success.

### 9. Time Simulation

**Question:** How to handle time in simulation?

**Context:**
- Original: signal received after 2 hours
- Simulation: instant (no actual wait)
- Time-dependent logic might behave differently

**Options:**
- A: Real time (simulation is slow)
- B: Instant (ignore time effects)
- C: Simulated clock (user controls time)
- D: Record/replay timestamps

**Leaning toward:** C - simulated clock for accurate time simulation.

### 10. Simulation Isolation

**Question:** How to ensure simulation doesn't affect production?

**Context:**
- Simulation executes step code
- Code might have side effects
- Need strong isolation

**Options:**
- A: Documentation only (user's responsibility)
- B: Simulation flag in context (code checks)
- C: Separate Deps with no-op implementations
- D: Sandbox execution environment

**Leaning toward:** C - separate Deps is the isolation mechanism.

---

## Use Cases

### 1. Debugging

"Why did the workflow charge $200 instead of $150?"

1. Load events
2. Find state before charge step
3. Inspect state to see amount calculation

### 2. What-If Analysis

"What if we had approved this order?"

1. Load events up to signal.waiting
2. Simulate signal.received with approval payload
3. See resulting state and events

### 3. Regression Testing

"Will this code change affect existing workflows?"

1. Record production workflow executions
2. Replay with new code
3. Compare outcomes

### 4. Training Data

"Generate examples of workflow outcomes"

1. Run simulations with varied inputs
2. Collect (input, output) pairs
3. Use for ML training or testing

---

## Implementation Status

| Component | Status |
|-----------|--------|
| GetStateAt | Not implemented |
| Manual simulation (via Replayer) | Possible now |
| Simulate API | Not implemented |
| State diff utility | Not implemented |
| Simulated clock | Not implemented |
| Branch override | Not implemented |
| Response recording | Not implemented |
