package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lirancohen/skene/retry"
)

// AnyStep is a type-erased step interface for dependency declarations.
// It allows Step[T] of any type to be used as dependencies.
type AnyStep interface {
	Name() string
}

// executableStep extends AnyStep with an Execute method.
// This is used internally by the Replayer.
type executableStep interface {
	AnyStep
	Execute(ctx context.Context, wctx Context) (any, error)
	Config() StepConfig
}

// StepConfig holds configuration for a step.
type StepConfig struct {
	// RetryPolicy is the retry policy for the step. If nil, no retries.
	RetryPolicy *retry.Policy

	// Timeout is the maximum duration for a single step execution.
	// Zero means no timeout.
	Timeout time.Duration

	// Compensation is the function to call when rolling back this step
	// in a saga. If nil, no compensation is needed.
	Compensation func(Context) error
}

// Step is a typed handle for a workflow step.
// It carries the step's output type as a type parameter, enabling
// compile-time checking of dependencies and type-safe output access.
type Step[T any] struct {
	name   string
	fn     func(Context) (T, error)
	config StepConfig
}

// NewStep creates a new typed step handle.
// The name must be unique within the workflow.
// The function receives a Context and returns the typed output or an error.
func NewStep[T any](name string, fn func(Context) (T, error)) Step[T] {
	return Step[T]{
		name: name,
		fn:   fn,
	}
}

// Name returns the step name.
func (s Step[T]) Name() string {
	return s.name
}

// After declares dependencies for this step.
// Returns a ConfiguredStep that can be passed to Define().
func (s Step[T]) After(deps ...AnyStep) ConfiguredStep {
	return ConfiguredStep{
		step: s,
		deps: deps,
	}
}

// GetOutput retrieves this step's output from the workflow context.
// Returns an error if the step has not completed or if the output cannot
// be unmarshaled to type T.
func (s Step[T]) GetOutput(ctx Context) (T, error) {
	var zero T
	wctx, ok := ctx.(outputAccessor)
	if !ok {
		return zero, fmt.Errorf("workflow: context does not support output access for step %q", s.name)
	}

	raw, found := wctx.getOutput(s.name)
	if !found {
		return zero, fmt.Errorf("workflow: step %q has not completed", s.name)
	}

	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, fmt.Errorf("workflow: failed to unmarshal output for step %q: %w", s.name, err)
	}

	return result, nil
}

// MustOutput retrieves this step's output from the workflow context.
// Panics if the step has not completed or if unmarshaling fails.
// Use GetOutput for error handling instead of panics.
func (s Step[T]) MustOutput(ctx Context) T {
	result, err := s.GetOutput(ctx)
	if err != nil {
		panic(err.Error())
	}
	return result
}

// Output is an alias for MustOutput for backward compatibility.
// Deprecated: Use GetOutput for error handling or MustOutput for explicit panic behavior.
func (s Step[T]) Output(ctx Context) T {
	return s.MustOutput(ctx)
}

// Execute runs the step function and returns the result.
// This is used internally by the Replayer.
func (s Step[T]) Execute(ctx context.Context, wctx Context) (any, error) {
	return s.fn(wctx)
}

// Config returns the step configuration.
func (s Step[T]) Config() StepConfig {
	return s.config
}

// WithRetry returns a new step with the given retry policy.
func (s Step[T]) WithRetry(policy *retry.Policy) Step[T] {
	s.config.RetryPolicy = policy
	return s
}

// WithTimeout returns a new step with the given timeout.
func (s Step[T]) WithTimeout(timeout time.Duration) Step[T] {
	s.config.Timeout = timeout
	return s
}

// WithCompensation returns a new step with the given compensation function.
// The compensation function is called when rolling back in a saga.
func (s Step[T]) WithCompensation(fn func(Context) error) Step[T] {
	s.config.Compensation = fn
	return s
}

// outputAccessor is an internal interface for retrieving step outputs.
type outputAccessor interface {
	getOutput(stepName string) (json.RawMessage, bool)
}

// ConfiguredStep is a step with its dependencies declared.
// It's created by calling Step.After() and passed to Define().
type ConfiguredStep struct {
	step executableStep
	deps []AnyStep
}

// Name returns the step name.
func (c ConfiguredStep) Name() string {
	return c.step.Name()
}

// Dependencies returns the names of dependency steps.
func (c ConfiguredStep) Dependencies() []string {
	names := make([]string, len(c.deps))
	for i, dep := range c.deps {
		names[i] = dep.Name()
	}
	return names
}

// getExecutableStep returns the underlying step for execution.
func (c ConfiguredStep) getExecutableStep() executableStep {
	return c.step
}

// Config returns the step configuration.
func (c ConfiguredStep) Config() StepConfig {
	return c.step.Config()
}
