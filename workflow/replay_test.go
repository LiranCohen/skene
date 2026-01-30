package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lirancohen/skene/event"
	"github.com/lirancohen/skene/retry"
)

// testStep is a simple implementation of StepNode for testing.
type testStep struct {
	name     string
	deps     []string
	execFunc func(ctx context.Context, ec *executionContext) (any, error)
}

func (s *testStep) Name() string          { return s.name }
func (s *testStep) Dependencies() []string { return s.deps }
func (s *testStep) Execute(ctx context.Context, ec *executionContext) (any, error) {
	if s.execFunc != nil {
		return s.execFunc(ctx, ec)
	}
	return map[string]string{"step": s.name}, nil
}

// testWorkflow creates a WorkflowDef for testing.
func testWorkflow(name string, steps ...StepNode) *WorkflowDef {
	stepMap := make(map[string]StepNode)
	for _, s := range steps {
		stepMap[s.Name()] = s
	}
	return &WorkflowDef{
		name:    name,
		version: "1",
		steps:   steps,
		stepMap: stepMap,
	}
}

func TestNewReplayer(t *testing.T) {
	tests := []struct {
		name         string
		config       ReplayerConfig
		wantRunID    string
		wantNextSeq  int64
	}{
		{
			name: "fresh execution (nil history)",
			config: ReplayerConfig{
				Workflow: testWorkflow("test"),
				RunID:    "run-1",
				Input:    map[string]string{"key": "value"},
			},
			wantRunID:   "run-1",
			wantNextSeq: 1,
		},
		{
			name: "with existing history",
			config: ReplayerConfig{
				Workflow: testWorkflow("test"),
				RunID:    "run-2",
				History:  NewHistory("run-2", []event.Event{{Sequence: 5}}),
			},
			wantRunID:   "run-2",
			wantNextSeq: 6,
		},
		{
			name: "with custom logger",
			config: ReplayerConfig{
				Workflow: testWorkflow("test"),
				RunID:    "run-3",
				Logger:   noopLogger{},
			},
			wantRunID:   "run-3",
			wantNextSeq: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReplayer(tt.config)
			if r.config.RunID != tt.wantRunID {
				t.Errorf("RunID = %q, want %q", r.config.RunID, tt.wantRunID)
			}
			if r.nextSequence != tt.wantNextSeq {
				t.Errorf("nextSequence = %d, want %d", r.nextSequence, tt.wantNextSeq)
			}
		})
	}
}

func TestReplayer_FreshExecution(t *testing.T) {
	step1 := &testStep{name: "step1"}
	step2 := &testStep{name: "step2", deps: []string{"step1"}}
	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{"order_id": "123"},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Should have 6 events: workflow.started, step1.started, step1.completed, step2.started, step2.completed, workflow.completed
	if len(output.NewEvents) != 6 {
		t.Fatalf("NewEvents count = %d, want 6", len(output.NewEvents))
	}

	// Verify event types
	expectedTypes := []event.EventType{
		event.EventWorkflowStarted,
		event.EventStepStarted,
		event.EventStepCompleted,
		event.EventStepStarted,
		event.EventStepCompleted,
		event.EventWorkflowCompleted,
	}
	for i, e := range output.NewEvents {
		if e.Type != expectedTypes[i] {
			t.Errorf("Event[%d].Type = %v, want %v", i, e.Type, expectedTypes[i])
		}
	}

	// Verify sequence numbers
	for i, e := range output.NewEvents {
		expectedSeq := int64(i + 1)
		if e.Sequence != expectedSeq {
			t.Errorf("Event[%d].Sequence = %d, want %d", i, e.Sequence, expectedSeq)
		}
	}

	// Verify run ID on events
	for i, e := range output.NewEvents {
		if e.RunID != "run-123" {
			t.Errorf("Event[%d].RunID = %q, want %q", i, e.RunID, "run-123")
		}
	}
}

func TestReplayer_ResumePartialHistory(t *testing.T) {
	step1 := &testStep{name: "step1"}
	step2Executed := false
	step2 := &testStep{
		name: "step2",
		deps: []string{"step1"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			step2Executed = true
			return map[string]string{"from": "step2"}, nil
		},
	}
	wf := testWorkflow("test-workflow", step1, step2)

	// Create history with step1 already completed
	history := NewHistory("run-123", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "run-123",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
			Data:     mustMarshal(event.WorkflowStartedData{WorkflowName: "test-workflow", Version: "1"}),
		},
		{
			ID:       "evt-2",
			RunID:    "run-123",
			Sequence: 2,
			Type:     event.EventStepCompleted,
			StepName: "step1",
			Output:   mustMarshal(map[string]string{"step": "step1"}),
		},
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if !step2Executed {
		t.Error("step2 should have been executed")
	}

	// Should have 3 new events: step2.started, step2.completed, workflow.completed
	if len(output.NewEvents) != 3 {
		t.Fatalf("NewEvents count = %d, want 3", len(output.NewEvents))
	}

	// Verify event types
	if output.NewEvents[0].Type != event.EventStepStarted {
		t.Errorf("Event[0].Type = %v, want step.started", output.NewEvents[0].Type)
	}
	if output.NewEvents[1].Type != event.EventStepCompleted {
		t.Errorf("Event[1].Type = %v, want step.completed", output.NewEvents[1].Type)
	}
	if output.NewEvents[1].StepName != "step2" {
		t.Errorf("Event[1].StepName = %q, want %q", output.NewEvents[1].StepName, "step2")
	}
	if output.NewEvents[2].Type != event.EventWorkflowCompleted {
		t.Errorf("Event[2].Type = %v, want workflow.completed", output.NewEvents[2].Type)
	}

	// Verify sequence continues from history
	if output.NewEvents[0].Sequence != 3 {
		t.Errorf("Event[0].Sequence = %d, want 3", output.NewEvents[0].Sequence)
	}
}

func TestReplayer_CompletedWorkflow(t *testing.T) {
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	// Create history with workflow already completed
	history := NewHistory("run-123", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "run-123",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
		},
		{
			ID:       "evt-2",
			RunID:    "run-123",
			Sequence: 2,
			Type:     event.EventStepCompleted,
			StepName: "step1",
			Output:   mustMarshal(map[string]string{"step": "step1"}),
		},
		{
			ID:       "evt-3",
			RunID:    "run-123",
			Sequence: 3,
			Type:     event.EventWorkflowCompleted,
		},
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// No new events should be generated
	if len(output.NewEvents) != 0 {
		t.Errorf("NewEvents count = %d, want 0", len(output.NewEvents))
	}
}

