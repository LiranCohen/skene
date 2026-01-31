package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/lirancohen/skene/event"
	"golang.org/x/sync/errgroup"
)

// ErrWorkflowCancelled is returned when attempting to execute a cancelled workflow.
var ErrWorkflowCancelled = errors.New("workflow cancelled")

// ErrWorkflowAlreadyCompleted is returned when the workflow has already completed.
var ErrWorkflowAlreadyCompleted = errors.New("workflow already completed")

// Logger defines the logging interface for the replay engine.
type Logger interface {
	Debug(msg string, keysAndValues ...any)
	Info(msg string, keysAndValues ...any)
	Error(msg string, keysAndValues ...any)
}

// noopLogger is a Logger that discards all messages.
type noopLogger struct{}

func (noopLogger) Debug(msg string, keysAndValues ...any) {}
func (noopLogger) Info(msg string, keysAndValues ...any)  {}
func (noopLogger) Error(msg string, keysAndValues ...any) {}

// StepNode represents a step in the workflow DAG.
// This is a minimal interface for the Replayer to work with.
type StepNode interface {
	// Name returns the step's unique name.
	Name() string

	// Dependencies returns the names of steps this step depends on.
	Dependencies() []string

	// Execute runs the step function with the given context.
	// Returns the step output (to be JSON-encoded) or an error.
	Execute(ctx context.Context, wctx *executionContext) (any, error)
}

// WorkflowDef represents a static workflow definition (DAG of steps).
type WorkflowDef struct {
	name    string
	version string
	steps   []StepNode
	// stepMap provides O(1) lookup by step name
	stepMap map[string]StepNode
	// order is the topologically sorted list of step names
	order []string
}

// Name returns the workflow name.
func (w *WorkflowDef) Name() string {
	return w.name
}

// Version returns the workflow version.
func (w *WorkflowDef) Version() string {
	return w.version
}

// WithVersion returns a copy of the workflow definition with the specified version.
func (w *WorkflowDef) WithVersion(version string) *WorkflowDef {
	// Create a shallow copy with the new version
	return &WorkflowDef{
		name:    w.name,
		version: version,
		steps:   w.steps,
		stepMap: w.stepMap,
		order:   w.order,
	}
}

// Steps returns the steps in topological order.
func (w *WorkflowDef) Steps() []StepNode {
	return w.steps
}

// GetStep returns a step by name.
func (w *WorkflowDef) GetStep(name string) (StepNode, bool) {
	step, ok := w.stepMap[name]
	return step, ok
}

// ReplayResult indicates the outcome of a replay operation.
type ReplayResult int

const (
	// ReplayCompleted indicates the workflow finished successfully.
	ReplayCompleted ReplayResult = iota
	// ReplayWaiting indicates the workflow is waiting for a signal.
	ReplayWaiting
	// ReplayFailed indicates the workflow failed with an error.
	ReplayFailed
	// ReplaySuspended indicates more work is needed (for scheduling).
	ReplaySuspended
)

// String returns a string representation of the ReplayResult.
func (r ReplayResult) String() string {
	switch r {
	case ReplayCompleted:
		return "completed"
	case ReplayWaiting:
		return "waiting"
	case ReplayFailed:
		return "failed"
	case ReplaySuspended:
		return "suspended"
	default:
		return "unknown"
	}
}

// ReplayerConfig configures the replay engine.
type ReplayerConfig struct {
	// Workflow is the static workflow definition (DAG).
	Workflow *WorkflowDef

	// History contains the events to replay (nil = fresh execution).
	History *History

	// RunID is the workflow run ID.
	RunID string

	// Input is the workflow input (typed by caller).
	Input any

	// Logger for replay engine logging.
	Logger Logger

	// OnStepEmit is called in two scenarios:
	// 1. During step execution when ctx.Emit(data) is called (mid-step emission)
	// 2. After step completion if output implements StepEmitter (post-step emission)
	// Receives context, step name, and emission data.
	OnStepEmit func(ctx context.Context, stepName string, data any)
}

