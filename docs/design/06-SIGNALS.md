# Feature: Signals (External Events)

## Overview

Signals pause workflow execution waiting for external input. When a Signal step is reached, the workflow persists and returns a "waiting" status. External systems call `SendSignal` to resume.

---

## Decisions

### Signal as a Workflow Primitive

**Decision:** Signal is a first-class workflow primitive like Act, Scene, etc.

**Rationale:**
- Explicit pause points in workflow definition
- Type-safe payload handling via `OnReceive`
- Timeout handling built-in

```go
Signal[OrderState]{
    Name: "manager-approval",
    OnReceive: func(ctx, state, payload, deps) error {
        var approval ApprovalPayload
        json.Unmarshal(payload, &approval)
        state.ApprovedBy = approval.ApproverID
        return nil
    },
    Timeout: 48 * time.Hour,
    OnTimeout: func(ctx, state, deps) error {
        return errors.New("approval timed out")
    },
}
```

### Event Model

```
Signal waiting:   signal.waiting
Signal received:  signal.received (includes payload, StateAfter)
Signal timeout:   signal.timeout
```

**Decision:** `signal.received` includes `StateAfter` so replay can skip `OnReceive` call.

### Timeout via Scheduled Jobs

**Decision:** Use River scheduled jobs for timeouts, not polling.

**Rationale:**
- O(1) per timeout (no scanning)
- Precise timing
- Scales with workflow count

```go
// When signal.waiting is recorded:
river.InsertTx(tx, SignalTimeoutJob{
    RunID: runID,
    SignalPath: signalPath,
}, river.ScheduledAt(timeoutAt))
```

### Timeout Job Idempotency

**Decision:** Timeout job checks if signal was already received before acting.

**Rationale:**
- Signal might arrive just before timeout
- Job must not override received signal

```go
func (w *SignalTimeoutWorker) Work(ctx, job) error {
    // Check if already received
    if signalReceived(job.RunID, job.SignalPath) {
        return nil // No-op
    }
    // Record timeout and resume
    return handleTimeout(...)
}
```

### SendSignal API

**Decision:** `SendSignal(ctx, runID, signalName, payload)` appends event and resumes workflow.

**Flow:**
1. Load workflow events
2. Find `signal.waiting` event for this signal
3. Validate signal still waiting (no received/timeout)
4. Append `signal.received` event
5. Schedule River job to resume workflow

### Signal Names are Unique Within Workflow

**Decision:** Signal names must be unique within a workflow run.

**Rationale:**
- Allows lookup by name: "find the signal.waiting for 'approval'"
- Multiple signals with same name would be ambiguous
- Use different names for different pause points

---

## Open Questions

### 1. Multiple Waiters for Same Signal

**Question:** Can multiple Signal steps wait for the same logical signal?

**Context:**
- Different branches might both wait for "approval"
- Or same signal name in different Scenes

**Options:**
- A: First match wins (current lookup behavior)
- B: All matching signals receive the payload
- C: Require globally unique signal names
- D: Use step path, not just name

**Leaning toward:** D - use step path for precision, name for convenience.

### 2. Signal Discovery

**Question:** How does an external system know what signals a workflow is waiting for?

**Context:**
- UI needs to show "pending approvals"
- Webhook handler needs to route to correct run

**Options:**
- A: Query API: `ListWaitingSignals(runID)` - already exists
- B: Query API: `FindRunsWaitingFor(signalName)` - new
- C: Callback/webhook when signal starts waiting
- D: Event stream subscription

**Leaning toward:** B - add query for finding runs by waiting signal.

### 3. Signal Payload Validation

**Question:** Should we validate signal payloads before accepting?

**Context:**
- OnReceive might fail if payload is malformed
- Better to reject early than fail during resume

**Options:**
- A: No validation (fail during OnReceive)
- B: Optional schema on Signal primitive
- C: Try-parse before accepting, reject if invalid
- D: Accept any JSON, OnReceive handles errors

**Leaning toward:** A - keep simple. OnReceive is the validation point.

### 4. Signal Expiry vs Timeout

**Question:** Should signals support expiry (can't be sent after deadline)?

**Context:**
- Timeout: workflow gives up waiting
- Expiry: signal becomes invalid after time (even before timeout)
- Example: "approval valid for 24h, wait for 48h"

**Options:**
- A: Not supported (timeout is sufficient)
- B: Add `Expiry` field separate from `Timeout`
- C: Validate in OnReceive (user handles)

**Leaning toward:** A - expiry logic belongs in OnReceive.

### 5. Signal Acknowledgment

**Question:** Should SendSignal return acknowledgment that signal was processed?

**Context:**
- Currently: SendSignal returns when event is appended
- Caller doesn't know if OnReceive succeeded
- Workflow might fail during OnReceive

**Options:**
- A: Return immediately after event append (current)
- B: Wait for OnReceive completion
- C: Async with callback/webhook for result
- D: Query result separately

**Leaning toward:** A - async is more robust. Caller queries for result.

### 6. Conditional Signals

**Question:** Should signals have conditions like other primitives?

**Context:**
- Acts, Scenes have `Condition func(*S) bool`
- Signal might be skipped based on state

**Decision made:** Yes, Signals have Condition field.

**Open:** Should condition be re-evaluated on signal delivery?

**Options:**
- A: Condition checked only when entering step
- B: Condition checked on delivery too (might reject)
- C: Condition only at entry (simpler, predictable)

**Leaning toward:** A - condition at entry only.

### 7. Signal Cancellation

**Question:** Can a waiting signal be cancelled without sending it?

**Context:**
- Workflow cancelled while waiting for signal
- Timeout job might fire after cancellation

**Options:**
- A: Cancellation overrides signal waiting (current)
- B: Cancel pending timeout job on workflow cancellation
- C: Let timeout job run, it checks for cancellation

**Leaning toward:** C - timeout job is idempotent anyway.

### 8. Signal Ordering

**Question:** What if multiple signals are sent to same workflow rapidly?

**Context:**
- Signal A sent at T1
- Signal B sent at T2 (different signal name)
- Both trigger resume jobs

**Options:**
- A: Process in order (serialize resume jobs)
- B: Process concurrently (event sequence resolves conflicts)
- C: Depends on River's job ordering

**Leaning toward:** B - concurrent is fine, sequence numbers prevent conflicts.

### 9. Bulk Signal Operations

**Question:** Should we support sending same signal to multiple runs?

**Context:**
- "Approve all pending orders from this customer"
- Batch operations are more efficient

**Options:**
- A: Loop over SendSignal (simple)
- B: `BatchSendSignal([]SignalRequest)` - already exists
- C: `SendSignalByQuery(filter, payload)` - new

**Leaning toward:** B is sufficient. C is too magical.

### 10. Signal Replay Behavior

**Question:** During replay, should OnReceive be called?

**Context:**
- On replay, signal.received already in history
- OnReceive was already executed
- Should we call it again?

**Decision:** No. Restore state from StateAfter, skip OnReceive.

**Rationale:**
- OnReceive might have side effects
- State was already captured
- Deterministic replay

**Open:** What if OnReceive was non-deterministic? (Accept as user's problem)

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Signal primitive | Complete |
| Event types | Complete |
| signal.waiting replay | Complete |
| signal.received replay | Complete |
| signal.timeout replay | Complete |
| SendSignal API | Not implemented (Phase 5) |
| Timeout scheduling | Not implemented (Phase 5) |
| SignalTimeoutJob | Not implemented (Phase 5) |
| ListWaitingSignals | Partially (history query) |
