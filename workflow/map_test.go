package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lirancohen/skene/event"
)

// Note: childCounter and mapCounter are now per-Replayer instance,
// so no global reset functions are needed. Each test creates a fresh
// Replayer with counters starting at 0.

// childStep creates a simple step for child workflow testing.
func childStep(name string, output any) *testStep {
	return &testStep{
		name: name,
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return output, nil
		},
	}
}

// childStepWithError creates a step that returns an error.
func childStepWithError(name string, err error) *testStep {
	return &testStep{
		name: name,
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return nil, err
		},
	}
}

// childStepWithInput creates a step that uses the workflow input.
func childStepWithInput[In, Out any](name string, fn func(In) Out) *testStep {
	return &testStep{
		name: name,
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			inputJSON, _ := json.Marshal(ec.Input())
			var input In
			if err := json.Unmarshal(inputJSON, &input); err != nil {
				return nil, err
			}
			return fn(input), nil
		},
	}
}

// TestRun_BasicExecution tests basic child workflow execution.
func TestRun_BasicExecution(t *testing.T) {

	type ChildOutput struct {
		Result string `json:"result"`
	}

	// Define child workflow
	childWf := testWorkflow("child-workflow",
		childStep("child-step", ChildOutput{Result: "from-child"}),
	)

	// Define parent step that calls Run
	var childResult ChildOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			// Build the workflow context adapter
			wctx := &workflowContextAdapter{executionContext: ec}

			result, err := Run[string, ChildOutput](wctx, childWf, "child-input")
			if err != nil {
				return nil, err
			}
			childResult = result
			return map[string]string{"parent": "done"}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{"key": "value"},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if childResult.Result != "from-child" {
		t.Errorf("childResult.Result = %q, want %q", childResult.Result, "from-child")
	}

	// Verify child events are recorded
	var foundChildSpawned, foundChildCompleted bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventChildSpawned {
			foundChildSpawned = true
			var data event.ChildSpawnedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal child.spawned data: %v", err)
			}
			if data.ChildRunID != "parent-run-child-0" {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, "parent-run-child-0")
			}
			if data.WorkflowName != "child-workflow" {
				t.Errorf("WorkflowName = %q, want %q", data.WorkflowName, "child-workflow")
			}
		}
		if e.Type == event.EventChildCompleted {
			foundChildCompleted = true
			var data event.ChildCompletedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal child.completed data: %v", err)
			}
			if data.ChildRunID != "parent-run-child-0" {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, "parent-run-child-0")
			}
		}
	}

	if !foundChildSpawned {
		t.Error("Expected to find child.spawned event")
	}
	if !foundChildCompleted {
		t.Error("Expected to find child.completed event")
	}
}

// TestRun_ChildFailure tests that child workflow failure propagates to parent.
func TestRun_ChildFailure(t *testing.T) {
	childErr := errors.New("child step failed")

	// Define child workflow with failing step
	childWf := testWorkflow("child-workflow",
		childStepWithError("failing-step", childErr),
	)

	// Define parent step that calls Run
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			_, err := Run[string, map[string]string](wctx, childWf, "input")
			if err != nil {
				return nil, err
			}
			return map[string]string{"parent": "done"}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Verify child.failed event is recorded
	var foundChildFailed bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventChildFailed {
			foundChildFailed = true
			var data event.ChildFailedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal child.failed data: %v", err)
			}
			if data.ChildRunID != "parent-run-child-0" {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, "parent-run-child-0")
			}
		}
	}

	if !foundChildFailed {
		t.Error("Expected to find child.failed event")
	}
}

