package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
)

// makeEvent is a test helper that creates an event with sensible defaults.
func makeEvent(id, runID string, seq int64, eventType event.EventType) event.Event {
	return event.Event{
		ID:        id,
		RunID:     runID,
		Sequence:  seq,
		Version:   1,
		Type:      eventType,
		StepName:  "",
		Data:      nil,
		Output:    nil,
		Timestamp: time.Now(),
		Metadata:  nil,
	}
}

func TestStore_Append(t *testing.T) {
	tests := []struct {
		name    string
		events  []event.Event // events to append before test
		event   event.Event   // event to append in test
		wantErr error
	}{
		{
			name:   "first event with sequence 1",
			events: nil,
			event:  makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
		},
		{
			name: "second event with sequence 2",
			events: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			event: makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
		},
		{
			name:    "wrong sequence (too high)",
			events:  nil,
			event:   makeEvent("evt-1", "run-1", 2, event.EventWorkflowStarted),
			wantErr: event.ErrSequenceConflict,
		},
		{
			name:    "wrong sequence (zero)",
			events:  nil,
			event:   makeEvent("evt-1", "run-1", 0, event.EventWorkflowStarted),
			wantErr: event.ErrSequenceConflict,
		},
		{
			name: "duplicate event ID",
			events: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			event:   makeEvent("evt-1", "run-1", 2, event.EventStepStarted),
			wantErr: event.ErrDuplicateEvent,
		},
		{
			name: "same sequence as existing (gap not allowed)",
			events: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			event:   makeEvent("evt-2", "run-1", 1, event.EventStepStarted),
			wantErr: event.ErrSequenceConflict,
		},
		{
			name: "different run starts at sequence 1",
			events: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			event: makeEvent("evt-2", "run-2", 1, event.EventWorkflowStarted),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			ctx := context.Background()

			// Setup: append prerequisite events
			for _, e := range tt.events {
				if err := store.Append(ctx, e); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Test
			err := store.Append(ctx, tt.event)

			// Verify
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("Append() error = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Errorf("Append() unexpected error = %v", err)
			}
		})
	}
}

func TestStore_AppendBatch(t *testing.T) {
	tests := []struct {
		name         string
		setupEvents  []event.Event // events to append before test
		batchEvents  []event.Event // events to append in batch
		wantErr      error
		wantSequence int64 // expected last sequence after test
	}{
		{
			name:        "empty batch",
			setupEvents: nil,
			batchEvents: nil,
		},
		{
			name:        "single event batch",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			wantSequence: 1,
		},
		{
			name:        "multiple events in batch",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-3", "run-1", 3, event.EventStepCompleted),
			},
			wantSequence: 3,
		},
		{
			name: "batch continues existing sequence",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			batchEvents: []event.Event{
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-3", "run-1", 3, event.EventStepCompleted),
			},
			wantSequence: 3,
		},
		{
			name:        "batch with wrong starting sequence",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 2, event.EventWorkflowStarted),
			},
			wantErr: event.ErrSequenceConflict,
		},
		{
			name:        "batch with gap in sequence",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 3, event.EventStepStarted), // gap!
			},
			wantErr: event.ErrSequenceConflict,
		},
		{
			name:        "batch with duplicate ID within batch",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-1", "run-1", 2, event.EventStepStarted), // same ID
			},
			wantErr: event.ErrDuplicateEvent,
		},
		{
			name: "batch with duplicate ID from existing",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 2, event.EventStepStarted), // same ID as existing
			},
			wantErr: event.ErrDuplicateEvent,
		},
		{
			name:        "batch for multiple runs",
			setupEvents: nil,
			batchEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-2", 1, event.EventWorkflowStarted),
				makeEvent("evt-3", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-4", "run-2", 2, event.EventStepStarted),
			},
			wantSequence: 2, // both runs should have sequence 2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			ctx := context.Background()

			// Setup
			for _, e := range tt.setupEvents {
				if err := store.Append(ctx, e); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Test
			err := store.AppendBatch(ctx, tt.batchEvents)

			// Verify error
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("AppendBatch() error = %v, want %v", err, tt.wantErr)
				}
				// Verify atomic rollback: no events from batch should be stored
				if len(tt.batchEvents) > 0 {
					runID := tt.batchEvents[0].RunID
					lastSeq, _ := store.GetLastSequence(ctx, runID)
					expectedSeq := int64(0)
					for _, e := range tt.setupEvents {
						if e.RunID == runID {
							expectedSeq = e.Sequence
						}
					}
					if lastSeq != expectedSeq {
						t.Errorf("Batch should have rolled back: last sequence = %d, want %d", lastSeq, expectedSeq)
					}
				}
			} else if err != nil {
				t.Errorf("AppendBatch() unexpected error = %v", err)
			} else if tt.wantSequence > 0 {
				// Verify sequence
				runID := tt.batchEvents[0].RunID
				lastSeq, err := store.GetLastSequence(ctx, runID)
				if err != nil {
					t.Fatalf("GetLastSequence() error = %v", err)
				}
				if lastSeq != tt.wantSequence {
					t.Errorf("GetLastSequence() = %d, want %d", lastSeq, tt.wantSequence)
				}
			}
		})
	}
}

