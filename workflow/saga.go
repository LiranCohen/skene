package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lirancohen/skene/event"
)

// SagaError represents both the original step failure and any compensation failures.
// It is returned when a saga step fails and compensation is attempted.
type SagaError struct {
	// OriginalError is the error that triggered compensation.
	OriginalError error

	// CompensationError contains errors from failed compensation steps.
	// If nil, all compensations succeeded.
	CompensationError error
}

// Error returns a string representation of the saga error.
func (e *SagaError) Error() string {
	if e.CompensationError != nil {
		return fmt.Sprintf("saga failed: %v (compensation also failed: %v)", e.OriginalError, e.CompensationError)
	}
	return fmt.Sprintf("saga failed: %v (compensation succeeded)", e.OriginalError)
}

// Unwrap returns the original error for errors.Is/As support.
func (e *SagaError) Unwrap() error {
	return e.OriginalError
}

// CompensationResult tracks the outcome of compensation execution.
type CompensationResult struct {
	// StepsCompensated lists steps that were successfully compensated.
	StepsCompensated []string

	// FailedStep is the step name that failed compensation, if any.
	FailedStep string

	// Error is the compensation error, if any.
	Error error
}

// runCompensation executes compensation in reverse completion order.
// It continues even if individual compensations fail (best effort).
// Returns a CompensationResult with the outcome.
func (r *Replayer) runCompensation(ctx context.Context, failedStep string, completedSteps []string) (*CompensationResult, error) {
	// Get steps to undo (reverse order of completion)
	stepsToUndo := make([]string, 0, len(completedSteps))
	for i := len(completedSteps) - 1; i >= 0; i-- {
		stepName := completedSteps[i]
		// Only include steps that have compensation functions
		if node, ok := r.config.Workflow.GetStep(stepName); ok {
			if configurable, ok := node.(interface{ Config() StepConfig }); ok {
				if configurable.Config().Compensation != nil {
					stepsToUndo = append(stepsToUndo, stepName)
				}
			}
		}
	}

	if len(stepsToUndo) == 0 {
		return &CompensationResult{}, nil
	}

	// Emit compensation.started event
	if err := r.emitCompensationStarted(failedStep, stepsToUndo); err != nil {
		return nil, fmt.Errorf("emit compensation.started: %w", err)
	}

	result := &CompensationResult{
		StepsCompensated: make([]string, 0, len(stepsToUndo)),
	}

	// Build execution context for compensation
	execCtx := r.buildExecutionContext(ctx, "compensation")

	// Execute compensation in order (already reversed)
	var compensationErrors []error
	for _, stepName := range stepsToUndo {
		node, ok := r.config.Workflow.GetStep(stepName)
		if !ok {
			continue
		}

		configurable, ok := node.(interface{ Config() StepConfig })
		if !ok {
			continue
		}

		compensateFn := configurable.Config().Compensation
		if compensateFn == nil {
			continue
		}

		// Execute compensation function
		if err := compensateFn(execCtx); err != nil {
			// Record failure but continue with other compensations
			compensationErrors = append(compensationErrors, fmt.Errorf("compensate %s: %w", stepName, err))
			if err := r.emitCompensationFailed(stepName, err); err != nil {
				return nil, fmt.Errorf("emit compensation.failed: %w", err)
			}
			if result.FailedStep == "" {
				result.FailedStep = stepName
			}
		} else {
			result.StepsCompensated = append(result.StepsCompensated, stepName)
		}
	}

	// Emit compensation.completed if all succeeded, or we're done trying
	if len(compensationErrors) == 0 {
		if err := r.emitCompensationCompleted(result.StepsCompensated); err != nil {
			return nil, fmt.Errorf("emit compensation.completed: %w", err)
		}
	}

	if len(compensationErrors) > 0 {
		result.Error = errors.Join(compensationErrors...)
	}

	return result, nil
}

// emitCompensationStarted emits a compensation.started event.
func (r *Replayer) emitCompensationStarted(triggerStep string, stepsToUndo []string) error {
	data, err := json.Marshal(event.CompensationStartedData{
		TriggerStep: triggerStep,
		StepsToUndo: stepsToUndo,
	})
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}

	r.emitEvent(event.Event{
		Type: event.EventCompensationStarted,
		Data: data,
	})
	return nil
}

// emitCompensationCompleted emits a compensation.completed event.
func (r *Replayer) emitCompensationCompleted(stepsCompensated []string) error {
	data, err := json.Marshal(event.CompensationCompletedData{
		StepsCompensated: stepsCompensated,
	})
	if err != nil {
		return fmt.Errorf("marshal event data: %w", err)
	}

	r.emitEvent(event.Event{
		Type: event.EventCompensationCompleted,
		Data: data,
	})
	return nil
}

// emitCompensationFailed emits a compensation.failed event.
func (r *Replayer) emitCompensationFailed(failedStep string, err error) error {
	data, marshalErr := json.Marshal(event.CompensationFailedData{
		FailedStep: failedStep,
		Error:      err.Error(),
	})
	if marshalErr != nil {
		return fmt.Errorf("marshal event data: %w", marshalErr)
	}

	r.emitEvent(event.Event{
		Type: event.EventCompensationFailed,
		Data: data,
	})
	return nil
}
