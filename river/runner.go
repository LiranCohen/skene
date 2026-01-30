package river

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lirancohen/skene/event"
	"github.com/lirancohen/skene/workflow"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertype"
)

// Common errors returned by the Runner.
var (
	// ErrRunnerNotStarted indicates an operation was attempted before Start.
	ErrRunnerNotStarted = errors.New("runner not started")

	// ErrRunnerAlreadyStarted indicates Start was called twice.
	ErrRunnerAlreadyStarted = errors.New("runner already started")

	// ErrRunNotFound indicates the requested run doesn't exist.
	ErrRunNotFound = errors.New("run not found")

	// ErrInvalidRunStatus indicates the operation is invalid for current status.
	ErrInvalidRunStatus = errors.New("invalid run status for operation")

	// ErrSignalAlreadyReceived indicates a signal was already received.
	ErrSignalAlreadyReceived = errors.New("signal already received")

	// ErrSignalNotWaiting indicates the workflow isn't waiting for this signal.
	ErrSignalNotWaiting = errors.New("signal not waiting")
)

// Runner manages workflow execution with River job queue.
// It implements the complete lifecycle of workflow execution,
// integrating the replay engine with durable job scheduling.
type Runner interface {
	// Lifecycle
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	// Workflow operations
	StartWorkflow(ctx context.Context, name string, input json.RawMessage) (string, error)
	StartWorkflowTx(ctx context.Context, tx pgx.Tx, name string, input json.RawMessage, opts StartOptions) (string, error)
	SendSignal(ctx context.Context, runID, signalName string, payload json.RawMessage) error
	CancelWorkflow(ctx context.Context, runID, reason string) error

	// Queries
	GetRun(ctx context.Context, runID string) (*Run, error)
	ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error)
	GetHistory(ctx context.Context, runID string) ([]event.Event, error)
}

// runner is the concrete implementation of Runner.
type runner struct {
	pool       *pgxpool.Pool
	eventStore event.EventStore
	registry   *Registry
	logger     Logger
	config     Config

	client  *river.Client[pgx.Tx]
	started bool
	mu      sync.RWMutex
}

// NewRunner creates a new Runner with the given configuration.
// Returns an error if required configuration is missing.
func NewRunner(config Config) (*runner, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	cfg := config.withDefaults()

	return &runner{
		pool:       cfg.Pool,
		eventStore: cfg.EventStore,
		registry:   cfg.Registry,
		logger:     cfg.Logger,
		config:     cfg,
	}, nil
}

// Start initializes the River client and starts workers.
// Must be called before any workflow operations.
func (r *runner) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return ErrRunnerAlreadyStarted
	}

	// Create River workers
	workers := river.NewWorkers()

	// Register workflow job worker
	river.AddWorker(workers, &workflowWorker{runner: r})

	// Register signal timeout worker
	river.AddWorker(workers, &signalTimeoutWorker{runner: r})

	// Register scheduled start worker
	river.AddWorker(workers, &scheduledStartWorker{runner: r})

	// Create River client
	client, err := river.NewClient(riverpgxv5.New(r.pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: r.config.Workers},
		},
		Workers:     workers,
		JobTimeout:  r.config.JobTimeout,
		ErrorHandler: &errorHandler{logger: r.logger},
	})
	if err != nil {
		return fmt.Errorf("create river client: %w", err)
	}

	r.client = client

	// Start the client
	if err := r.client.Start(ctx); err != nil {
		return fmt.Errorf("start river client: %w", err)
	}

	r.started = true
	r.logger.Info("runner started", "workers", r.config.Workers)

	return nil
}

// Stop gracefully shuts down the runner.
// Waits for in-flight jobs up to ShutdownTimeout.
func (r *runner) Stop(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return nil
	}

	// Create timeout context for shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, r.config.ShutdownTimeout)
	defer cancel()

	// Stop the River client
	if err := r.client.Stop(shutdownCtx); err != nil {
		r.logger.Warn("river client stop error", "error", err)
	}

	r.started = false
	r.logger.Info("runner stopped")

	return nil
}

// StartWorkflow starts a new workflow execution.
// Generates a UUID for RunID if not provided in opts.
// Returns the RunID.
func (r *runner) StartWorkflow(ctx context.Context, name string, input json.RawMessage) (string, error) {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return "", ErrRunnerNotStarted
	}

	// Start a transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	runID, err := r.StartWorkflowTx(ctx, tx, name, input, StartOptions{})
	if err != nil {
		return "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit transaction: %w", err)
	}

	return runID, nil
}