// ReplayOutput contains the result of a replay operation.
type ReplayOutput struct {
	// Result indicates the replay outcome.
	Result ReplayResult

	// FinalOutput is the output of the final step (when completed).
	FinalOutput any

	// Error contains the error on failure.
	Error error

	// WaitingSignal is the signal name when waiting (deprecated, use WaitingForSignals).
	WaitingSignal string

	// WaitingForSignals lists signals the workflow is waiting for.
	WaitingForSignals []WaitingSignal

	// NewEvents contains events generated during replay.
	NewEvents []event.Event

	// NextSequence is the sequence number for the next event.
	NextSequence int64
}

// stepResult captures the outcome of a parallel step execution.
// Each goroutine produces a stepResult that the main goroutine collects.
type stepResult struct {
	stepName string
	output   any
	err      error
	events   []event.Event
}

// Replayer executes workflows via event sourcing.
// It walks the workflow DAG, skips completed steps (restoring outputs from history),
// and executes pending steps. Independent steps are executed in parallel.
//
// Concurrency: The Replayer uses a mutex to protect the outputCache during
// parallel execution. Event collection from parallel steps is done via
// channels, with sequence numbers assigned after collection.
type Replayer struct {
	config       ReplayerConfig
	history      *History
	logger       Logger
	nextSequence int64

	// outputCache stores deserialized outputs during replay.
	// Protected by outputMu during parallel execution.
	outputCache map[string]any
	outputMu    sync.RWMutex

	// newEvents collects events generated during replay
	// Protected by eventMu during concurrent access (e.g., Map operations)
	newEvents []event.Event
	eventMu   sync.Mutex

	// waitingSignals tracks signals the workflow is waiting for
	// Protected by signalMu during concurrent access
	waitingSignals []WaitingSignal
	signalMu       sync.Mutex

	// childCounter tracks children spawned via Run() for unique RunID generation.
	// Uses atomic operations for thread-safe access.
	childCounter atomic.Int64
	// mapCounter tracks Map calls for unique map identifier generation.
	// Uses atomic operations for thread-safe access.
	mapCounter atomic.Int64
}

// NewReplayer creates a new Replayer with the given configuration.
func NewReplayer(config ReplayerConfig) *Replayer {
	logger := config.Logger
	if logger == nil {
		logger = noopLogger{}
	}

	history := config.History
	if history == nil {
		history = NewHistory(config.RunID, nil)
	}

	return &Replayer{
		config:         config,
		history:        history,
		logger:         logger,
		nextSequence:   history.LastSequence() + 1,
		outputCache:    make(map[string]any),
		newEvents:      make([]event.Event, 0),
		waitingSignals: make([]WaitingSignal, 0),
	}
}

