package workflow

import (
	"context"
	"errors"
	"testing"

	"github.com/lirancohen/skene/event"
)

func TestSagaError(t *testing.T) {
	tests := []struct {
		name              string
		originalErr       error
		compensationErr   error
		wantErrorContains string
		wantUnwrap        error
	}{
		{
			name:              "original error only, compensation succeeded",
			originalErr:       errors.New("step failed"),
			compensationErr:   nil,
			wantErrorContains: "saga failed: step failed (compensation succeeded)",
			wantUnwrap:        errors.New("step failed"),
		},
		{
			name:              "both original and compensation errors",
			originalErr:       errors.New("step failed"),
			compensationErr:   errors.New("refund failed"),
			wantErrorContains: "saga failed: step failed (compensation also failed: refund failed)",
			wantUnwrap:        errors.New("step failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sagaErr := &SagaError{
				OriginalError:     tt.originalErr,
				CompensationError: tt.compensationErr,
			}

			// Test Error() method
			gotError := sagaErr.Error()
			if gotError != tt.wantErrorContains {
				t.Errorf("Error() = %q, want %q", gotError, tt.wantErrorContains)
			}

			// Test Unwrap() method
			unwrapped := sagaErr.Unwrap()
			if unwrapped.Error() != tt.wantUnwrap.Error() {
				t.Errorf("Unwrap() = %v, want %v", unwrapped, tt.wantUnwrap)
			}

			// Test errors.Is works through unwrap
			if !errors.Is(sagaErr, tt.originalErr) {
				t.Error("errors.Is should return true for original error")
			}
		})
	}
}

func TestRunCompensation(t *testing.T) {
	// Track compensation calls
	var compensationCalls []string

	// Create steps with compensation
	stepA := NewStep("step-a", func(ctx Context) (string, error) {
		return "a-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "a")
		return nil
	})

	stepB := NewStep("step-b", func(ctx Context) (string, error) {
		return "b-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "b")
		return nil
	})

	stepC := NewStep("step-c", func(ctx Context) (string, error) {
		return "c-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "c")
		return nil
	})

	// Step without compensation
	stepD := NewStep("step-d", func(ctx Context) (string, error) {
		return "d-result", nil
	})

	workflow := Define("test-saga",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepB),
		stepD.After(stepC),
	)

	t.Run("compensation runs in reverse order", func(t *testing.T) {
		compensationCalls = nil // reset

		replayer := NewReplayer(ReplayerConfig{
			Workflow: workflow,
			RunID:    "test-run-1",
		})

		// Simulate steps a, b, c completed (d failed)
		completedSteps := []string{"step-a", "step-b", "step-c"}

		result, err := replayer.runCompensation(context.Background(), "step-d", completedSteps)
		if err != nil {
			t.Fatalf("runCompensation returned error: %v", err)
		}

		// Compensation should run in reverse order: c, b, a
		expected := []string{"c", "b", "a"}
		if len(compensationCalls) != len(expected) {
			t.Errorf("compensation calls = %v, want %v", compensationCalls, expected)
		} else {
			for i, call := range compensationCalls {
				if call != expected[i] {
					t.Errorf("compensation call %d = %q, want %q", i, call, expected[i])
				}
			}
		}

		// Check result
		if len(result.StepsCompensated) != 3 {
			t.Errorf("StepsCompensated = %v, want 3 steps", result.StepsCompensated)
		}
		if result.Error != nil {
			t.Errorf("unexpected error: %v", result.Error)
		}
	})

	t.Run("only compensates steps with compensation functions", func(t *testing.T) {
		compensationCalls = nil // reset

		replayer := NewReplayer(ReplayerConfig{
			Workflow: workflow,
			RunID:    "test-run-2",
		})

		// Include step-d which has no compensation
		completedSteps := []string{"step-a", "step-b", "step-c", "step-d"}

		result, err := replayer.runCompensation(context.Background(), "unknown-step", completedSteps)
		if err != nil {
			t.Fatalf("runCompensation returned error: %v", err)
		}

		// Should only compensate a, b, c (not d)
		expected := []string{"c", "b", "a"}
		if len(compensationCalls) != len(expected) {
			t.Errorf("compensation calls = %v, want %v", compensationCalls, expected)
		}

		if len(result.StepsCompensated) != 3 {
			t.Errorf("StepsCompensated = %v, want 3 steps", result.StepsCompensated)
		}
	})

	t.Run("emits compensation events", func(t *testing.T) {
		compensationCalls = nil // reset

		replayer := NewReplayer(ReplayerConfig{
			Workflow: workflow,
			RunID:    "test-run-3",
		})

		completedSteps := []string{"step-a", "step-b"}

		_, err := replayer.runCompensation(context.Background(), "step-c", completedSteps)
		if err != nil {
			t.Fatalf("runCompensation returned error: %v", err)
		}

		// Check events
		events := replayer.newEvents
		if len(events) < 2 {
			t.Fatalf("expected at least 2 events, got %d", len(events))
		}

		// First event should be compensation.started
		if events[0].Type != event.EventCompensationStarted {
			t.Errorf("first event type = %v, want %v", events[0].Type, event.EventCompensationStarted)
		}

		// Last event should be compensation.completed
		if events[len(events)-1].Type != event.EventCompensationCompleted {
			t.Errorf("last event type = %v, want %v", events[len(events)-1].Type, event.EventCompensationCompleted)
		}
	})
}