func TestReplayer_CancelledWorkflow(t *testing.T) {
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	// Create history with workflow cancelled
	history := NewHistory("run-123", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "run-123",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
		},
		{
			ID:       "evt-2",
			RunID:    "run-123",
			Sequence: 2,
			Type:     event.EventWorkflowCancelled,
			Data:     mustMarshal(event.WorkflowCancelledData{Reason: "user requested"}),
		},
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	if !errors.Is(output.Error, ErrWorkflowCancelled) {
		t.Errorf("Error = %v, want ErrWorkflowCancelled", output.Error)
	}
}

func TestReplayer_StepFailure(t *testing.T) {
	stepError := errors.New("step execution failed")
	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return nil, stepError
		},
	}
	wf := testWorkflow("test-workflow", step1)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	if output.Error == nil {
		t.Error("Error should not be nil")
	}

	// Should have 3 events: workflow.started, step.started, step.failed
	if len(output.NewEvents) != 3 {
		t.Fatalf("NewEvents count = %d, want 3", len(output.NewEvents))
	}

	if output.NewEvents[1].Type != event.EventStepStarted {
		t.Errorf("Event[1].Type = %v, want step.started", output.NewEvents[1].Type)
	}
	if output.NewEvents[2].Type != event.EventStepFailed {
		t.Errorf("Event[2].Type = %v, want step.failed", output.NewEvents[2].Type)
	}
}

func TestReplayer_FatalErrorInHistory(t *testing.T) {
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	// Create history with a fatal step failure (will_retry=false)
	history := NewHistory("run-123", []event.Event{
		{
			ID:       "evt-1",
			RunID:    "run-123",
			Sequence: 1,
			Type:     event.EventWorkflowStarted,
		},
		{
			ID:       "evt-2",
			RunID:    "run-123",
			Sequence: 2,
			Type:     event.EventStepFailed,
			StepName: "step1",
			Data:     mustMarshal(event.StepFailedData{Error: "permanent error", WillRetry: false}),
		},
	})

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		History:  history,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	if output.Error == nil || output.Error.Error() == "" {
		t.Error("Error should indicate the step failure")
	}
}

func TestReplayer_ContextCancellation(t *testing.T) {
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	output, err := r.Replay(ctx)
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	// Note: The exact behavior depends on when cancellation is checked
	// It could be ReplaySuspended or could execute some steps first
	if output.Result != ReplaySuspended && output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplaySuspended or ReplayCompleted", output.Result)
	}
}

func TestReplayer_SequentialSteps(t *testing.T) {
	executionOrder := []string{}

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder = append(executionOrder, "step1")
			return map[string]string{"step": "1"}, nil
		},
	}
	step2 := &testStep{
		name: "step2",
		deps: []string{"step1"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder = append(executionOrder, "step2")
			return map[string]string{"step": "2"}, nil
		},
	}
	step3 := &testStep{
		name: "step3",
		deps: []string{"step2"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder = append(executionOrder, "step3")
			return map[string]string{"step": "3"}, nil
		},
	}

	wf := testWorkflow("test-workflow", step1, step2, step3)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify execution order
	expectedOrder := []string{"step1", "step2", "step3"}
	if len(executionOrder) != len(expectedOrder) {
		t.Fatalf("execution order length = %d, want %d", len(executionOrder), len(expectedOrder))
	}
	for i, step := range executionOrder {
		if step != expectedOrder[i] {
			t.Errorf("execution order[%d] = %q, want %q", i, step, expectedOrder[i])
		}
	}
}

func TestReplayer_StepCanAccessDependencyOutput(t *testing.T) {
	type Step1Output struct {
		Value int `json:"value"`
	}

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return Step1Output{Value: 42}, nil
		},
	}

	var step2ReceivedValue int
	step2 := &testStep{
		name: "step2",
		deps: []string{"step1"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			output, ok := ec.GetOutput("step1")
			if !ok {
				return nil, errors.New("step1 output not found")
			}
			var s1out Step1Output
			if err := json.Unmarshal(output, &s1out); err != nil {
				return nil, err
			}
			step2ReceivedValue = s1out.Value
			return map[string]int{"received": s1out.Value}, nil
		},
	}

	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if step2ReceivedValue != 42 {
		t.Errorf("step2 received value = %d, want 42", step2ReceivedValue)
	}
}

func TestReplayer_StepCanAccessWorkflowInput(t *testing.T) {
	type WorkflowInput struct {
		OrderID string `json:"order_id"`
	}

	var receivedInput any
	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			receivedInput = ec.Input()
			return map[string]string{"done": "true"}, nil
		},
	}

	wf := testWorkflow("test-workflow", step1)
	input := WorkflowInput{OrderID: "order-456"}

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    input,
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Verify step received the workflow input
	gotInput, ok := receivedInput.(WorkflowInput)
	if !ok {
		t.Fatalf("receivedInput type = %T, want WorkflowInput", receivedInput)
	}
	if gotInput.OrderID != "order-456" {
		t.Errorf("receivedInput.OrderID = %q, want %q", gotInput.OrderID, "order-456")
	}
}

func TestReplayer_EventsHaveCorrectRunID(t *testing.T) {
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "unique-run-789",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	for i, e := range output.NewEvents {
		if e.RunID != "unique-run-789" {
			t.Errorf("Event[%d].RunID = %q, want %q", i, e.RunID, "unique-run-789")
		}
	}
}

func TestReplayer_EventsHaveUniqueIDs(t *testing.T) {
	step1 := &testStep{name: "step1"}
	step2 := &testStep{name: "step2", deps: []string{"step1"}}
	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-123",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	seenIDs := make(map[string]bool)
	for i, e := range output.NewEvents {
		if e.ID == "" {
			t.Errorf("Event[%d].ID is empty", i)
		}
		if seenIDs[e.ID] {
			t.Errorf("Event[%d].ID = %q is duplicate", i, e.ID)
		}
		seenIDs[e.ID] = true
	}
}

