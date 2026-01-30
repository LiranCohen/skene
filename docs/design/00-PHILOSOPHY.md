# Design Philosophy

## The Golden Rule

**When in doubt, ask: "How would Rob Pike design this in Go?"**

This means:
- **Simplicity over cleverness** - Boring, obvious code wins
- **Explicit over implicit** - No magic, no hidden behavior
- **Functions take what they need, return what they produce** - Clear contracts
- **Composition over configuration** - Small pieces that combine
- **If it doesn't feel like Go, it's probably wrong**

When evaluating any design decision, apply this test. If the answer requires explaining why Go's nature doesn't apply here, reconsider the approach.

---

## Overview

This document captures the core design principles and goals for the event-sourced Skene system. These principles guide decisions when trade-offs arise.

---

## Core Goals

### 1. Simplicity Over Features

The system should be simple to understand, use, and debug. Prefer fewer concepts that compose well over many specialized features.

**Implications:**
- One `Workflow[S]` interface, not separate Task/Workflow types
- State mutation model (familiar Go patterns) over explicit wiring
- Primitives that combine naturally (Scene+Chorus+Cue) over DAG-specific syntax

### 2. Code Navigability (IDE-First Design)

Developers should be able to click through code in their IDE to follow what's being called and how it's used. No magic strings, no runtime-only wiring, no indirection that breaks "Go to Definition".

**Implications:**
- Step functions are real Go functions - click to jump to implementation
- Workflow definitions are Go code - IDE understands the structure
- Type parameters enable autocomplete and type checking
- Avoid string-based registries where possible (use type identity)
- Prefer function references over string names for step identification

**Anti-patterns to avoid:**
```go
// BAD: String-based step reference (can't click through)
workflow.Step("validate-order")

// GOOD: Direct function reference (cmd+click works)
workflow.Step(ValidateOrder)

// BAD: Magic convention (IDE can't help)
// "charge" step automatically gets output from "validate" step

// GOOD: Explicit reference (IDE shows connection)
charge.After(validate)
```

This is why the current pattern uses function references in Act.Run and explicit struct composition - the IDE can trace the entire workflow.

### 3. Toolkit, Not Framework

Provide composable building blocks that users assemble, not a framework that controls their code.

**Implications:**
- Steps are functions with standard signatures
- Dependencies injected via `Deps`, not hidden globals
- Users own their workflow definitions, we execute them

### 4. Static Traceability

Workflows should be fully understandable by examining the definition. No runtime-only behavior that can't be predicted from the code.

**Implications:**
- Workflow structure is declarative (Scene, Chorus, Cue trees)
- Conditional logic lives in explicit Cue selectors, not hidden in steps
- Event sourcing captures all decisions for replay

### 5. Auditability

Every question about "what happened and why" should be answerable from the event log.

**Implications:**
- Events capture inputs, outputs, decisions, and timing
- Cue branch selection is recorded (not re-evaluated on replay)
- StateAfter enables point-in-time reconstruction

### 6. Composability

Any workflow can become a component of a larger workflow without modification.

**Implications:**
- Workflow primitives nest naturally
- State type is the composition boundary (parent and child can have different state types via adapters)
- Child workflows are just another step in a Scene

---

## Design Tensions

### State Mutation vs Explicit I/O

**Previous consideration:** Steps mutate shared state `*S`. Data flow is implicit.

```go
func chargePayment(ctx context.Context, state *OrderState, deps *Deps) error {
    // Implicitly reads state.Amount (from previous step)
    // Implicitly writes state.TransactionID
}
```

**Problems with state mutation:**
- Can't see what a step needs without reading the implementation
- Testing requires constructing full state even if step uses 2 fields
- "What was the input to this step?" requires diffing state snapshots
- Implicit coupling between steps through shared state shape

**Decision: Explicit I/O with typed step handles.**

```go
// Step declarations are typed handles
var (
    Validate = Step[*ValidateOutput]("validate", validateOrder)
    Charge   = Step[*ChargeOutput]("charge", chargePayment)
)

// Functions have explicit inputs (via context) and outputs (return type)
func chargePayment(ctx workflow.Context) (*ChargeOutput, error) {
    v := Validate.Output(ctx)  // Explicit: reads from Validate step

    txID, err := processPayment(v.Amount)
    if err != nil {
        return nil, err
    }

    return &ChargeOutput{TransactionID: txID}, nil  // Explicit: produces this
}
```

**Why this works:**
- Function return type documents what the step produces
- `Step.Output(ctx)` is explicit about what it reads - no hidden dependencies
- Typed handles mean compile-time checking, not runtime string errors
- No pass-through problem: any step can read any prior step's output directly
- Testing is trivial: mock the context with the outputs you need

### Task Definition: Typed Step Handles

**Previous consideration:** Steps as structs with function fields.

```go
Act[OrderState]{
    Name: "charge-payment",
    Run:  chargePayment,
}
```

**Decision: Steps as typed handles.**

```go
var Charge = Step[*ChargeOutput]("charge", chargePayment)
```

**Why:**
- The handle IS the reference - no strings in code paths
- Generic parameter `[*ChargeOutput]` documents the output type
- `Charge.After(Validate)` - dependencies are typed, not strings
- `Charge.Output(ctx)` - returns `*ChargeOutput`, compiler knows the type
- Click-through works: click `Charge` → goes to declaration
- Click `chargePayment` → goes to function

### Workflow Structure: Explicit DAG with Typed Dependencies

**Previous consideration:** Control flow via nested structures (Scene, Chorus, Cue).

**Decision: Explicit DAG with typed step dependencies.**

