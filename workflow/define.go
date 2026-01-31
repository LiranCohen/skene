package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// stepNodeAdapter adapts ConfiguredStep to the StepNode interface
// required by the Replayer.
type stepNodeAdapter struct {
	configured ConfiguredStep
}

// Name returns the step name.
func (s *stepNodeAdapter) Name() string {
	return s.configured.Name()
}

// Dependencies returns the names of steps this step depends on.
func (s *stepNodeAdapter) Dependencies() []string {
	return s.configured.Dependencies()
}

// Execute runs the step function with the given context.
func (s *stepNodeAdapter) Execute(ctx context.Context, execCtx *executionContext) (any, error) {
	// Create a workflowContext that implements the Context interface
	wctx := &workflowContextAdapter{
		executionContext: execCtx,
	}
	return s.configured.getExecutableStep().Execute(ctx, wctx)
}

// Config returns the step configuration.
func (s *stepNodeAdapter) Config() StepConfig {
	return s.configured.Config()
}

// workflowContextAdapter adapts executionContext to the Context interface
// that step functions expect.
type workflowContextAdapter struct {
	*executionContext
}

// Deadline implements context.Context.
func (w *workflowContextAdapter) Deadline() (deadline time.Time, ok bool) {
	return w.ctx.Deadline()
}

// Done implements context.Context.
func (w *workflowContextAdapter) Done() <-chan struct{} {
	return w.ctx.Done()
}

// Err implements context.Context.
func (w *workflowContextAdapter) Err() error {
	return w.ctx.Err()
}

// Value implements context.Context.
func (w *workflowContextAdapter) Value(key any) any {
	return w.ctx.Value(key)
}

// getInput implements inputAccessor for Input[T]().
func (w *workflowContextAdapter) getInput() json.RawMessage {
	// The input is already stored as any in executionContext
	// We need to marshal it to JSON for the Input[T] function
	if w.input == nil {
		return nil
	}
	// If it's already a []byte (json.RawMessage), return it directly
	if data, ok := w.input.([]byte); ok {
		return data
	}
	if data, ok := w.input.(json.RawMessage); ok {
		return data
	}
	// Otherwise marshal it
	data, err := json.Marshal(w.input)
	if err != nil {
		panic(fmt.Sprintf("workflow: failed to marshal input: %v", err))
	}
	return data
}

// getOutput implements outputAccessor for Step[T].Output().
func (w *workflowContextAdapter) getOutput(stepName string) (json.RawMessage, bool) {
	return w.GetOutput(stepName)
}

// getReplayer implements replayerAccessor for workflow.Run().
func (w *workflowContextAdapter) getReplayer() *Replayer {
	return w.executionContext.getReplayer()
}

// getBranchChoice implements branchExecutor for branch execution.
func (w *workflowContextAdapter) getBranchChoice(branchName string) (string, bool) {
	return w.executionContext.getBranchChoice(branchName)
}

// recordBranchChoice implements branchExecutor for branch execution.
func (w *workflowContextAdapter) recordBranchChoice(branchName, choice string) {
	w.executionContext.recordBranchChoice(branchName, choice)
}

// Emit sends data to the OnStepEmit callback during step execution.
// This is fire-and-forget - emissions don't affect control flow.
func (w *workflowContextAdapter) Emit(data any) {
	w.executionContext.Emit(data)
}

// Define creates a workflow from steps with explicit dependencies.
// Steps without After() calls have no dependencies and can run immediately.
// Returns an error if validation fails (cycles, duplicates, missing deps).
func Define(name string, steps ...ConfiguredStep) *WorkflowDef {
	if len(steps) == 0 {
		panic("workflow: Define requires at least one step")
	}

	// Build step map for validation
	stepMap := make(map[string]ConfiguredStep)
	for _, s := range steps {
		stepName := s.Name()
		if _, exists := stepMap[stepName]; exists {
			panic(fmt.Sprintf("workflow: duplicate step name %q", stepName))
		}
		stepMap[stepName] = s
	}

	// Validate all dependencies exist
	for _, s := range steps {
		for _, dep := range s.Dependencies() {
			if _, exists := stepMap[dep]; !exists {
				panic(fmt.Sprintf("workflow: step %q depends on unknown step %q", s.Name(), dep))
			}
		}
	}

	// Validate branch steps are executable
	for _, s := range steps {
		branch, ok := s.step.(*Branch)
		if !ok {
			continue
		}
		for caseName, caseStep := range branch.cases {
			if _, ok := caseStep.(executableStep); !ok {
				panic(fmt.Sprintf("workflow: branch %q case %q step must be executable", branch.name, caseName))
			}
		}
		if branch.defaultStep != nil {
			if _, ok := branch.defaultStep.(executableStep); !ok {
				panic(fmt.Sprintf("workflow: branch %q default step must be executable", branch.name))
			}
		}
	}

	// Detect cycles using Kahn's algorithm (topological sort)
	order, err := topologicalSort(steps)
	if err != nil {
		panic(fmt.Sprintf("workflow: %v", err))
	}

	// Build the WorkflowDef with adapted StepNodes
	nodeMap := make(map[string]StepNode)
	nodes := make([]StepNode, len(steps))

	// Create adapter nodes
	for i, s := range steps {
		adapter := &stepNodeAdapter{configured: s}
		nodes[i] = adapter
		nodeMap[s.Name()] = adapter
	}

	// Reorder nodes according to topological sort
	sortedNodes := make([]StepNode, len(order))
	for i, name := range order {
		sortedNodes[i] = nodeMap[name]
	}

	return &WorkflowDef{
		name:    name,
		version: "1",
		steps:   sortedNodes,
		stepMap: nodeMap,
		order:   order,
	}
}

