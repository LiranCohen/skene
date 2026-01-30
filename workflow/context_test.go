package workflow

import (
	"context"
	"encoding/json"
	"testing"
)

func TestParentRunID(t *testing.T) {
	tests := []struct {
		name         string
		ctx          Context
		wantParentID string
		wantOK       bool
	}{
		{
			name: "root workflow context returns empty and false",
			ctx: newWorkflowContext(
				context.Background(),
				"run-123",
				"test-workflow",
				json.RawMessage(`{}`),
				nil,
			),
			wantParentID: "",
			wantOK:       false,
		},
		{
			name: "child context returns parent run ID",
			ctx: newChildContext(
				context.Background(),
				"run-123-child-0",
				"child-workflow",
				json.RawMessage(`{}`),
				nil,
				"run-123",
			),
			wantParentID: "run-123",
			wantOK:       true,
		},
		{
			name: "map child context returns parent run ID",
			ctx: newMapChildContext(
				context.Background(),
				"run-123-map-0-5",
				"child-workflow",
				json.RawMessage(`{}`),
				nil,
				"run-123",
				5,
			),
			wantParentID: "run-123",
			wantOK:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotOK := ParentRunID(tt.ctx)
			if gotID != tt.wantParentID {
				t.Errorf("ParentRunID() id = %q, want %q", gotID, tt.wantParentID)
			}
			if gotOK != tt.wantOK {
				t.Errorf("ParentRunID() ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestChildIndex(t *testing.T) {
	tests := []struct {
		name      string
		ctx       Context
		wantIndex int
		wantOK    bool
	}{
		{
			name: "root workflow context returns -1 and false",
			ctx: newWorkflowContext(
				context.Background(),
				"run-123",
				"test-workflow",
				json.RawMessage(`{}`),
				nil,
			),
			wantIndex: -1,
			wantOK:    false,
		},
		{
			name: "Run child context returns -1 and false",
			ctx: newChildContext(
				context.Background(),
				"run-123-child-0",
				"child-workflow",
				json.RawMessage(`{}`),
				nil,
				"run-123",
			),
			wantIndex: -1,
			wantOK:    false,
		},
		{
			name: "Map child context returns index",
			ctx: newMapChildContext(
				context.Background(),
				"run-123-map-0-5",
				"child-workflow",
				json.RawMessage(`{}`),
				nil,
				"run-123",
				5,
			),
			wantIndex: 5,
			wantOK:    true,
		},
		{
			name: "Map child with index 0",
			ctx: newMapChildContext(
				context.Background(),
				"run-123-map-0-0",
				"child-workflow",
				json.RawMessage(`{}`),
				nil,
				"run-123",
				0,
			),
			wantIndex: 0,
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIndex, gotOK := ChildIndex(tt.ctx)
			if gotIndex != tt.wantIndex {
				t.Errorf("ChildIndex() index = %d, want %d", gotIndex, tt.wantIndex)
			}
			if gotOK != tt.wantOK {
				t.Errorf("ChildIndex() ok = %v, want %v", gotOK, tt.wantOK)
			}
		})
	}
}

func TestChildContext_WorkflowMethods(t *testing.T) {
	// Verify childContext properly delegates to workflowContext
	ctx := newMapChildContext(
		context.Background(),
		"run-123-map-0-5",
		"child-workflow",
		json.RawMessage(`{"key": "value"}`),
		nil,
		"run-123",
		5,
	)

	if ctx.RunID() != "run-123-map-0-5" {
		t.Errorf("RunID() = %q, want %q", ctx.RunID(), "run-123-map-0-5")
	}

	if ctx.WorkflowName() != "child-workflow" {
		t.Errorf("WorkflowName() = %q, want %q", ctx.WorkflowName(), "child-workflow")
	}

	input := ctx.getInput()
	if string(input) != `{"key": "value"}` {
		t.Errorf("getInput() = %q, want %q", string(input), `{"key": "value"}`)
	}
}

func TestChildContext_OutputAccessor(t *testing.T) {
	// Verify childContext supports output access
	history := NewHistory("run-123-map-0-5", nil)
	ctx := newMapChildContext(
		context.Background(),
		"run-123-map-0-5",
		"child-workflow",
		json.RawMessage(`{}`),
		history,
		"run-123",
		5,
	)

	// Output not found initially
	_, found := ctx.getOutput("step1")
	if found {
		t.Error("getOutput() should return false for missing step")
	}

	// Set output in cache
	ctx.setOutput("step1", map[string]string{"result": "ok"})

	// Output found after setting
	output, found := ctx.getOutput("step1")
	if !found {
		t.Error("getOutput() should return true after setOutput")
	}
	if output == nil {
		t.Error("getOutput() should return non-nil output")
	}
}