```go
var (
    Validate = Step[*ValidateOutput]("validate", validateOrder)
    Fraud    = Step[*FraudOutput]("fraud", checkFraud)
    Charge   = Step[*ChargeOutput]("charge", chargePayment)
    Ship     = Step[*ShipOutput]("ship", shipOrder)
)

var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    Fraud.After(Validate),
    Charge.After(Validate, Fraud),  // Waits for both
    Ship.After(Charge),
)

// Visualizes as:
// Validate ──┬──→ Charge ──→ Ship
// Fraud ─────┘
```

**Why:**
- Structure is static data - can visualize before execution
- Dependencies are explicit and typed - `Charge.After(Validat)` won't compile
- Parallel execution is implicit: steps without dependencies run in parallel
- No nesting ceremony - flat list of steps with explicit edges
- Rob Pike would approve: it's just data describing a graph

### Type Safety Spectrum

**Decision: Compile-time safety via typed step handles.**

```go
Charge.After(Validat)  // Won't compile - Validat doesn't exist
Charge.After(Validate) // Compiles - Validate is a valid step handle

v := Validate.Output(ctx)  // Returns *ValidateOutput - type is known
v.Ammount                  // Won't compile - typo in field name
```

Runtime errors are unacceptable for structural issues. The type system should catch:
- References to non-existent steps
- Type mismatches when reading step outputs
- Missing dependencies

### Fan-Out and Child Workflows

**Problem:** Processing a dynamic list of items (e.g., images) with durability per item.

**Decision: `workflow.Map` for durable fan-out.**

```go
func processAllImages(ctx workflow.Context) (*ProcessOutput, error) {
    fetch := FetchImages.Output(ctx)

    // Map spawns child workflows - each is independently durable
    // If we crash after 50/100, we resume from 51
    results, err := workflow.Map(ctx, fetch.Images, SingleImageWorkflow)
    if err != nil {
        return nil, err
    }

    // Aggregate results
    return aggregateResults(results), nil
}
```

**Why:**
- Child workflows are independently durable (own event streams)
- Parent workflow just sees "map over items, get results"
- Feels like Go: `workflow.Map` is analogous to parallel map
- Each child can have complex internal structure (its own DAG)
- Crash recovery is granular: completed children aren't re-run

### Framework Control vs User Control

**Framework control:** Framework calls user code at specific points.
```go
// Framework decides when to call, what context to provide
func (t *Task) Execute(ctx context.Context) error
```

**User control:** User calls framework when ready.
```go
// User decides when to start, how to handle results
result, err := runner.Execute(ctx, workflow, input)
```

**Decision:** Lean toward user control. Runner provides execution, user controls when/how to invoke. Steps are user code called by framework (unavoidable for orchestration).

---

## Principles for Open Questions

When evaluating options for open questions, apply these principles:

1. **Prefer the simpler option** unless complexity provides clear value
2. **Prefer explicit over implicit** when debugging is involved
3. **Prefer composition over configuration** when possible
4. **Prefer definition-time errors over runtime errors**
5. **Prefer reversible decisions** - don't paint into corners

---

## What Skene Is Not

### Not a General DAG Engine

Skene is optimized for workflows with mostly-sequential steps, optional parallelism (Chorus), and conditional branching (Cue). Pure DAGs with complex dependency graphs are better served by dedicated tools.

### Not a Data Pipeline Framework

Skene orchestrates side-effectful business logic. For data transformation pipelines with large datasets, use Apache Beam, Spark, or similar.

### Not a Distributed Saga Coordinator

While Skene supports saga compensation within a workflow, it doesn't coordinate sagas across multiple independent services. For that, use dedicated saga orchestrators or choreography patterns.

### Not a Low-Latency System

Event sourcing adds overhead. Skene is appropriate for workflows where individual steps take milliseconds to minutes, not microseconds.

---

## Comparison with Alternatives

### vs Temporal

Temporal uses imperative workflow code (looks like normal Go). Skene uses declarative structures.

| Aspect | Temporal | Skene |
|--------|----------|-------|
| Workflow definition | Imperative code | Declarative tree |
| State management | Implicit (replayed) | Explicit (state type S) |
| Debugging | Replay execution | Inspect event log |
| Learning curve | Lower (just write code) | Higher (learn primitives) |

### vs Step Functions

Step Functions uses JSON/YAML DSL. Skene uses Go code.

| Aspect | Step Functions | Skene |
|--------|---------------|-------|
| Definition | JSON/YAML | Go code |
| Type safety | None | Compile-time |
| Testing | External tools | Standard Go tests |
| Deployment | AWS-specific | Any PostgreSQL |

### vs River (directly)

River is a job queue. Skene adds workflow orchestration on top.

| Aspect | River | Skene |
|--------|-------|-------|
| Abstraction | Jobs | Workflows |
| State management | Job args | Event-sourced state |
| Coordination | Manual | Automatic (Scene, Chorus) |
| Use case | Background jobs | Multi-step workflows |

---

## Future Directions

These are areas for potential future exploration, not commitments:

### Visual Editor

Generate visual representation from workflow definition. Edit visually, export to Go code. The static workflow structure makes this feasible.

### Distributed Tracing Integration

First-class OpenTelemetry integration with automatic span creation per step.

### Workflow Versioning Migrations

Tools to help migrate in-flight workflows when definitions change.

### Advanced Fan-Out Patterns

Beyond `workflow.Map`:
- `workflow.MapWithConcurrency(ctx, items, workflow, maxConcurrent)`
- `workflow.MapPartitioned(ctx, items, workflow, partitionKey)`
- Streaming results as children complete
