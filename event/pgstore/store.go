// Package pgstore provides a PostgreSQL-based event store implementation.
package pgstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lirancohen/skene/event"
)

// Store implements event.EventStore with PostgreSQL.
// It also implements the TxEventStore interface for transactional operations.
type Store struct {
	pool *pgxpool.Pool
}

// New creates a new PostgreSQL event store.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Append adds a single event to the store.
func (s *Store) Append(ctx context.Context, e event.Event) error {
	return s.AppendBatch(ctx, []event.Event{e})
}

// AppendBatch adds multiple events atomically.
func (s *Store) AppendBatch(ctx context.Context, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := s.appendBatchInTx(ctx, tx, events); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// AppendBatchTx adds events within the given transaction.
// This implements the TxEventStore interface.
// Accepts any type that provides access to a pgx.Tx, either by being a pgx.Tx
// directly, implementing the PgxTxProvider interface, or by being a wrapper type.
func (s *Store) AppendBatchTx(ctx context.Context, tx Tx, events []event.Event) error {
	if len(events) == 0 {
		return nil
	}

	rawTx, err := extractPgxTx(tx)
	if err != nil {
		return err
	}

	return s.appendBatchInTx(ctx, rawTx, events)
}

// appendBatchInTx is the internal implementation for batch append.
func (s *Store) appendBatchInTx(ctx context.Context, tx pgx.Tx, events []event.Event) error {
	// Group events by run_id to validate sequences
	eventsByRun := make(map[string][]event.Event)
	for _, e := range events {
		eventsByRun[e.RunID] = append(eventsByRun[e.RunID], e)
	}

	// Validate sequences for each run
	for runID, runEvents := range eventsByRun {
		// Use advisory lock to prevent concurrent inserts for the same run
		// This avoids the PostgreSQL limitation of FOR UPDATE with aggregates
		_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, runID)
		if err != nil {
			return fmt.Errorf("acquire advisory lock: %w", err)
		}

		// Get current last sequence (advisory lock protects concurrent access)
		var lastSeq int64
		err = tx.QueryRow(ctx, `
			SELECT COALESCE(MAX(sequence), 0)
			FROM skene_events
			WHERE run_id = $1
		`, runID).Scan(&lastSeq)
		if err != nil {
			return fmt.Errorf("get last sequence: %w", err)
		}

		// Validate consecutive sequences
		expectedSeq := lastSeq + 1
		for _, e := range runEvents {
			if e.Sequence != expectedSeq {
				return &event.SequenceConflictError{
					RunID:    runID,
					Expected: expectedSeq,
					Actual:   e.Sequence,
				}
			}
			expectedSeq++
		}
	}

	// Insert all events
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(`
			INSERT INTO skene_events (id, run_id, sequence, version, type, step_name, data, output, metadata, timestamp)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, e.ID, e.RunID, e.Sequence, e.Version, string(e.Type), e.StepName, e.Data, e.Output, e.Metadata, e.Timestamp)
	}

	results := tx.SendBatch(ctx, batch)
	defer results.Close()

	for range events {
		_, err := results.Exec()
		if err != nil {
			// Check for duplicate key error
			if isDuplicateKeyError(err) {
				return event.ErrDuplicateEvent
			}
			return fmt.Errorf("insert event: %w", err)
		}
	}

	return nil
}

// Load retrieves all events for a workflow run, ordered by sequence.
func (s *Store) Load(ctx context.Context, runID string) ([]event.Event, error) {
	return s.loadEvents(ctx, s.pool, runID, 0)
}

// LoadTx loads events within the given transaction.
// This implements the TxEventStore interface.
// Accepts any type that provides access to a pgx.Tx.
func (s *Store) LoadTx(ctx context.Context, tx Tx, runID string) ([]event.Event, error) {
	rawTx, err := extractPgxTx(tx)
	if err != nil {
		return nil, err
	}
	return s.loadEvents(ctx, rawTx, runID, 0)
}

// LoadSince retrieves events with sequence > afterSequence, ordered by sequence.
func (s *Store) LoadSince(ctx context.Context, runID string, afterSequence int64) ([]event.Event, error) {
	return s.loadEvents(ctx, s.pool, runID, afterSequence)
}

// querier is an interface satisfied by both pgxpool.Pool and pgx.Tx.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// loadEvents is the internal implementation for loading events.
func (s *Store) loadEvents(ctx context.Context, q querier, runID string, afterSequence int64) ([]event.Event, error) {
	rows, err := q.Query(ctx, `
		SELECT id, run_id, sequence, version, type, step_name, data, output, metadata, timestamp
		FROM skene_events
		WHERE run_id = $1 AND sequence > $2
		ORDER BY sequence ASC
	`, runID, afterSequence)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []event.Event
	for rows.Next() {
		var e event.Event
		var eventType string
		var stepName *string
		if err := rows.Scan(&e.ID, &e.RunID, &e.Sequence, &e.Version, &eventType, &stepName, &e.Data, &e.Output, &e.Metadata, &e.Timestamp); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.Type = parseEventType(eventType)
		if stepName != nil {
			e.StepName = *stepName
		}
		events = append(events, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	return events, nil
}

// GetLastSequence returns the highest sequence number for a run.
func (s *Store) GetLastSequence(ctx context.Context, runID string) (int64, error) {
	var lastSeq int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(sequence), 0)
		FROM skene_events
		WHERE run_id = $1
	`, runID).Scan(&lastSeq)
	if err != nil {
		return 0, fmt.Errorf("get last sequence: %w", err)
	}
	return lastSeq, nil
}