func TestReplayer_NextSequence(t *testing.T) {
	tests := []struct {
		name             string
		historySeq       int64
		numSteps         int
		wantNextSequence int64
	}{
		{
			name:             "fresh execution with 1 step",
			historySeq:       0,
			numSteps:         1,
			wantNextSequence: 5, // 1=workflow.started, 2=step.started, 3=step.completed, 4=workflow.completed, next=5
		},
		{
			name:             "resume from seq 5 with 1 step",
			historySeq:       5,
			numSteps:         1,
			wantNextSequence: 10, // 6=workflow.started (no input in history), 7=step.started, 8=step.completed, 9=workflow.completed, next=10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			steps := make([]StepNode, tt.numSteps)
			for i := 0; i < tt.numSteps; i++ {
				steps[i] = &testStep{name: "step" + string(rune('1'+i))}
			}
			wf := testWorkflow("test-workflow", steps...)

			var history *History
			if tt.historySeq > 0 {
				events := []event.Event{
					{Sequence: tt.historySeq, Type: event.EventWorkflowStarted},
				}
				history = NewHistory("run-123", events)
			}

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "run-123",
				History:  history,
				Input:    map[string]string{},
			})

			output, err := r.Replay(context.Background())
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}

			if output.NextSequence != tt.wantNextSequence {
				t.Errorf("NextSequence = %d, want %d", output.NextSequence, tt.wantNextSequence)
			}
		})
	}
}

func TestReplayResult_String(t *testing.T) {
	tests := []struct {
		result ReplayResult
		want   string
	}{
		{ReplayCompleted, "completed"},
		{ReplayWaiting, "waiting"},
		{ReplayFailed, "failed"},
		{ReplaySuspended, "suspended"},
		{ReplayResult(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.result.String(); got != tt.want {
				t.Errorf("ReplayResult.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecutionContext_Methods(t *testing.T) {
	var mu sync.RWMutex
	ec := &executionContext{
		ctx:          context.Background(),
		runID:        "run-abc",
		workflowName: "test-workflow",
		input:        map[string]string{"key": "value"},
		history:      NewHistory("run-abc", nil),
		outputCache:  make(map[string]any),
		outputMu:     &mu,
	}

	if ec.RunID() != "run-abc" {
		t.Errorf("RunID() = %q, want %q", ec.RunID(), "run-abc")
	}

	if ec.WorkflowName() != "test-workflow" {
		t.Errorf("WorkflowName() = %q, want %q", ec.WorkflowName(), "test-workflow")
	}

	if ec.Input() == nil {
		t.Error("Input() should not be nil")
	}

	// GetOutput from empty cache/history
	if _, ok := ec.GetOutput("nonexistent"); ok {
		t.Error("GetOutput() should return ok=false for nonexistent step")
	}

	// GetOutput from cache
	ec.outputCache["cached-step"] = map[string]string{"cached": "true"}
	output, ok := ec.GetOutput("cached-step")
	if !ok {
		t.Error("GetOutput() should return ok=true for cached step")
	}
	if output == nil {
		t.Error("GetOutput() should return non-nil output for cached step")
	}
}

func TestWorkflowDef_Methods(t *testing.T) {
	step1 := &testStep{name: "step1"}
	step2 := &testStep{name: "step2", deps: []string{"step1"}}

	wf := &WorkflowDef{
		name:    "test-workflow",
		version: "2",
		steps:   []StepNode{step1, step2},
		stepMap: map[string]StepNode{"step1": step1, "step2": step2},
	}

	if wf.Name() != "test-workflow" {
		t.Errorf("Name() = %q, want %q", wf.Name(), "test-workflow")
	}

	if wf.Version() != "2" {
		t.Errorf("Version() = %q, want %q", wf.Version(), "2")
	}

	steps := wf.Steps()
	if len(steps) != 2 {
		t.Errorf("Steps() length = %d, want 2", len(steps))
	}

	s, ok := wf.GetStep("step1")
	if !ok {
		t.Error("GetStep(step1) should return ok=true")
	}
	if s.Name() != "step1" {
		t.Errorf("GetStep(step1).Name() = %q, want %q", s.Name(), "step1")
	}

	_, ok = wf.GetStep("nonexistent")
	if ok {
		t.Error("GetStep(nonexistent) should return ok=false")
	}
}

// =============================================================================
// Parallel Execution Tests
// =============================================================================

func TestReplayer_TwoIndependentStepsRunInParallel(t *testing.T) {
	// Use channels to detect concurrent execution
	step1Started := make(chan struct{})
	step2Started := make(chan struct{})
	proceed := make(chan struct{})

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			step1Started <- struct{}{}
			<-proceed
			return map[string]string{"step": "1"}, nil
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			step2Started <- struct{}{}
			<-proceed
			return map[string]string{"step": "2"}, nil
		},
	}

	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-parallel-2",
		Input:    map[string]string{},
	})

	// Run replay in a goroutine
	done := make(chan *ReplayOutput)
	go func() {
		output, _ := r.Replay(context.Background())
		done <- output
	}()

	// Wait for both steps to start (proves they're running concurrently)
	<-step1Started
	<-step2Started

	// Let both complete
	close(proceed)

	output := <-done

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Should have 6 events: workflow.started, 2x(step.started, step.completed), workflow.completed
	if len(output.NewEvents) != 6 {
		t.Errorf("NewEvents count = %d, want 6", len(output.NewEvents))
	}
}

func TestReplayer_ThreeIndependentStepsRunInParallel(t *testing.T) {
	// Use channels to detect concurrent execution
	started := make(chan string, 3)
	proceed := make(chan struct{})

	makeStep := func(name string) *testStep {
		return &testStep{
			name: name,
			execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
				started <- name
				<-proceed
				return map[string]string{"step": name}, nil
			},
		}
	}

	step1 := makeStep("step1")
	step2 := makeStep("step2")
	step3 := makeStep("step3")

	wf := testWorkflow("test-workflow", step1, step2, step3)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-parallel-3",
		Input:    map[string]string{},
	})

	// Run replay in a goroutine
	done := make(chan *ReplayOutput)
	go func() {
		output, _ := r.Replay(context.Background())
		done <- output
	}()

	// Wait for all three steps to start (proves they're running concurrently)
	startedSteps := make(map[string]bool)
	for i := 0; i < 3; i++ {
		name := <-started
		startedSteps[name] = true
	}

	if len(startedSteps) != 3 {
		t.Errorf("Expected 3 steps started concurrently, got %d", len(startedSteps))
	}

	// Let all complete
	close(proceed)

	output := <-done

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Should have 8 events: workflow.started, 3x(step.started, step.completed), workflow.completed
	if len(output.NewEvents) != 8 {
		t.Errorf("NewEvents count = %d, want 8", len(output.NewEvents))
	}
}

