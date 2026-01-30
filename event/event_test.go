package event

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEventType_String(t *testing.T) {
	tests := []struct {
		name     string
		et       EventType
		expected string
	}{
		// Workflow events
		{"workflow.started", EventWorkflowStarted, "workflow.started"},
		{"workflow.completed", EventWorkflowCompleted, "workflow.completed"},
		{"workflow.failed", EventWorkflowFailed, "workflow.failed"},
		{"workflow.cancelled", EventWorkflowCancelled, "workflow.cancelled"},
		// Step events
		{"step.started", EventStepStarted, "step.started"},
		{"step.completed", EventStepCompleted, "step.completed"},
		{"step.failed", EventStepFailed, "step.failed"},
		// Branch events
		{"branch.evaluated", EventBranchEvaluated, "branch.evaluated"},
		// Signal events
		{"signal.waiting", EventSignalWaiting, "signal.waiting"},
		{"signal.received", EventSignalReceived, "signal.received"},
		{"signal.timeout", EventSignalTimeout, "signal.timeout"},
		// Child events
		{"child.spawned", EventChildSpawned, "child.spawned"},
		{"child.completed", EventChildCompleted, "child.completed"},
		{"child.failed", EventChildFailed, "child.failed"},
		// Map events
		{"map.started", EventMapStarted, "map.started"},
		{"map.completed", EventMapCompleted, "map.completed"},
		{"map.failed", EventMapFailed, "map.failed"},
		// Compensation events
		{"compensation.started", EventCompensationStarted, "compensation.started"},
		{"compensation.completed", EventCompensationCompleted, "compensation.completed"},
		{"compensation.failed", EventCompensationFailed, "compensation.failed"},
		// Snapshot event
		{"snapshot", EventSnapshot, "snapshot"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.et) != tt.expected {
				t.Errorf("EventType = %q, expected %q", tt.et, tt.expected)
			}
		})
	}
}

