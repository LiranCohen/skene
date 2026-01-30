# Architecture: Event-Sourced Skene

## Overview

This document describes the architecture for the event-sourced Skene system. This is a complete replacement of the snapshot-based runner, not a parallel implementation.

---

## Core Principles

### 1. Events as Source of Truth

The event log is the authoritative record of workflow execution. State can always be reconstructed by replaying events.

### 2. Static Workflow Structure

Workflows are defined as static DAGs of typed step handles. The structure is known at compile time, can be validated before execution, and can be visualized as a graph.

```go
var OrderWorkflow = workflow.Define("order-processing",
    Validate,
    Fraud.After(Validate),
    Charge.After(Validate, Fraud),
    Ship.After(Charge),
)
```

### 3. Explicit Data Flow

Step functions have explicit inputs (via typed `Step.Output(ctx)` calls) and explicit outputs (return values). No implicit state mutation.

```go
func chargePayment(ctx workflow.Context) (*ChargeOutput, error) {
    v := Validate.Output(ctx)  // Explicit input
    // ...
    return &ChargeOutput{TransactionID: txID}, nil  // Explicit output
}
```

### 4. Deterministic Replay

Given the same events, replay produces identical outputs. This requires:
- No external calls during replay (skip completed steps)
- Same workflow definition
- Recorded branch decisions

### 5. Transactional Guarantees

Event append, job completion, and next job insertion happen atomically in a single PostgreSQL transaction.

---

## Package Structure (Final)

After migration, the package structure reflects the typed step model:

```
skene/
├── workflow.go         # Core types: Step[T], WorkflowDef, Context
├── step.go             # Step handle implementation
├── define.go           # workflow.Define() and DAG construction
├── context.go          # workflow.Context, Input[T], Output access
├── map.go              # workflow.Map for fan-out
├── branch.go           # Branch (conditional execution)
│
├── event.go            # Event types and constants
├── event_data.go       # Event payload structs
├── store.go            # EventStore, TxEventStore interfaces
├── history.go          # History query helper
├── replay.go           # Replay engine
│
├── river/              # River integration
│   ├── runner.go       # Main runner with event sourcing
│   ├── config.go       # Configuration
│   ├── jobs.go         # Job types and handlers
│   ├── signals.go      # Signal delivery
│   ├── queries.go      # GetRun, ListRuns, GetHistory
│   └── testing/        # MockRunner
│
├── pgstore/            # PostgreSQL event store
│   ├── pgstore.go
│   └── schema.sql
│
├── memstore/           # Memory event store
│   └── memstore.go
│
├── retry/              # Retry policies
├── logger.go           # Logger interface
├── hooks.go            # Hook interfaces
├── errors.go           # Error types
└── internal/           # Internal utilities
```

---

## What Gets Deleted

The entire snapshot-based system is replaced:

```
DELETE:
├── workflow.go         # Old primitives (Act, Scene, Chorus, Cue, Signal)
├── runner.go           # Snapshot-based runner
├── store/              # Snapshot store package
│   ├── store.go
│   ├── errors.go
│   └── memory/
├── deps.go             # Old Deps container (replaced by Context)
├── saga.go             # Old saga types (integrated into step model)
└── river/store.go      # River's internal snapshot store
```

---

## Component Relationships

```
┌─────────────────────────────────────────────────────────────────┐
│                         User Code                               │
│  - Define step handles: Step[Output]("name", fn)               │
│  - Define workflows: workflow.Define("name", steps...)          │
│  - Call Runner.StartWorkflow, SendSignal, etc.                 │
└────────────────────────────┬────────────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────────────┐
│                     river.Runner                                │
│  - Workflow lifecycle management                                │
│  - Job scheduling via River                                     │
│  - Signal delivery                                              │
│  - Query APIs                                                   │
└──────┬─────────────────────────────────────────────────┬────────┘
       │                                                 │
       │ Uses                                            │ Uses
       ▼                                                 ▼
┌──────────────────┐                          ┌──────────────────┐
│    Replayer      │                          │   EventStore     │
│  - DAG walking   │                          │  - Append        │
│  - Output restore│                          │  - Load          │
│  - Event gen     │                          │  - Transaction   │
└────────┬─────────┘                          └────────┬─────────┘
         │                                             │
         │ Uses                                        │ Impl
         ▼                                             ▼
┌──────────────────┐                          ┌──────────────────┐
│    History       │                          │    pgstore       │
│  - Event queries │                          │  - PostgreSQL    │
│  - Output lookup │                          │  - Same DB as    │
└──────────────────┘                          │    River         │
                                              └──────────────────┘
```