func TestReplayer_DiamondPattern(t *testing.T) {
	// Diamond: A -> (B, C) -> D
	// A runs first, then B and C in parallel, then D
	executionOrder := make(chan string, 4)

	stepA := &testStep{
		name: "A",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder <- "A"
			return map[string]string{"step": "A"}, nil
		},
	}
	stepB := &testStep{
		name: "B",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder <- "B"
			return map[string]string{"step": "B"}, nil
		},
	}
	stepC := &testStep{
		name: "C",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder <- "C"
			return map[string]string{"step": "C"}, nil
		},
	}
	stepD := &testStep{
		name: "D",
		deps: []string{"B", "C"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			executionOrder <- "D"
			return map[string]string{"step": "D"}, nil
		},
	}

	wf := testWorkflow("diamond-workflow", stepA, stepB, stepC, stepD)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-diamond",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	// Collect execution order
	close(executionOrder)
	var order []string
	for name := range executionOrder {
		order = append(order, name)
	}

	// A must be first, D must be last
	if order[0] != "A" {
		t.Errorf("First step = %q, want A", order[0])
	}
	if order[3] != "D" {
		t.Errorf("Last step = %q, want D", order[3])
	}

	// B and C can be in any order (they run in parallel)
	middle := order[1:3]
	hasB := middle[0] == "B" || middle[1] == "B"
	hasC := middle[0] == "C" || middle[1] == "C"
	if !hasB || !hasC {
		t.Errorf("Middle steps = %v, want B and C in any order", middle)
	}

	// Should have 10 events: workflow.started, 4x(step.started, step.completed), workflow.completed
	if len(output.NewEvents) != 10 {
		t.Errorf("NewEvents count = %d, want 10", len(output.NewEvents))
	}
}

func TestReplayer_ParallelStepsDependencyAccess(t *testing.T) {
	// Verify that parallel steps B and C can both access A's output
	type OutputA struct {
		Value int `json:"value"`
	}

	var (
		bReceivedValue int
		cReceivedValue int
	)

	stepA := &testStep{
		name: "A",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return OutputA{Value: 42}, nil
		},
	}
	stepB := &testStep{
		name: "B",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			output, ok := ec.GetOutput("A")
			if !ok {
				return nil, errors.New("A output not found")
			}
			var a OutputA
			if err := json.Unmarshal(output, &a); err != nil {
				return nil, err
			}
			bReceivedValue = a.Value
			return map[string]int{"from_b": a.Value}, nil
		},
	}
	stepC := &testStep{
		name: "C",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			output, ok := ec.GetOutput("A")
			if !ok {
				return nil, errors.New("A output not found")
			}
			var a OutputA
			if err := json.Unmarshal(output, &a); err != nil {
				return nil, err
			}
			cReceivedValue = a.Value
			return map[string]int{"from_c": a.Value}, nil
		},
	}

	wf := testWorkflow("test-workflow", stepA, stepB, stepC)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-parallel-access",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if bReceivedValue != 42 {
		t.Errorf("B received value = %d, want 42", bReceivedValue)
	}
	if cReceivedValue != 42 {
		t.Errorf("C received value = %d, want 42", cReceivedValue)
	}
}

func TestReplayer_EventOrderingDeterministic(t *testing.T) {
	// Run the same workflow multiple times and verify event order is consistent
	// Steps are sorted by name, so event order should be deterministic

	makeWorkflow := func() *WorkflowDef {
		stepA := &testStep{name: "step_a"}
		stepB := &testStep{name: "step_b"}
		stepC := &testStep{name: "step_c"}
		return testWorkflow("test-workflow", stepA, stepB, stepC)
	}

	var firstRunOrder []string
	for i := 0; i < 5; i++ {
		wf := makeWorkflow()
		r := NewReplayer(ReplayerConfig{
			Workflow: wf,
			RunID:    "run-" + string(rune('0'+i)),
			Input:    map[string]string{},
		})

		output, err := r.Replay(context.Background())
		if err != nil {
			t.Fatalf("Replay() error = %v", err)
		}

		// Extract step names from events
		var stepOrder []string
		for _, e := range output.NewEvents {
			if e.StepName != "" {
				stepOrder = append(stepOrder, e.StepName)
			}
		}

		if i == 0 {
			firstRunOrder = stepOrder
		} else {
			// Compare with first run
			if len(stepOrder) != len(firstRunOrder) {
				t.Errorf("Run %d: step order length = %d, want %d", i, len(stepOrder), len(firstRunOrder))
				continue
			}
			for j, name := range stepOrder {
				if name != firstRunOrder[j] {
					t.Errorf("Run %d: step[%d] = %q, want %q", i, j, name, firstRunOrder[j])
				}
			}
		}
	}
}

func TestReplayer_SequenceNumbersAreSequential(t *testing.T) {
	// Verify sequence numbers are always sequential, even for parallel steps
	step1 := &testStep{name: "step1"}
	step2 := &testStep{name: "step2"}
	step3 := &testStep{name: "step3"}

	wf := testWorkflow("test-workflow", step1, step2, step3)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-seq",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Verify sequence numbers are sequential starting from 1
	for i, e := range output.NewEvents {
		expectedSeq := int64(i + 1)
		if e.Sequence != expectedSeq {
			t.Errorf("Event[%d].Sequence = %d, want %d", i, e.Sequence, expectedSeq)
		}
	}
}

func TestReplayer_FindReadyStepsReturnsCorrectOrder(t *testing.T) {
	// Test the findReadySteps method returns steps in deterministic order
	step3 := &testStep{name: "zzz"} // Last alphabetically
	step2 := &testStep{name: "mmm"} // Middle
	step1 := &testStep{name: "aaa"} // First alphabetically

	// Add in non-alphabetical order to verify sorting
	wf := testWorkflow("test-workflow", step3, step2, step1)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-sort",
		Input:    map[string]string{},
	})

	completed := make(map[string]bool)
	ready := r.findReadySteps(completed)

	// All three should be ready (no dependencies)
	if len(ready) != 3 {
		t.Fatalf("findReadySteps returned %d steps, want 3", len(ready))
	}

	// Should be in alphabetical order
	expectedOrder := []string{"aaa", "mmm", "zzz"}
	for i, step := range ready {
		if step.Name() != expectedOrder[i] {
			t.Errorf("ready[%d].Name() = %q, want %q", i, step.Name(), expectedOrder[i])
		}
	}
}

// =============================================================================
// Parallel Execution Failure Mode Tests
// =============================================================================