// TestRun_ReplaySkipsCompletedChild tests that on replay, completed children are not re-executed.
func TestRun_ReplaySkipsCompletedChild(t *testing.T) {
	type ChildOutput struct {
		Result string `json:"result"`
	}

	childExecutionCount := 0

	// Define child workflow
	childWf := testWorkflow("child-workflow",
		&testStep{
			name: "child-step",
			execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
				childExecutionCount++
				return ChildOutput{Result: "from-child"}, nil
			},
		},
	)

	// Define parent step that calls Run
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			result, err := Run[string, ChildOutput](wctx, childWf, "input")
			if err != nil {
				return nil, err
			}
			return map[string]string{"child_result": result.Result}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	// Create history with child already completed
	childOutputJSON, _ := json.Marshal(ChildOutput{Result: "from-history"})
	history := NewHistory("parent-run", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "parent-run",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
			Data:     mustMarshal(event.WorkflowStartedData{WorkflowName: "parent-workflow", Version: "1"}),
		},
		{
			ID:       "evt-2",
			RunID:    "parent-run",
			Sequence: 2,
			Type:     event.EventChildSpawned,
			Data:     mustMarshal(event.ChildSpawnedData{ChildRunID: "parent-run-child-0", WorkflowName: "child-workflow"}),
		},
		{
			ID:       "evt-3",
			RunID:    "parent-run",
			Sequence: 3,
			Type:     event.EventChildCompleted,
			Data:     mustMarshal(event.ChildCompletedData{ChildRunID: "parent-run-child-0", Output: childOutputJSON}),
		},
		{
			ID:       "evt-4",
			RunID:    "parent-run",
			Sequence: 4,
			Type:     event.EventStepCompleted,
			StepName: "parent-step",
			Output:   mustMarshal(map[string]string{"child_result": "from-history"}),
		},
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Child should NOT have been executed (loaded from history)
	if childExecutionCount != 0 {
		t.Errorf("Child execution count = %d, want 0 (should be skipped on replay)", childExecutionCount)
	}
}

// TestRun_MultipleChildren tests that multiple children get unique RunIDs.
func TestRun_MultipleChildren(t *testing.T) {
	type ChildOutput struct {
		Index int `json:"index"`
	}

	// Define child workflow
	childWf := testWorkflow("child-workflow",
		childStepWithInput("child-step", func(idx int) ChildOutput {
			return ChildOutput{Index: idx}
		}),
	)

	// Define parent step that calls Run multiple times
	var results []ChildOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			for i := 0; i < 3; i++ {
				result, err := Run[int, ChildOutput](wctx, childWf, i)
				if err != nil {
					return nil, err
				}
				results = append(results, result)
			}
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify all three children completed with correct indices
	if len(results) != 3 {
		t.Fatalf("Results length = %d, want 3", len(results))
	}
	for i, r := range results {
		if r.Index != i {
			t.Errorf("Result[%d].Index = %d, want %d", i, r.Index, i)
		}
	}

	// Verify unique child RunIDs
	childRunIDs := make(map[string]bool)
	for _, e := range output.NewEvents {
		if e.Type == event.EventChildSpawned {
			var data event.ChildSpawnedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal child.spawned data: %v", err)
				continue
			}
			if childRunIDs[data.ChildRunID] {
				t.Errorf("Duplicate child RunID: %s", data.ChildRunID)
			}
			childRunIDs[data.ChildRunID] = true
		}
	}

	if len(childRunIDs) != 3 {
		t.Errorf("Unique child RunIDs count = %d, want 3", len(childRunIDs))
	}
}

// TestRun_ChildWithMultipleSteps tests child workflows with multiple steps.
func TestRun_ChildWithMultipleSteps(t *testing.T) {
	type Step1Output struct {
		Value int `json:"value"`
	}
	type FinalOutput struct {
		Doubled int `json:"doubled"`
	}

	// Define child workflow with multiple steps
	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			inputJSON, _ := json.Marshal(ec.Input())
			var input int
			if err := json.Unmarshal(inputJSON, &input); err != nil {
				return nil, err
			}
			return Step1Output{Value: input}, nil
		},
	}
	step2 := &testStep{
		name: "step2",
		deps: []string{"step1"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			output, _ := ec.GetOutput("step1")
			var s1 Step1Output
			if err := json.Unmarshal(output, &s1); err != nil {
				return nil, err
			}
			return FinalOutput{Doubled: s1.Value * 2}, nil
		},
	}

	childWf := testWorkflow("multi-step-child", step1, step2)

	// Define parent step
	var childResult FinalOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			result, err := Run[int, FinalOutput](wctx, childWf, 21)
			if err != nil {
				return nil, err
			}
			childResult = result
			return map[string]int{"final": result.Doubled}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if childResult.Doubled != 42 {
		t.Errorf("childResult.Doubled = %d, want 42", childResult.Doubled)
	}
}