---

## Data Flow

### Workflow Execution

```
1. StartWorkflow(name, input)
   └── Append workflow.started event
   └── Insert WorkflowJob into River

2. River dispatches job to worker

3. Worker executes job:
   a. Begin transaction
   b. Load events from EventStore
   c. Create History from events (step outputs indexed by step name)
   d. Create Replayer with History and workflow definition
   e. Walk DAG:
      - For each ready step (dependencies satisfied):
        - If completed: load output from history
        - If pending: execute step function, append step.completed event
   f. If more work: insert next job
   g. Commit transaction

4. Repeat step 3 until workflow completes/fails/waits
```

### Step Execution Detail

```
1. Replayer identifies next ready step (all dependencies completed)

2. Step function is called with workflow.Context:
   - Context provides workflow.Input[T](ctx) for original input
   - Context provides Step.Output(ctx) for any prior step's output
   - Outputs are loaded from History (already deserialized)

3. Step function returns (output, error)

4. On success:
   - Append step.completed event with serialized output
   - Output becomes available for dependent steps

5. On error:
   - Append step.failed event
   - Apply retry policy if configured
   - Or mark workflow failed
```

### Signal Delivery

```
1. SendSignal(runID, signalName, payload)
   └── Begin transaction
   └── Load events
   └── Find signal.waiting event
   └── Append signal.received event
   └── Insert resume job
   └── Commit transaction

2. Resume job triggers normal execution flow
```

### Fan-Out (workflow.Map)

```
1. Parent step calls workflow.Map(ctx, items, ChildWorkflow)

2. For each item:
   a. Append child.spawned event with child input
   b. Insert child WorkflowJob

3. Each child runs independently:
   - Own event stream (child run ID)
   - Full durability guarantees

4. Parent waits for all children:
   - Append map.waiting event
   - On each child completion: check if all done
   - When all done: collect results, continue parent

5. On parent resume after crash:
   - Completed children: skip (events exist)
   - Remaining children: spawn new jobs
```

---

## Key Interfaces

### Step Handle

```go
// Step is a typed handle for a workflow step
type Step[T any] struct {
    name string
    fn   func(Context) (T, error)
}

// Step creates a new typed step handle
func Step[T any](name string, fn func(Context) (T, error)) Step[T]

// After declares dependencies (returns a configured step for workflow.Define)
func (s Step[T]) After(deps ...AnyStep) ConfiguredStep

// Output retrieves this step's output from the workflow context
func (s Step[T]) Output(ctx Context) T
```

### Workflow Definition

```go
// Define creates a workflow from steps with explicit dependencies
func Define(name string, steps ...ConfiguredStep) *WorkflowDef

// WorkflowDef represents a static workflow structure
type WorkflowDef struct {
    Name  string
    Steps []StepNode  // Topologically sorted
}

// Graph returns the workflow structure for visualization
func (w *WorkflowDef) Graph() *DAG
```

### Context

```go
// Context provides access to workflow data and prior outputs
type Context interface {
    context.Context

    // RunID returns the current workflow run ID
    RunID() string

    // WorkflowName returns the workflow name
    WorkflowName() string
}

// Input retrieves the workflow input (typed)
func Input[T any](ctx Context) T

// Map executes a child workflow for each item
func Map[In, Out any](ctx Context, items []In, workflow *WorkflowDef) ([]Out, error)

// Run executes a child workflow synchronously
func Run[In, Out any](ctx Context, workflow *WorkflowDef, input In) (Out, error)
```