// topologicalSort performs Kahn's algorithm to get topological order.
// Returns an error if cycles are detected.
func topologicalSort(steps []ConfiguredStep) ([]string, error) {
	// Build adjacency list and in-degree map
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // step -> steps that depend on it

	// Initialize all steps with 0 in-degree
	for _, s := range steps {
		inDegree[s.Name()] = 0
	}

	// Calculate in-degrees
	for _, s := range steps {
		name := s.Name()
		deps := s.Dependencies()
		inDegree[name] = len(deps)
		for _, dep := range deps {
			dependents[dep] = append(dependents[dep], name)
		}
	}

	// Find all nodes with no incoming edges
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	// Process nodes
	var order []string
	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		// Reduce in-degree of dependents
		for _, dependent := range dependents[current] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	// If we didn't process all nodes, there's a cycle
	if len(order) != len(steps) {
		// Find nodes in cycle (those with remaining in-degree > 0)
		var cycleNodes []string
		for name, degree := range inDegree {
			if degree > 0 {
				cycleNodes = append(cycleNodes, name)
			}
		}
		return nil, fmt.Errorf("cycle detected involving steps: %v", cycleNodes)
	}

	return order, nil
}

// DAG represents the workflow dependency graph for visualization.
type DAG struct {
	nodes map[string]*DAGNode
	order []string
}

// DAGNode represents a node in the DAG.
type DAGNode struct {
	Name   string   // Step name
	DepsOn []string // Steps this depends on
	Blocks []string // Steps that depend on this
}

// Graph returns the workflow structure for visualization.
func (w *WorkflowDef) Graph() *DAG {
	nodes := make(map[string]*DAGNode)

	// Initialize all nodes
	for _, step := range w.steps {
		nodes[step.Name()] = &DAGNode{
			Name:   step.Name(),
			DepsOn: step.Dependencies(),
			Blocks: []string{},
		}
	}

	// Build reverse dependencies (who blocks whom)
	for _, step := range w.steps {
		for _, dep := range step.Dependencies() {
			nodes[dep].Blocks = append(nodes[dep].Blocks, step.Name())
		}
	}

	return &DAG{
		nodes: nodes,
		order: w.order,
	}
}

// Nodes returns all DAG nodes.
func (d *DAG) Nodes() map[string]*DAGNode {
	return d.nodes
}

// Order returns the topological order of step names.
func (d *DAG) Order() []string {
	return d.order
}

// GetNode returns a node by name.
func (d *DAG) GetNode(name string) (*DAGNode, bool) {
	node, ok := d.nodes[name]
	return node, ok
}

// StepDef represents a step definition for export/visualization.
type StepDef struct {
	Name         string    `json:"name"`
	Dependencies []string  `json:"dependencies"`
	IsBranch     bool      `json:"isBranch"`
	BranchCases  []CaseDef `json:"branchCases,omitempty"`
}

// CaseDef represents a branch case definition.
type CaseDef struct {
	Value     string `json:"value"`     // Case value (e.g., "artificial", "real_upload")
	StepName  string `json:"stepName"`  // Step executed for this case
	IsDefault bool   `json:"isDefault"` // True if this is the default case
}

// GetStepDefs returns step definitions for all steps in the workflow.
// This includes branch case information for visualization.
func (w *WorkflowDef) GetStepDefs() []StepDef {
	defs := make([]StepDef, 0, len(w.steps))

	for _, step := range w.steps {
		def := StepDef{
			Name:         step.Name(),
			Dependencies: step.Dependencies(),
		}

		// Check if this is a branch by looking at the underlying adapter
		if adapter, ok := step.(*stepNodeAdapter); ok {
			if branch, ok := adapter.configured.step.(*Branch); ok {
				def.IsBranch = true
				def.BranchCases = make([]CaseDef, 0)

				// Add all cases
				for value, caseStep := range branch.cases {
					def.BranchCases = append(def.BranchCases, CaseDef{
						Value:    value,
						StepName: caseStep.Name(),
					})
				}

				// Add default case if present
				if branch.defaultStep != nil {
					def.BranchCases = append(def.BranchCases, CaseDef{
						Value:     "default",
						StepName:  branch.defaultStep.Name(),
						IsDefault: true,
					})
				}
			}
		}

		defs = append(defs, def)
	}

	return defs
}
