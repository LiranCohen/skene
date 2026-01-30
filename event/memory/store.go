// Package memory provides an in-memory implementation of event.EventStore.
// This implementation is suitable for testing and development.
package memory

import (
	"context"
	"sync"

	"github.com/lirancohen/skene/event"
)

// Store is a thread-safe in-memory implementation of event.EventStore.
// The zero value is ready for use.
type Store struct {
	mu     sync.RWMutex
	events map[string][]event.Event // runID -> events (sorted by sequence)
	ids    map[string]struct{}      // set of all event IDs for duplicate detection
}

// New creates a new in-memory event store.
func New() *Store {
	return &Store{
		events: make(map[string][]event.Event),
		ids:    make(map[string]struct{}),
	}
}

// Append adds a single event to the store.
// Returns ErrSequenceConflict if event.Sequence != lastSequence + 1.
// Returns ErrDuplicateEvent if an event with the same ID already exists.
func (s *Store) Append(ctx context.Context, e event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.appendLocked(e)
}

// appendLocked adds an event without acquiring the lock.
// Caller must hold s.mu.
func (s *Store) appendLocked(e event.Event) error {
	// Initialize maps if nil (supports zero value)
	if s.events == nil {
		s.events = make(map[string][]event.Event)
	}
	if s.ids == nil {
		s.ids = make(map[string]struct{})
	}

	// Check for duplicate event ID
	if _, exists := s.ids[e.ID]; exists {
		return event.ErrDuplicateEvent
	}

	// Validate sequence
	runEvents := s.events[e.RunID]
	expectedSeq := int64(len(runEvents)) + 1
	if e.Sequence != expectedSeq {
		return &event.SequenceConflictError{
			RunID:    e.RunID,
			Expected: expectedSeq,
			Actual:   e.Sequence,
		}
	}

	// Append the event
	s.events[e.RunID] = append(runEvents, e)
	s.ids[e.ID] = struct{}{}

	return nil
}

// AppendBatch adds multiple events atomically.
// All events must have consecutive sequence numbers starting from lastSequence + 1.
// If any event fails validation, no events are appended (all-or-nothing).
func (s *Store) AppendBatch(ctx context.Context, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Initialize maps if nil (supports zero value)
	if s.events == nil {
		s.events = make(map[string][]event.Event)
	}
	if s.ids == nil {
		s.ids = make(map[string]struct{})
	}

	// Validate all events before appending any
	// Track new IDs to check for duplicates within the batch
	newIDs := make(map[string]struct{}, len(events))

	// Group events by runID for sequence validation
	runSequences := make(map[string]int64)
	for runID, runEvents := range s.events {
		runSequences[runID] = int64(len(runEvents))
	}

	for _, e := range events {
		// Check for duplicate with existing events
		if _, exists := s.ids[e.ID]; exists {
			return event.ErrDuplicateEvent
		}

		// Check for duplicate within batch
		if _, exists := newIDs[e.ID]; exists {
			return event.ErrDuplicateEvent
		}
		newIDs[e.ID] = struct{}{}

		// Validate sequence
		expectedSeq := runSequences[e.RunID] + 1
		if e.Sequence != expectedSeq {
			return &event.SequenceConflictError{
				RunID:    e.RunID,
				Expected: expectedSeq,
				Actual:   e.Sequence,
			}
		}
		runSequences[e.RunID] = e.Sequence
	}

	// All validation passed, append events
	for _, e := range events {
		s.events[e.RunID] = append(s.events[e.RunID], e)
		s.ids[e.ID] = struct{}{}
	}

	return nil
}

// Load retrieves all events for a workflow run, ordered by sequence.
// Returns an empty slice if the run doesn't exist or has no events.
func (s *Store) Load(ctx context.Context, runID string) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runEvents := s.events[runID]
	if len(runEvents) == 0 {
		return []event.Event{}, nil
	}

	// Return a copy to prevent external modification
	result := make([]event.Event, len(runEvents))
	copy(result, runEvents)
	return result, nil
}

// LoadSince retrieves events with sequence > afterSequence, ordered by sequence.
// Returns an empty slice if no events match.
func (s *Store) LoadSince(ctx context.Context, runID string, afterSequence int64) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	runEvents := s.events[runID]
	if len(runEvents) == 0 {
		return []event.Event{}, nil
	}

	// Since sequences are 1-indexed and gapless, afterSequence corresponds to index afterSequence-1
	// We want events with sequence > afterSequence, so start from index afterSequence
	startIdx := max(int(afterSequence), 0)
	if startIdx >= len(runEvents) {
		return []event.Event{}, nil
	}

	// Return a copy to prevent external modification
	result := make([]event.Event, len(runEvents)-startIdx)
	copy(result, runEvents[startIdx:])
	return result, nil
}

// GetLastSequence returns the highest sequence number for a run.
// Returns 0 if the run doesn't exist or has no events.
func (s *Store) GetLastSequence(ctx context.Context, runID string) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return int64(len(s.events[runID])), nil
}
