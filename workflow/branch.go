package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// Branch represents a conditional execution point in a workflow.
// It evaluates a selector function to choose which case step to execute.
// On replay, the recorded choice is used for determinism.
type Branch struct {
	name        string
	selector    func(Context) string
	cases       map[string]AnyStep
	defaultStep AnyStep
	deps        []AnyStep
}

// NewBranch creates a new branch with a selector function.
// The selector receives the workflow context and returns the case name to execute.
// The selector should be deterministic (same inputs -> same choice).
func NewBranch(name string, selector func(Context) string) *Branch {
	return &Branch{
		name:     name,
		selector: selector,
		cases:    make(map[string]AnyStep),
	}
}

// Name returns the branch name.
// Implements AnyStep interface.
func (b *Branch) Name() string {
	return b.name
}

// Config returns the step configuration.
// Branches don't have configuration, so this returns an empty StepConfig.
func (b *Branch) Config() StepConfig {
	return StepConfig{}
}

// Case adds a case to the branch.
// The value is matched against the selector's return value.
// Returns the branch for method chaining.
// Step executability is validated at Define() time.
func (b *Branch) Case(value string, step AnyStep) *Branch {
	b.cases[value] = step
	return b
}

// Default sets the default case step.
// Used when the selector returns a value not matching any case.
// Returns the branch for method chaining.
// Step executability is validated at Define() time.
func (b *Branch) Default(step AnyStep) *Branch {
	b.defaultStep = step
	return b
}

// After declares dependencies for this branch.
// Returns a ConfiguredStep that can be passed to Define().
func (b *Branch) After(deps ...AnyStep) ConfiguredStep {
	b.deps = deps
	return ConfiguredStep{
		step: b,
		deps: deps,
	}
}

// Execute runs the branch selector and executes the selected case.
// On fresh execution, records the choice for future replay.
// This is called by the Replayer.
func (b *Branch) Execute(ctx context.Context, wctx Context) (any, error) {
	// Get the branch executor from context to record choice and check history
	executor, ok := wctx.(branchExecutor)
	if !ok {
		return nil, fmt.Errorf("branch %q: context does not support branch execution", b.name)
	}

	// Check for recorded choice first (replay mode)
	choice, recorded := executor.getBranchChoice(b.name)

	if !recorded {
		// Fresh execution: evaluate selector
		choice = b.selector(wctx)

		// Record the choice
		executor.recordBranchChoice(b.name, choice)
	}

	// Get the step to execute
	step := b.cases[choice]
	if step == nil {
		step = b.defaultStep
	}
	if step == nil {
		return nil, fmt.Errorf("branch %q: no case for %q and no default", b.name, choice)
	}

	// Type assert to executable step (validated at Define() time, but check at runtime too)
	exec, ok := step.(executableStep)
	if !ok {
		return nil, fmt.Errorf("branch %q case %q: step %q is not executable", b.name, choice, step.Name())
	}

	// Execute the selected step
	output, err := exec.Execute(ctx, wctx)
	if err != nil {
		return nil, fmt.Errorf("branch %q case %q: %w", b.name, choice, err)
	}

	return output, nil
}

// branchExecutor is the internal interface for branch execution support.
// It allows branches to check for recorded choices and record new choices.
type branchExecutor interface {
	getBranchChoice(branchName string) (string, bool)
	recordBranchChoice(branchName, choice string)
}

// BranchOutput retrieves the output from whichever case was taken in a branch.
// Returns the zero value and false if the branch hasn't executed yet.
func BranchOutput[T any](ctx Context, branch *Branch) (T, bool) {
	var zero T

	wctx, ok := ctx.(branchOutputAccessor)
	if !ok {
		return zero, false
	}

	// Get the recorded choice to determine which step was executed
	choice, found := wctx.getBranchChoice(branch.name)
	if !found {
		return zero, false
	}

	// Get the step that was executed
	step := branch.cases[choice]
	if step == nil {
		step = branch.defaultStep
	}
	if step == nil {
		return zero, false
	}

	// Get the output for that step
	outputAccessor, ok := ctx.(outputAccessor)
	if !ok {
		return zero, false
	}

	raw, found := outputAccessor.getOutput(step.Name())
	if !found {
		return zero, false
	}

	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, false
	}

	return result, true
}

// branchOutputAccessor is the internal interface for accessing branch choices.
type branchOutputAccessor interface {
	getBranchChoice(branchName string) (string, bool)
}