// Replay executes the workflow, returning new events.
// It executes steps in waves, where each wave contains steps whose dependencies
// are all complete. Steps within a wave execute in parallel (fail-fast).
func (r *Replayer) Replay(ctx context.Context) (*ReplayOutput, error) {
	// Check for cancellation
	if r.history.IsCancelled() {
		return &ReplayOutput{
			Result:       ReplayFailed,
			Error:        ErrWorkflowCancelled,
			NewEvents:    r.newEvents,
			NextSequence: r.nextSequence,
		}, nil
	}

	// Check if already completed
	if r.history.IsCompleted() {
		return &ReplayOutput{
			Result:       ReplayCompleted,
			NewEvents:    r.newEvents,
			NextSequence: r.nextSequence,
		}, nil
	}

	// Generate workflow.started if this is a fresh execution
	if r.history.GetWorkflowInput() == nil && len(r.newEvents) == 0 {
		if err := r.emitWorkflowStarted(); err != nil {
			return nil, fmt.Errorf("emit workflow.started: %w", err)
		}
	}

	// Build completed map from history
	completed := make(map[string]bool)
	for _, step := range r.config.Workflow.Steps() {
		stepName := step.Name()
		if r.history.HasCompleted(stepName) {
			completed[stepName] = true
			r.logger.Debug("skipping completed step", "step", stepName)
		}
	}

	// Check for fatal errors in history before starting
	for _, step := range r.config.Workflow.Steps() {
		stepName := step.Name()
		if err := r.history.GetFatalError(stepName); err != nil {
			return &ReplayOutput{
				Result:       ReplayFailed,
				Error:        fmt.Errorf("step %q failed: %w", stepName, err),
				NewEvents:    r.newEvents,
				NextSequence: r.nextSequence,
			}, nil
		}
	}

	// Execute steps in waves
	var finalOutput any
	var lastStepName string

	for {
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			return &ReplayOutput{
				Result:       ReplaySuspended,
				Error:        err,
				NewEvents:    r.newEvents,
				NextSequence: r.nextSequence,
			}, nil
		}

		// Check for workflow cancellation
		if r.history.IsCancelled() {
			return &ReplayOutput{
				Result:       ReplayFailed,
				Error:        ErrWorkflowCancelled,
				NewEvents:    r.newEvents,
				NextSequence: r.nextSequence,
			}, nil
		}

		// Find steps ready to execute
		ready := r.findReadySteps(completed)
		if len(ready) == 0 {
			break
		}

		// Execute ready steps in parallel
		results, err := r.executeParallel(ctx, ready)
		if err != nil {
			return nil, fmt.Errorf("execute parallel: %w", err)
		}

		// Process results: collect events and check for failures/waiting
		var failedResult *stepResult
		var isWaiting bool
		for i := range results {
			result := &results[i]

			// Assign sequence numbers to events and emit them
			for _, evt := range result.events {
				r.emitEvent(evt)
			}

			if result.err != nil {
				if errors.Is(result.err, ErrReplayWaiting) {
					// Step is waiting for signal - not a failure
					isWaiting = true
				} else if failedResult == nil {
					// Record first failure for reporting
					failedResult = result
				}
			} else {
				// Mark step as completed
				completed[result.stepName] = true
				finalOutput = result.output
				lastStepName = result.stepName
			}
		}

		// If any step is waiting for a signal, return waiting status
		if isWaiting {
			return &ReplayOutput{
				Result:            ReplayWaiting,
				WaitingForSignals: r.getWaitingSignals(),
				NewEvents:         r.newEvents,
				NextSequence:      r.nextSequence,
			}, nil
		}

		// If any step failed, return failure (fail-fast already cancelled others)
		if failedResult != nil {
			return &ReplayOutput{
				Result:       ReplayFailed,
				Error:        fmt.Errorf("step %q failed: %w", failedResult.stepName, failedResult.err),
				NewEvents:    r.newEvents,
				NextSequence: r.nextSequence,
			}, nil
		}
	}

	// All steps completed - emit workflow.completed
	if err := r.emitWorkflowCompleted(finalOutput); err != nil {
		return nil, fmt.Errorf("emit workflow.completed: %w", err)
	}

	r.logger.Info("workflow completed", "lastStep", lastStepName)

	return &ReplayOutput{
		Result:       ReplayCompleted,
		FinalOutput:  finalOutput,
		NewEvents:    r.newEvents,
		NextSequence: r.nextSequence,
	}, nil
}

// findReadySteps returns steps whose dependencies are all complete.
// A step is ready if:
// 1. It has not already completed (in history or new events)
// 2. It has no fatal error in history
// 3. All its dependencies have completed
//
// Steps are returned in a deterministic order (sorted by name) for
// consistent event ordering across replays.
func (r *Replayer) findReadySteps(completed map[string]bool) []StepNode {
	var ready []StepNode
	for _, step := range r.config.Workflow.Steps() {
		stepName := step.Name()

		// Skip already completed steps
		if completed[stepName] {
			continue
		}

		// Skip steps with fatal errors
		if r.history.GetFatalError(stepName) != nil {
			continue
		}

		// Check if all dependencies are complete
		allDepsComplete := true
		for _, dep := range step.Dependencies() {
			if !completed[dep] {
				allDepsComplete = false
				break
			}
		}

		if allDepsComplete {
			ready = append(ready, step)
		}
	}

	// Sort by name for deterministic ordering
	sort.Slice(ready, func(i, j int) bool {
		return ready[i].Name() < ready[j].Name()
	})

	return ready
}