### EventStore

```go
type EventStore interface {
    Append(ctx context.Context, event Event) error
    AppendBatch(ctx context.Context, events []Event) error
    Load(ctx context.Context, runID string) ([]Event, error)
    LoadSince(ctx context.Context, runID string, afterSeq int64) ([]Event, error)
    GetLastSequence(ctx context.Context, runID string) (int64, error)
}
```

### TxEventStore (for River integration)

```go
type TxEventStore interface {
    EventStore
    AppendTx(ctx context.Context, tx pgx.Tx, event Event) error
    AppendBatchTx(ctx context.Context, tx pgx.Tx, events []Event) error
    LoadTx(ctx context.Context, tx pgx.Tx, runID string) ([]Event, error)
}
```

### Runner

```go
type Runner interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error

    StartWorkflow(ctx context.Context, name string, input json.RawMessage) (string, error)
    StartWorkflowTx(ctx context.Context, tx pgx.Tx, name string, input json.RawMessage, opts StartOptions) (string, error)
    SendSignal(ctx context.Context, runID, signalName string, payload json.RawMessage) error
    CancelWorkflow(ctx context.Context, runID, reason string) error

    GetRun(ctx context.Context, runID string) (*Run, error)
    ListRuns(ctx context.Context, filter RunFilter) ([]RunSummary, error)
    GetHistory(ctx context.Context, runID string) ([]Event, error)
}
```

---

## Migration Path

### Phase 1: Core Types (Complete)
- Event types and store interfaces
- PostgreSQL and memory implementations

### Phase 2: Replay Engine (Complete)
- Basic replay for sequential steps

### Phase 3: Typed Step Model
- `Step[T]` handle implementation
- `workflow.Define()` DAG construction
- `Step.Output(ctx)` retrieval
- `workflow.Input[T](ctx)` retrieval
- Compile-time type safety

### Phase 4: Parallel Execution
- Steps without dependencies run in parallel
- Parallel step completion tracking
- Error handling for parallel failures

### Phase 5: Child Workflows
- `workflow.Run()` for single child
- `workflow.Map()` for fan-out
- Independent child event streams
- Parent-child coordination

### Phase 6: Branching
- `Branch()` conditional execution
- Branch selection recording
- Deterministic replay of branches

### Phase 7: Signals
- Signal wait points in steps
- Signal delivery and resume
- Timeout handling

### Phase 8: Runner Integration
- New river.Runner with event sourcing
- Job handlers
- Query APIs

### Phase 9: Advanced Features
- Retry policies per step
- Saga pattern / compensation
- Workflow versioning

### Final: Migration
1. Complete all phases
2. Delete old code (workflow.go primitives, runner.go, store/)
3. Move new implementation to root package
4. Update river/ to use event sourcing
5. Update documentation and examples

---

## Invariants

### Event Ordering
- Events within a run are strictly ordered by sequence
- Sequence numbers are gapless (1, 2, 3, ...)
- Append fails if sequence != lastSequence + 1

### Transactional Consistency
- Events and River jobs committed atomically
- No lost events (job completes without events)
- No orphaned jobs (events written without job completion)

### Replay Determinism
- Same events → same outputs
- Branch decisions recorded, not re-evaluated
- Completed steps skipped, outputs restored from events

### Type Safety
- Step outputs are typed at compile time
- `Step.Output(ctx)` returns the declared type
- Dependencies are typed (can't reference non-existent steps)

### DAG Integrity
- Workflow structure is static (known before execution)
- Dependencies form a DAG (no cycles)
- All step dependencies must be satisfied before execution

### Idempotency
- Signal delivery is idempotent (can't send twice)
- Job retry is safe (replay skips completed steps)
- Cancellation is idempotent (already cancelled = no-op)

### Output Immutability
- Step outputs are immutable once recorded
- Same step always produces same output on replay
- Outputs stored as serialized JSON in events
