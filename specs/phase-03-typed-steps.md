# Phase 3: Typed Step Model

## Goal
Implement the typed step handle system with generics, workflow definition via DAG, and type-safe output access.

## Primary Design Document
- `docs/design/02-WORKFLOW-MODEL.md`

## Related Documents
- `docs/design/00-ARCHITECTURE.md` (package structure, key interfaces)
- `docs/design/03-REPLAY.md` (replay integration)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Step Handle (`workflow/step.go`)

```go
// AnyStep is a type-erased step for dependency declarations
type AnyStep interface {
    Name() string
}

// Step is a typed handle for a workflow step
type Step[T any] struct {
    name string
    fn   func(Context) (T, error)
}

// NewStep creates a new typed step handle
func NewStep[T any](name string, fn func(Context) (T, error)) Step[T]

// Name returns the step name
func (s Step[T]) Name() string

// After declares dependencies (returns a configured step for workflow.Define)
func (s Step[T]) After(deps ...AnyStep) ConfiguredStep

// Output retrieves this step's output from the workflow context
func (s Step[T]) Output(ctx Context) T

// Execute runs the step function (internal use)
func (s Step[T]) Execute(ctx Context) (any, error)
```

### ConfiguredStep (`workflow/step.go`)

```go
// ConfiguredStep is a step with its dependencies declared
type ConfiguredStep struct {
    step   AnyStep
    deps   []AnyStep
}

// Name returns the step name
func (c ConfiguredStep) Name() string

// Dependencies returns the dependency step names
func (c ConfiguredStep) Dependencies() []string
```

### Workflow Definition (`workflow/define.go`)

```go
// WorkflowDef represents a static workflow structure
type WorkflowDef struct {
    name  string
    steps []StepNode
}

// Define creates a workflow from steps with explicit dependencies
func Define(name string, steps ...ConfiguredStep) *WorkflowDef

// Name returns the workflow name
func (w *WorkflowDef) Name() string

// Steps returns the steps in topological order
func (w *WorkflowDef) Steps() []StepNode

// Graph returns the workflow structure for visualization
func (w *WorkflowDef) Graph() *DAG

// StepNode represents a step in the workflow DAG
type StepNode struct {
    name     string
    step     AnyStep
    deps     []string
    execute  func(Context) (any, error)
}

// DAG represents the workflow dependency graph
type DAG struct {
    nodes map[string]*DAGNode
    order []string // topological order
}

// DAGNode represents a node in the DAG
type DAGNode struct {
    Name     string
    DepsOn   []string // steps this depends on
    Blocks   []string // steps that depend on this
}
```

### Workflow Context (`workflow/context.go`)

```go
// Context provides access to workflow data and prior outputs
type Context interface {
    context.Context

    // RunID returns the current workflow run ID
    RunID() string

    // WorkflowName returns the workflow name
    WorkflowName() string
}

// Input retrieves the workflow input (typed)
func Input[T any](ctx Context) T

// workflowContext implements Context (internal)
type workflowContext struct {
    context.Context
    runID        string
    workflowName string
    input        json.RawMessage
    history      *History
    outputs      map[string]any // cached deserialized outputs
}

// newWorkflowContext creates a context for step execution
func newWorkflowContext(parent context.Context, runID, workflowName string, input json.RawMessage, history *History) *workflowContext
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/step.go` | Step[T] typed handles, ConfiguredStep |
| `workflow/define.go` | WorkflowDef, DAG construction, topological sort |
| `workflow/context.go` | workflow.Context, Input[T], output access |

---

## Acceptance Criteria

### Step Handles
- [ ] NewStep creates typed step with name and function
- [ ] Step.Name() returns the step name
- [ ] Step.After() returns ConfiguredStep with dependencies
- [ ] Step without After() has no dependencies (can run immediately)
- [ ] Step.Output(ctx) returns typed output from context
- [ ] Step.Output panics if step not completed (programmer error)
- [ ] Step.Execute calls the step function

### Workflow Definition
- [ ] Define() creates WorkflowDef from ConfiguredSteps
- [ ] Define() validates no duplicate step names
- [ ] Define() validates no cycles in dependencies
- [ ] Define() validates all dependencies exist
- [ ] Steps() returns topological order
- [ ] Graph() returns DAG for visualization

### Context
- [ ] Input[T](ctx) returns typed workflow input
- [ ] Input panics if type mismatch (programmer error)
- [ ] Step.Output(ctx) retrieves from history
- [ ] Outputs are cached after first deserialization
- [ ] RunID() returns the workflow run ID
- [ ] WorkflowName() returns the workflow name

### Tests
- [ ] Single step workflow
- [ ] Linear chain: A -> B -> C
- [ ] Diamond: A -> B, A -> C, B -> D, C -> D
- [ ] Parallel: A, B, C all run independently
- [ ] Cycle detection fails Define()
- [ ] Missing dependency fails Define()
- [ ] Duplicate name fails Define()
- [ ] Type-safe output retrieval
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### Step Handle Tests
- Create step with output type
- Step.After with single dependency
- Step.After with multiple dependencies
- Step without After (no deps)

### Workflow Definition Tests
- Simple linear workflow A -> B -> C
- Diamond dependency pattern
- Independent parallel steps
- Cycle detection error
- Duplicate name error
- Missing dependency error
- Topological sort correctness

### Context Tests
- Input retrieval with correct type
- Input with wrong type panics
- Output retrieval from history
- Output caching (deserialize once)
- Output not available panics

### Integration Tests
- Define workflow, create context, execute steps
- Verify step outputs accessible to dependents
- Verify independent steps have no dependencies

---

## Dependencies
- Phase 1: Events & Storage (event types)
- Phase 2: Replay Engine (History)

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Panic for programmer errors (wrong type, missing output)
- Return errors for runtime failures
- Table-driven tests
- Test behavior, not implementation