// executeParallel runs multiple steps concurrently.
// It uses errgroup for goroutine management and context cancellation.
// On first failure, remaining steps are cancelled (fail-fast).
// All completed steps' events are preserved regardless of failure.
//
// Concurrency: Each goroutine collects its own events. After all goroutines
// complete, events are merged and assigned sequential sequence numbers.
func (r *Replayer) executeParallel(ctx context.Context, steps []StepNode) ([]stepResult, error) {
	if len(steps) == 0 {
		return nil, nil
	}

	// Single step - no need for parallel execution
	if len(steps) == 1 {
		result := r.executeStepWithEvents(ctx, steps[0])
		return []stepResult{result}, nil
	}

	results := make([]stepResult, len(steps))
	g, gctx := errgroup.WithContext(ctx)

	for i, step := range steps {
		i, step := i, step // capture for goroutine
		g.Go(func() error {
			results[i] = r.executeStepWithEvents(gctx, step)
			if results[i].err != nil {
				// Return error to trigger cancellation of other goroutines
				return results[i].err
			}
			return nil
		})
	}

	// Wait for all goroutines to complete
	// Note: g.Wait() returns the first error, but we collect all results
	_ = g.Wait()

	return results, nil
}

// executeStepWithEvents executes a step and returns its result with events.
// This method is safe for concurrent execution.
// It supports retry based on the step's retry policy.
// For branches, events are emitted with the case step name and branch context.
func (r *Replayer) executeStepWithEvents(ctx context.Context, step StepNode) stepResult {
	stepName := step.Name()
	result := stepResult{
		stepName: stepName,
		events:   make([]event.Event, 0, 4), // May need more events for retries
	}

	// Check for context cancellation before execution
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}

	// Get step configuration for retry policy and timeout
	var stepConfig StepConfig
	if configurable, ok := step.(interface{ Config() StepConfig }); ok {
		stepConfig = configurable.Config()
	}

	// Check if this is a branch step - if so, we need to determine the case step name
	// and emit events with that name instead of the branch name
	var branchName string
	var caseValue string
	var eventStepName string = stepName // Default to step name

	// Branches are wrapped in stepNodeAdapter, so we need to unwrap
	if adapter, ok := step.(*stepNodeAdapter); ok {
		if branch, ok := adapter.configured.step.(*Branch); ok {
			branchName = stepName // The branch name

			// Get the choice - first check history (replay mode), then evaluate selector
			// We need to peek at what choice will be made to set up the event step name
			choice, recorded := r.history.GetBranchChoice(branchName)
			if !recorded {
				// Fresh execution - we need to evaluate the selector to know the choice
				// Build a temporary execution context to evaluate the selector
				tmpCtx := r.buildExecutionContext(ctx, branchName)
				wctx := &workflowContextAdapter{executionContext: tmpCtx}
				choice = branch.selector(wctx)

				// Record the choice NOW so the branch won't re-evaluate the selector
				// This ensures selector is only called once per execution
				r.RecordBranchEvaluated(branchName, choice)
			}
			caseValue = choice
			eventStepName = branch.GetCaseStepName(choice)
			if eventStepName == "" {
				eventStepName = stepName // Fallback to branch name if no case found
			}
		}
	}

	// Retry loop
	attempt := 1
	// maxAttempts is used implicitly via ShouldRetry check

	for {
		// Check for context cancellation before each attempt
		if err := ctx.Err(); err != nil {
			result.err = err
			return result
		}

		// Emit step.started event for this attempt
		// For branches, use case step name and include branch context
		startedData, _ := json.Marshal(event.StepStartedData{
			Attempt:    attempt,
			BranchName: branchName,
			CaseValue:  caseValue,
		})
		result.events = append(result.events, event.Event{
			Type:     event.EventStepStarted,
			StepName: eventStepName,
			Data:     startedData,
		})

		// Set up step context with timeout if configured
		stepCtx := ctx
		var cancelFn context.CancelFunc
		if stepConfig.Timeout > 0 {
			stepCtx, cancelFn = context.WithTimeout(ctx, stepConfig.Timeout)
		}

		// Build execution context with the step context (includes timeout)
		execCtx := r.buildExecutionContext(stepCtx, eventStepName)

		// Execute the step
		output, err := step.Execute(stepCtx, execCtx)

		if cancelFn != nil {
			cancelFn()
		}

		if err == nil {
			// Success - cache output and return
			// For branches, cache under the case step name so BranchOutput can retrieve it
			cacheKey := stepName
			if branchName != "" {
				cacheKey = eventStepName
			}
			r.outputMu.Lock()
			r.outputCache[cacheKey] = output
			r.outputMu.Unlock()

			result.output = output

			// Create step.completed event with branch context if applicable
			outputJSON, _ := json.Marshal(output)
			completedData, _ := json.Marshal(event.StepCompletedData{
				BranchName: branchName,
				CaseValue:  caseValue,
			})
			result.events = append(result.events, event.Event{
				Type:     event.EventStepCompleted,
				StepName: eventStepName,
				Output:   outputJSON,
				Data:     completedData,
			})

			// Check for custom emission
			if emitter, ok := output.(StepEmitter); ok {
				if emitData := emitter.GetStepEmission(); emitData != nil {
					if r.config.OnStepEmit != nil {
						r.config.OnStepEmit(ctx, eventStepName, emitData)
					}
				}
			}

			return result
		}

		// Check for replay waiting (step is pausing for signal)
		// This is not a failure - don't emit step.failed, don't retry
		if errors.Is(err, ErrReplayWaiting) {
			result.err = err
			return result
		}

		// Step failed - check if we should retry
		willRetry := stepConfig.RetryPolicy != nil && stepConfig.RetryPolicy.ShouldRetry(attempt, err)

		// Create step.failed event with branch context if applicable
		failedData, _ := json.Marshal(event.StepFailedData{
			Error:      err.Error(),
			Attempt:    attempt,
			WillRetry:  willRetry,
			BranchName: branchName,
			CaseValue:  caseValue,
		})
		result.events = append(result.events, event.Event{
			Type:     event.EventStepFailed,
			StepName: eventStepName,
			Data:     failedData,
		})

		if !willRetry {
			// No more retries - return failure
			result.err = err
			return result
		}

		// Wait before retry (with context cancellation support)
		delay := stepConfig.RetryPolicy.NextDelay(attempt)
		select {
		case <-ctx.Done():
			result.err = ctx.Err()
			return result
		case <-time.After(delay):
			// Continue to next attempt
		}

		attempt++
	}
}