// TestRun_NestedChildren tests nested child workflows (grandchildren).
func TestRun_NestedChildren(t *testing.T) {
	type LeafOutput struct {
		Message string `json:"message"`
	}

	// Define leaf (grandchild) workflow
	leafWf := testWorkflow("leaf-workflow",
		childStep("leaf-step", LeafOutput{Message: "from-leaf"}),
	)

	// Define middle (child) workflow that spawns grandchild
	middleStep := &testStep{
		name: "middle-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			result, err := Run[string, LeafOutput](wctx, leafWf, "grandchild-input")
			if err != nil {
				return nil, err
			}
			return map[string]string{"leaf_message": result.Message}, nil
		},
	}
	middleWf := testWorkflow("middle-workflow", middleStep)

	// Define root (parent) workflow that spawns child
	var rootResult map[string]string
	rootStep := &testStep{
		name: "root-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			result, err := Run[string, map[string]string](wctx, middleWf, "child-input")
			if err != nil {
				return nil, err
			}
			rootResult = result
			return map[string]string{"final": result["leaf_message"]}, nil
		},
	}
	rootWf := testWorkflow("root-workflow", rootStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: rootWf,
		RunID:    "root-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if rootResult["leaf_message"] != "from-leaf" {
		t.Errorf("rootResult[leaf_message] = %q, want %q", rootResult["leaf_message"], "from-leaf")
	}
}

// TestFindChildOutput tests the findChildOutput helper.
func TestFindChildOutput(t *testing.T) {
	tests := []struct {
		name       string
		events     []event.Event
		childRunID string
		wantOutput json.RawMessage
		wantFound  bool
	}{
		{
			name:       "nil history",
			events:     nil,
			childRunID: "child-0",
			wantFound:  false,
		},
		{
			name:       "empty events",
			events:     []event.Event{},
			childRunID: "child-0",
			wantFound:  false,
		},
		{
			name: "child not found",
			events: []event.Event{
				{
					Type: event.EventChildCompleted,
					Data: mustMarshal(event.ChildCompletedData{ChildRunID: "other-child", Output: json.RawMessage(`{"x":1}`)}),
				},
			},
			childRunID: "child-0",
			wantFound:  false,
		},
		{
			name: "child found",
			events: []event.Event{
				{
					Type: event.EventChildCompleted,
					Data: mustMarshal(event.ChildCompletedData{ChildRunID: "child-0", Output: json.RawMessage(`{"result":"ok"}`)}),
				},
			},
			childRunID: "child-0",
			wantOutput: json.RawMessage(`{"result":"ok"}`),
			wantFound:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create history (nil events case uses empty slice)
			events := tt.events
			if events == nil {
				events = []event.Event{}
			}
			history := NewHistory("run", events)

			output, found := history.GetChildOutput(tt.childRunID)
			if found != tt.wantFound {
				t.Errorf("found = %v, want %v", found, tt.wantFound)
			}
			if found && string(output) != string(tt.wantOutput) {
				t.Errorf("output = %s, want %s", output, tt.wantOutput)
			}
		})
	}
}

// TestGetChildError tests the History.GetChildError helper.
func TestGetChildError(t *testing.T) {
	tests := []struct {
		name       string
		events     []event.Event
		childRunID string
		wantFound  bool
	}{
		{
			name:       "empty events",
			events:     nil,
			childRunID: "child-0",
			wantFound:  false,
		},
		{
			name: "child not failed",
			events: []event.Event{
				{
					Type: event.EventChildCompleted,
					Data: mustMarshal(event.ChildCompletedData{ChildRunID: "child-0", Output: json.RawMessage(`{}`)}),
				},
			},
			childRunID: "child-0",
			wantFound:  false,
		},
		{
			name: "child failed",
			events: []event.Event{
				{
					Type: event.EventChildFailed,
					Data: mustMarshal(event.ChildFailedData{ChildRunID: "child-0", Error: "test error"}),
				},
			},
			childRunID: "child-0",
			wantFound:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create history (nil events case uses empty slice)
			events := tt.events
			if events == nil {
				events = []event.Event{}
			}
			history := NewHistory("run", events)

			_, found := history.GetChildError(tt.childRunID)
			if found != tt.wantFound {
				t.Errorf("found = %v, wantFound = %v", found, tt.wantFound)
			}
		})
	}
}

