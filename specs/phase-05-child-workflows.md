# Phase 5: Child Workflows & Map

## Goal
Implement child workflow spawning and workflow.Map for fan-out patterns.

## Primary Design Document
- `docs/design/02-WORKFLOW-MODEL.md` (Child Workflows and Fan-Out section)

## Related Documents
- `docs/design/00-ARCHITECTURE.md` (workflow.Run, workflow.Map)
- `docs/design/01-EVENTS.md` (child.*, map.* events)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Child Workflow Execution (`workflow/map.go`)

```go
// Run executes a child workflow synchronously
func Run[In, Out any](ctx Context, workflow *WorkflowDef, input In) (Out, error)

// Map executes a child workflow for each item (fan-out)
func Map[In, Out any](ctx Context, items []In, workflow *WorkflowDef) ([]Out, error)

// MapOptions configures map execution
type MapOptions struct {
    MaxConcurrency int // 0 = unlimited
}

// MapWithOptions executes map with configuration
func MapWithOptions[In, Out any](ctx Context, items []In, workflow *WorkflowDef, opts MapOptions) ([]Out, error)
```

### Child Workflow Context

```go
// childContext extends workflowContext for child workflows
type childContext struct {
    *workflowContext
    parentRunID string
    childIndex  int // for map children
}

// ParentRunID returns the parent workflow run ID (if child)
func ParentRunID(ctx Context) (string, bool)

// ChildIndex returns the index in a Map call (if map child)
func ChildIndex(ctx Context) (int, bool)
```

### Event Helpers

```go
// RecordChildSpawned appends a child.spawned event
func (r *Replayer) recordChildSpawned(childRunID, workflowName string, input json.RawMessage) event.Event

// RecordChildCompleted appends a child.completed event
func (r *Replayer) recordChildCompleted(childRunID string, output json.RawMessage) event.Event

// RecordChildFailed appends a child.failed event
func (r *Replayer) recordChildFailed(childRunID string, err error) event.Event

// RecordMapStarted appends a map.started event
func (r *Replayer) recordMapStarted(itemCount int) event.Event

// RecordMapCompleted appends a map.completed event
func (r *Replayer) recordMapCompleted(results json.RawMessage) event.Event
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/map.go` | Run, Map, MapWithOptions functions |
| `workflow/context.go` | Extended with child context support |

---

## Acceptance Criteria

### workflow.Run
- [ ] Spawns child workflow with own RunID
- [ ] Records child.spawned event
- [ ] Waits for child to complete
- [ ] Records child.completed or child.failed
- [ ] Returns typed output
- [ ] Replay skips completed children (loads output from events)

### workflow.Map
- [ ] Spawns child workflow for each item
- [ ] Records map.started with item count
- [ ] Children have unique RunIDs (parent-child-0, parent-child-1, etc.)
- [ ] Collects all results
- [ ] Records map.completed with results
- [ ] Handles partial failure (some children fail)
- [ ] Replay skips completed children

### Durability
- [ ] Parent crash after 50/100 children: resumes correctly
- [ ] Completed children not re-executed
- [ ] Remaining children spawned on resume

### Tests
- [ ] Single child workflow execution
- [ ] Child workflow failure propagates to parent
- [ ] Map with all successes
- [ ] Map with partial failure
- [ ] Map resume after crash (partial completion)
- [ ] Nested Map (child with Map inside)
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### Run Tests
- Parent calls Run with child workflow
- Child completes successfully
- Child fails, error propagates
- Replay with completed child (no re-execution)

### Map Tests
- Map over 3 items, all succeed
- Map over 3 items, one fails
- Map with MaxConcurrency limit
- Large map (100 items)

### Durability Tests
- Simulate crash after N children complete
- Resume and verify only remaining children execute
- Verify final results include all children

### Context Tests
- ParentRunID returns correct value in child
- ChildIndex returns correct value in map child
- ParentRunID returns false, "" in root workflow

---

## Dependencies
- Phase 1: Events & Storage
- Phase 2: Replay Engine
- Phase 3: Typed Step Model
- Phase 4: Parallel Execution

---

## Implementation Notes

### Child RunID Generation
```go
childRunID := fmt.Sprintf("%s-child-%d", parentRunID, index)
```

### Event Stream Separation
- Each child has its own event stream (different RunID)
- Parent events reference child by ChildRunID
- Parent waits for child completion

### Map Concurrency
- Use errgroup or semaphore for MaxConcurrency
- Collect results in order (not completion order)

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors, don't panic
- Wrap errors with context
- Table-driven tests
