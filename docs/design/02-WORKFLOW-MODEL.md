# Feature: Workflow Model

## Overview

This document describes the core workflow model: how workflows are defined, how steps are declared, and how data flows between them.

---

## Core Concepts

### Workflows are Static DAGs

A workflow is a directed acyclic graph of steps with explicit dependencies. The structure is declared statically and can be visualized before execution.

```go
var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    Fraud.After(Validate),
    Charge.After(Validate, Fraud),
    Ship.After(Charge),
)
```

### Steps are Typed Handles

Steps are declared as typed handles that carry their output type. This enables compile-time checking of dependencies and type-safe output access.

```go
var Validate = Step[*ValidateOutput]("validate", validateOrder)
//          ↑ output type          ↑ name       ↑ function
```

### Functions Have Explicit I/O

Step functions receive a context and return their output. They read prior outputs explicitly via typed step handles.

```go
func chargePayment(ctx workflow.Context) (*ChargeOutput, error) {
    // Read from prior steps - explicit, type-safe
    v := Validate.Output(ctx)  // Returns *ValidateOutput
    f := Fraud.Output(ctx)     // Returns *FraudOutput

    // Do work
    txID, err := processPayment(v.Amount)
    if err != nil {
        return nil, err
    }

    // Return output - explicit, typed
    return &ChargeOutput{TransactionID: txID}, nil
}
```

---

## Step Declaration

### Basic Step

```go
var StepName = Step[OutputType]("name", function)
```

- `OutputType`: The type this step returns (must be serializable)
- `"name"`: String identifier for serialization and display
- `function`: The Go function to execute

### Step Function Signature

```go
func stepFunction(ctx workflow.Context) (*OutputType, error)
```

- Receives `workflow.Context` for accessing inputs and prior outputs
- Returns a pointer to the output type (or nil) and an error
- Should be a pure function of its inputs (for replay determinism)

### Reading Workflow Input

```go
func validateOrder(ctx workflow.Context) (*ValidateOutput, error) {
    input := workflow.Input[OrderInput](ctx)  // Original workflow input

    customer, err := getCustomer(input.CustomerID)
    // ...
}
```

### Reading Prior Step Outputs

```go
func shipOrder(ctx workflow.Context) (*ShipOutput, error) {
    // Read from any prior step - no pass-through needed
    v := Validate.Output(ctx)   // From step 1
    input := workflow.Input[OrderInput](ctx)  // Original input

    tracking, err := createShipment(v.Address, input.OrderID)
    // ...
}
```

---

## Workflow Definition

### Basic Definition

```go
var MyWorkflow = workflow.Define("workflow-name",
    StepA,
    StepB.After(StepA),
    StepC.After(StepA),
    StepD.After(StepB, StepC),
)
```

### Dependencies

- Steps without `.After()` can run immediately (or in parallel)
- `.After(step1, step2)` means "wait for both step1 and step2"
- Dependencies must be in the same workflow definition

### Implicit Parallelism

Steps without dependencies between them run in parallel:

```go
var Workflow = workflow.Define("parallel-example",
    FetchA,           // Runs immediately
    FetchB,           // Runs immediately (parallel with FetchA)
    FetchC,           // Runs immediately (parallel with A and B)
    Combine.After(FetchA, FetchB, FetchC),  // Waits for all three
)
```

### Conditional Branching

```go
var Workflow = workflow.Define("branching-example",
    Validate,
    Branch("payment-method").After(Validate).
        Case("card", ChargeCard).
        Case("crypto", ChargeCrypto).
        Default(ChargeCard),
    Ship.After("payment-method"),
)

// Selector function determines which branch
func (b Branch) Select(ctx workflow.Context) string {
    v := Validate.Output(ctx)
    return v.PaymentMethod  // "card" or "crypto"
}
```

---

## Child Workflows and Fan-Out

### Spawning a Child Workflow

```go
func parentStep(ctx workflow.Context) (*ParentOutput, error) {
    childInput := ChildInput{...}

    result, err := workflow.Run(ctx, ChildWorkflow, childInput)
    if err != nil {
        return nil, err
    }

    return &ParentOutput{ChildResult: result}, nil
}
```

### Map (Parallel Fan-Out)

Process a list of items, each as an independent child workflow:

```go
func processAllImages(ctx workflow.Context) (*ProcessOutput, error) {
    fetch := FetchImages.Output(ctx)

    // Each item spawns a child workflow - independently durable
    results, err := workflow.Map(ctx, fetch.Images, SingleImageWorkflow)
    if err != nil {
        return nil, err
    }

    return aggregateResults(results), nil
}
```

**Durability:** If the parent crashes after processing 50/100 items:
- Parent resumes at the Map step
- 50 completed children are skipped (events exist)
- Remaining 50 children are spawned

### Child Workflow Definition

```go
// Child workflow for processing a single image
var SingleImageWorkflow = workflow.Define("process-single-image",
    Analyze,
    Tag.After(Analyze),
    Resize,                    // Parallel with Tag
    Store.After(Tag, Resize),
)

func analyzeImage(ctx workflow.Context) (*AnalysisResult, error) {
    // workflow.Input gets input for THIS workflow (the child)
    img := workflow.Input[Image](ctx)

    result, err := visionService.Analyze(img.URL)
    // ...
}
```

---