// buildExecutionContext creates an execution context with access to prior outputs.
func (r *Replayer) buildExecutionContext(ctx context.Context, stepName string) *executionContext {
	return &executionContext{
		ctx:          ctx,
		runID:        r.config.RunID,
		workflowName: r.config.Workflow.Name(),
		stepName:     stepName,
		input:        r.config.Input,
		history:      r.history,
		outputCache:  r.outputCache,
		outputMu:     &r.outputMu,
		replayer:     r,
	}
}

// executionContext provides step functions with access to workflow data.
// It is safe for concurrent use when accessing the output cache.
// It implements the Context interface (which embeds context.Context).
type executionContext struct {
	ctx          context.Context
	runID        string
	workflowName string
	stepName     string         // Current step name for emissions
	input        any
	history      *History
	outputCache  map[string]any
	outputMu     *sync.RWMutex // Protects outputCache during parallel execution
	replayer     *Replayer     // Reference to replayer for child workflow execution
}

// Deadline implements context.Context.
func (e *executionContext) Deadline() (deadline time.Time, ok bool) {
	return e.ctx.Deadline()
}

// Done implements context.Context.
func (e *executionContext) Done() <-chan struct{} {
	return e.ctx.Done()
}

// Err implements context.Context.
func (e *executionContext) Err() error {
	return e.ctx.Err()
}

// Value implements context.Context.
func (e *executionContext) Value(key any) any {
	return e.ctx.Value(key)
}

// RunID returns the workflow run ID.
func (e *executionContext) RunID() string {
	return e.runID
}

// WorkflowName returns the workflow name.
func (e *executionContext) WorkflowName() string {
	return e.workflowName
}

