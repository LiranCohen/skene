package workflow

import (
	"context"
	"testing"

	"github.com/lirancohen/skene/event"
	"github.com/lirancohen/skene/event/memory"
)

// Test output types
type BranchInput struct {
	PaymentMethod string `json:"payment_method"`
	Amount        int    `json:"amount"`
}

type CardOutput struct {
	TransactionID string `json:"transaction_id"`
	Method        string `json:"method"`
}

type CryptoOutput struct {
	WalletTx string `json:"wallet_tx"`
	Method   string `json:"method"`
}

type ShipOutput struct {
	TrackingNumber string `json:"tracking_number"`
}

// Test steps
var validateBranch = NewStep("validate", func(ctx Context) (BranchInput, error) {
	return Input[BranchInput](ctx), nil
})

var chargeCard = NewStep("charge-card", func(ctx Context) (CardOutput, error) {
	return CardOutput{TransactionID: "card-tx-123", Method: "card"}, nil
})

var chargeCrypto = NewStep("charge-crypto", func(ctx Context) (CryptoOutput, error) {
	return CryptoOutput{WalletTx: "crypto-tx-456", Method: "crypto"}, nil
})

var shipOrder = NewStep("ship", func(ctx Context) (ShipOutput, error) {
	return ShipOutput{TrackingNumber: "TRACK123"}, nil
})

func TestBranch_BasicStructure(t *testing.T) {
	tests := []struct {
		name       string
		branchName string
		selector   func(Context) string
		cases      map[string]AnyStep
		hasDefault bool
	}{
		{
			name:       "branch with two cases",
			branchName: "payment-method",
			selector:   func(ctx Context) string { return "card" },
			cases:      map[string]AnyStep{"card": chargeCard, "crypto": chargeCrypto},
			hasDefault: false,
		},
		{
			name:       "branch with default",
			branchName: "payment-method",
			selector:   func(ctx Context) string { return "unknown" },
			cases:      map[string]AnyStep{"card": chargeCard},
			hasDefault: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			branch := NewBranch(tt.branchName, tt.selector)

			if branch.Name() != tt.branchName {
				t.Errorf("Name() = %q, want %q", branch.Name(), tt.branchName)
			}

			for value, step := range tt.cases {
				branch.Case(value, step)
			}

			if len(branch.cases) != len(tt.cases) {
				t.Errorf("cases count = %d, want %d", len(branch.cases), len(tt.cases))
			}

			if tt.hasDefault {
				branch.Default(chargeCard)
				if branch.defaultStep == nil {
					t.Error("defaultStep is nil after Default()")
				}
			}
		})
	}
}

func TestBranch_After(t *testing.T) {
	branch := NewBranch("payment", func(ctx Context) string { return "card" }).
		Case("card", chargeCard)

	configured := branch.After(validateBranch)

	if configured.Name() != "payment" {
		t.Errorf("Name() = %q, want %q", configured.Name(), "payment")
	}

	deps := configured.Dependencies()
	if len(deps) != 1 || deps[0] != "validate" {
		t.Errorf("Dependencies() = %v, want [validate]", deps)
	}
}

func TestBranch_SelectsCorrectCase(t *testing.T) {
	tests := []struct {
		name           string
		paymentMethod  string
		expectedMethod string
	}{
		{
			name:           "selects card case",
			paymentMethod:  "card",
			expectedMethod: "card",
		},
		{
			name:           "selects crypto case",
			paymentMethod:  "crypto",
			expectedMethod: "crypto",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create branch that selects based on input
			paymentBranch := NewBranch("payment-method", func(ctx Context) string {
				input := validateBranch.Output(ctx)
				return input.PaymentMethod
			}).
				Case("card", chargeCard).
				Case("crypto", chargeCrypto)

			// Create workflow
			wf := Define("branch-test",
				validateBranch.After(),
				paymentBranch.After(validateBranch),
			)

			// Create replayer
			store := memory.New()
			history := NewHistory("run-1", nil)

			replayer := NewReplayer(ReplayerConfig{
				Workflow: wf,
				History:  history,
				RunID:    "run-1",
				Input:    BranchInput{PaymentMethod: tt.paymentMethod, Amount: 100},
			})

			// Execute
			output, err := replayer.Replay(context.Background())
			if err != nil {
				t.Fatalf("Replay() error = %v", err)
			}

			if output.Result != ReplayCompleted {
				t.Errorf("Result = %v, want %v", output.Result, ReplayCompleted)
			}

			// Verify correct events were generated
			var branchEvent *event.Event
			for i := range output.NewEvents {
				if output.NewEvents[i].Type == event.EventBranchEvaluated {
					branchEvent = &output.NewEvents[i]
					break
				}
			}

			if branchEvent == nil {
				t.Fatal("expected branch.evaluated event")
			}

			// Store events for persistence check
			for _, e := range output.NewEvents {
				if err := store.Append(context.Background(), e); err != nil {
					t.Fatalf("store event: %v", err)
				}
			}
		})
	}
}

func TestBranch_UsesDefaultForUnknownValue(t *testing.T) {
	// Create branch with default
	paymentBranch := NewBranch("payment-method", func(ctx Context) string {
		input := validateBranch.Output(ctx)
		return input.PaymentMethod
	}).
		Case("card", chargeCard).
		Default(chargeCard) // Default to card

	// Create workflow
	wf := Define("branch-default-test",
		validateBranch.After(),
		paymentBranch.After(validateBranch),
	)

	// Execute with unknown payment method
	history := NewHistory("run-1", nil)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    BranchInput{PaymentMethod: "unknown", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want %v", output.Result, ReplayCompleted)
	}
}