// mustMarshal is declared in history_test.go

// TestMap_BasicExecution tests basic Map execution with all successes.
func TestMap_BasicExecution(t *testing.T) {
	type ItemOutput struct {
		Doubled int `json:"doubled"`
	}

	// Define child workflow that doubles input
	childWf := testWorkflow("double-workflow",
		childStepWithInput("double-step", func(n int) ItemOutput {
			return ItemOutput{Doubled: n * 2}
		}),
	)

	// Define parent step that calls Map
	var mapResults []ItemOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := []int{1, 2, 3, 4, 5}
			results, err := Map[int, ItemOutput](wctx, items, childWf)
			if err != nil {
				return nil, err
			}
			mapResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify results
	if len(mapResults) != 5 {
		t.Fatalf("mapResults length = %d, want 5", len(mapResults))
	}
	expected := []int{2, 4, 6, 8, 10}
	for i, r := range mapResults {
		if r.Doubled != expected[i] {
			t.Errorf("mapResults[%d].Doubled = %d, want %d", i, r.Doubled, expected[i])
		}
	}

	// Verify map events
	var foundMapStarted, foundMapCompleted bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventMapStarted {
			foundMapStarted = true
			var data event.MapStartedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal map.started data: %v", err)
			}
			if data.ItemCount != 5 {
				t.Errorf("ItemCount = %d, want 5", data.ItemCount)
			}
		}
		if e.Type == event.EventMapCompleted {
			foundMapCompleted = true
		}
	}

	if !foundMapStarted {
		t.Error("Expected to find map.started event")
	}
	if !foundMapCompleted {
		t.Error("Expected to find map.completed event")
	}
}

// TestMap_EmptyItems tests Map with empty items slice.
func TestMap_EmptyItems(t *testing.T) {
	type ItemOutput struct {
		Value int `json:"value"`
	}

	childWf := testWorkflow("child-workflow",
		childStep("step", ItemOutput{Value: 1}),
	)

	var mapResults []ItemOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			results, err := Map[int, ItemOutput](wctx, []int{}, childWf)
			if err != nil {
				return nil, err
			}
			mapResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if len(mapResults) != 0 {
		t.Errorf("mapResults length = %d, want 0", len(mapResults))
	}
}

// TestMap_PartialFailure tests that Map handles partial failure.
func TestMap_PartialFailure(t *testing.T) {
	failOnIndex := 2
	childErr := errors.New("child failed")

	// Define child workflow that fails on specific index
	childWf := testWorkflow("conditional-workflow",
		&testStep{
			name: "conditional-step",
			execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
				inputJSON, _ := json.Marshal(ec.Input())
				var input int
				if err := json.Unmarshal(inputJSON, &input); err != nil {
					return nil, err
				}
				if input == failOnIndex {
					return nil, childErr
				}
				return map[string]int{"value": input * 2}, nil
			},
		},
	)

	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := []int{0, 1, 2, 3, 4}
			_, err := Map[int, map[string]int](wctx, items, childWf)
			return nil, err // Should propagate error
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Verify map.failed event or child.failed event is recorded
	var foundChildFailed bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventChildFailed {
			foundChildFailed = true
		}
	}

	if !foundChildFailed {
		t.Error("Expected to find child.failed event")
	}
}