// StartWorkflowTx starts a workflow within an existing transaction.
// This allows atomic workflow start with other operations.
func (r *runner) StartWorkflowTx(ctx context.Context, tx pgx.Tx, name string, input json.RawMessage, opts StartOptions) (string, error) {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return "", ErrRunnerNotStarted
	}

	// Validate workflow exists
	def, err := r.registry.Get(name)
	if err != nil {
		return "", err
	}

	// Generate or use provided RunID
	runID := opts.RunID
	if runID == "" {
		runID = uuid.New().String()
	}

	// Create workflow.started event
	startedData, err := json.Marshal(event.WorkflowStartedData{
		WorkflowName: name,
		Version:      def.Version(),
		Input:        input,
	})
	if err != nil {
		return "", fmt.Errorf("marshal started data: %w", err)
	}

	startEvent := event.Event{
		ID:        uuid.New().String(),
		RunID:     runID,
		Sequence:  1,
		Version:   1,
		Type:      event.EventWorkflowStarted,
		Data:      startedData,
		Timestamp: time.Now(),
	}

	// Append event using transaction
	if txStore, ok := r.eventStore.(TxEventStore); ok {
		if err := txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, []event.Event{startEvent}); err != nil {
			return "", fmt.Errorf("append started event: %w", err)
		}
	} else {
		// Fall back to non-transactional append
		if err := r.eventStore.Append(ctx, startEvent); err != nil {
			return "", fmt.Errorf("append started event: %w", err)
		}
	}

	// Insert workflow job
	insertOpts := &river.InsertOpts{
		MaxAttempts: 3,
	}
	if opts.Priority > 0 {
		insertOpts.Priority = opts.Priority
	}

	_, err = r.client.InsertTx(ctx, tx, WorkflowJobArgs{
		RunID:        runID,
		WorkflowName: name,
		Version:      def.Version(),
	}, insertOpts)
	if err != nil {
		return "", fmt.Errorf("insert workflow job: %w", err)
	}

	r.logger.Info("workflow started", "runID", runID, "workflow", name)

	return runID, nil
}

