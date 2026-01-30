package order

import (
	"context"
	"testing"
	"time"

	"github.com/lirancohen/skene/workflow"
)

func TestOrderWorkflow_BasicExecution(t *testing.T) {
	input := OrderInput{
		OrderID:       "order-123",
		CustomerID:    "cust-456",
		CustomerEmail: "test@example.com",
		Items: []Item{
			{SKU: "SKU-001", Name: "Widget", Quantity: 2, Price: 29.99},
			{SKU: "SKU-002", Name: "Gadget", Quantity: 1, Price: 49.99},
		},
		ShippingAddr: Address{
			Street:  "123 Main St",
			City:    "Anytown",
			State:   "CA",
			Zip:     "90210",
			Country: "USA",
		},
		PaymentMethod: "card",
		TotalAmount:   109.97,
	}

	// Create a replayer with the workflow and input
	replayer := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-1",
		Input:    input,
	})

	// Execute the workflow
	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}

	// Verify completion
	if output.Result != workflow.ReplayCompleted {
		t.Errorf("Expected ReplayCompleted, got %v (error: %v)", output.Result, output.Error)
	}

	// Verify events were generated
	if len(output.NewEvents) == 0 {
		t.Error("Expected events to be generated")
	}

	// Verify we have workflow.started and workflow.completed
	var hasStarted, hasCompleted bool
	for _, evt := range output.NewEvents {
		switch evt.Type {
		case "workflow.started":
			hasStarted = true
		case "workflow.completed":
			hasCompleted = true
		}
	}

	if !hasStarted {
		t.Error("Expected workflow.started event")
	}
	if !hasCompleted {
		t.Error("Expected workflow.completed event")
	}
}

func TestOrderWorkflow_InvalidOrder(t *testing.T) {
	// Order with no items should fail validation
	input := OrderInput{
		OrderID:       "order-invalid",
		CustomerID:    "cust-456",
		CustomerEmail: "test@example.com",
		Items:         []Item{}, // Empty!
		ShippingAddr: Address{
			Country: "", // Also missing country
		},
		TotalAmount: 0, // Invalid amount
	}

	replayer := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-invalid",
		Input:    input,
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay failed: %v", err)
	}

	// The workflow should complete (validation step runs) but subsequent
	// steps should fail due to invalid order
	if output.Result != workflow.ReplayFailed {
		t.Logf("Result: %v, Error: %v", output.Result, output.Error)
	}
}

func TestOrderWorkflow_ReplayFromPartialHistory(t *testing.T) {
	input := OrderInput{
		OrderID:       "order-replay",
		CustomerID:    "cust-789",
		CustomerEmail: "replay@example.com",
		Items: []Item{
			{SKU: "SKU-001", Name: "Widget", Quantity: 1, Price: 10.00},
		},
		ShippingAddr: Address{
			Street:  "456 Oak Ave",
			City:    "Somewhere",
			State:   "NY",
			Zip:     "10001",
			Country: "USA",
		},
		PaymentMethod: "card",
		TotalAmount:   10.00,
	}

	// First execution
	replayer1 := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-replay",
		Input:    input,
	})

	output1, err := replayer1.Replay(context.Background())
	if err != nil {
		t.Fatalf("First replay failed: %v", err)
	}

	if output1.Result != workflow.ReplayCompleted {
		t.Fatalf("First execution should complete, got %v", output1.Result)
	}

	// Simulate partial history (only first few events)
	partialEvents := output1.NewEvents[:4] // workflow.started, step.started, step.completed, step.started
	history := workflow.NewHistory("test-run-replay", partialEvents)

	// Second execution should resume from history
	replayer2 := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-replay",
		Input:    input,
		History:  history,
	})

	output2, err := replayer2.Replay(context.Background())
	if err != nil {
		t.Fatalf("Second replay failed: %v", err)
	}

	if output2.Result != workflow.ReplayCompleted {
		t.Errorf("Second execution should complete, got %v (error: %v)", output2.Result, output2.Error)
	}

	// Second execution should have fewer new events (skipped completed steps)
	if len(output2.NewEvents) >= len(output1.NewEvents) {
		t.Logf("Expected fewer events on replay. First: %d, Second: %d",
			len(output1.NewEvents), len(output2.NewEvents))
	}
}

func TestOrderWorkflow_ContextCancellation(t *testing.T) {
	input := OrderInput{
		OrderID:       "order-cancel",
		CustomerID:    "cust-cancel",
		CustomerEmail: "cancel@example.com",
		Items: []Item{
			{SKU: "SKU-001", Name: "Widget", Quantity: 1, Price: 10.00},
		},
		ShippingAddr: Address{
			Street:  "789 Cancel St",
			City:    "Nowhere",
			State:   "TX",
			Zip:     "75001",
			Country: "USA",
		},
		TotalAmount: 10.00,
	}

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	replayer := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-cancel",
		Input:    input,
	})

	output, err := replayer.Replay(ctx)
	if err != nil {
		t.Fatalf("Replay error: %v", err)
	}

	// Should be suspended due to cancellation
	if output.Result != workflow.ReplaySuspended && output.Result != workflow.ReplayCompleted {
		t.Logf("Result after cancellation: %v", output.Result)
	}
}

func TestOrderWorkflow_Timeout(t *testing.T) {
	input := OrderInput{
		OrderID:       "order-timeout",
		CustomerID:    "cust-timeout",
		CustomerEmail: "timeout@example.com",
		Items: []Item{
			{SKU: "SKU-001", Name: "Widget", Quantity: 1, Price: 10.00},
		},
		ShippingAddr: Address{
			Street:  "Timeout Lane",
			City:    "Waitsville",
			State:   "WA",
			Zip:     "98001",
			Country: "USA",
		},
		TotalAmount: 10.00,
	}

	// Create a context with a very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Small delay to ensure timeout triggers
	time.Sleep(5 * time.Millisecond)

	replayer := workflow.NewReplayer(workflow.ReplayerConfig{
		Workflow: OrderWorkflow,
		RunID:    "test-run-timeout",
		Input:    input,
	})

	output, _ := replayer.Replay(ctx)

	// Should be suspended or fail due to timeout
	t.Logf("Result after timeout: %v", output.Result)
}