func TestReplayer_ParallelStepFailureCancelsOthers(t *testing.T) {
	// Test that when one parallel step fails, others are cancelled (fail-fast)
	stepError := errors.New("step2 failed intentionally")

	step1Started := make(chan struct{})
	step1Ctx := make(chan context.Context, 1)
	step2Started := make(chan struct{})
	step3Started := make(chan struct{})

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			close(step1Started)
			step1Ctx <- ctx
			// Wait for context cancellation (from step2 failure)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			close(step2Started)
			// Fail immediately
			return nil, stepError
		},
	}
	step3 := &testStep{
		name: "step3",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			close(step3Started)
			// Wait for context cancellation
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	wf := testWorkflow("test-workflow", step1, step2, step3)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-fail-fast",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	// Should have failed
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Error should mention step2
	if output.Error == nil || !errors.Is(output.Error, stepError) {
		// Check if error message contains info about step2
		if output.Error == nil {
			t.Error("Error should not be nil")
		}
	}

	// Verify step1's context was cancelled
	select {
	case ctx := <-step1Ctx:
		if ctx.Err() == nil {
			t.Error("step1's context should have been cancelled")
		}
	default:
		// step1 might not have sent its context if it finished before we could receive
	}
}

func TestReplayer_ParallelFailureRecordsFailedEvent(t *testing.T) {
	// Verify that step.failed event is recorded for the failing step
	stepError := errors.New("deliberate failure")

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return nil, stepError
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			// This step may or may not complete depending on timing
			return map[string]string{"step": "2"}, nil
		},
	}

	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-fail-event",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Find the step.failed event
	var foundFailedEvent bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepFailed && e.StepName == "step1" {
			foundFailedEvent = true
			// Verify error is recorded in the event data
			var failedData event.StepFailedData
			if err := json.Unmarshal(e.Data, &failedData); err != nil {
				t.Errorf("Failed to unmarshal step.failed data: %v", err)
			}
			if failedData.Error != stepError.Error() {
				t.Errorf("StepFailedData.Error = %q, want %q", failedData.Error, stepError.Error())
			}
			break
		}
	}

	if !foundFailedEvent {
		t.Error("Expected to find step.failed event for step1")
	}
}

func TestReplayer_ParallelFailurePreservesCompletedOutputs(t *testing.T) {
	// Verify that completed step outputs are preserved even when another step fails
	// Use synchronization to ensure step1 completes before step2 fails
	step1Done := make(chan struct{})

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			defer close(step1Done)
			return map[string]string{"step1": "completed"}, nil
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			// Wait for step1 to complete first
			<-step1Done
			return nil, errors.New("step2 failed")
		},
	}

	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-preserve-output",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Find step1's completed event
	var foundStep1Completed bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepCompleted && e.StepName == "step1" {
			foundStep1Completed = true
			// Verify output is present
			var stepOutput map[string]string
			if err := json.Unmarshal(e.Output, &stepOutput); err != nil {
				t.Errorf("Failed to unmarshal step1 output: %v", err)
			}
			if stepOutput["step1"] != "completed" {
				t.Errorf("step1 output = %v, want map with step1=completed", stepOutput)
			}
			break
		}
	}

	if !foundStep1Completed {
		t.Error("Expected to find step.completed event for step1")
	}
}

func TestReplayer_ParallelFailureAllEventsRecorded(t *testing.T) {
	// Verify all parallel events are recorded before returning error
	// One step succeeds quickly, one fails, one is cancelled
	var mu sync.Mutex
	completedSteps := make(map[string]bool)

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			mu.Lock()
			completedSteps["step1"] = true
			mu.Unlock()
			return map[string]string{"step": "1"}, nil
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return nil, errors.New("step2 failed")
		},
	}
	step3 := &testStep{
		name: "step3",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			// Check for cancellation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				mu.Lock()
				completedSteps["step3"] = true
				mu.Unlock()
				return map[string]string{"step": "3"}, nil
			}
		},
	}

	wf := testWorkflow("test-workflow", step1, step2, step3)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-all-events",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Count event types
	eventCounts := make(map[event.EventType]int)
	for _, e := range output.NewEvents {
		eventCounts[e.Type]++
	}

	// Should have workflow.started
	if eventCounts[event.EventWorkflowStarted] != 1 {
		t.Errorf("workflow.started count = %d, want 1", eventCounts[event.EventWorkflowStarted])
	}

	// Should have at least one step.failed
	if eventCounts[event.EventStepFailed] < 1 {
		t.Errorf("step.failed count = %d, want >= 1", eventCounts[event.EventStepFailed])
	}

	// Verify sequence numbers are still sequential
	for i, e := range output.NewEvents {
		expectedSeq := int64(i + 1)
		if e.Sequence != expectedSeq {
			t.Errorf("Event[%d].Sequence = %d, want %d", i, e.Sequence, expectedSeq)
		}
	}
}

func TestReplayer_DiamondPatternWithFailure(t *testing.T) {
	// Diamond: A -> (B, C) -> D
	// B fails, verify C's output is preserved and D never executes
	bFailed := make(chan struct{})
	var dExecuted bool

	stepA := &testStep{
		name: "A",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			return map[string]string{"step": "A"}, nil
		},
	}
	stepB := &testStep{
		name: "B",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			defer close(bFailed)
			return nil, errors.New("B failed")
		},
	}
	stepC := &testStep{
		name: "C",
		deps: []string{"A"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			// Wait for B to fail first to test cancellation
			<-bFailed
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				return map[string]string{"step": "C"}, nil
			}
		},
	}
	stepD := &testStep{
		name: "D",
		deps: []string{"B", "C"},
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			dExecuted = true
			return map[string]string{"step": "D"}, nil
		},
	}

	wf := testWorkflow("diamond-workflow", stepA, stepB, stepC, stepD)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-diamond-fail",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() unexpected error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// D should never execute because B failed
	if dExecuted {
		t.Error("Step D should not have executed since B failed")
	}

	// Verify A completed
	var foundACompleted bool
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepCompleted && e.StepName == "A" {
			foundACompleted = true
			break
		}
	}
	if !foundACompleted {
		t.Error("Expected A to have completed before failure")
	}
}

