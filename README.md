# Skene

A type-safe workflow orchestration framework for Go using generics.

**Status:** Under Development

## Overview

Skene provides an event-sourced execution model with typed step handles and explicit I/O dependencies. The framework is designed for building reliable, observable workflows with crash recovery and replay capabilities.

The name "skene" (σκηνή) comes from ancient Greek theater - it was the stage building where all the theatrical action originated.

## Design Documents

See `docs/design/` for detailed design documents:

- `00-PHILOSOPHY.md` - Design principles
- `00-ARCHITECTURE.md` - System architecture
- `02-WORKFLOW-MODEL.md` - Typed step model
- `01-EVENTS.md` - Event types and storage
- `03-REPLAY.md` - Replay engine design
- And more...

## Quick Example

```go
package main

import "github.com/lirancohen/skene/workflow"

// Define typed step outputs
type ValidateOutput struct {
    Amount  int
    Address string
}

type ChargeOutput struct {
    TransactionID string
}

// Define steps with typed handles
var (
    Validate = workflow.NewStep("validate", validateOrder)
    Charge   = workflow.NewStep("charge", chargePayment)
)

// Define workflow with explicit dependencies
var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    Charge.After(Validate),
)

func validateOrder(ctx workflow.Context) (ValidateOutput, error) {
    // Access typed input
    input := workflow.Input[OrderInput](ctx)
    return ValidateOutput{Amount: input.Amount, Address: input.Address}, nil
}

func chargePayment(ctx workflow.Context) (ChargeOutput, error) {
    // Access typed output from dependency
    validated := Validate.Output(ctx)
    // ... charge validated.Amount
    return ChargeOutput{TransactionID: "txn-123"}, nil
}
```

## License

MIT
