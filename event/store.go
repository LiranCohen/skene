package event

import (
	"context"
	"errors"
	"fmt"
)

// Common errors returned by EventStore implementations.
var (
	// ErrSequenceConflict indicates the event sequence number doesn't match
	// the expected next sequence (lastSequence + 1).
	ErrSequenceConflict = errors.New("sequence conflict")

	// ErrDuplicateEvent indicates an event with the same ID already exists.
	ErrDuplicateEvent = errors.New("duplicate event ID")
)

// SequenceConflictError provides details about a sequence conflict.
type SequenceConflictError struct {
	RunID    string
	Expected int64
	Actual   int64
}

func (e *SequenceConflictError) Error() string {
	return fmt.Sprintf("sequence conflict for run %s: expected %d, got %d", e.RunID, e.Expected, e.Actual)
}

func (e *SequenceConflictError) Unwrap() error {
	return ErrSequenceConflict
}

// EventStore defines the interface for event persistence.
// Implementations must be safe for concurrent use.
type EventStore interface {
	// Append adds a single event to the store.
	// Returns ErrSequenceConflict if event.Sequence != lastSequence + 1.
	// Returns ErrDuplicateEvent if an event with the same ID already exists.
	Append(ctx context.Context, event Event) error

	// AppendBatch adds multiple events atomically.
	// All events must have consecutive sequence numbers starting from lastSequence + 1.
	// If any event fails validation, no events are appended (all-or-nothing).
	// Returns ErrSequenceConflict if sequences are invalid.
	// Returns ErrDuplicateEvent if any event ID already exists.
	AppendBatch(ctx context.Context, events []Event) error

	// Load retrieves all events for a workflow run, ordered by sequence.
	// Returns an empty slice if the run doesn't exist or has no events.
	Load(ctx context.Context, runID string) ([]Event, error)

	// LoadSince retrieves events with sequence > afterSequence, ordered by sequence.
	// Returns an empty slice if no events match.
	LoadSince(ctx context.Context, runID string, afterSequence int64) ([]Event, error)

	// GetLastSequence returns the highest sequence number for a run.
	// Returns 0 if the run doesn't exist or has no events.
	GetLastSequence(ctx context.Context, runID string) (int64, error)
}
