package river

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/lirancohen/skene/event"
	"github.com/lirancohen/skene/workflow"
	"github.com/riverqueue/river"
)

// workflowWorker processes main workflow execution jobs.
type workflowWorker struct {
	river.WorkerDefaults[WorkflowJobArgs]
	runner *runner
}

// Work executes the workflow job.
func (w *workflowWorker) Work(ctx context.Context, job *river.Job[WorkflowJobArgs]) error {
	args := job.Args
	r := w.runner

	r.logger.Debug("executing workflow job",
		"runID", args.RunID,
		"workflow", args.WorkflowName,
		"version", args.Version,
	)

	// Start transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Load events
	var events []event.Event
	if txStore, ok := r.eventStore.(TxEventStore); ok {
		events, err = txStore.LoadTx(ctx, pgxTxAdapter{tx}, args.RunID)
	} else {
		events, err = r.eventStore.Load(ctx, args.RunID)
	}
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	// Check for cancellation or completion
	history := workflow.NewHistory(args.RunID, events)
	if history.IsCancelled() {
		r.logger.Info("workflow already cancelled", "runID", args.RunID)
		return nil
	}
	if history.IsCompleted() {
		r.logger.Info("workflow already completed", "runID", args.RunID)
		return nil
	}

	// Get workflow definition
	def, err := r.registry.Get(args.WorkflowName)
	if err != nil {
		return fmt.Errorf("get workflow definition: %w", err)
	}

	// Get workflow input from started event
	var workflowInput any
	if inputData := history.GetWorkflowInput(); inputData != nil {
		workflowInput = inputData
	}

	// Create replayer
	replayer := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: def,
		History:  history,
		RunID:    args.RunID,
		Input:    workflowInput,
		Logger:   workflowLoggerAdapter{r.logger},
	})

	// Execute replay
	output, err := replayer.Replay(ctx)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}

	// Append new events
	if len(output.NewEvents) > 0 {
		if txStore, ok := r.eventStore.(TxEventStore); ok {
			if err := txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, output.NewEvents); err != nil {
				return fmt.Errorf("append events: %w", err)
			}
		} else {
			if err := r.eventStore.AppendBatch(ctx, output.NewEvents); err != nil {
				return fmt.Errorf("append events: %w", err)
			}
		}
	}

	// Handle replay result
	switch output.Result {
	case workflow.ReplayCompleted:
		r.logger.Info("workflow completed", "runID", args.RunID)

	case workflow.ReplayFailed:
		r.logger.Info("workflow failed", "runID", args.RunID, "error", output.Error)
		// Emit workflow.failed event
		if err := w.emitWorkflowFailed(ctx, tx, args.RunID, output.NextSequence, output.Error); err != nil {
			return fmt.Errorf("emit workflow failed: %w", err)
		}

	case workflow.ReplayWaiting:
		r.logger.Debug("workflow waiting for signal", "runID", args.RunID)
		// Schedule signal timeout jobs
		for _, sig := range output.WaitingForSignals {
			if err := w.scheduleSignalTimeout(ctx, tx, args.RunID, sig); err != nil {
				return fmt.Errorf("schedule signal timeout: %w", err)
			}
		}

	case workflow.ReplaySuspended:
		r.logger.Debug("workflow suspended, scheduling next job", "runID", args.RunID)
		// Schedule next execution job
		_, err := r.client.InsertTx(ctx, tx, WorkflowJobArgs{
			RunID:        args.RunID,
			WorkflowName: args.WorkflowName,
			Version:      args.Version,
		}, nil)
		if err != nil {
			return fmt.Errorf("insert next job: %w", err)
		}
	}

	return tx.Commit(ctx)
}

// emitWorkflowFailed appends a workflow.failed event.
func (w *workflowWorker) emitWorkflowFailed(ctx context.Context, tx pgx.Tx, runID string, sequence int64, workflowErr error) error {
	r := w.runner

	failedData, err := json.Marshal(event.WorkflowFailedData{
		Error: workflowErr.Error(),
	})
	if err != nil {
		return fmt.Errorf("marshal failed data: %w", err)
	}

	failedEvent := event.Event{
		ID:        uuid.New().String(),
		RunID:     runID,
		Sequence:  sequence,
		Version:   1,
		Type:      event.EventWorkflowFailed,
		Data:      failedData,
		Timestamp: time.Now(),
	}

	if txStore, ok := r.eventStore.(TxEventStore); ok {
		return txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, []event.Event{failedEvent})
	}
	return r.eventStore.Append(ctx, failedEvent)
}

