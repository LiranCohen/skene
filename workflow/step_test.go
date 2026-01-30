package workflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lirancohen/skene/retry"
)

func TestNewStep(t *testing.T) {
	type Output struct {
		Value int `json:"value"`
	}

	tests := []struct {
		name     string
		stepName string
		wantName string
	}{
		{
			name:     "simple name",
			stepName: "validate",
			wantName: "validate",
		},
		{
			name:     "name with hyphens",
			stepName: "process-payment",
			wantName: "process-payment",
		},
		{
			name:     "name with dots",
			stepName: "api.call",
			wantName: "api.call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NewStep(tt.stepName, func(ctx Context) (Output, error) {
				return Output{Value: 42}, nil
			})

			if step.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", step.Name(), tt.wantName)
			}
		})
	}
}

func TestStep_After(t *testing.T) {
	type AOutput struct{ A string }
	type BOutput struct{ B string }
	type COutput struct{ C string }

	stepA := NewStep("stepA", func(ctx Context) (AOutput, error) {
		return AOutput{A: "a"}, nil
	})
	stepB := NewStep("stepB", func(ctx Context) (BOutput, error) {
		return BOutput{B: "b"}, nil
	})
	stepC := NewStep("stepC", func(ctx Context) (COutput, error) {
		return COutput{C: "c"}, nil
	})

	tests := []struct {
		name     string
		step     AnyStep
		deps     []AnyStep
		wantDeps []string
	}{
		{
			name:     "no dependencies",
			step:     stepA,
			deps:     nil,
			wantDeps: []string{},
		},
		{
			name:     "single dependency",
			step:     stepB,
			deps:     []AnyStep{stepA},
			wantDeps: []string{"stepA"},
		},
		{
			name:     "multiple dependencies",
			step:     stepC,
			deps:     []AnyStep{stepA, stepB},
			wantDeps: []string{"stepA", "stepB"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// For Step type, we need to call After
			switch s := tt.step.(type) {
			case Step[AOutput]:
				configured := s.After(tt.deps...)
				validateConfiguredStep(t, configured, tt.step.Name(), tt.wantDeps)
			case Step[BOutput]:
				configured := s.After(tt.deps...)
				validateConfiguredStep(t, configured, tt.step.Name(), tt.wantDeps)
			case Step[COutput]:
				configured := s.After(tt.deps...)
				validateConfiguredStep(t, configured, tt.step.Name(), tt.wantDeps)
			}
		})
	}
}

func validateConfiguredStep(t *testing.T, cs ConfiguredStep, wantName string, wantDeps []string) {
	t.Helper()

	if cs.Name() != wantName {
		t.Errorf("ConfiguredStep.Name() = %q, want %q", cs.Name(), wantName)
	}

	deps := cs.Dependencies()
	if len(deps) != len(wantDeps) {
		t.Errorf("ConfiguredStep.Dependencies() length = %d, want %d", len(deps), len(wantDeps))
		return
	}
	for i, dep := range deps {
		if dep != wantDeps[i] {
			t.Errorf("ConfiguredStep.Dependencies()[%d] = %q, want %q", i, dep, wantDeps[i])
		}
	}
}

func TestStep_Execute(t *testing.T) {
	type Input struct {
		OrderID string `json:"order_id"`
	}
	type Output struct {
		Validated bool   `json:"validated"`
		OrderID   string `json:"order_id"`
	}

	tests := []struct {
		name       string
		stepFn     func(ctx Context) (Output, error)
		input      Input
		wantOutput Output
		wantErr    bool
	}{
		{
			name: "successful execution",
			stepFn: func(ctx Context) (Output, error) {
				return Output{Validated: true, OrderID: "123"}, nil
			},
			input:      Input{OrderID: "123"},
			wantOutput: Output{Validated: true, OrderID: "123"},
			wantErr:    false,
		},
		{
			name: "execution with error",
			stepFn: func(ctx Context) (Output, error) {
				return Output{}, errStepFailed
			},
			input:   Input{OrderID: "456"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NewStep("test-step", tt.stepFn)

			// Create a test context
			inputJSON, _ := json.Marshal(tt.input)
			wctx := newWorkflowContext(
				context.Background(),
				"run-123",
				"test-workflow",
				inputJSON,
				nil,
			)

			result, err := step.Execute(context.Background(), wctx)

			if (err != nil) != tt.wantErr {
				t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				output, ok := result.(Output)
				if !ok {
					t.Fatalf("Execute() result type = %T, want Output", result)
				}
				if output != tt.wantOutput {
					t.Errorf("Execute() = %+v, want %+v", output, tt.wantOutput)
				}
			}
		})
	}
}

