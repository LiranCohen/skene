package workflow

import (
	"testing"
)

func TestDefine_SingleStep(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("single", func(ctx Context) (Output, error) {
		return Output{Value: 1}, nil
	})

	wf := Define("single-step-workflow", step.After())

	if wf.Name() != "single-step-workflow" {
		t.Errorf("Name() = %q, want %q", wf.Name(), "single-step-workflow")
	}

	steps := wf.Steps()
	if len(steps) != 1 {
		t.Fatalf("Steps() len = %d, want 1", len(steps))
	}

	if steps[0].Name() != "single" {
		t.Errorf("Steps()[0].Name() = %q, want %q", steps[0].Name(), "single")
	}
}

func TestDefine_LinearChain(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })

	// A -> B -> C
	wf := Define("linear",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepB),
	)

	steps := wf.Steps()
	if len(steps) != 3 {
		t.Fatalf("Steps() len = %d, want 3", len(steps))
	}

	// Check topological order: A must come before B, B must come before C
	order := make(map[string]int)
	for i, s := range steps {
		order[s.Name()] = i
	}

	if order["a"] > order["b"] {
		t.Error("a must come before b")
	}
	if order["b"] > order["c"] {
		t.Error("b must come before c")
	}
}

func TestDefine_DiamondPattern(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }
	type DOutput struct{ D int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })
	stepD := NewStep("d", func(ctx Context) (DOutput, error) { return DOutput{}, nil })

	// Diamond: A -> B, A -> C, B -> D, C -> D
	wf := Define("diamond",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepA),
		stepD.After(stepB, stepC),
	)

	steps := wf.Steps()
	if len(steps) != 4 {
		t.Fatalf("Steps() len = %d, want 4", len(steps))
	}

	// Check topological order
	order := make(map[string]int)
	for i, s := range steps {
		order[s.Name()] = i
	}

	// A must come first
	if order["a"] > order["b"] || order["a"] > order["c"] {
		t.Error("a must come before b and c")
	}
	// B and C must come before D
	if order["b"] > order["d"] || order["c"] > order["d"] {
		t.Error("b and c must come before d")
	}
}

func TestDefine_ParallelSteps(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })

	// A, B, C all run independently (no dependencies)
	wf := Define("parallel",
		stepA.After(),
		stepB.After(),
		stepC.After(),
	)

	steps := wf.Steps()
	if len(steps) != 3 {
		t.Fatalf("Steps() len = %d, want 3", len(steps))
	}

	// All steps should have no dependencies
	for _, step := range steps {
		deps := step.Dependencies()
		if len(deps) != 0 {
			t.Errorf("step %q has %d dependencies, want 0", step.Name(), len(deps))
		}
	}
}

func TestDefine_DuplicateName_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on duplicate step name")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %T, want string", r)
		}
		if msg == "" {
			t.Error("panic message should not be empty")
		}
	}()

	type Output struct{ Value int }
	step1 := NewStep("duplicate", func(ctx Context) (Output, error) { return Output{}, nil })
	step2 := NewStep("duplicate", func(ctx Context) (Output, error) { return Output{}, nil })

	Define("duplicate-workflow", step1.After(), step2.After())
}

func TestDefine_MissingDependency_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on missing dependency")
		}
	}()

	type AOutput struct{ A int }
	type BOutput struct{ B int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	missing := NewStep("missing", func(ctx Context) (AOutput, error) { return AOutput{}, nil })

	// B depends on "missing" which is not in the workflow
	Define("missing-dep", stepA.After(), stepB.After(missing))
}

func TestDefine_CycleDetection_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on cycle")
		}
	}()

	type AOutput struct{ A int }
	type BOutput struct{ B int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })

	// Cycle: A depends on B, B depends on A
	Define("cycle-workflow", stepA.After(stepB), stepB.After(stepA))
}

func TestDefine_EmptySteps_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on empty steps")
		}
	}()

	Define("empty-workflow")
}

func TestWorkflowDef_Graph(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })

	// A -> B, A -> C
	wf := Define("graph-test",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepA),
	)

	graph := wf.Graph()

	// Test nodes exist
	nodeA, ok := graph.GetNode("a")
	if !ok {
		t.Fatal("node 'a' not found")
	}
	nodeB, ok := graph.GetNode("b")
	if !ok {
		t.Fatal("node 'b' not found")
	}
	nodeC, ok := graph.GetNode("c")
	if !ok {
		t.Fatal("node 'c' not found")
	}

	// Test dependencies
	if len(nodeA.DepsOn) != 0 {
		t.Errorf("nodeA.DepsOn len = %d, want 0", len(nodeA.DepsOn))
	}
	if len(nodeB.DepsOn) != 1 || nodeB.DepsOn[0] != "a" {
		t.Errorf("nodeB.DepsOn = %v, want [a]", nodeB.DepsOn)
	}
	if len(nodeC.DepsOn) != 1 || nodeC.DepsOn[0] != "a" {
		t.Errorf("nodeC.DepsOn = %v, want [a]", nodeC.DepsOn)
	}

	// Test blocks (reverse dependencies)
	if len(nodeA.Blocks) != 2 {
		t.Errorf("nodeA.Blocks len = %d, want 2", len(nodeA.Blocks))
	}
	if len(nodeB.Blocks) != 0 {
		t.Errorf("nodeB.Blocks len = %d, want 0", len(nodeB.Blocks))
	}
	if len(nodeC.Blocks) != 0 {
		t.Errorf("nodeC.Blocks len = %d, want 0", len(nodeC.Blocks))
	}
}