// scheduleSignalTimeout schedules a signal timeout job.
func (w *workflowWorker) scheduleSignalTimeout(ctx context.Context, tx pgx.Tx, runID string, sig workflow.WaitingSignal) error {
	r := w.runner

	_, err := r.client.InsertTx(ctx, tx, SignalTimeoutJobArgs{
		RunID:      runID,
		SignalName: sig.SignalName,
	}, &river.InsertOpts{
		ScheduledAt: sig.TimeoutAt,
	})
	return err
}

// signalTimeoutWorker processes signal timeout jobs.
type signalTimeoutWorker struct {
	river.WorkerDefaults[SignalTimeoutJobArgs]
	runner *runner
}

// Work handles signal timeout.
func (w *signalTimeoutWorker) Work(ctx context.Context, job *river.Job[SignalTimeoutJobArgs]) error {
	args := job.Args
	r := w.runner

	r.logger.Debug("processing signal timeout",
		"runID", args.RunID,
		"signal", args.SignalName,
	)

	// Load events to check signal state
	events, err := r.eventStore.Load(ctx, args.RunID)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	history := workflow.NewHistory(args.RunID, events)

	// Check if signal was already received
	state := history.GetSignalState(args.SignalName)
	if state == nil {
		// Signal not in history, shouldn't happen
		r.logger.Warn("signal timeout for unknown signal",
			"runID", args.RunID,
			"signal", args.SignalName,
		)
		return nil
	}

	if state.Received {
		// Signal already received, no timeout needed
		r.logger.Debug("signal already received, skipping timeout",
			"runID", args.RunID,
			"signal", args.SignalName,
		)
		return nil
	}

	if state.Timeout {
		// Already timed out
		return nil
	}

	// Start transaction
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// Append signal.timeout event
	timeoutData, err := json.Marshal(event.SignalTimeoutData{
		SignalName: args.SignalName,
	})
	if err != nil {
		return fmt.Errorf("marshal timeout data: %w", err)
	}

	timeoutEvent := event.Event{
		ID:        uuid.New().String(),
		RunID:     args.RunID,
		Sequence:  history.LastSequence() + 1,
		Version:   1,
		Type:      event.EventSignalTimeout,
		Data:      timeoutData,
		Timestamp: time.Now(),
	}

	if txStore, ok := r.eventStore.(TxEventStore); ok {
		if err := txStore.AppendBatchTx(ctx, pgxTxAdapter{tx}, []event.Event{timeoutEvent}); err != nil {
			return fmt.Errorf("append timeout event: %w", err)
		}
	} else {
		if err := r.eventStore.Append(ctx, timeoutEvent); err != nil {
			return fmt.Errorf("append timeout event: %w", err)
		}
	}

	// Get workflow info for resume job
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
		RunID:        args.RunID,
		WorkflowName: workflowName,
		Version:      version,
	}, nil)
	if err != nil {
		return fmt.Errorf("insert resume job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	r.logger.Info("signal timed out", "runID", args.RunID, "signal", args.SignalName)

	return nil
}

// scheduledStartWorker processes scheduled workflow start jobs.
type scheduledStartWorker struct {
	river.WorkerDefaults[ScheduledStartJobArgs]
	runner *runner
}

// Work starts a workflow at the scheduled time.
func (w *scheduledStartWorker) Work(ctx context.Context, job *river.Job[ScheduledStartJobArgs]) error {
	args := job.Args
	r := w.runner

	r.logger.Debug("processing scheduled start",
		"workflow", args.WorkflowName,
		"runID", args.RunID,
	)

	opts := StartOptions{
		RunID:    args.RunID,
		Priority: args.Priority,
	}

	// Start a transaction for atomic workflow start
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	runID, err := r.StartWorkflowTx(ctx, tx, args.WorkflowName, args.Input, opts)
	if err != nil {
		return fmt.Errorf("start workflow: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	r.logger.Info("scheduled workflow started",
		"workflow", args.WorkflowName,
		"runID", runID,
	)

	return nil
}

// workflowLoggerAdapter adapts the runner Logger to workflow.Logger.
type workflowLoggerAdapter struct {
	logger Logger
}

func (a workflowLoggerAdapter) Debug(msg string, keysAndValues ...any) {
	a.logger.Debug(msg, keysAndValues...)
}

func (a workflowLoggerAdapter) Info(msg string, keysAndValues ...any) {
	a.logger.Info(msg, keysAndValues...)
}

func (a workflowLoggerAdapter) Error(msg string, keysAndValues ...any) {
	a.logger.Error(msg, keysAndValues...)
}