func TestReplayer_ContextCancellationDuringParallel(t *testing.T) {
	// Test that external context cancellation properly cancels parallel steps
	step1Started := make(chan struct{})
	step2Started := make(chan struct{})

	step1 := &testStep{
		name: "step1",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			close(step1Started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	step2 := &testStep{
		name: "step2",
		execFunc: func(ctx context.Context, ec *executionContext) (any, error) {
			close(step2Started)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	wf := testWorkflow("test-workflow", step1, step2)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-cancel",
		Input:    map[string]string{},
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan *ReplayOutput)
	go func() {
		output, _ := r.Replay(ctx)
		done <- output
	}()

	// Wait for both steps to start
	<-step1Started
	<-step2Started

	// Cancel the context
	cancel()

	output := <-done

	// Should be suspended or failed due to cancellation
	if output.Result != ReplaySuspended && output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplaySuspended or ReplayFailed", output.Result)
	}
}

// =============================================================================
// Child Workflow Event Helper Tests
// =============================================================================

func TestReplayer_RecordChildSpawned(t *testing.T) {
	tests := []struct {
		name         string
		childRunID   string
		workflowName string
		input        map[string]any
	}{
		{
			name:         "basic child spawned event",
			childRunID:   "run-123-child-0",
			workflowName: "child-workflow",
			input:        map[string]any{"item": "test"},
		},
		{
			name:         "child spawned with empty input",
			childRunID:   "run-456-child-1",
			workflowName: "empty-input-child",
			input:        map[string]any{},
		},
		{
			name:         "child spawned with complex input",
			childRunID:   "parent-map-0-child-5",
			workflowName: "process-item",
			input:        map[string]any{"id": float64(123), "items": []any{float64(1), float64(2), float64(3)}, "nested": map[string]any{"key": "value"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			inputJSON, _ := json.Marshal(tt.input)
			evt := r.RecordChildSpawned(tt.childRunID, tt.workflowName, inputJSON)

			// Verify event type
			if evt.Type != event.EventChildSpawned {
				t.Errorf("Event.Type = %v, want child.spawned", evt.Type)
			}

			// Verify event has required fields
			if evt.RunID != "parent-run" {
				t.Errorf("Event.RunID = %q, want %q", evt.RunID, "parent-run")
			}
			if evt.Sequence != 1 {
				t.Errorf("Event.Sequence = %d, want 1", evt.Sequence)
			}
			if evt.ID == "" {
				t.Error("Event.ID should not be empty")
			}

			// Verify event data
			var data event.ChildSpawnedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.ChildRunID != tt.childRunID {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, tt.childRunID)
			}
			if data.WorkflowName != tt.workflowName {
				t.Errorf("WorkflowName = %q, want %q", data.WorkflowName, tt.workflowName)
			}
			// Compare by unmarshaling both to verify JSON equivalence
			var gotInput, wantInput map[string]any
			if err := json.Unmarshal(data.Input, &gotInput); err != nil {
				t.Errorf("Failed to unmarshal data.Input: %v", err)
			}
			if err := json.Unmarshal(inputJSON, &wantInput); err != nil {
				t.Errorf("Failed to unmarshal inputJSON: %v", err)
			}
			gotInputJSON, _ := json.Marshal(gotInput)
			wantInputJSON, _ := json.Marshal(wantInput)
			if string(gotInputJSON) != string(wantInputJSON) {
				t.Errorf("Input = %s, want %s", gotInputJSON, wantInputJSON)
			}

			// Verify event was added to newEvents
			if len(r.newEvents) != 1 {
				t.Errorf("newEvents length = %d, want 1", len(r.newEvents))
			}
		})
	}
}

func TestReplayer_RecordChildCompleted(t *testing.T) {
	tests := []struct {
		name       string
		childRunID string
		output     map[string]any
		nilOutput  bool
	}{
		{
			name:       "basic child completed",
			childRunID: "run-123-child-0",
			output:     map[string]any{"result": "success"},
		},
		{
			name:       "child completed with empty output",
			childRunID: "run-456-child-1",
			nilOutput:  true,
		},
		{
			name:       "child completed with complex output",
			childRunID: "parent-map-0-child-5",
			output:     map[string]any{"processed": true, "count": float64(42), "items": []any{"a", "b"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			var outputJSON json.RawMessage
			if !tt.nilOutput {
				outputJSON, _ = json.Marshal(tt.output)
			}
			evt := r.RecordChildCompleted(tt.childRunID, outputJSON)

			// Verify event type
			if evt.Type != event.EventChildCompleted {
				t.Errorf("Event.Type = %v, want child.completed", evt.Type)
			}

			// Verify event has required fields
			if evt.RunID != "parent-run" {
				t.Errorf("Event.RunID = %q, want %q", evt.RunID, "parent-run")
			}

			// Verify event data
			var data event.ChildCompletedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.ChildRunID != tt.childRunID {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, tt.childRunID)
			}
			if tt.nilOutput {
				if len(data.Output) != 0 {
					t.Errorf("Output = %s, want nil/empty", data.Output)
				}
			} else {
				// Compare by unmarshaling both to verify JSON equivalence
				var gotOutput, wantOutput map[string]any
				if err := json.Unmarshal(data.Output, &gotOutput); err != nil {
					t.Errorf("Failed to unmarshal data.Output: %v", err)
				}
				if err := json.Unmarshal(outputJSON, &wantOutput); err != nil {
					t.Errorf("Failed to unmarshal outputJSON: %v", err)
				}
				gotOutputJSON, _ := json.Marshal(gotOutput)
				wantOutputJSON, _ := json.Marshal(wantOutput)
				if string(gotOutputJSON) != string(wantOutputJSON) {
					t.Errorf("Output = %s, want %s", gotOutputJSON, wantOutputJSON)
				}
			}
		})
	}
}

func TestReplayer_RecordChildFailed(t *testing.T) {
	tests := []struct {
		name       string
		childRunID string
		err        error
	}{
		{
			name:       "basic child failure",
			childRunID: "run-123-child-0",
			err:        errors.New("child workflow failed"),
		},
		{
			name:       "child failure with detailed error",
			childRunID: "run-456-child-1",
			err:        errors.New("step 'process' failed: database connection timeout"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			evt := r.RecordChildFailed(tt.childRunID, tt.err)

			// Verify event type
			if evt.Type != event.EventChildFailed {
				t.Errorf("Event.Type = %v, want child.failed", evt.Type)
			}

			// Verify event data
			var data event.ChildFailedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.ChildRunID != tt.childRunID {
				t.Errorf("ChildRunID = %q, want %q", data.ChildRunID, tt.childRunID)
			}
			if data.Error != tt.err.Error() {
				t.Errorf("Error = %q, want %q", data.Error, tt.err.Error())
			}
		})
	}
}

func TestReplayer_RecordMapStarted(t *testing.T) {
	tests := []struct {
		name      string
		mapIndex  int64
		itemCount int
	}{
		{
			name:      "map with single item",
			mapIndex:  0,
			itemCount: 1,
		},
		{
			name:      "map with multiple items",
			mapIndex:  1,
			itemCount: 10,
		},
		{
			name:      "map with large item count",
			mapIndex:  2,
			itemCount: 1000,
		},
		{
			name:      "map with zero items",
			mapIndex:  3,
			itemCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			evt := r.RecordMapStarted(tt.mapIndex, tt.itemCount)

			// Verify event type
			if evt.Type != event.EventMapStarted {
				t.Errorf("Event.Type = %v, want map.started", evt.Type)
			}

			// Verify event data
			var data event.MapStartedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.MapIndex != tt.mapIndex {
				t.Errorf("MapIndex = %d, want %d", data.MapIndex, tt.mapIndex)
			}
			if data.ItemCount != tt.itemCount {
				t.Errorf("ItemCount = %d, want %d", data.ItemCount, tt.itemCount)
			}
		})
	}
}

func TestReplayer_RecordMapCompleted(t *testing.T) {
	tests := []struct {
		name     string
		mapIndex int64
		results  []map[string]any
	}{
		{
			name:     "map completed with array results",
			mapIndex: 0,
			results:  []map[string]any{{"id": float64(1)}, {"id": float64(2)}, {"id": float64(3)}},
		},
		{
			name:     "map completed with empty results",
			mapIndex: 1,
			results:  []map[string]any{},
		},
		{
			name:     "map completed with complex results",
			mapIndex: 2,
			results:  []map[string]any{{"status": "success", "data": map[string]any{"key": "value"}}, {"status": "success", "data": map[string]any{"key": "value2"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			resultsJSON, _ := json.Marshal(tt.results)
			evt := r.RecordMapCompleted(tt.mapIndex, resultsJSON)

			// Verify event type
			if evt.Type != event.EventMapCompleted {
				t.Errorf("Event.Type = %v, want map.completed", evt.Type)
			}

			// Verify event data
			var data event.MapCompletedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.MapIndex != tt.mapIndex {
				t.Errorf("MapIndex = %d, want %d", data.MapIndex, tt.mapIndex)
			}
			// Compare by unmarshaling both to verify JSON equivalence
			var gotResults, wantResults []map[string]any
			if err := json.Unmarshal(data.Results, &gotResults); err != nil {
				t.Errorf("Failed to unmarshal data.Results: %v", err)
			}
			if err := json.Unmarshal(resultsJSON, &wantResults); err != nil {
				t.Errorf("Failed to unmarshal resultsJSON: %v", err)
			}
			gotResultsJSON, _ := json.Marshal(gotResults)
			wantResultsJSON, _ := json.Marshal(wantResults)
			if string(gotResultsJSON) != string(wantResultsJSON) {
				t.Errorf("Results = %s, want %s", gotResultsJSON, wantResultsJSON)
			}
		})
	}
}

func TestReplayer_RecordMapFailed(t *testing.T) {
	tests := []struct {
		name        string
		mapIndex    int64
		failedIndex int
		err         error
	}{
		{
			name:        "first item failed",
			mapIndex:    0,
			failedIndex: 0,
			err:         errors.New("processing failed for item 0"),
		},
		{
			name:        "middle item failed",
			mapIndex:    1,
			failedIndex: 5,
			err:         errors.New("timeout processing item"),
		},
		{
			name:        "last item failed",
			mapIndex:    2,
			failedIndex: 99,
			err:         errors.New("validation error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step1 := &testStep{name: "step1"}
			wf := testWorkflow("test-workflow", step1)

			r := NewReplayer(ReplayerConfig{
				Workflow: wf,
				RunID:    "parent-run",
				Input:    map[string]string{},
			})

			evt := r.RecordMapFailed(tt.mapIndex, tt.failedIndex, tt.err)

			// Verify event type
			if evt.Type != event.EventMapFailed {
				t.Errorf("Event.Type = %v, want map.failed", evt.Type)
			}

			// Verify event data
			var data event.MapFailedData
			if err := json.Unmarshal(evt.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal event data: %v", err)
			}
			if data.MapIndex != tt.mapIndex {
				t.Errorf("MapIndex = %d, want %d", data.MapIndex, tt.mapIndex)
			}
			if data.FailedIndex != tt.failedIndex {
				t.Errorf("FailedIndex = %d, want %d", data.FailedIndex, tt.failedIndex)
			}
			if data.Error != tt.err.Error() {
				t.Errorf("Error = %q, want %q", data.Error, tt.err.Error())
			}
		})
	}
}

func TestReplayer_ChildEventSequencing(t *testing.T) {
	// Verify that multiple child events get sequential sequence numbers
	step1 := &testStep{name: "step1"}
	wf := testWorkflow("test-workflow", step1)

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "parent-run",
		Input:    map[string]string{},
	})

	// Record a series of child events
	evt1 := r.RecordMapStarted(0, 3)
	evt2 := r.RecordChildSpawned("child-0", "child-wf", json.RawMessage(`{}`))
	evt3 := r.RecordChildCompleted("child-0", json.RawMessage(`{"done": true}`))
	evt4 := r.RecordChildSpawned("child-1", "child-wf", json.RawMessage(`{}`))
	evt5 := r.RecordChildFailed("child-1", errors.New("failed"))
	evt6 := r.RecordMapFailed(0, 1, errors.New("map failed"))

	// Verify sequential sequence numbers
	events := []event.Event{evt1, evt2, evt3, evt4, evt5, evt6}
	for i, e := range events {
		expectedSeq := int64(i + 1)
		if e.Sequence != expectedSeq {
			t.Errorf("Event[%d].Sequence = %d, want %d", i, e.Sequence, expectedSeq)
		}
	}

	// Verify total events
	if len(r.newEvents) != 6 {
		t.Errorf("newEvents length = %d, want 6", len(r.newEvents))
	}

	// Verify next sequence
	if r.nextSequence != 7 {
		t.Errorf("nextSequence = %d, want 7", r.nextSequence)
	}
}

// =============================================================================
// Retry Behavior Tests
// =============================================================================

func TestReplayer_StepWithRetrySucceedsAfterFailures(t *testing.T) {
	// Step fails twice, succeeds on third attempt
	attempts := 0
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		attempts++
		if attempts < 3 {
			return nil, errors.New("temporary error")
		}
		return map[string]string{"attempt": "3"}, nil
	}).WithRetry(&retry.Policy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond, // Very short for testing
		Multiplier:   1.0,
		Jitter:       0,
	})

	wf := Define("test-retry", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-retry-success",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	// Verify events: workflow.started, 3x(step.started, step.failed/completed), workflow.completed
	// Expected: started, step.started(1), step.failed(1), step.started(2), step.failed(2), step.started(3), step.completed, workflow.completed
	// Count: 1 + 2 + 2 + 2 + 1 = 8
	expectedEventTypes := []event.EventType{
		event.EventWorkflowStarted,
		event.EventStepStarted,
		event.EventStepFailed,
		event.EventStepStarted,
		event.EventStepFailed,
		event.EventStepStarted,
		event.EventStepCompleted,
		event.EventWorkflowCompleted,
	}

	if len(output.NewEvents) != len(expectedEventTypes) {
		t.Fatalf("NewEvents count = %d, want %d", len(output.NewEvents), len(expectedEventTypes))
	}

	for i, e := range output.NewEvents {
		if e.Type != expectedEventTypes[i] {
			t.Errorf("Event[%d].Type = %v, want %v", i, e.Type, expectedEventTypes[i])
		}
	}

	// Verify step.failed events have correct attempt numbers and WillRetry flags
	var failedEvents []event.StepFailedData
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepFailed {
			var data event.StepFailedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal step.failed data: %v", err)
			}
			failedEvents = append(failedEvents, data)
		}
	}

	if len(failedEvents) != 2 {
		t.Fatalf("Expected 2 step.failed events, got %d", len(failedEvents))
	}

	// First failure: attempt 1, will retry
	if failedEvents[0].Attempt != 1 || !failedEvents[0].WillRetry {
		t.Errorf("First failure: Attempt=%d, WillRetry=%v, want Attempt=1, WillRetry=true",
			failedEvents[0].Attempt, failedEvents[0].WillRetry)
	}

	// Second failure: attempt 2, will retry
	if failedEvents[1].Attempt != 2 || !failedEvents[1].WillRetry {
		t.Errorf("Second failure: Attempt=%d, WillRetry=%v, want Attempt=2, WillRetry=true",
			failedEvents[1].Attempt, failedEvents[1].WillRetry)
	}
}