var errStepFailed = errTest("step execution failed")

type errTest string

func (e errTest) Error() string { return string(e) }

func TestStep_Output(t *testing.T) {
	type StepOutput struct {
		Value   int    `json:"value"`
		Message string `json:"message"`
	}

	step := NewStep("my-step", func(ctx Context) (StepOutput, error) {
		return StepOutput{Value: 42, Message: "hello"}, nil
	})

	tests := []struct {
		name        string
		setupOutput func(wctx *workflowContext)
		wantPanic   bool
		wantValue   StepOutput
	}{
		{
			name: "output from history",
			setupOutput: func(wctx *workflowContext) {
				// Add output to history via mock
				wctx.outputs["my-step"] = StepOutput{Value: 42, Message: "hello"}
			},
			wantPanic: false,
			wantValue: StepOutput{Value: 42, Message: "hello"},
		},
		{
			name: "output not available",
			setupOutput: func(wctx *workflowContext) {
				// Don't add any output
			},
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wctx := newWorkflowContext(
				context.Background(),
				"run-123",
				"test-workflow",
				nil,
				nil,
			)
			tt.setupOutput(wctx)

			if tt.wantPanic {
				defer func() {
					if r := recover(); r == nil {
						t.Error("Output() should panic when step not completed")
					}
				}()
				step.Output(wctx)
			} else {
				result := step.Output(wctx)
				if result != tt.wantValue {
					t.Errorf("Output() = %+v, want %+v", result, tt.wantValue)
				}
			}
		})
	}
}

func TestStep_Output_TypeMismatch(t *testing.T) {
	type ExpectedOutput struct {
		Value int `json:"value"`
	}
	type ActualOutput struct {
		Name string `json:"name"`
	}

	step := NewStep("my-step", func(ctx Context) (ExpectedOutput, error) {
		return ExpectedOutput{}, nil
	})

	// Create context with mismatched output type (wrong JSON structure)
	wctx := newWorkflowContext(
		context.Background(),
		"run-123",
		"test-workflow",
		nil,
		nil,
	)
	wctx.outputs["my-step"] = ActualOutput{Name: "not a number"}

	// This should still work as JSON unmarshaling is lenient
	// The Value field will be zero since Name doesn't map to Value
	result := step.Output(wctx)
	if result.Value != 0 {
		t.Errorf("Output().Value = %d, want 0", result.Value)
	}
}

func TestConfiguredStep_Name(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("configured-step", func(ctx Context) (Output, error) {
		return Output{Value: 1}, nil
	})

	configured := step.After()

	if configured.Name() != "configured-step" {
		t.Errorf("Name() = %q, want %q", configured.Name(), "configured-step")
	}
}

func TestConfiguredStep_Dependencies(t *testing.T) {
	type AOutput struct{ A int }
	type BOutput struct{ B int }
	type COutput struct{ C int }

	stepA := NewStep("a", func(ctx Context) (AOutput, error) { return AOutput{}, nil })
	stepB := NewStep("b", func(ctx Context) (BOutput, error) { return BOutput{}, nil })
	stepC := NewStep("c", func(ctx Context) (COutput, error) { return COutput{}, nil })

	configured := stepC.After(stepA, stepB)
	deps := configured.Dependencies()

	if len(deps) != 2 {
		t.Fatalf("Dependencies() length = %d, want 2", len(deps))
	}
	if deps[0] != "a" {
		t.Errorf("Dependencies()[0] = %q, want %q", deps[0], "a")
	}
	if deps[1] != "b" {
		t.Errorf("Dependencies()[1] = %q, want %q", deps[1], "b")
	}
}

func TestStep_ImplementsAnyStep(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("test", func(ctx Context) (Output, error) {
		return Output{}, nil
	})

	// Verify Step[T] implements AnyStep
	var _ AnyStep = step
}