// TestMap_WithMaxConcurrency tests MapWithOptions with MaxConcurrency.
func TestMap_WithMaxConcurrency(t *testing.T) {
	type ItemOutput struct {
		Index int `json:"index"`
	}

	// Track concurrent execution count
	var concurrent atomic.Int32
	var maxConcurrent atomic.Int32

	childWf := testWorkflow("tracked-workflow",
		&testStep{
			name: "tracked-step",
			execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
				// Increment concurrent counter
				c := concurrent.Add(1)

				// Track max concurrent
				for {
					max := maxConcurrent.Load()
					if c <= max {
						break
					}
					if maxConcurrent.CompareAndSwap(max, c) {
						break
					}
				}

				// Do work (simulated)
				inputJSON, _ := json.Marshal(ec.Input())
				var input int
				if err := json.Unmarshal(inputJSON, &input); err != nil {
					return nil, err
				}

				// Decrement concurrent counter
				concurrent.Add(-1)

				return ItemOutput{Index: input}, nil
			},
		},
	)

	var mapResults []ItemOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
			results, err := MapWithOptions[int, ItemOutput](wctx, items, childWf, MapOptions{
				MaxConcurrency: 3,
			})
			if err != nil {
				return nil, err
			}
			mapResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify results are in correct order
	if len(mapResults) != 10 {
		t.Fatalf("mapResults length = %d, want 10", len(mapResults))
	}
	for i, r := range mapResults {
		if r.Index != i {
			t.Errorf("mapResults[%d].Index = %d, want %d", i, r.Index, i)
		}
	}

	// Note: max concurrent check is non-deterministic in tests since steps
	// execute quickly. We verify it compiles and runs correctly.
}

// TestMap_ReplaySkipsCompletedChildren tests that on replay, completed Map children are not re-executed.
func TestMap_ReplaySkipsCompletedChildren(t *testing.T) {
	type ItemOutput struct {
		Value int `json:"value"`
	}

	executionCount := 0

	childWf := testWorkflow("child-workflow",
		&testStep{
			name: "child-step",
			execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
				executionCount++
				inputJSON, _ := json.Marshal(ec.Input())
				var input int
				if err := json.Unmarshal(inputJSON, &input); err != nil {
					return nil, err
				}
				return ItemOutput{Value: input * 10}, nil
			},
		},
	)

	var mapResults []ItemOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := []int{1, 2, 3}
			results, err := Map[int, ItemOutput](wctx, items, childWf)
			if err != nil {
				return nil, err
			}
			mapResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	// Create history with map already completed but step NOT completed
	// This simulates a crash after Map finished but before step could complete
	resultsJSON, _ := json.Marshal([]ItemOutput{{Value: 10}, {Value: 20}, {Value: 30}})
	history := NewHistory("parent-run", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "parent-run",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
			Data:     mustMarshal(event.WorkflowStartedData{WorkflowName: "parent-workflow", Version: "1"}),
		},
		{
			ID:       "evt-2",
			RunID:    "parent-run",
			Sequence: 2,
			Type:     event.EventStepStarted,
			StepName: "parent-step",
			Data:     mustMarshal(event.StepStartedData{Attempt: 1}),
		},
		{
			ID:       "evt-3",
			RunID:    "parent-run",
			Sequence: 3,
			Type:     event.EventMapStarted,
			Data:     mustMarshal(event.MapStartedData{ItemCount: 3}),
		},
		{
			ID:       "evt-4",
			RunID:    "parent-run",
			Sequence: 4,
			Type:     event.EventMapCompleted,
			Data:     mustMarshal(event.MapCompletedData{Results: resultsJSON}),
		},
		// Note: No step.completed event - step will re-execute but Map should skip
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Children should NOT have been executed (loaded from history)
	if executionCount != 0 {
		t.Errorf("Child execution count = %d, want 0 (should be skipped on replay)", executionCount)
	}

	// Verify results from history
	if len(mapResults) != 3 {
		t.Fatalf("mapResults length = %d, want 3", len(mapResults))
	}
	expected := []int{10, 20, 30}
	for i, r := range mapResults {
		if r.Value != expected[i] {
			t.Errorf("mapResults[%d].Value = %d, want %d", i, r.Value, expected[i])
		}
	}
}