// SendSignal sends a signal to a waiting workflow.
// Idempotent: sending the same signal twice is a no-op.
func (r *runner) SendSignal(ctx context.Context, runID, signalName string, payload json.RawMessage) error {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return ErrRunnerNotStarted
	}

	// Load events to check current state
	events, err := r.eventStore.Load(ctx, runID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	if len(events) == 0 {
		return ErrRunNotFound
	}

	history := workflow.NewHistory(runID, events)

	// Check signal state
	state := history.GetSignalState(signalName)
	if state == nil || !state.Waiting {
		return ErrSignalNotWaiting
	}
	if state.Received {
		// Idempotent: already received
		r.logger.Debug("signal already received", "runID", runID, "signal", signalName)
		return nil
	}
	if state.Timeout {
		// Signal timed out, cannot receive
		return fmt.Errorf("signal %q already timed out", signalName)
	}

	// Start transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Append signal.received event
	receivedData, err := json.Marshal(event.SignalReceivedData{
		SignalName: signalName,
		Payload:    payload,
	})
	if err != nil {
		return fmt.Errorf("marshal signal received data: %w", err)
	}

	receivedEvent := event.Event{
		ID:        uuid.New().String(),
		RunID:     runID,
		Sequence:  history.LastSequence() + 1,
		Version:   1,
		Type:      event.EventSignalReceived,
		Data:      receivedData,
		Timestamp: time.Now(),
	}

	if txStore, ok := r.eventStore.(TxEventStore); ok {
		if err := txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, []event.Event{receivedEvent}); err != nil {
			return fmt.Errorf("append signal received event: %w", err)
		}
	} else {
		if err := r.eventStore.Append(ctx, receivedEvent); err != nil {
			return fmt.Errorf("append signal received event: %w", err)
		}
	}

	// Get workflow name from history to schedule resume job
	var workflowName string
	var version string
	for _, e := range events {
		if e.Type == event.EventWorkflowStarted {
			var data event.WorkflowStartedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				workflowName = data.WorkflowName
				version = data.Version
			}
			break
		}
	}

	// Insert resume job
	_, err = r.client.InsertTx(ctx, tx, WorkflowJobArgs{
		RunID:        runID,
		WorkflowName: workflowName,
		Version:      version,
	}, nil)
	if err != nil {
		return fmt.Errorf("insert resume job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	r.logger.Info("signal sent", "runID", runID, "signal", signalName)

	return nil
}

// CancelWorkflow cancels a running or waiting workflow.
func (r *runner) CancelWorkflow(ctx context.Context, runID, reason string) error {
	r.mu.RLock()
	started := r.started
	r.mu.RUnlock()

	if !started {
		return ErrRunnerNotStarted
	}

	// Load events to check current state
	events, err := r.eventStore.Load(ctx, runID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	if len(events) == 0 {
		return ErrRunNotFound
	}

	history := workflow.NewHistory(runID, events)

	// Check if workflow is cancellable
	if history.IsCancelled() {
		return nil // Already cancelled, idempotent
	}
	if history.IsCompleted() {
		return ErrInvalidRunStatus
	}

	// Start transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Append workflow.cancelled event
	cancelledData, err := json.Marshal(event.WorkflowCancelledData{
		Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("marshal cancelled data: %w", err)
	}

	cancelledEvent := event.Event{
		ID:        uuid.New().String(),
		RunID:     runID,
		Sequence:  history.LastSequence() + 1,
		Version:   1,
		Type:      event.EventWorkflowCancelled,
		Data:      cancelledData,
		Timestamp: time.Now(),
	}

	if txStore, ok := r.eventStore.(TxEventStore); ok {
		if err := txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, []event.Event{cancelledEvent}); err != nil {
			return fmt.Errorf("append cancelled event: %w", err)
		}
	} else {
		if err := r.eventStore.Append(ctx, cancelledEvent); err != nil {
			return fmt.Errorf("append cancelled event: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	r.logger.Info("workflow cancelled", "runID", runID, "reason", reason)

	return nil
}

// GetRun returns the full Run details from events.
func (r *runner) GetRun(ctx context.Context, runID string) (*Run, error) {
	events, err := r.eventStore.Load(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load events: %w", err)
	}
	if len(events) == 0 {
		return nil, ErrRunNotFound
	}

	return buildRunFromEvents(runID, events), nil
}

// ListRuns returns run summaries matching the filter.
func (r *runner) ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error) {
	// This is a placeholder - actual implementation requires querying
	// a runs table or scanning events, which depends on the EventStore
	// implementation. For now, return empty slice.
	return []RunSummary{}, nil
}

// GetHistory returns all events for a run.
func (r *runner) GetHistory(ctx context.Context, runID string) ([]event.Event, error) {
	events, err := r.eventStore.Load(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("load events: %w", err)
	}
	if len(events) == 0 {
		return nil, ErrRunNotFound
	}
	return events, nil
}

// buildRunFromEvents constructs a Run from event history.
func buildRunFromEvents(runID string, events []event.Event) *Run {
	run := &Run{
		ID:     runID,
		Status: RunStatusPending,
	}

	for _, e := range events {
		switch e.Type {
		case event.EventWorkflowStarted:
			var data event.WorkflowStartedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				run.WorkflowName = data.WorkflowName
				run.Version = data.Version
				run.Input = data.Input
				run.StartedAt = e.Timestamp
				run.Status = RunStatusRunning
			}

		case event.EventWorkflowCompleted:
			var data event.WorkflowCompletedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				run.Output = data.Output
				completedAt := e.Timestamp
				run.CompletedAt = &completedAt
				run.Status = RunStatusCompleted
			}

		case event.EventWorkflowFailed:
			var data event.WorkflowFailedData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				run.Error = data.Error
				completedAt := e.Timestamp
				run.CompletedAt = &completedAt
				run.Status = RunStatusFailed
			}

		case event.EventWorkflowCancelled:
			var data event.WorkflowCancelledData
			if err := json.Unmarshal(e.Data, &data); err == nil {
				run.Error = data.Reason
				completedAt := e.Timestamp
				run.CompletedAt = &completedAt
				run.Status = RunStatusCancelled
			}

		case event.EventSignalWaiting:
			if run.Status == RunStatusRunning {
				run.Status = RunStatusWaiting
			}

		case event.EventSignalReceived:
			if run.Status == RunStatusWaiting {
				run.Status = RunStatusRunning
			}
		}
	}

	return run
}

// pgxTxAdapter adapts pgx.Tx to the Tx interface.
type pgxTxAdapter struct {
	tx pgx.Tx
}

func (a pgxTxAdapter) Commit(ctx context.Context) error {
	return a.tx.Commit(ctx)
}

func (a pgxTxAdapter) Rollback(ctx context.Context) error {
	return a.tx.Rollback(ctx)
}

// PgxTx returns the underlying pgx.Tx.
// This satisfies the pgstore.PgxTxProvider interface.
func (a pgxTxAdapter) PgxTx() pgx.Tx {
	return a.tx
}

// errorHandler handles River job errors.
type errorHandler struct {
	logger Logger
}

func (h *errorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	h.logger.Error("job error", "job_kind", job.Kind, "error", err)
	return nil
}

func (h *errorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	h.logger.Error("job panic", "job_kind", job.Kind, "panic", panicVal, "trace", trace)
	return nil
}