func TestReplayer_StepWithRetryExhaustsAttempts(t *testing.T) {
	// Step always fails
	attempts := 0
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		attempts++
		return nil, errors.New("persistent error")
	}).WithRetry(&retry.Policy{
		MaxAttempts:  3,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
		Jitter:       0,
	})

	wf := Define("test-retry-fail", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-retry-exhaust",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}

	// Verify final step.failed has WillRetry=false
	var lastFailedData event.StepFailedData
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepFailed {
			if err := json.Unmarshal(e.Data, &lastFailedData); err != nil {
				t.Fatalf("Failed to unmarshal step.failed data: %v", err)
			}
		}
	}

	if lastFailedData.Attempt != 3 {
		t.Errorf("Final failure Attempt = %d, want 3", lastFailedData.Attempt)
	}
	if lastFailedData.WillRetry {
		t.Error("Final failure WillRetry should be false")
	}
}

func TestReplayer_StepWithNoRetryPolicy(t *testing.T) {
	// Step without retry fails immediately
	attempts := 0
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		attempts++
		return nil, errors.New("error")
	})
	// No WithRetry call

	wf := Define("test-no-retry", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-no-retry",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry)", attempts)
	}

	// Verify step.failed has WillRetry=false
	var failedData event.StepFailedData
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepFailed {
			if err := json.Unmarshal(e.Data, &failedData); err != nil {
				t.Fatalf("Failed to unmarshal step.failed data: %v", err)
			}
		}
	}

	if failedData.WillRetry {
		t.Error("WillRetry should be false for step without retry policy")
	}
}