// Input returns the workflow input.
func (e *executionContext) Input() any {
	return e.input
}

// GetOutput returns the output for a completed step.
// It first checks the output cache (for steps completed in this replay),
// then falls back to history.
// Thread-safe: uses RLock when accessing outputCache during parallel execution.
func (e *executionContext) GetOutput(stepName string) (json.RawMessage, bool) {
	// Check cache first (current replay session)
	e.outputMu.RLock()
	output, ok := e.outputCache[stepName]
	e.outputMu.RUnlock()

	if ok {
		// Marshal to JSON for consistent interface
		data, err := json.Marshal(output)
		if err != nil {
			return nil, false
		}
		return data, true
	}

	// Fall back to history
	return e.history.GetOutput(stepName)
}

// getReplayer returns the replayer for child workflow execution.
func (e *executionContext) getReplayer() *Replayer {
	return e.replayer
}

// getHistory returns the workflow history.
// Used by signalAccessor interface for signal operations.
func (e *executionContext) getHistory() *History {
	return e.history
}

// getBranchChoice returns the recorded branch choice from history.
// Used by branches to check if a choice was already made (replay mode).
func (e *executionContext) getBranchChoice(branchName string) (string, bool) {
	return e.history.GetBranchChoice(branchName)
}

// recordBranchChoice records a branch choice by emitting a branch.evaluated event.
func (e *executionContext) recordBranchChoice(branchName, choice string) {
	e.replayer.RecordBranchEvaluated(branchName, choice)
}

// Emit sends data to the OnStepEmit callback if configured.
// This is fire-and-forget - emissions don't affect control flow.
// Implements Context.Emit().
func (e *executionContext) Emit(data any) {
	if e.replayer == nil || e.replayer.config.OnStepEmit == nil {
		return
	}
	e.replayer.config.OnStepEmit(e.ctx, e.stepName, data)
}

// emitEvent appends an event to the new events list and returns the populated event.
// Thread-safe for use from concurrent goroutines (e.g., Map operations).
func (r *Replayer) emitEvent(e event.Event) event.Event {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()

	e.ID = uuid.New().String()
	e.RunID = r.config.RunID
	e.Sequence = r.nextSequence
	e.Version = 1
	e.Timestamp = time.Now()

	r.newEvents = append(r.newEvents, e)
	r.nextSequence++

	return e
}

// emitWorkflowStarted emits a workflow.started event.
func (r *Replayer) emitWorkflowStarted() error {
	inputJSON, err := json.Marshal(r.config.Input)
	if err != nil {
		return fmt.Errorf("marshal input: %w", err)
	}

	data, err := json.Marshal(event.WorkflowStartedData{
		WorkflowName: r.config.Workflow.Name(),
		Version:      r.config.Workflow.Version(),
		Input:        inputJSON,
	})
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}

	r.emitEvent(event.Event{
		Type: event.EventWorkflowStarted,
		Data: data,
	})
	return nil
}

// emitWorkflowCompleted emits a workflow.completed event.
func (r *Replayer) emitWorkflowCompleted(output any) error {
	var outputJSON json.RawMessage
	if output != nil {
		var err error
		outputJSON, err = json.Marshal(output)
		if err != nil {
			return fmt.Errorf("marshal output: %w", err)
		}
	}

	data, err := json.Marshal(event.WorkflowCompletedData{
		Output: outputJSON,
	})
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}

	r.emitEvent(event.Event{
		Type: event.EventWorkflowCompleted,
		Data: data,
	})
	return nil
}

// RecordBranchEvaluated appends a branch.evaluated event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
// Also updates the history index so subsequent lookups within the same replay find the choice.
func (r *Replayer) RecordBranchEvaluated(branchName, choice string) event.Event {
	// Update history index so subsequent lookups within this replay find the choice
	// This prevents the branch from re-evaluating the selector
	r.history.RecordBranchChoice(branchName, choice)

	data, _ := json.Marshal(event.BranchEvaluatedData{
		BranchName: branchName,
		Choice:     choice,
	})

	return r.emitEvent(event.Event{
		Type: event.EventBranchEvaluated,
		Data: data,
	})
}

