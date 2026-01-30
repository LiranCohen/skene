package event

import (
	"errors"
	"testing"
)

func TestSequenceConflictError(t *testing.T) {
	tests := []struct {
		name     string
		err      *SequenceConflictError
		contains string
	}{
		{
			name: "basic error message",
			err: &SequenceConflictError{
				RunID:    "run-123",
				Expected: 5,
				Actual:   3,
			},
			contains: "sequence conflict for run run-123: expected 5, got 3",
		},
		{
			name: "first event",
			err: &SequenceConflictError{
				RunID:    "run-abc",
				Expected: 1,
				Actual:   2,
			},
			contains: "sequence conflict for run run-abc: expected 1, got 2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if msg != tt.contains {
				t.Errorf("Error message = %q, want %q", msg, tt.contains)
			}
		})
	}
}

func TestSequenceConflictError_Unwrap(t *testing.T) {
	err := &SequenceConflictError{
		RunID:    "run-123",
		Expected: 5,
		Actual:   3,
	}

	// Should unwrap to ErrSequenceConflict
	if !errors.Is(err, ErrSequenceConflict) {
		t.Errorf("SequenceConflictError should unwrap to ErrSequenceConflict")
	}

	// Should not match ErrDuplicateEvent
	if errors.Is(err, ErrDuplicateEvent) {
		t.Errorf("SequenceConflictError should not match ErrDuplicateEvent")
	}
}

func TestSentinelErrors(t *testing.T) {
	// Verify sentinel errors are distinct
	if errors.Is(ErrSequenceConflict, ErrDuplicateEvent) {
		t.Errorf("ErrSequenceConflict should not match ErrDuplicateEvent")
	}

	// Verify error messages
	if ErrSequenceConflict.Error() != "sequence conflict" {
		t.Errorf("ErrSequenceConflict message = %q, want %q", ErrSequenceConflict.Error(), "sequence conflict")
	}

	if ErrDuplicateEvent.Error() != "duplicate event ID" {
		t.Errorf("ErrDuplicateEvent message = %q, want %q", ErrDuplicateEvent.Error(), "duplicate event ID")
	}
}