func TestReplayer_StepStartedEventHasAttemptNumber(t *testing.T) {
	attempts := 0
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		attempts++
		if attempts < 2 {
			return nil, errors.New("temporary error")
		}
		return map[string]string{"ok": "true"}, nil
	}).WithRetry(&retry.Policy{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
		Jitter:       0,
	})

	wf := Define("test-started-attempt", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-started-attempt",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Collect step.started events
	var startedEvents []event.StepStartedData
	for _, e := range output.NewEvents {
		if e.Type == event.EventStepStarted {
			var data event.StepStartedData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				t.Fatalf("Failed to unmarshal step.started data: %v", err)
			}
			startedEvents = append(startedEvents, data)
		}
	}

	if len(startedEvents) != 2 {
		t.Fatalf("Expected 2 step.started events, got %d", len(startedEvents))
	}

	if startedEvents[0].Attempt != 1 {
		t.Errorf("First step.started Attempt = %d, want 1", startedEvents[0].Attempt)
	}
	if startedEvents[1].Attempt != 2 {
		t.Errorf("Second step.started Attempt = %d, want 2", startedEvents[1].Attempt)
	}
}

func TestReplayer_StepWithTimeout(t *testing.T) {
	// Step that takes too long
	executed := false
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		executed = true
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
			return map[string]string{"done": "true"}, nil
		}
	}).WithTimeout(10 * time.Millisecond)

	wf := Define("test-timeout", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-timeout",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if !executed {
		t.Fatal("Step was not executed")
	}

	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want ReplayFailed", output.Result)
	}

	// Should have context.DeadlineExceeded error
	if output.Error == nil {
		t.Error("Expected error for timeout")
	}
}

func TestReplayer_StepWithTimeoutAndRetry(t *testing.T) {
	// Step times out but retries and succeeds
	attempts := 0
	step1 := NewStep("step1", func(ctx Context) (map[string]string, error) {
		attempts++
		if attempts < 2 {
			// First attempt times out
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
				return nil, errors.New("should have timed out")
			}
		}
		// Second attempt succeeds immediately
		return map[string]string{"attempt": "2"}, nil
	}).WithTimeout(10 * time.Millisecond).WithRetry(&retry.Policy{
		MaxAttempts:  2,
		InitialDelay: 1 * time.Millisecond,
		Multiplier:   1.0,
		Jitter:       0,
	})

	wf := Define("test-timeout-retry", step1.After())

	r := NewReplayer(ReplayerConfig{
		Workflow: wf,
		RunID:    "run-timeout-retry",
		Input:    map[string]string{},
	})

	output, err := r.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want ReplayCompleted", output.Result)
	}

	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}
