// Package query defines optional interfaces for extending EventStore implementations
// with dashboard-specific query capabilities.
//
// Following Rob Pike's principle: "The bigger the interface, the weaker the abstraction."
// Each interface has a single method, allowing stores to implement only what they need.
//
// These interfaces are OPTIONAL. Dashboard code should type-assert to check if a
// store implements the desired interface:
//
//	if counter, ok := store.(query.RunCounter); ok {
//	    total, err := counter.CountRuns(ctx, filter)
//	    // use total for pagination
//	}
//
// Stores that don't implement these interfaces can still be used with dashboards -
// the dashboard falls back to loading and counting events directly.
package query

import "context"

// RunFilter specifies criteria for querying workflow runs.
// All fields are optional; zero values mean "no filter".
type RunFilter struct {
	// WorkflowType filters by workflow name (e.g., "order-processing").
	WorkflowType string

	// Status filters by run status (e.g., "completed", "failed", "running").
	Status string

	// ParentOnly excludes child workflows (runs spawned by another workflow).
	ParentOnly bool

	// Limit caps the number of results (0 means no limit).
	Limit int

	// Offset skips the first N results (for pagination).
	Offset int
}

// RunCounter enables efficient counting of runs matching a filter.
// Implement this to support pagination totals without loading all events.
type RunCounter interface {
	// CountRuns returns the total number of runs matching the filter.
	// The Limit and Offset fields are ignored for counting.
	CountRuns(ctx context.Context, filter RunFilter) (int64, error)
}

// EntityQuerier enables finding workflow runs by application-defined entity.
// Entity correlation is stored in Event.Metadata with keys "entity_type" and "entity_id".
//
// Example: find all workflows for a specific user:
//
//	runIDs, err := querier.QueryByEntity(ctx, "user", "user-123")
type EntityQuerier interface {
	// QueryByEntity returns run IDs for workflows correlated to an entity.
	// Returns an empty slice if no workflows match.
	QueryByEntity(ctx context.Context, entityType, entityID string) ([]string, error)
}

// ChildQuerier enables finding child workflows spawned by a parent.
// This is derived from EventChildSpawned events in the parent's history.
type ChildQuerier interface {
	// QueryChildren returns run IDs of child workflows spawned by parentRunID.
	// Returns an empty slice if the parent has no children.
	QueryChildren(ctx context.Context, parentRunID string) ([]string, error)
}

// ParentQuerier enables finding the parent of a child workflow.
// This is the inverse of ChildQuerier.
type ParentQuerier interface {
	// QueryParent returns the run ID of the parent workflow.
	// Returns empty string if the run has no parent (is a root workflow).
	QueryParent(ctx context.Context, childRunID string) (string, error)
}
