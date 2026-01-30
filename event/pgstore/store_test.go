//go:build integration

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lirancohen/skene/event"
	"github.com/lirancohen/skene/event/pgstore"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupTestDB(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("skene_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to create pool: %v", err)
	}

	// Create the events table
	_, err = pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS skene_events (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			sequence BIGINT NOT NULL,
			version INTEGER NOT NULL DEFAULT 1,
			type TEXT NOT NULL,
			step_name TEXT,
			data JSONB,
			output JSONB,
			metadata JSONB,
			timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT skene_events_run_sequence UNIQUE (run_id, sequence)
		);
		CREATE INDEX IF NOT EXISTS idx_skene_events_run_id ON skene_events (run_id, sequence);
	`)
	if err != nil {
		pool.Close()
		container.Terminate(ctx)
		t.Fatalf("failed to create table: %v", err)
	}

	cleanup := func() {
		pool.Close()
		container.Terminate(ctx)
	}

	return pool, cleanup
}

func TestStore_Append(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := pgstore.New(pool)
	ctx := context.Background()

	tests := []struct {
		name      string
		event     event.Event
		wantErr   bool
		errTarget error
	}{
		{
			name: "first event with sequence 1",
			event: event.Event{
				ID:        "evt-1",
				RunID:     "run-1",
				Sequence:  1,
				Version:   1,
				Type:      event.EventWorkflowStarted,
				Timestamp: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "second event with sequence 2",
			event: event.Event{
				ID:        "evt-2",
				RunID:     "run-1",
				Sequence:  2,
				Version:   1,
				Type:      event.EventStepStarted,
				Timestamp: time.Now(),
			},
			wantErr: false,
		},
		{
			name: "wrong sequence (too high)",
			event: event.Event{
				ID:        "evt-3",
				RunID:     "run-1",
				Sequence:  5,
				Version:   1,
				Type:      event.EventStepCompleted,
				Timestamp: time.Now(),
			},
			wantErr:   true,
			errTarget: event.ErrSequenceConflict,
		},
		{
			name: "duplicate event ID",
			event: event.Event{
				ID:        "evt-1",
				RunID:     "run-2",
				Sequence:  1,
				Version:   1,
				Type:      event.EventWorkflowStarted,
				Timestamp: time.Now(),
			},
			wantErr:   true,
			errTarget: event.ErrDuplicateEvent,
		},
		{
			name: "different run starts at sequence 1",
			event: event.Event{
				ID:        "evt-run2-1",
				RunID:     "run-2",
				Sequence:  1,
				Version:   1,
				Type:      event.EventWorkflowStarted,
				Timestamp: time.Now(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Append(ctx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("Append() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.errTarget != nil {
				var seqErr *event.SequenceConflictError
				if tt.errTarget == event.ErrSequenceConflict {
					if ok := isSequenceConflictError(err, &seqErr); !ok {
						t.Errorf("Append() error = %v, want SequenceConflictError", err)
					}
				} else if tt.errTarget == event.ErrDuplicateEvent {
					if err != event.ErrDuplicateEvent {
						t.Errorf("Append() error = %v, want ErrDuplicateEvent", err)
					}
				}
			}
		})
	}
}

func TestStore_AppendBatch(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := pgstore.New(pool)
	ctx := context.Background()

	tests := []struct {
		name    string
		events  []event.Event
		wantErr bool
	}{
		{
			name:    "empty batch",
			events:  []event.Event{},
			wantErr: false,
		},
		{
			name: "single event batch",
			events: []event.Event{
				{
					ID:        "batch-1",
					RunID:     "batch-run-1",
					Sequence:  1,
					Version:   1,
					Type:      event.EventWorkflowStarted,
					Timestamp: time.Now(),
				},
			},
			wantErr: false,
		},
		{
			name: "multiple events in batch",
			events: []event.Event{
				{
					ID:        "batch-2",
					RunID:     "batch-run-2",
					Sequence:  1,
					Version:   1,
					Type:      event.EventWorkflowStarted,
					Timestamp: time.Now(),
				},
				{
					ID:        "batch-3",
					RunID:     "batch-run-2",
					Sequence:  2,
					Version:   1,
					Type:      event.EventStepStarted,
					Timestamp: time.Now(),
				},
				{
					ID:        "batch-4",
					RunID:     "batch-run-2",
					Sequence:  3,
					Version:   1,
					Type:      event.EventStepCompleted,
					Timestamp: time.Now(),
				},
			},
			wantErr: false,
		},
		{
			name: "batch with gap in sequence",
			events: []event.Event{
				{
					ID:        "gap-1",
					RunID:     "gap-run",
					Sequence:  1,
					Version:   1,
					Type:      event.EventWorkflowStarted,
					Timestamp: time.Now(),
				},
				{
					ID:        "gap-2",
					RunID:     "gap-run",
					Sequence:  3, // gap: skips 2
					Version:   1,
					Type:      event.EventStepStarted,
					Timestamp: time.Now(),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.AppendBatch(ctx, tt.events)
			if (err != nil) != tt.wantErr {
				t.Errorf("AppendBatch() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStore_Load(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := pgstore.New(pool)
	ctx := context.Background()

	// Add some events
	events := []event.Event{
		{
			ID:        "load-1",
			RunID:     "load-run",
			Sequence:  1,
			Version:   1,
			Type:      event.EventWorkflowStarted,
			Timestamp: time.Now(),
		},
		{
			ID:        "load-2",
			RunID:     "load-run",
			Sequence:  2,
			Version:   1,
			Type:      event.EventStepStarted,
			Timestamp: time.Now(),
		},
		{
			ID:        "load-3",
			RunID:     "load-run",
			Sequence:  3,
			Version:   1,
			Type:      event.EventStepCompleted,
			Timestamp: time.Now(),
		},
	}

	if err := store.AppendBatch(ctx, events); err != nil {
		t.Fatalf("failed to append events: %v", err)
	}

	tests := []struct {
		name      string
		runID     string
		wantCount int
	}{
		{
			name:      "load existing run",
			runID:     "load-run",
			wantCount: 3,
		},
		{
			name:      "load non-existent run",
			runID:     "non-existent",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded, err := store.Load(ctx, tt.runID)
			if err != nil {
				t.Errorf("Load() error = %v", err)
				return
			}
			if len(loaded) != tt.wantCount {
				t.Errorf("Load() got %d events, want %d", len(loaded), tt.wantCount)
			}

			// Verify order
			for i := 1; i < len(loaded); i++ {
				if loaded[i].Sequence <= loaded[i-1].Sequence {
					t.Errorf("Load() events not in order: %d <= %d", loaded[i].Sequence, loaded[i-1].Sequence)
				}
			}
		})
	}
}

func TestStore_LoadSince(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := pgstore.New(pool)
	ctx := context.Background()

	// Add events
	events := []event.Event{
		{
			ID:        "since-1",
			RunID:     "since-run",
			Sequence:  1,
			Version:   1,
			Type:      event.EventWorkflowStarted,
			Timestamp: time.Now(),
		},
		{
			ID:        "since-2",
			RunID:     "since-run",
			Sequence:  2,
			Version:   1,
			Type:      event.EventStepStarted,
			Timestamp: time.Now(),
		},
		{
			ID:        "since-3",
			RunID:     "since-run",
			Sequence:  3,
			Version:   1,
			Type:      event.EventStepCompleted,
			Timestamp: time.Now(),
		},
	}

	if err := store.AppendBatch(ctx, events); err != nil {
		t.Fatalf("failed to append events: %v", err)
	}

	tests := []struct {
		name          string
		runID         string
		afterSequence int64
		wantCount     int
		wantFirstSeq  int64
	}{
		{
			name:          "load all (since 0)",
			runID:         "since-run",
			afterSequence: 0,
			wantCount:     3,
			wantFirstSeq:  1,
		},
		{
			name:          "load since sequence 1",
			runID:         "since-run",
			afterSequence: 1,
			wantCount:     2,
			wantFirstSeq:  2,
		},
		{
			name:          "load since last sequence",
			runID:         "since-run",
			afterSequence: 3,
			wantCount:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loaded, err := store.LoadSince(ctx, tt.runID, tt.afterSequence)
			if err != nil {
				t.Errorf("LoadSince() error = %v", err)
				return
			}
			if len(loaded) != tt.wantCount {
				t.Errorf("LoadSince() got %d events, want %d", len(loaded), tt.wantCount)
			}
			if tt.wantCount > 0 && loaded[0].Sequence != tt.wantFirstSeq {
				t.Errorf("LoadSince() first sequence = %d, want %d", loaded[0].Sequence, tt.wantFirstSeq)
			}
		})
	}
}

func TestStore_GetLastSequence(t *testing.T) {
	pool, cleanup := setupTestDB(t)
	defer cleanup()

	store := pgstore.New(pool)
	ctx := context.Background()

	// Add events
	events := []event.Event{
		{
			ID:        "last-1",
			RunID:     "last-run",
			Sequence:  1,
			Version:   1,
			Type:      event.EventWorkflowStarted,
			Timestamp: time.Now(),
		},
		{
			ID:        "last-2",
			RunID:     "last-run",
			Sequence:  2,
			Version:   1,
			Type:      event.EventStepStarted,
			Timestamp: time.Now(),
		},
	}

	if err := store.AppendBatch(ctx, events); err != nil {
		t.Fatalf("failed to append events: %v", err)
	}

	tests := []struct {
		name    string
		runID   string
		wantSeq int64
	}{
		{
			name:    "existing run",
			runID:   "last-run",
			wantSeq: 2,
		},
		{
			name:    "non-existent run",
			runID:   "non-existent",
			wantSeq: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seq, err := store.GetLastSequence(ctx, tt.runID)
			if err != nil {
				t.Errorf("GetLastSequence() error = %v", err)
				return
			}
			if seq != tt.wantSeq {
				t.Errorf("GetLastSequence() = %d, want %d", seq, tt.wantSeq)
			}
		})
	}
}

func isSequenceConflictError(err error, target **event.SequenceConflictError) bool {
	if err == nil {
		return false
	}
	seqErr, ok := err.(*event.SequenceConflictError)
	if ok && target != nil {
		*target = seqErr
	}
	return ok
}