func TestWorkflowDef_GetStep(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("findme", func(ctx Context) (Output, error) {
		return Output{Value: 42}, nil
	})

	wf := Define("get-step-test", step.After())

	// Test found
	found, ok := wf.GetStep("findme")
	if !ok {
		t.Fatal("GetStep should find 'findme'")
	}
	if found.Name() != "findme" {
		t.Errorf("found.Name() = %q, want %q", found.Name(), "findme")
	}

	// Test not found
	_, ok = wf.GetStep("notfound")
	if ok {
		t.Error("GetStep should not find 'notfound'")
	}
}

func TestDAG_Order(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })

	wf := Define("order-test",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepB),
	)

	graph := wf.Graph()
	order := graph.Order()

	if len(order) != 3 {
		t.Fatalf("Order() len = %d, want 3", len(order))
	}

	// Check order constraints
	orderMap := make(map[string]int)
	for i, name := range order {
		orderMap[name] = i
	}

	if orderMap["a"] > orderMap["b"] {
		t.Error("a must come before b")
	}
	if orderMap["b"] > orderMap["c"] {
		t.Error("b must come before c")
	}
}

func TestDAG_Nodes(t *testing.T) {
	type Output struct{ Value int }

	stepA := NewStep("a", func(ctx Context) (Output, error) { return Output{}, nil })
	stepB := NewStep("b", func(ctx Context) (Output, error) { return Output{}, nil })

	wf := Define("nodes-test",
		stepA.After(),
		stepB.After(stepA),
	)

	graph := wf.Graph()
	nodes := graph.Nodes()

	if len(nodes) != 2 {
		t.Errorf("Nodes() len = %d, want 2", len(nodes))
	}

	if _, ok := nodes["a"]; !ok {
		t.Error("nodes should contain 'a'")
	}
	if _, ok := nodes["b"]; !ok {
		t.Error("nodes should contain 'b'")
	}
}

// nonExecutableStep implements AnyStep but not executableStep.
// Used to test that Define() validates branch case/default steps.
type nonExecutableStep struct {
	name string
}

func (s nonExecutableStep) Name() string { return s.name }

func TestDefine_NonExecutableBranchCase_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on non-executable branch case step")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %T, want string", r)
		}
		if msg == "" {
			t.Error("panic message should not be empty")
		}
	}()

	type Output struct{ Value int }

	step := NewStep("step", func(ctx Context) (Output, error) { return Output{}, nil })
	badStep := nonExecutableStep{name: "bad"}

	branch := NewBranch("branch", func(ctx Context) string { return "case1" }).
		Case("case1", badStep)

	Define("non-executable-case-workflow", step.After(), branch.After(step))
}

func TestDefine_NonExecutableBranchDefault_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Define should panic on non-executable branch default step")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %T, want string", r)
		}
		if msg == "" {
			t.Error("panic message should not be empty")
		}
	}()

	type Output struct{ Value int }

	step := NewStep("step", func(ctx Context) (Output, error) { return Output{}, nil })
	badStep := nonExecutableStep{name: "bad"}

	branch := NewBranch("branch", func(ctx Context) string { return "unknown" }).
		Default(badStep)

	Define("non-executable-default-workflow", step.After(), branch.After(step))
}

func TestWorkflowDef_Version(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("step", func(ctx Context) (Output, error) {
		return Output{}, nil
	})

	wf := Define("version-test", step.After())

	if wf.Version() != "1" {
		t.Errorf("Version() = %q, want %q", wf.Version(), "1")
	}
}

func TestGetInput_UnmarshalableInput_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("getInput should panic on unmarshalable input")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value = %T, want string", r)
		}
		if msg == "" {
			t.Error("panic message should not be empty")
		}
	}()

	// Create an input type that cannot be marshaled (has a channel)
	type UnmarshalableInput struct {
		Ch chan int
	}

	// Create a workflowContextAdapter with an unmarshalable input
	adapter := &workflowContextAdapter{
		executionContext: &executionContext{
			input: UnmarshalableInput{Ch: make(chan int)},
		},
	}

	// This should panic because channels cannot be marshaled to JSON
	adapter.getInput()
}