func TestStore_Load(t *testing.T) {
	tests := []struct {
		name        string
		setupEvents []event.Event
		loadRunID   string
		wantCount   int
	}{
		{
			name:        "load empty store",
			setupEvents: nil,
			loadRunID:   "run-1",
			wantCount:   0,
		},
		{
			name: "load non-existent run",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			loadRunID: "run-2",
			wantCount: 0,
		},
		{
			name: "load single event",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			loadRunID: "run-1",
			wantCount: 1,
		},
		{
			name: "load multiple events",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-3", "run-1", 3, event.EventStepCompleted),
			},
			loadRunID: "run-1",
			wantCount: 3,
		},
		{
			name: "load only specific run",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-2", 1, event.EventWorkflowStarted),
				makeEvent("evt-3", "run-1", 2, event.EventStepStarted),
			},
			loadRunID: "run-1",
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			ctx := context.Background()

			// Setup
			for _, e := range tt.setupEvents {
				if err := store.Append(ctx, e); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Test
			events, err := store.Load(ctx, tt.loadRunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if len(events) != tt.wantCount {
				t.Errorf("Load() returned %d events, want %d", len(events), tt.wantCount)
			}

			// Verify events are sorted by sequence
			for i := 1; i < len(events); i++ {
				if events[i].Sequence <= events[i-1].Sequence {
					t.Errorf("Events not sorted by sequence: %d <= %d", events[i].Sequence, events[i-1].Sequence)
				}
			}
		})
	}
}

func TestStore_Load_ReturnsCopy(t *testing.T) {
	store := New()
	ctx := context.Background()

	// Setup
	if err := store.Append(ctx, makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted)); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Load events
	events1, _ := store.Load(ctx, "run-1")
	events2, _ := store.Load(ctx, "run-1")

	// Modify the first slice
	events1[0].ID = "modified"

	// Verify the second slice is not affected
	if events2[0].ID == "modified" {
		t.Errorf("Load() should return a copy, but modifications affected subsequent loads")
	}
}

func TestStore_LoadSince(t *testing.T) {
	tests := []struct {
		name          string
		setupEvents   []event.Event
		loadRunID     string
		afterSequence int64
		wantCount     int
		wantFirstSeq  int64
	}{
		{
			name:          "load since 0 (all events)",
			setupEvents:   []event.Event{makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted)},
			loadRunID:     "run-1",
			afterSequence: 0,
			wantCount:     1,
			wantFirstSeq:  1,
		},
		{
			name: "load since specific sequence",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-3", "run-1", 3, event.EventStepCompleted),
			},
			loadRunID:     "run-1",
			afterSequence: 1,
			wantCount:     2,
			wantFirstSeq:  2,
		},
		{
			name: "load since last sequence (no new events)",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
			},
			loadRunID:     "run-1",
			afterSequence: 2,
			wantCount:     0,
		},
		{
			name: "load since beyond last sequence",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			loadRunID:     "run-1",
			afterSequence: 10,
			wantCount:     0,
		},
		{
			name:          "load since for non-existent run",
			setupEvents:   nil,
			loadRunID:     "run-1",
			afterSequence: 0,
			wantCount:     0,
		},
		{
			name: "load since negative sequence (treated as 0)",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
			},
			loadRunID:     "run-1",
			afterSequence: -5,
			wantCount:     2,
			wantFirstSeq:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			ctx := context.Background()

			// Setup
			for _, e := range tt.setupEvents {
				if err := store.Append(ctx, e); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Test
			events, err := store.LoadSince(ctx, tt.loadRunID, tt.afterSequence)
			if err != nil {
				t.Fatalf("LoadSince() error = %v", err)
			}

			if len(events) != tt.wantCount {
				t.Errorf("LoadSince() returned %d events, want %d", len(events), tt.wantCount)
			}

			if tt.wantCount > 0 && events[0].Sequence != tt.wantFirstSeq {
				t.Errorf("LoadSince() first event sequence = %d, want %d", events[0].Sequence, tt.wantFirstSeq)
			}
		})
	}
}

func TestStore_GetLastSequence(t *testing.T) {
	tests := []struct {
		name        string
		setupEvents []event.Event
		queryRunID  string
		wantSeq     int64
	}{
		{
			name:        "empty store",
			setupEvents: nil,
			queryRunID:  "run-1",
			wantSeq:     0,
		},
		{
			name: "non-existent run",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			queryRunID: "run-2",
			wantSeq:    0,
		},
		{
			name: "single event",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
			},
			queryRunID: "run-1",
			wantSeq:    1,
		},
		{
			name: "multiple events",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-1", 2, event.EventStepStarted),
				makeEvent("evt-3", "run-1", 3, event.EventStepCompleted),
			},
			queryRunID: "run-1",
			wantSeq:    3,
		},
		{
			name: "multiple runs",
			setupEvents: []event.Event{
				makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted),
				makeEvent("evt-2", "run-2", 1, event.EventWorkflowStarted),
				makeEvent("evt-3", "run-1", 2, event.EventStepStarted),
			},
			queryRunID: "run-1",
			wantSeq:    2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New()
			ctx := context.Background()

			// Setup
			for _, e := range tt.setupEvents {
				if err := store.Append(ctx, e); err != nil {
					t.Fatalf("Setup failed: %v", err)
				}
			}

			// Test
			seq, err := store.GetLastSequence(ctx, tt.queryRunID)
			if err != nil {
				t.Fatalf("GetLastSequence() error = %v", err)
			}

			if seq != tt.wantSeq {
				t.Errorf("GetLastSequence() = %d, want %d", seq, tt.wantSeq)
			}
		})
	}
}

