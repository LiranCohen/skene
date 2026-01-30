package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lirancohen/skene/event"
)

// ErrReplayWaiting is a sentinel error indicating the replay should pause.
// The Replayer catches this error and returns ReplayWaiting status.
var ErrReplayWaiting = errors.New("replay waiting for signal")

// SignalWaitError represents signal-related errors.
type SignalWaitError struct {
	SignalName string
	Reason     string // "timeout", "cancelled"
}

func (e *SignalWaitError) Error() string {
	if e.SignalName == "" {
		return fmt.Sprintf("signal %s", e.Reason)
	}
	return fmt.Sprintf("signal %q %s", e.SignalName, e.Reason)
}

// ErrSignalTimeout indicates the signal timed out.
var ErrSignalTimeout = &SignalWaitError{Reason: "timeout"}

// Is implements error matching for SignalWaitError.
func (e *SignalWaitError) Is(target error) bool {
	t, ok := target.(*SignalWaitError)
	if !ok {
		return false
	}
	// Match by reason when SignalName is empty (sentinel comparison)
	if t.SignalName == "" {
		return e.Reason == t.Reason
	}
	return e.SignalName == t.SignalName && e.Reason == t.Reason
}

// signalAccessor is an internal interface for signal operations.
type signalAccessor interface {
	getHistory() *History
	recordSignalWaiting(signalName string, timeoutAt time.Time)
	addWaitingSignal(signalName string, timeoutAt time.Time)
}

// WaitForSignal pauses execution until signal is received or timeout.
// On first execution, it records a signal.waiting event and returns ErrReplayWaiting.
// On replay with signal.received, it returns the payload immediately.
// On replay with signal.timeout, it returns a SignalWaitError with reason "timeout".
// On replay with signal.waiting (no received/timeout), it returns ErrReplayWaiting.
func WaitForSignal(ctx Context, signalName string, timeout time.Duration) (json.RawMessage, error) {
	accessor, ok := ctx.(signalAccessor)
	if !ok {
		panic("workflow: context does not support signal operations")
	}

	// Check if signal already processed
	state := accessor.getHistory().GetSignalState(signalName)
	if state != nil {
		if state.Received {
			return state.Payload, nil
		}
		if state.Timeout {
			return nil, &SignalWaitError{SignalName: signalName, Reason: "timeout"}
		}
		if state.Waiting {
			// Still waiting - signal replay waiting
			accessor.addWaitingSignal(signalName, state.TimeoutAt)
			return nil, ErrReplayWaiting
		}
	}

	// First time - record waiting
	timeoutAt := time.Now().Add(timeout)
	accessor.recordSignalWaiting(signalName, timeoutAt)
	accessor.addWaitingSignal(signalName, timeoutAt)
	return nil, ErrReplayWaiting
}

// WaitForSignalTyped pauses and deserializes the payload to type T.
// Returns the zero value of T and an error if the signal times out or
// deserialization fails.
func WaitForSignalTyped[T any](ctx Context, signalName string, timeout time.Duration) (T, error) {
	var zero T

	payload, err := WaitForSignal(ctx, signalName, timeout)
	if err != nil {
		return zero, err
	}

	var result T
	if err := json.Unmarshal(payload, &result); err != nil {
		return zero, fmt.Errorf("unmarshal signal %q payload: %w", signalName, err)
	}

	return result, nil
}

// recordSignalWaiting records a signal.waiting event.
func (e *executionContext) recordSignalWaiting(signalName string, timeoutAt time.Time) {
	data, _ := json.Marshal(event.SignalWaitingData{
		SignalName: signalName,
		TimeoutAt:  timeoutAt,
	})

	e.replayer.emitEvent(event.Event{
		Type: event.EventSignalWaiting,
		Data: data,
	})
}

// addWaitingSignal adds a signal to the waiting signals list.
func (e *executionContext) addWaitingSignal(signalName string, timeoutAt time.Time) {
	e.replayer.addWaitingSignal(signalName, timeoutAt)
}

// WaitingSignal describes a signal the workflow is waiting for.
type WaitingSignal struct {
	SignalName string
	TimeoutAt  time.Time
}