func TestStep_WithRetry(t *testing.T) {
	type Output struct{ Value int }

	tests := []struct {
		name         string
		policy       *retry.Policy
		wantAttempts int
	}{
		{
			name:         "default policy",
			policy:       retry.Default(),
			wantAttempts: 3,
		},
		{
			name:         "no retry policy",
			policy:       retry.NoRetry(),
			wantAttempts: 1,
		},
		{
			name: "custom policy",
			policy: &retry.Policy{
				MaxAttempts:  5,
				InitialDelay: time.Second,
				Multiplier:   2.0,
			},
			wantAttempts: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NewStep("test", func(ctx Context) (Output, error) {
				return Output{Value: 1}, nil
			})

			configured := step.WithRetry(tt.policy)

			if configured.Config().RetryPolicy == nil {
				t.Fatal("RetryPolicy should not be nil")
			}
			if configured.Config().RetryPolicy.MaxAttempts != tt.wantAttempts {
				t.Errorf("MaxAttempts = %d, want %d",
					configured.Config().RetryPolicy.MaxAttempts, tt.wantAttempts)
			}
		})
	}
}

func TestStep_WithTimeout(t *testing.T) {
	type Output struct{ Value int }

	tests := []struct {
		name        string
		timeout     time.Duration
		wantTimeout time.Duration
	}{
		{
			name:        "30 second timeout",
			timeout:     30 * time.Second,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "zero timeout",
			timeout:     0,
			wantTimeout: 0,
		},
		{
			name:        "1 minute timeout",
			timeout:     time.Minute,
			wantTimeout: time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NewStep("test", func(ctx Context) (Output, error) {
				return Output{Value: 1}, nil
			})

			configured := step.WithTimeout(tt.timeout)

			if configured.Config().Timeout != tt.wantTimeout {
				t.Errorf("Timeout = %v, want %v",
					configured.Config().Timeout, tt.wantTimeout)
			}
		})
	}
}

func TestStep_WithCompensation(t *testing.T) {
	type Output struct{ Value int }

	tests := []struct {
		name           string
		compensation   func(Context) error
		expectCompFunc bool
	}{
		{
			name:           "with compensation function",
			compensation:   func(Context) error { return nil },
			expectCompFunc: true,
		},
		{
			name:           "nil compensation function",
			compensation:   nil,
			expectCompFunc: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step := NewStep("test", func(ctx Context) (Output, error) {
				return Output{Value: 1}, nil
			})

			configured := step.WithCompensation(tt.compensation)

			hasCompFunc := configured.Config().Compensation != nil
			if hasCompFunc != tt.expectCompFunc {
				t.Errorf("HasCompensation = %v, want %v", hasCompFunc, tt.expectCompFunc)
			}
		})
	}
}

func TestStep_ChainedConfiguration(t *testing.T) {
	type Output struct{ Value int }

	step := NewStep("test", func(ctx Context) (Output, error) {
		return Output{Value: 1}, nil
	})

	// Chain all configuration methods
	configured := step.
		WithRetry(retry.Default()).
		WithTimeout(30 * time.Second).
		WithCompensation(func(Context) error { return nil })

	// Verify all configurations are preserved
	config := configured.Config()

	if config.RetryPolicy == nil {
		t.Error("RetryPolicy should not be nil")
	}
	if config.RetryPolicy.MaxAttempts != 3 {
		t.Errorf("RetryPolicy.MaxAttempts = %d, want 3", config.RetryPolicy.MaxAttempts)
	}

	if config.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", config.Timeout)
	}

	if config.Compensation == nil {
		t.Error("Compensation should not be nil")
	}
}

func TestConfiguredStep_Config(t *testing.T) {
	type Output struct{ Value int }

	dep := NewStep("dep", func(ctx Context) (Output, error) {
		return Output{Value: 1}, nil
	})

	step := NewStep("test", func(ctx Context) (Output, error) {
		return Output{Value: 2}, nil
	}).
		WithRetry(retry.Default()).
		WithTimeout(time.Minute)

	// Create configured step with After
	configuredStep := step.After(dep)

	// Verify config is accessible through ConfiguredStep
	config := configuredStep.Config()

	if config.RetryPolicy == nil {
		t.Error("RetryPolicy should be accessible through ConfiguredStep")
	}
	if config.Timeout != time.Minute {
		t.Errorf("Timeout = %v, want 1m", config.Timeout)
	}
}