func TestStore_ZeroValue(t *testing.T) {
	// Zero value should be ready for use
	var store Store
	ctx := context.Background()

	// Should be able to append
	err := store.Append(ctx, makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted))
	if err != nil {
		t.Errorf("Zero value Append() error = %v", err)
	}

	// Should be able to load
	events, err := store.Load(ctx, "run-1")
	if err != nil {
		t.Errorf("Zero value Load() error = %v", err)
	}
	if len(events) != 1 {
		t.Errorf("Zero value Load() returned %d events, want 1", len(events))
	}
}

func TestStore_Concurrent_DifferentRuns(t *testing.T) {
	store := New()
	ctx := context.Background()

	const numRuns = 10
	const eventsPerRun = 100

	var wg sync.WaitGroup
	errs := make(chan error, numRuns)

	for i := 0; i < numRuns; i++ {
		wg.Add(1)
		runID := "run-" + string(rune('A'+i))

		go func(runID string) {
			defer wg.Done()
			for seq := int64(1); seq <= eventsPerRun; seq++ {
				e := makeEvent(runID+"-"+string(rune('0'+seq%10)), runID, seq, event.EventStepStarted)
				e.ID = runID + "-" + string(rune(seq)) // unique ID
				if err := store.Append(ctx, e); err != nil {
					errs <- err
					return
				}
			}
		}(runID)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("Concurrent append error: %v", err)
	}

	// Verify all runs have correct number of events
	for i := 0; i < numRuns; i++ {
		runID := "run-" + string(rune('A'+i))
		seq, _ := store.GetLastSequence(ctx, runID)
		if seq != eventsPerRun {
			t.Errorf("Run %s has sequence %d, want %d", runID, seq, eventsPerRun)
		}
	}
}

func TestStore_Concurrent_ReadWrite(t *testing.T) {
	store := New()
	ctx := context.Background()

	const numWriters = 5
	const numReaders = 10
	const eventsToWrite = 50

	// Pre-populate with some events
	for i := int64(1); i <= 10; i++ {
		e := makeEvent("evt-"+string(rune('0'+i)), "run-1", i, event.EventStepStarted)
		if err := store.Append(ctx, e); err != nil {
			t.Fatalf("Setup failed: %v", err)
		}
	}

	var wg sync.WaitGroup
	done := make(chan struct{})

	// Start readers
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, err := store.Load(ctx, "run-1")
					if err != nil {
						t.Errorf("Concurrent Load error: %v", err)
						return
					}
					_, err = store.GetLastSequence(ctx, "run-1")
					if err != nil {
						t.Errorf("Concurrent GetLastSequence error: %v", err)
						return
					}
				}
			}
		}()
	}

	// Start writers
	var writeSeq int64 = 11
	var seqMu sync.Mutex

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsToWrite; j++ {
				seqMu.Lock()
				seq := writeSeq
				writeSeq++
				seqMu.Unlock()

				e := makeEvent("evt-w-"+string(rune(seq)), "run-1", seq, event.EventStepStarted)
				// This may fail due to concurrent writes, which is expected
				_ = store.Append(ctx, e)
			}
		}()
	}

	// Let it run for a bit
	time.Sleep(100 * time.Millisecond)
	close(done)
	wg.Wait()
}

func TestStore_SequenceConflictError_Details(t *testing.T) {
	store := New()
	ctx := context.Background()

	// Append first event
	if err := store.Append(ctx, makeEvent("evt-1", "run-1", 1, event.EventWorkflowStarted)); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Try to append with wrong sequence
	err := store.Append(ctx, makeEvent("evt-2", "run-1", 5, event.EventStepStarted))

	var seqErr *event.SequenceConflictError
	if !errors.As(err, &seqErr) {
		t.Fatalf("Expected SequenceConflictError, got %T", err)
	}

	if seqErr.RunID != "run-1" {
		t.Errorf("SequenceConflictError.RunID = %q, want %q", seqErr.RunID, "run-1")
	}
	if seqErr.Expected != 2 {
		t.Errorf("SequenceConflictError.Expected = %d, want %d", seqErr.Expected, 2)
	}
	if seqErr.Actual != 5 {
		t.Errorf("SequenceConflictError.Actual = %d, want %d", seqErr.Actual, 5)
	}
}

// Verify Store implements EventStore interface
var _ event.EventStore = (*Store)(nil)
