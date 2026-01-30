# Phase 6: Branching (Conditional Execution)

## Goal
Implement conditional branching in workflows where different steps execute based on runtime decisions.

## Primary Design Document
- `docs/design/02-WORKFLOW-MODEL.md` (Conditional Branching section)

## Related Documents
- `docs/design/03-REPLAY.md` (Branch replay)
- `docs/design/01-EVENTS.md` (branch.evaluated event)
- `CLAUDE.md` (code quality standards)

---

## Types to Implement

### Branch (`workflow/branch.go`)

```go
// Branch represents a conditional execution point
type Branch struct {
    name     string
    selector func(Context) string
    cases    map[string]AnyStep
    default_ AnyStep
    deps     []AnyStep
}

// NewBranch creates a branch with a selector function
func NewBranch(name string, selector func(Context) string) *Branch

// Case adds a case to the branch
func (b *Branch) Case(value string, step AnyStep) *Branch

// Default sets the default case
func (b *Branch) Default(step AnyStep) *Branch

// After declares dependencies for the branch
func (b *Branch) After(deps ...AnyStep) ConfiguredStep

// Name returns the branch name (implements AnyStep)
func (b *Branch) Name() string
```

### Branch in Workflow Definition

```go
// Example usage:
var PaymentBranch = workflow.NewBranch("payment-method", func(ctx workflow.Context) string {
    v := Validate.Output(ctx)
    return v.PaymentMethod // "card" or "crypto"
}).
    Case("card", ChargeCard).
    Case("crypto", ChargeCrypto).
    Default(ChargeCard)

var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    PaymentBranch.After(Validate),
    Ship.After(PaymentBranch),
)
```

### Branch Output Access

```go
// BranchOutput retrieves the output from whichever branch was taken
func BranchOutput[T any](ctx Context, branch *Branch) (T, bool)
```

---

## File Deliverables

| File | Description |
|------|-------------|
| `workflow/branch.go` | Branch type and methods |
| `workflow/define.go` | Extended to handle Branch in DAG |
| `workflow/replay.go` | Extended with branch replay logic |

---

## Acceptance Criteria

### Branch Definition
- [ ] NewBranch creates branch with selector
- [ ] Case adds case to branch
- [ ] Default sets default case
- [ ] After declares branch dependencies
- [ ] Branch implements AnyStep

### Branch Execution
- [ ] Selector called with context containing prior outputs
- [ ] Selected case step is executed
- [ ] Default used if selector returns unknown value
- [ ] branch.evaluated event recorded with choice
- [ ] Subsequent steps can depend on branch

### Deterministic Replay
- [ ] On replay, use recorded choice (never re-evaluate selector)
- [ ] branch.evaluated event provides the choice
- [ ] Same events always produce same execution path

### Output Access
- [ ] BranchOutput returns output from taken branch
- [ ] BranchOutput returns (zero, false) if branch not taken yet
- [ ] Steps after branch can access branch output

### Tests
- [ ] Branch selects correct case
- [ ] Branch uses default for unknown value
- [ ] Branch replay uses recorded choice
- [ ] Step after branch accesses branch output
- [ ] Multiple branches in workflow
- [ ] `go build ./...` passes
- [ ] `go test -v -race ./...` passes

---

## Test Scenarios

### Branch Definition Tests
- Create branch with two cases
- Create branch with default
- Branch without default (error on unknown value)
- Branch with dependencies

### Branch Execution Tests
- Selector returns "card" -> ChargeCard executes
- Selector returns "crypto" -> ChargeCrypto executes
- Selector returns "unknown" -> Default executes
- Branch output accessible to subsequent steps

### Replay Tests
- First execution: selector called, choice recorded
- Replay: selector NOT called, choice from event
- Verify determinism (same choice on replay)

### Integration Tests
- Workflow with branch in middle
- Workflow with multiple branches
- Branch after parallel steps
- Parallel steps after branch

---

## Dependencies
- Phase 1: Events & Storage
- Phase 2: Replay Engine
- Phase 3: Typed Step Model

---

## Implementation Notes

### Branch in DAG
- Branch is a special node type
- Has dependencies like regular steps
- Selected case becomes the executed step
- Steps after branch depend on the branch (not the cases)

### Selector Context
- Selector receives context with all prior outputs
- Can read any completed step's output
- Should be deterministic (same inputs -> same choice)

### Replay Logic
```go
func (r *Replayer) replayBranch(ctx context.Context, branch *Branch) (any, error) {
    // Check for recorded choice
    if choice, ok := r.history.GetBranchChoice(branch.Name()); ok {
        // Replay: use recorded choice
        return r.executeStep(ctx, branch.cases[choice])
    }

    // First execution: evaluate selector
    wctx := r.buildContext(ctx)
    choice := branch.selector(wctx)

    // Record choice
    r.recordBranchEvaluated(branch.Name(), choice)

    // Execute selected case
    step := branch.cases[choice]
    if step == nil {
        step = branch.default_
    }
    if step == nil {
        return nil, fmt.Errorf("branch %s: no case for %q and no default", branch.Name(), choice)
    }

    return r.executeStep(ctx, step)
}
```

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors for unknown branch without default
- Table-driven tests
- Test deterministic replay thoroughly
