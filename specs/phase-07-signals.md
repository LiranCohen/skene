# Phase 7: Signals (External Events)

## Goal
Implement signal wait points that pause workflow execution until external input is received.

## Primary Design Document
- `docs/design/06-SIGNALS.md`

## Related Documents
- `docs/design/01-EVENTS.md` (signal.* events)
- `docs/design/03-REPLAY.md` (Signal wait replay)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Signal Wait (`workflow/signal.go`)

```go
// WaitForSignal pauses execution until signal is received or timeout
func WaitForSignal(ctx Context, signalName string, timeout time.Duration) (json.RawMessage, error)

// WaitForSignalTyped pauses and deserializes the payload
func WaitForSignalTyped[T any](ctx Context, signalName string, timeout time.Duration) (T, error)

// SignalWaitError represents signal-related errors
type SignalWaitError struct {
    SignalName string
    Reason     string // "timeout", "cancelled"
}

func (e *SignalWaitError) Error() string

// ErrSignalTimeout indicates the signal timed out
var ErrSignalTimeout = &SignalWaitError{Reason: "timeout"}
```

### Signal State in History

```go
// SignalState tracks signal lifecycle
type SignalState struct {
    Waiting   bool            // signal.waiting recorded
    Received  bool            // signal.received recorded
    Timeout   bool            // signal.timeout recorded
    Payload   json.RawMessage // from signal.received
    TimeoutAt time.Time       // from signal.waiting
}

// GetSignalState returns the state of a signal
func (h *History) GetSignalState(signalName string) *SignalState
```

### Replay Output Extension

```go
type ReplayOutput struct {
    // ... existing fields ...

    // WaitingForSignals lists signals the workflow is waiting for
    WaitingForSignals []WaitingSignal
}

type WaitingSignal struct {
    SignalName string
    TimeoutAt  time.Time
}
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/signal.go` | WaitForSignal, WaitForSignalTyped |
| `workflow/history.go` | Extended with GetSignalState |
| `workflow/replay.go` | Extended with signal wait handling |

---

## Acceptance Criteria

### WaitForSignal
- [ ] Records signal.waiting event if not already waiting
- [ ] Returns immediately if signal.received in history
- [ ] Returns error if signal.timeout in history
- [ ] Returns ReplayWaiting status to caller
- [ ] Timeout stored in signal.waiting event

### Signal Replay
- [ ] On replay with signal.received: return payload, continue
- [ ] On replay with signal.timeout: return error
- [ ] On replay with signal.waiting (no received): return ReplayWaiting
- [ ] Never call step function twice for same signal

### Signal State
- [ ] GetSignalState returns correct state
- [ ] Waiting, Received, Timeout are mutually exclusive progressions
- [ ] Payload only present when Received

### Tests
- [ ] WaitForSignal records waiting event
- [ ] Signal received continues execution
- [ ] Signal timeout returns error
- [ ] Replay with received signal (no wait)
- [ ] Replay with timeout (returns error)
- [ ] Multiple signals in same workflow
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### WaitForSignal Tests
- First execution: records signal.waiting, returns waiting
- Replay with signal.received: returns payload
- Replay with signal.timeout: returns timeout error
- WaitForSignalTyped deserializes correctly
- WaitForSignalTyped with wrong type returns error

### Signal State Tests
- Empty history: no signal state
- After signal.waiting: Waiting=true
- After signal.received: Received=true, has Payload
- After signal.timeout: Timeout=true

### Integration Tests
- Step calls WaitForSignal, workflow pauses
- External sends signal, workflow resumes
- Signal timeout triggers timeout path
- Multiple signals in workflow

---

## Dependencies
- Phase 1: Events & Storage
- Phase 2: Replay Engine
- Phase 3: Typed Step Model

---

## Implementation Notes

### Signal in Step Function

```go
func awaitApproval(ctx workflow.Context) (*ApprovalOutput, error) {
    // This returns immediately if signal already received (replay)
    // Otherwise records signal.waiting and returns ReplayWaiting
    payload, err := workflow.WaitForSignal(ctx, "approval", 24*time.Hour)
    if err != nil {
        return nil, err // Timeout or cancellation
    }

    var approval ApprovalData
    if err := json.Unmarshal(payload, &approval); err != nil {
        return nil, fmt.Errorf("invalid approval payload: %w", err)
    }

    return &ApprovalOutput{ApprovedBy: approval.ApprovedBy}, nil
}
```

### Signal Wait Implementation

```go
func WaitForSignal(ctx Context, signalName string, timeout time.Duration) (json.RawMessage, error) {
    wctx := ctx.(*workflowContext)

    // Check if signal already processed
    state := wctx.history.GetSignalState(signalName)
    if state != nil {
        if state.Received {
            return state.Payload, nil
        }
        if state.Timeout {
            return nil, &SignalWaitError{SignalName: signalName, Reason: "timeout"}
        }
        if state.Waiting {
            // Still waiting - signal replay waiting
            wctx.signalWaiting(signalName, state.TimeoutAt)
            return nil, ErrReplayWaiting
        }
    }

    // First time - record waiting
    timeoutAt := time.Now().Add(timeout)
    wctx.recordSignalWaiting(signalName, timeoutAt)
    wctx.signalWaiting(signalName, timeoutAt)
    return nil, ErrReplayWaiting
}
```

### ErrReplayWaiting

```go
// ErrReplayWaiting is a sentinel error indicating the replay should pause
var ErrReplayWaiting = errors.New("replay waiting for signal")
```

The Replayer catches this error and returns ReplayWaiting status.

---

## Code Quality Reminders (from CLAUDE.md)
- Return errors, don't panic
- Wrap errors with context
- Use sentinel errors for control flow
- Table-driven tests