func TestRunCompensationWithFailure(t *testing.T) {
	var compensationCalls []string
	compensationError := errors.New("refund failed")

	stepA := NewStep("step-a", func(ctx Context) (string, error) {
		return "a-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "a")
		return nil
	})

	stepB := NewStep("step-b", func(ctx Context) (string, error) {
		return "b-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "b")
		return compensationError
	})

	stepC := NewStep("step-c", func(ctx Context) (string, error) {
		return "c-result", nil
	}).WithCompensation(func(ctx Context) error {
		compensationCalls = append(compensationCalls, "c")
		return nil
	})

	workflow := Define("test-saga-fail",
		stepA.After(),
		stepB.After(stepA),
		stepC.After(stepB),
	)

	t.Run("continues compensation on failure (best effort)", func(t *testing.T) {
		compensationCalls = nil // reset

		replayer := NewReplayer(ReplayerConfig{
			Workflow: workflow,
			RunID:    "test-run-fail",
		})

		completedSteps := []string{"step-a", "step-b", "step-c"}

		result, err := replayer.runCompensation(context.Background(), "unknown-step", completedSteps)
		if err != nil {
			t.Fatalf("runCompensation returned error: %v", err)
		}

		// All compensations should be attempted (c, b, a)
		expected := []string{"c", "b", "a"}
		if len(compensationCalls) != len(expected) {
			t.Errorf("compensation calls = %v, want %v", compensationCalls, expected)
		}

		// Result should indicate failure at step-b
		if result.FailedStep != "step-b" {
			t.Errorf("FailedStep = %q, want %q", result.FailedStep, "step-b")
		}

		if result.Error == nil {
			t.Error("expected error in result")
		}

		// Check that a and c were compensated successfully
		if len(result.StepsCompensated) != 2 {
			t.Errorf("StepsCompensated = %v, want 2 steps (a and c)", result.StepsCompensated)
		}
	})

	t.Run("emits compensation.failed event", func(t *testing.T) {
		compensationCalls = nil // reset

		replayer := NewReplayer(ReplayerConfig{
			Workflow: workflow,
			RunID:    "test-run-fail-events",
		})

		completedSteps := []string{"step-a", "step-b", "step-c"}

		_, err := replayer.runCompensation(context.Background(), "unknown-step", completedSteps)
		if err != nil {
			t.Fatalf("runCompensation returned error: %v", err)
		}

		// Check for compensation.failed event
		var foundFailed bool
		for _, evt := range replayer.newEvents {
			if evt.Type == event.EventCompensationFailed {
				foundFailed = true
				break
			}
		}

		if !foundFailed {
			t.Error("expected compensation.failed event")
		}
	})
}

func TestRunCompensationNoSteps(t *testing.T) {
	stepA := NewStep("step-a", func(ctx Context) (string, error) {
		return "a-result", nil
	})
	// No compensation function

	workflow := Define("test-no-compensation", stepA.After())

	replayer := NewReplayer(ReplayerConfig{
		Workflow: workflow,
		RunID:    "test-no-comp",
	})

	result, err := replayer.runCompensation(context.Background(), "step-a", []string{"step-a"})
	if err != nil {
		t.Fatalf("runCompensation returned error: %v", err)
	}

	if len(result.StepsCompensated) != 0 {
		t.Errorf("StepsCompensated = %v, want empty", result.StepsCompensated)
	}

	// No events should be emitted
	if len(replayer.newEvents) != 0 {
		t.Errorf("expected no events, got %d", len(replayer.newEvents))
	}
}