## Visualization

Since workflow structure is static data, it can be visualized before execution:

```go
graph := OrderWorkflow.Graph()

// Returns structure that can render as:
//
// Validate ──┬──→ Charge ──→ Ship
// Fraud ─────┘
```

For workflows with child workflows:

```
┌─────────────────────────────────────────┐
│ image-processing                        │
├─────────────────────────────────────────┤
│ FetchImages ──→ ProcessAll              │
│                    │                    │
│                    └─→ [Map: 100 items] │
│                           │             │
│                           ▼             │
│                    ┌──────────────┐     │
│                    │ Child (each) │     │
│                    │ Analyze──→Tag│     │
│                    │       ↘   ↓ │     │
│                    │ Resize──→Store    │
│                    └──────────────┘     │
└─────────────────────────────────────────┘
```

---

## Testing

### Unit Testing a Step

```go
func TestChargePayment(t *testing.T) {
    ctx := workflow.TestContext().
        WithOutput(Validate, &ValidateOutput{Amount: 100}).
        WithOutput(Fraud, &FraudOutput{Score: 0.2})

    result, err := chargePayment(ctx)

    assert.NoError(t, err)
    assert.NotEmpty(t, result.TransactionID)
}
```

### Testing a Workflow

```go
func TestOrderWorkflow(t *testing.T) {
    // Use in-memory event store
    runner := workflow.TestRunner()

    result, err := runner.Run(OrderWorkflow, OrderInput{
        OrderID:    "order-123",
        CustomerID: "customer-456",
    })

    assert.NoError(t, err)
    assert.NotEmpty(t, result.TrackingNumber)
}
```

---

## Open Questions

### 1. Step Configuration (Retry, Timeout)

**Question:** How to configure retry policies and timeouts per step?

**Options:**

A) Methods on step handle:
```go
var Charge = Step[*ChargeOutput]("charge", chargePayment).
    WithRetry(3, time.Second).
    WithTimeout(30 * time.Second)
```

B) Separate configuration in workflow definition:
```go
var Workflow = workflow.Define("order",
    Charge.After(Validate).
        Retry(3, time.Second).
        Timeout(30 * time.Second),
)
```

C) Configuration struct:
```go
var Charge = Step[*ChargeOutput]("charge", chargePayment, StepConfig{
    MaxRetries: 3,
    Timeout:    30 * time.Second,
})
```

**Leaning toward:** A - methods on step handle keep it self-contained.

### 2. Error Handling and Compensation

**Question:** How to handle step failures and compensation (saga pattern)?

**Options:**

A) Compensation function on step:
```go
var Charge = Step[*ChargeOutput]("charge", chargePayment).
    OnFailure(refundPayment)
```

B) Workflow-level error handling:
```go
var Workflow = workflow.Define("order",
    Charge.After(Validate),
).OnStepFailure(func(ctx workflow.Context, step string, err error) {
    // Handle compensation
})
```

C) Try/catch-like blocks:
```go
var Workflow = workflow.Define("order",
    workflow.Try(
        Charge.After(Validate),
        Ship.After(Charge),
    ).Catch(func(ctx workflow.Context, err error) {
        // Compensation
    }),
)
```

**Leaning toward:** A for simple cases, C for complex multi-step compensation.

### 3. Accessing Outputs from Different Branches

**Question:** Can a step access output from a branch it didn't depend on?

```go
var Workflow = workflow.Define("example",
    Branch("choice").
        Case("a", StepA).
        Case("b", StepB),
    Final.After("choice"),
)

func finalStep(ctx workflow.Context) (*FinalOutput, error) {
    // Which output is available? StepA or StepB?
    // Should this be a union type? Optional?
}
```

**Options:**

A) Output returns nil if that branch wasn't taken
B) Require explicit union type
C) Separate outputs by branch, final step checks which exists

**Leaning toward:** A with clear documentation.

### 4. Step Handle Scope

**Question:** Should step handles be package-level vars or defined inline?

```go
// Option A: Package-level (current approach)
var Validate = Step[*ValidateOutput]("validate", validateOrder)

// Option B: Inline in workflow definition
var Workflow = workflow.Define("order",
    Step[*ValidateOutput]("validate", validateOrder),
    Step[*ChargeOutput]("charge", chargePayment).After(???), // How to reference?
)
```

**Decision:** Package-level vars. They provide:
- Clear reference points for `.After()` dependencies
- Reusability if same step used in multiple workflows
- IDE navigation

### 5. Workflow Input Type

**Question:** Should workflow input type be part of the definition?

```go
// Option A: Input type inferred from first run
var Workflow = workflow.Define("order", ...)

// Option B: Input type declared
var Workflow = workflow.Define[OrderInput]("order", ...)
```

**Leaning toward:** B - explicit input type enables validation and documentation.

---

## Summary

| Concept | Implementation |
|---------|----------------|
| Workflow structure | Static DAG via `workflow.Define()` |
| Step declaration | Typed handles: `Step[Output]("name", fn)` |
| Dependencies | Explicit: `StepB.After(StepA)` |
| Data flow | `StepA.Output(ctx)` returns typed output |
| Fan-out | `workflow.Map(ctx, items, ChildWorkflow)` |
| Visualization | Possible because structure is data |
| Testing | Mock context with `workflow.TestContext()` |