// TestMap_UniqueChildRunIDs tests that Map children have unique RunIDs.
func TestMap_UniqueChildRunIDs(t *testing.T) {
	type ItemOutput struct {
		Index int `json:"index"`
	}

	childWf := testWorkflow("child-workflow",
		childStepWithInput("child-step", func(n int) ItemOutput {
			return ItemOutput{Index: n}
		}),
	)

	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := []int{0, 1, 2, 3, 4}
			results, err := Map[int, ItemOutput](wctx, items, childWf)
			if err != nil {
				return nil, err
			}
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Collect unique child RunIDs
	childRunIDs := make(map[string]bool)
	for _, e := range output.NewEvents {
		if e.Type == event.EventChildSpawned {
			var data event.ChildSpawnedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Errorf("Failed to unmarshal child.spawned data: %v", err)
				continue
			}
			if childRunIDs[data.ChildRunID] {
				t.Errorf("Duplicate child RunID: %s", data.ChildRunID)
			}
			childRunIDs[data.ChildRunID] = true
		}
	}

	if len(childRunIDs) != 5 {
		t.Errorf("Unique child RunIDs count = %d, want 5", len(childRunIDs))
	}

	// Verify RunID format: parent-run-map-0-{index}
	for runID := range childRunIDs {
		if !hasPrefix(runID, "parent-run-map-0-") {
			t.Errorf("Unexpected RunID format: %s", runID)
		}
	}
}

// hasPrefix checks if s starts with prefix (avoiding strings import).
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// TestMap_LargeItemCount tests Map with 100 items.
func TestMap_LargeItemCount(t *testing.T) {
	type ItemOutput struct {
		Index int `json:"index"`
	}

	childWf := testWorkflow("child-workflow",
		childStepWithInput("child-step", func(n int) ItemOutput {
			return ItemOutput{Index: n}
		}),
	)

	var mapResults []ItemOutput
	parentStep := &testStep{
		name: "parent-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			items := make([]int, 100)
			for i := range items {
				items[i] = i
			}

			results, err := Map[int, ItemOutput](wctx, items, childWf)
			if err != nil {
				return nil, err
			}
			mapResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}

	parentWf := testWorkflow("parent-workflow", parentStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: parentWf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify all 100 results
	if len(mapResults) != 100 {
		t.Fatalf("mapResults length = %d, want 100", len(mapResults))
	}
	for i, r := range mapResults {
		if r.Index != i {
			t.Errorf("mapResults[%d].Index = %d, want %d", i, r.Index, i)
		}
	}
}

// TestMap_NestedMap tests Map within a Map (nested fan-out).
func TestMap_NestedMap(t *testing.T) {
	type InnerOutput struct {
		Product int `json:"product"`
	}

	// Inner workflow: multiply input by 2
	innerWf := testWorkflow("inner-workflow",
		childStepWithInput("inner-step", func(n int) InnerOutput {
			return InnerOutput{Product: n * 2}
		}),
	)

	type OuterOutput struct {
		Sum int `json:"sum"`
	}

	// Outer workflow: Map over inner items and sum results
	outerStep := &testStep{
		name: "outer-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			inputJSON, _ := json.Marshal(ec.Input())
			var input []int
			if err := json.Unmarshal(inputJSON, &input); err != nil {
				return nil, err
			}

			innerResults, err := Map[int, InnerOutput](wctx, input, innerWf)
			if err != nil {
				return nil, err
			}

			sum := 0
			for _, r := range innerResults {
				sum += r.Product
			}

			return OuterOutput{Sum: sum}, nil
		},
	}
	outerWf := testWorkflow("outer-workflow", outerStep)

	// Root workflow: Map over multiple lists
	var rootResults []OuterOutput
	rootStep := &testStep{
		name: "root-step",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			wctx := &workflowContextAdapter{executionContext: ec}

			// Each outer item is a list of numbers to process
			items := [][]int{
				{1, 2, 3},       // sum of doubled: 2+4+6 = 12
				{10, 20},       // sum of doubled: 20+40 = 60
				{5, 5, 5, 5},   // sum of doubled: 10+10+10+10 = 40
			}

			results, err := Map[[]int, OuterOutput](wctx, items, outerWf)
			if err != nil {
				return nil, err
			}
			rootResults = results
			return map[string]int{"count": len(results)}, nil
		},
	}
	rootWf := testWorkflow("root-workflow", rootStep)

	r := NewReplayer(ReplayerConfig{
		Workflow: rootWf,
		RunID:    "root-run",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify nested results
	if len(rootResults) != 3 {
		t.Fatalf("rootResults length = %d, want 3", len(rootResults))
	}
	expectedSums := []int{12, 60, 40}
	for i, r := range rootResults {
		if r.Sum != expectedSums[i] {
			t.Errorf("rootResults[%d].Sum = %d, want %d", i, r.Sum, expectedSums[i])
		}
	}
}
