# Phase 9: Advanced Features

## Goal
Implement retry policies, saga pattern (compensation), and workflow versioning.

## Primary Design Documents
- `docs/design/05-SAGA.md` (Compensation)
- `docs/design/09-VERSIONING.md` (Versioning)

## Related Documents
- `docs/design/02-WORKFLOW-MODEL.md` (Step configuration)
- `docs/design/01-EVENTS.md` (compensation.* events)
- `CLAUDE.md` (code quality standards)

---

## Part A: Retry Policies

### Types to Implement (`retry/policy.go`)

```go
// Policy defines retry behavior for a step
type Policy struct {
    MaxAttempts int           // Maximum attempts (including first)
    InitialDelay time.Duration // Delay before first retry
    MaxDelay    time.Duration // Maximum delay
    Multiplier  float64       // Backoff multiplier
    Jitter      float64       // Random jitter factor (0-1)
}

// Default returns a sensible default policy
func Default() *Policy

// NoRetry returns a policy that doesn't retry
func NoRetry() *Policy

// NextDelay calculates the delay for the given attempt
func (p *Policy) NextDelay(attempt int) time.Duration

// ShouldRetry returns true if another attempt should be made
func (p *Policy) ShouldRetry(attempt int, err error) bool
```

### Step with Retry

```go
// WithRetry configures retry policy on a step
func (s Step[T]) WithRetry(policy *retry.Policy) Step[T]

// WithTimeout configures timeout on a step
func (s Step[T]) WithTimeout(timeout time.Duration) Step[T]
```

### Acceptance Criteria
- [ ] Default policy: 3 attempts, exponential backoff
- [ ] NextDelay calculates correct delay with jitter
- [ ] ShouldRetry respects MaxAttempts
- [ ] Step.WithRetry configures policy
- [ ] Replay respects retry policy
- [ ] step.failed events include attempt count

---

## Part B: Saga Pattern (Compensation)

### Types to Implement (`workflow/saga.go`)

```go
// WithCompensation adds a compensation function to a step
func (s Step[T]) WithCompensation(fn func(Context) error) Step[T]

// SagaError represents both original and compensation errors
type SagaError struct {
    OriginalError     error
    CompensationError error
}

func (e *SagaError) Error() string
func (e *SagaError) Unwrap() error
```

### Compensation in Workflow

```go
// Example:
var ChargePayment = workflow.NewStep("charge", chargePayment).
    WithCompensation(refundPayment)

// When Ship fails, ChargePayment.Compensate is called
```

### Compensation Execution

```go
// runCompensation executes compensation in reverse order
func (r *Replayer) runCompensation(ctx context.Context, failedStep string) error
```

### Acceptance Criteria
- [ ] WithCompensation adds compensation function
- [ ] On step failure, compensation runs in reverse order
- [ ] Only completed steps are compensated
- [ ] compensation.started event recorded
- [ ] compensation.completed/failed events recorded
- [ ] SagaError includes both errors
- [ ] Compensation continues on failure (best effort)

---

## Part C: Workflow Versioning

### Types to Implement (`workflow/version.go`)

```go
// WithVersion sets the workflow version
func (w *WorkflowDef) WithVersion(version string) *WorkflowDef

// Version returns the workflow version
func (w *WorkflowDef) Version() string
```

### Registry with Versions

```go
// Register adds a workflow (uses version from definition)
func (r *Registry) Register(def *WorkflowDef)

// Get retrieves latest version
func (r *Registry) Get(name string) (*WorkflowDef, error)

// GetVersion retrieves specific version
func (r *Registry) GetVersion(name, version string) (*WorkflowDef, error)

// SetLatest marks a version as latest
func (r *Registry) SetLatest(name, version string) error
```

### Version in Events

```go
// WorkflowStartedData includes version
type WorkflowStartedData struct {
    WorkflowName string          `json:"workflow_name"`
    Version      string          `json:"version"`
    Input        json.RawMessage `json:"input"`
}
```

### Acceptance Criteria
- [ ] Workflows can have versions
- [ ] Registry stores multiple versions
- [ ] New runs use latest version
- [ ] In-flight runs use their original version
- [ ] Version recorded in workflow.started event
- [ ] Jobs include version for lookup

---

## File Deliverables

| File | Description |
|------|-------------|
| `retry/policy.go` | Retry policy implementation |
| `workflow/step.go` | Extended with WithRetry, WithTimeout, WithCompensation |
| `workflow/saga.go` | Compensation execution |
| `workflow/version.go` | Versioning helpers |
| `river/registry.go` | Extended with version support |

---

## Test Scenarios

### Retry Tests
- Step fails, retries, succeeds
- Step fails all retries
- Exponential backoff timing
- Jitter within expected range
- NoRetry policy doesn't retry

### Compensation Tests
- Step fails, prior steps compensated
- Compensation in reverse order
- Compensation failure recorded, continues
- SagaError has both errors
- Replay skips completed compensation

### Versioning Tests
- Register multiple versions
- New run uses latest
- In-flight run uses original version
- GetVersion retrieves specific version
- SetLatest changes default

---

## Dependencies
- Phase 1-8 (all prior phases)

---

## Code Quality Reminders (from CLAUDE.md)
- Accept interfaces, return concrete types
- Return errors, don't panic
- Wrap errors with context
- Table-driven tests
