package workflow

import (
	"context"
	"encoding/json"
	"fmt"
)

// Context provides access to workflow data and prior outputs.
// It embeds context.Context for cancellation and deadline support.
type Context interface {
	context.Context

	// RunID returns the current workflow run ID.
	RunID() string

	// WorkflowName returns the workflow name.
	WorkflowName() string
}

// GetInput retrieves the workflow input with type safety.
// Returns an error if the context doesn't support input access or if
// the input cannot be unmarshaled to type T.
func GetInput[T any](ctx Context) (T, error) {
	var zero T
	wctx, ok := ctx.(inputAccessor)
	if !ok {
		return zero, fmt.Errorf("workflow: context does not support input access")
	}

	raw := wctx.getInput()
	var result T
	if err := json.Unmarshal(raw, &result); err != nil {
		return zero, fmt.Errorf("workflow: failed to unmarshal input: %w", err)
	}

	return result, nil
}

// MustInput retrieves the workflow input with type safety.
// Panics if the input cannot be converted to type T.
// Use GetInput for error handling instead of panics.
func MustInput[T any](ctx Context) T {
	result, err := GetInput[T](ctx)
	if err != nil {
		panic(err.Error())
	}
	return result
}

// Input is an alias for MustInput for backward compatibility.
// Deprecated: Use GetInput for error handling or MustInput for explicit panic behavior.
func Input[T any](ctx Context) T {
	return MustInput[T](ctx)
}

// inputAccessor is an internal interface for retrieving workflow input.
type inputAccessor interface {
	getInput() json.RawMessage
}

// workflowContext implements Context for step execution.
type workflowContext struct {
	context.Context
	runID        string
	workflowName string
	input        json.RawMessage
	history      *History
	outputs      map[string]any // cached deserialized outputs from current run
}

// newWorkflowContext creates a context for step execution.
func newWorkflowContext(
	parent context.Context,
	runID, workflowName string,
	input json.RawMessage,
	history *History,
) *workflowContext {
	return &workflowContext{
		Context:      parent,
		runID:        runID,
		workflowName: workflowName,
		input:        input,
		history:      history,
		outputs:      make(map[string]any),
	}
}

// RunID returns the workflow run ID.
func (c *workflowContext) RunID() string {
	return c.runID
}

// WorkflowName returns the workflow name.
func (c *workflowContext) WorkflowName() string {
	return c.workflowName
}

// getInput returns the raw workflow input JSON.
func (c *workflowContext) getInput() json.RawMessage {
	return c.input
}

// getOutput returns the raw output for a completed step.
// It checks the in-memory cache first, then falls back to history.
func (c *workflowContext) getOutput(stepName string) (json.RawMessage, bool) {
	// Check cache first (outputs from current run)
	if output, ok := c.outputs[stepName]; ok {
		data, err := json.Marshal(output)
		if err != nil {
			return nil, false
		}
		return data, true
	}

	// Fall back to history
	if c.history != nil {
		return c.history.GetOutput(stepName)
	}
	return nil, false
}

// setOutput stores an output in the cache for dependent steps.
func (c *workflowContext) setOutput(stepName string, output any) {
	c.outputs[stepName] = output
}

// childContext extends workflowContext for child workflow execution.
// It tracks the parent-child relationship and provides context for Map children.
type childContext struct {
	*workflowContext
	parentRunID string
	childIndex  int  // -1 if not a map child
	isMapChild  bool // distinguishes Run children from Map children
}

// newChildContext creates a context for child workflow execution.
func newChildContext(
	parent context.Context,
	runID, workflowName string,
	input json.RawMessage,
	history *History,
	parentRunID string,
) *childContext {
	return &childContext{
		workflowContext: newWorkflowContext(parent, runID, workflowName, input, history),
		parentRunID:     parentRunID,
		childIndex:      -1,
		isMapChild:      false,
	}
}

// newMapChildContext creates a context for a Map child workflow execution.
func newMapChildContext(
	parent context.Context,
	runID, workflowName string,
	input json.RawMessage,
	history *History,
	parentRunID string,
	childIndex int,
) *childContext {
	return &childContext{
		workflowContext: newWorkflowContext(parent, runID, workflowName, input, history),
		parentRunID:     parentRunID,
		childIndex:      childIndex,
		isMapChild:      true,
	}
}

// parentRunIDAccessor is an internal interface for accessing parent run ID.
type parentRunIDAccessor interface {
	getParentRunID() string
}

// childIndexAccessor is an internal interface for accessing child index.
type childIndexAccessor interface {
	getChildIndex() (int, bool)
}

// getParentRunID returns the parent workflow run ID.
func (c *childContext) getParentRunID() string {
	return c.parentRunID
}

// getChildIndex returns the child index for Map children.
// Returns -1, false if this is not a Map child.
func (c *childContext) getChildIndex() (int, bool) {
	if !c.isMapChild {
		return -1, false
	}
	return c.childIndex, true
}

// ParentRunID returns the parent workflow run ID if this is a child workflow.
// Returns ("", false) for root workflows.
func ParentRunID(ctx Context) (string, bool) {
	accessor, ok := ctx.(parentRunIDAccessor)
	if !ok {
		return "", false
	}
	parentID := accessor.getParentRunID()
	if parentID == "" {
		return "", false
	}
	return parentID, true
}

// ChildIndex returns the index in a Map call if this is a Map child workflow.
// Returns (-1, false) for root workflows and Run children.
func ChildIndex(ctx Context) (int, bool) {
	accessor, ok := ctx.(childIndexAccessor)
	if !ok {
		return -1, false
	}
	return accessor.getChildIndex()
}
