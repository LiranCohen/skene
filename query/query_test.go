package query_test

import (
	"context"
	"testing"

	"github.com/lirancohen/skene/query"
)

// TestInterfaceCompliance verifies that the interfaces are well-defined
// and can be implemented. These are compile-time checks.
func TestInterfaceCompliance(t *testing.T) {
	// Verify interfaces can be type-asserted (compile-time check)
	var _ query.RunCounter = (*mockRunCounter)(nil)
	var _ query.EntityQuerier = (*mockEntityQuerier)(nil)
	var _ query.ChildQuerier = (*mockChildQuerier)(nil)
	var _ query.ParentQuerier = (*mockParentQuerier)(nil)
}

// Mock implementations for compile-time verification

type mockRunCounter struct{}

func (m *mockRunCounter) CountRuns(ctx context.Context, filter query.RunFilter) (int64, error) {
	return 0, nil
}

type mockEntityQuerier struct{}

func (m *mockEntityQuerier) QueryByEntity(ctx context.Context, entityType, entityID string) ([]string, error) {
	return nil, nil
}

type mockChildQuerier struct{}

func (m *mockChildQuerier) QueryChildren(ctx context.Context, parentRunID string) ([]string, error) {
	return nil, nil
}

type mockParentQuerier struct{}

func (m *mockParentQuerier) QueryParent(ctx context.Context, childRunID string) (string, error) {
	return "", nil
}

func TestRunFilterDefaults(t *testing.T) {
	tests := []struct {
		name   string
		filter query.RunFilter
		check  func(t *testing.T, f query.RunFilter)
	}{
		{
			name:   "empty filter",
			filter: query.RunFilter{},
			check: func(t *testing.T, f query.RunFilter) {
				if f.Limit != 0 {
					t.Errorf("expected Limit 0, got %d", f.Limit)
				}
				if f.Offset != 0 {
					t.Errorf("expected Offset 0, got %d", f.Offset)
				}
			},
		},
		{
			name: "filter with workflow type",
			filter: query.RunFilter{
				WorkflowType: "order-processing",
			},
			check: func(t *testing.T, f query.RunFilter) {
				if f.WorkflowType != "order-processing" {
					t.Errorf("expected WorkflowType 'order-processing', got %q", f.WorkflowType)
				}
			},
		},
		{
			name: "filter with status",
			filter: query.RunFilter{
				Status: "completed",
			},
			check: func(t *testing.T, f query.RunFilter) {
				if f.Status != "completed" {
					t.Errorf("expected Status 'completed', got %q", f.Status)
				}
			},
		},
		{
			name: "filter excludes children",
			filter: query.RunFilter{
				ParentOnly: true,
			},
			check: func(t *testing.T, f query.RunFilter) {
				if !f.ParentOnly {
					t.Error("expected ParentOnly true")
				}
			},
		},
		{
			name: "filter with pagination",
			filter: query.RunFilter{
				Limit:  25,
				Offset: 50,
			},
			check: func(t *testing.T, f query.RunFilter) {
				if f.Limit != 25 {
					t.Errorf("expected Limit 25, got %d", f.Limit)
				}
				if f.Offset != 50 {
					t.Errorf("expected Offset 50, got %d", f.Offset)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.filter)
		})
	}
}