func TestEvent_JSONRoundtrip(t *testing.T) {
	timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name  string
		event Event
	}{
		{
			name: "workflow started event",
			event: Event{
				ID:        "evt-001",
				RunID:     "run-123",
				Sequence:  1,
				Version:   1,
				Type:      EventWorkflowStarted,
				StepName:  "",
				Data:      json.RawMessage(`{"workflow_name":"order","version":1,"input":{"order_id":"abc"}}`),
				Output:    nil,
				Timestamp: timestamp,
				Metadata:  map[string]string{"trace_id": "trace-123"},
			},
		},
		{
			name: "step completed event with output",
			event: Event{
				ID:        "evt-002",
				RunID:     "run-123",
				Sequence:  5,
				Version:   1,
				Type:      EventStepCompleted,
				StepName:  "validate",
				Data:      json.RawMessage(`{"duration_ns":1500000}`),
				Output:    json.RawMessage(`{"valid":true,"amount":99.99}`),
				Timestamp: timestamp,
				Metadata:  nil,
			},
		},
		{
			name: "step failed event",
			event: Event{
				ID:        "evt-003",
				RunID:     "run-456",
				Sequence:  3,
				Version:   1,
				Type:      EventStepFailed,
				StepName:  "charge",
				Data:      json.RawMessage(`{"error":"insufficient funds","attempt":2,"will_retry":true}`),
				Output:    nil,
				Timestamp: timestamp,
				Metadata:  map[string]string{"user_id": "user-789"},
			},
		},
		{
			name: "event with empty metadata",
			event: Event{
				ID:        "evt-004",
				RunID:     "run-789",
				Sequence:  1,
				Version:   1,
				Type:      EventSnapshot,
				StepName:  "",
				Data:      json.RawMessage(`{"state":{}}`),
				Output:    nil,
				Timestamp: timestamp,
				Metadata:  map[string]string{},
			},
		},
		{
			name: "event with nil data fields",
			event: Event{
				ID:        "evt-005",
				RunID:     "run-999",
				Sequence:  1,
				Version:   1,
				Type:      EventWorkflowCancelled,
				StepName:  "",
				Data:      nil,
				Output:    nil,
				Timestamp: timestamp,
				Metadata:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.event)
			if err != nil {
				t.Fatalf("Failed to marshal event: %v", err)
			}

			// Unmarshal back to Event
			var decoded Event
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal event: %v", err)
			}

			// Compare fields
			if decoded.ID != tt.event.ID {
				t.Errorf("ID mismatch: got %q, want %q", decoded.ID, tt.event.ID)
			}
			if decoded.RunID != tt.event.RunID {
				t.Errorf("RunID mismatch: got %q, want %q", decoded.RunID, tt.event.RunID)
			}
			if decoded.Sequence != tt.event.Sequence {
				t.Errorf("Sequence mismatch: got %d, want %d", decoded.Sequence, tt.event.Sequence)
			}
			if decoded.Version != tt.event.Version {
				t.Errorf("Version mismatch: got %d, want %d", decoded.Version, tt.event.Version)
			}
			if decoded.Type != tt.event.Type {
				t.Errorf("Type mismatch: got %q, want %q", decoded.Type, tt.event.Type)
			}
			if decoded.StepName != tt.event.StepName {
				t.Errorf("StepName mismatch: got %q, want %q", decoded.StepName, tt.event.StepName)
			}
			if !decoded.Timestamp.Equal(tt.event.Timestamp) {
				t.Errorf("Timestamp mismatch: got %v, want %v", decoded.Timestamp, tt.event.Timestamp)
			}

			// Compare JSON raw messages
			if string(decoded.Data) != string(tt.event.Data) {
				t.Errorf("Data mismatch: got %q, want %q", decoded.Data, tt.event.Data)
			}
			if string(decoded.Output) != string(tt.event.Output) {
				t.Errorf("Output mismatch: got %q, want %q", decoded.Output, tt.event.Output)
			}

			// Compare metadata
			if len(decoded.Metadata) != len(tt.event.Metadata) {
				t.Errorf("Metadata length mismatch: got %d, want %d", len(decoded.Metadata), len(tt.event.Metadata))
			}
			for k, v := range tt.event.Metadata {
				if decoded.Metadata[k] != v {
					t.Errorf("Metadata[%q] mismatch: got %q, want %q", k, decoded.Metadata[k], v)
				}
			}
		})
	}
}

func TestEvent_JSONFieldNames(t *testing.T) {
	event := Event{
		ID:        "evt-001",
		RunID:     "run-123",
		Sequence:  1,
		Version:   1,
		Type:      EventStepCompleted,
		StepName:  "validate",
		Data:      json.RawMessage(`{}`),
		Output:    json.RawMessage(`{}`),
		Timestamp: time.Now(),
		Metadata:  map[string]string{"key": "value"},
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	// Unmarshal into a map to check field names
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	expectedFields := []string{"id", "run_id", "sequence", "version", "type", "step_name", "data", "output", "timestamp", "metadata"}
	for _, field := range expectedFields {
		if _, ok := fields[field]; !ok {
			t.Errorf("Expected JSON field %q not found", field)
		}
	}
}

func TestEvent_OmitEmptyFields(t *testing.T) {
	// Event with empty optional fields
	event := Event{
		ID:        "evt-001",
		RunID:     "run-123",
		Sequence:  1,
		Version:   1,
		Type:      EventWorkflowStarted,
		StepName:  "",
		Data:      nil,
		Output:    nil,
		Timestamp: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		Metadata:  nil,
	}

	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("Failed to marshal event: %v", err)
	}

	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("Failed to unmarshal to map: %v", err)
	}

	// step_name should be omitted when empty
	if _, ok := fields["step_name"]; ok {
		t.Errorf("Expected step_name to be omitted when empty")
	}

	// data should be omitted when nil
	if _, ok := fields["data"]; ok {
		t.Errorf("Expected data to be omitted when nil")
	}

	// output should be omitted when nil
	if _, ok := fields["output"]; ok {
		t.Errorf("Expected output to be omitted when nil")
	}

	// metadata should be omitted when nil
	if _, ok := fields["metadata"]; ok {
		t.Errorf("Expected metadata to be omitted when nil")
	}
}