// Tx represents a database transaction for the TxEventStore interface.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// PgxTxProvider is an interface for types that can provide a pgx.Tx.
// This allows different transaction wrapper types to be used with the store.
type PgxTxProvider interface {
	PgxTx() pgx.Tx
}

// pgxTx wraps a pgx.Tx to satisfy the Tx interface.
type pgxTx struct {
	pgx.Tx
}

// PgxTx returns the underlying pgx.Tx.
func (p pgxTx) PgxTx() pgx.Tx {
	return p.Tx
}

// WrapTx wraps a pgx.Tx to work with the TxEventStore interface.
func WrapTx(tx pgx.Tx) Tx {
	return pgxTx{tx}
}

// extractPgxTx extracts the underlying pgx.Tx from various wrapper types.
func extractPgxTx(tx Tx) (pgx.Tx, error) {
	// Try direct pgx.Tx first
	if pgxTx, ok := tx.(pgx.Tx); ok {
		return pgxTx, nil
	}

	// Try our wrapper
	if wrapper, ok := tx.(pgxTx); ok {
		return wrapper.Tx, nil
	}

	// Try PgxTxProvider interface
	if provider, ok := tx.(PgxTxProvider); ok {
		return provider.PgxTx(), nil
	}

	// Try to access tx field via reflection-like type assertions
	// This handles the river.pgxTxAdapter case
	type txFielder interface {
		Tx() pgx.Tx
	}
	if f, ok := tx.(txFielder); ok {
		return f.Tx(), nil
	}

	return nil, errors.New("pgstore: tx must be a pgx.Tx or implement PgxTxProvider")
}

// parseEventType converts a string to an event.EventType.
func parseEventType(s string) event.EventType {
	switch s {
	case "workflow.started":
		return event.EventWorkflowStarted
	case "workflow.completed":
		return event.EventWorkflowCompleted
	case "workflow.failed":
		return event.EventWorkflowFailed
	case "workflow.cancelled":
		return event.EventWorkflowCancelled
	case "step.started":
		return event.EventStepStarted
	case "step.completed":
		return event.EventStepCompleted
	case "step.failed":
		return event.EventStepFailed
	case "branch.evaluated":
		return event.EventBranchEvaluated
	case "signal.waiting":
		return event.EventSignalWaiting
	case "signal.received":
		return event.EventSignalReceived
	case "signal.timeout":
		return event.EventSignalTimeout
	case "child.spawned":
		return event.EventChildSpawned
	case "child.completed":
		return event.EventChildCompleted
	case "child.failed":
		return event.EventChildFailed
	case "map.started":
		return event.EventMapStarted
	case "map.completed":
		return event.EventMapCompleted
	case "map.failed":
		return event.EventMapFailed
	case "compensation.started":
		return event.EventCompensationStarted
	case "compensation.completed":
		return event.EventCompensationCompleted
	case "compensation.failed":
		return event.EventCompensationFailed
	case "snapshot":
		return event.EventSnapshot
	default:
		return event.EventType(s)
	}
}

// isDuplicateKeyError checks if the error is a PostgreSQL duplicate key violation.
func isDuplicateKeyError(err error) bool {
	// PostgreSQL error code 23505 is unique_violation
	return err != nil && (errors.Is(err, pgx.ErrNoRows) == false &&
		(containsString(err.Error(), "23505") || containsString(err.Error(), "duplicate key")))
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