// RecordChildSpawned appends a child.spawned event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordChildSpawned(childRunID, workflowName string, input json.RawMessage) event.Event {
	data, _ := json.Marshal(event.ChildSpawnedData{
		ChildRunID:   childRunID,
		WorkflowName: workflowName,
		Input:        input,
	})

	return r.emitEvent(event.Event{
		Type: event.EventChildSpawned,
		Data: data,
	})
}

// RecordChildCompleted appends a child.completed event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordChildCompleted(childRunID string, output json.RawMessage) event.Event {
	data, _ := json.Marshal(event.ChildCompletedData{
		ChildRunID: childRunID,
		Output:     output,
	})

	return r.emitEvent(event.Event{
		Type: event.EventChildCompleted,
		Data: data,
	})
}

// RecordChildFailed appends a child.failed event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordChildFailed(childRunID string, err error) event.Event {
	data, _ := json.Marshal(event.ChildFailedData{
		ChildRunID: childRunID,
		Error:      err.Error(),
	})

	return r.emitEvent(event.Event{
		Type: event.EventChildFailed,
		Data: data,
	})
}

// RecordMapStarted appends a map.started event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordMapStarted(mapIndex int64, itemCount int) event.Event {
	data, _ := json.Marshal(event.MapStartedData{
		MapIndex:  mapIndex,
		ItemCount: itemCount,
	})

	return r.emitEvent(event.Event{
		Type: event.EventMapStarted,
		Data: data,
	})
}

// RecordMapCompleted appends a map.completed event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordMapCompleted(mapIndex int64, results json.RawMessage) event.Event {
	data, _ := json.Marshal(event.MapCompletedData{
		MapIndex: mapIndex,
		Results:  results,
	})

	return r.emitEvent(event.Event{
		Type: event.EventMapCompleted,
		Data: data,
	})
}

// RecordMapFailed appends a map.failed event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordMapFailed(mapIndex int64, failedIndex int, err error) event.Event {
	data, _ := json.Marshal(event.MapFailedData{
		MapIndex:    mapIndex,
		FailedIndex: failedIndex,
		Error:       err.Error(),
	})

	return r.emitEvent(event.Event{
		Type: event.EventMapFailed,
		Data: data,
	})
}

// addWaitingSignal adds a signal to the waiting signals list.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) addWaitingSignal(signalName string, timeoutAt time.Time) {
	r.signalMu.Lock()
	defer r.signalMu.Unlock()

	// Check if already waiting for this signal
	for _, ws := range r.waitingSignals {
		if ws.SignalName == signalName {
			return
		}
	}

	r.waitingSignals = append(r.waitingSignals, WaitingSignal{
		SignalName: signalName,
		TimeoutAt:  timeoutAt,
	})
}

// getWaitingSignals returns a copy of the waiting signals list.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) getWaitingSignals() []WaitingSignal {
	r.signalMu.Lock()
	defer r.signalMu.Unlock()

	result := make([]WaitingSignal, len(r.waitingSignals))
	copy(result, r.waitingSignals)
	return result
}

// nextChildIndex returns the next child index and increments the counter.
// Thread-safe for use from concurrent goroutines using atomic operations.
func (r *Replayer) nextChildIndex() int64 {
	return r.childCounter.Add(1) - 1
}

// nextMapIndex returns the next map index and increments the counter.
// Thread-safe for use from concurrent goroutines using atomic operations.
func (r *Replayer) nextMapIndex() int64 {
	return r.mapCounter.Add(1) - 1
}

// RecordSignalWaiting appends a signal.waiting event.
// Returns the emitted event.
// Thread-safe for use from concurrent goroutines.
func (r *Replayer) RecordSignalWaiting(signalName string, timeoutAt time.Time) event.Event {
	data, _ := json.Marshal(event.SignalWaitingData{
		SignalName: signalName,
		TimeoutAt:  timeoutAt,
	})

	return r.emitEvent(event.Event{
		Type: event.EventSignalWaiting,
		Data: data,
	})
}