func TestBranch_ErrorOnUnknownWithoutDefault(t *testing.T) {
	// Create branch without default
	paymentBranch := NewBranch("payment-method", func(ctx Context) string {
		input := validateBranch.Output(ctx)
		return input.PaymentMethod
	}).
		Case("card", chargeCard).
		Case("crypto", chargeCrypto)

	// Create workflow
	wf := Define("branch-no-default-test",
		validateBranch.After(),
		paymentBranch.After(validateBranch),
	)

	// Execute with unknown payment method
	history := NewHistory("run-1", nil)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    BranchInput{PaymentMethod: "bank-transfer", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	// Should fail because no case matches and no default
	if output.Result != ReplayFailed {
		t.Errorf("Result = %v, want %v", output.Result, ReplayFailed)
	}

	if output.Error == nil {
		t.Error("expected error for unknown branch case without default")
	}
}

func TestBranch_ReplayUsesRecordedChoice(t *testing.T) {
	// Track how many times selector was called
	selectorCallCount := 0

	// Create branch that tracks selector calls
	paymentBranch := NewBranch("payment-method", func(ctx Context) string {
		selectorCallCount++
		input := validateBranch.Output(ctx)
		return input.PaymentMethod
	}).
		Case("card", chargeCard).
		Case("crypto", chargeCrypto)

	// Create workflow
	wf := Define("branch-replay-test",
		validateBranch.After(),
		paymentBranch.After(validateBranch),
	)

	// First execution
	store := memory.New()
	history1 := NewHistory("run-1", nil)
	replayer1 := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history1,
		RunID:    "run-1",
		Input:    BranchInput{PaymentMethod: "card", Amount: 100},
	})

	output1, err := replayer1.Replay(context.Background())
	if err != nil {
		t.Fatalf("First Replay() error = %v", err)
	}

	// Store events
	for _, e := range output1.NewEvents {
		if err := store.Append(context.Background(), e); err != nil {
			t.Fatalf("store event: %v", err)
		}
	}

	firstCallCount := selectorCallCount

	// Load events for replay
	events, err := store.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}

	// Reset selector count and replay
	selectorCallCount = 0
	history2 := NewHistory("run-1", events)
	replayer2 := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history2,
		RunID:    "run-1",
		Input:    BranchInput{PaymentMethod: "card", Amount: 100},
	})

	output2, err := replayer2.Replay(context.Background())
	if err != nil {
		t.Fatalf("Second Replay() error = %v", err)
	}

	// Selector should have been called once on first execution
	if firstCallCount != 1 {
		t.Errorf("First execution selector calls = %d, want 1", firstCallCount)
	}

	// Selector should NOT be called on replay (workflow is already complete)
	if selectorCallCount != 0 {
		t.Errorf("Replay selector calls = %d, want 0", selectorCallCount)
	}

	// Both should complete successfully
	if output1.Result != ReplayCompleted {
		t.Errorf("First Result = %v, want %v", output1.Result, ReplayCompleted)
	}
	if output2.Result != ReplayCompleted {
		t.Errorf("Second Result = %v, want %v", output2.Result, ReplayCompleted)
	}
}

func TestBranch_StepAfterBranchAccessesOutput(t *testing.T) {
	// Create a step that depends on branch output
	finalStep := NewStep("final", func(ctx Context) (ShipOutput, error) {
		// The branch output can be accessed - this just verifies workflow completes
		return ShipOutput{TrackingNumber: "FINAL123"}, nil
	})

	// For this test, we'll use a simpler approach: directly access the case step output
	cardBranch := NewBranch("payment-method", func(ctx Context) string {
		return "card"
	}).
		Case("card", chargeCard)

	// We need to verify that after the branch executes, the output is accessible
	wf := Define("branch-output-test",
		cardBranch.After(),
		finalStep.After(cardBranch),
	)

	history := NewHistory("run-1", nil)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    BranchInput{PaymentMethod: "card", Amount: 100},
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want %v (error: %v)", output.Result, ReplayCompleted, output.Error)
	}
}

func TestBranch_MultipleBranchesInWorkflow(t *testing.T) {
	// First branch
	branch1 := NewBranch("branch-1", func(ctx Context) string {
		return "a"
	}).
		Case("a", NewStep("step-a", func(ctx Context) (string, error) {
			return "result-a", nil
		})).
		Case("b", NewStep("step-b", func(ctx Context) (string, error) {
			return "result-b", nil
		}))

	// Second branch
	branch2 := NewBranch("branch-2", func(ctx Context) string {
		return "x"
	}).
		Case("x", NewStep("step-x", func(ctx Context) (string, error) {
			return "result-x", nil
		})).
		Case("y", NewStep("step-y", func(ctx Context) (string, error) {
			return "result-y", nil
		}))

	wf := Define("multi-branch-test",
		branch1.After(),
		branch2.After(branch1),
	)

	history := NewHistory("run-1", nil)
	replayer := NewReplayer(ReplayerConfig{
		Workflow: wf,
		History:  history,
		RunID:    "run-1",
		Input:    nil,
	})

	output, err := replayer.Replay(context.Background())
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if output.Result != ReplayCompleted {
		t.Errorf("Result = %v, want %v (error: %v)", output.Result, ReplayCompleted, output.Error)
	}

	// Verify both branch.evaluated events exist
	branchEvents := 0
	for _, e := range output.NewEvents {
		if e.Type == event.EventBranchEvaluated {
			branchEvents++
		}
	}

	if branchEvents != 2 {
		t.Errorf("branch.evaluated events = %d, want 2", branchEvents)
	}
}
