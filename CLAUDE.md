# Claude Code Instructions for Skene

## Code Quality Standards

**This library aims to be gold-standard Go code.** Follow these principles:

### Interface Design
- Accept interfaces, return concrete types
- Keep interfaces small and focused
- Design for composition, not inheritance

### Error Handling
- Return errors, don't panic (except for programmer errors)
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Validate at boundaries, trust internally

### API Design
- Make the zero value useful when possible
- If something is required, require it in the constructor
- Avoid scattered nil checks - validate once at construction or use null-object pattern
- Be explicit about ownership and lifetimes

### Concurrency
- Don't communicate by sharing memory; share memory by communicating
- Use channels for coordination, mutexes for state
- Document concurrency requirements in comments

### Testing
- Use interfaces to enable testing without mocks when possible
- Table-driven tests for comprehensive coverage
- Test behavior, not implementation
- Use testcontainers for integration tests requiring databases

---

## Project Overview

Skene is a type-safe workflow orchestration framework for Go using generics. It provides an event-sourced execution model with typed step handles and explicit I/O dependencies.

The name "skene" (σκηνή) comes from ancient Greek theater - it was the stage building where all the theatrical action originated.

## Project Structure

```
skene/
├── go.mod              # Module definition
├── CLAUDE.md           # Code quality standards (this file)
├── README.md           # Project description
│
├── workflow/           # Core workflow types
│   ├── step.go         # Step[T] typed handles
│   ├── context.go      # Workflow context
│   ├── define.go       # WorkflowDef and DAG building
│   ├── replay.go       # Replay engine
│   ├── runner.go       # Simple runner for testing
│   └── testing.go      # Test helpers
│
├── event/              # Event system
│   ├── event.go        # Event types
│   ├── data.go         # Event data structs
│   ├── store.go        # Store interface
│   ├── history.go      # History helper
│   └── memory/         # In-memory store
│       └── store.go
│
├── retry/              # Retry policies
│   └── policy.go
│
├── docs/
│   └── design/         # Design documents
│
└── _examples/
    └── order-workflow/
        └── main.go
```

## Key Design Patterns

### Typed Step Handles with Generics

Steps are parameterized by their output type:
```go
var Validate = workflow.NewStep("validate", func(ctx workflow.Context) (ValidateOutput, error) {
    input := workflow.Input[OrderInput](ctx)
    return ValidateOutput{Amount: input.Amount}, nil
})
```

### Explicit Dependencies with After()

Dependencies are declared explicitly, forming a DAG:
```go
var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    Fraud.After(Validate),
    Charge.After(Validate, Fraud),
    Ship.After(Charge),
)
```

### Type-Safe Output Access

Steps read outputs from their dependencies with full type safety:
```go
var Charge = workflow.NewStep("charge", func(ctx workflow.Context) (ChargeOutput, error) {
    validated := Validate.Output(ctx) // Returns ValidateOutput
    fraud := Fraud.Output(ctx)        // Returns FraudOutput
    // ...
})
```

### Event-Sourced Execution

All execution is recorded as events, enabling:
- Crash recovery (replay from events)
- Debugging (full execution history)
- Testing (inject events to test specific scenarios)

## Testing

Run all tests:
```bash
go test ./...
```

Run with verbose output:
```bash
go test -v ./...
```

## Building

Build the package:
```bash
go build ./...
```

Build the example:
```bash
go run ./_examples/order-workflow
```
