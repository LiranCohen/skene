# Feature: Workflow Versioning

## Overview

Workflow definitions can change over time. Versioning ensures in-flight workflows continue with their original definition while new runs use the updated version.

---

## Decisions

### Version-Per-Execution

**Decision:** Each workflow execution is bound to a specific version.

**Rationale:**
- In-flight workflows use their original definition
- New runs use the latest version
- No mid-execution version switching

```go
// At start:
workflow.started event records WorkflowVersion: 2

// All subsequent jobs:
WorkflowJobArgs{Version: 2}  // Always use version 2 for this run
```

### Version in Events

**Decision:** `WorkflowStarted` event includes workflow version.

**Rationale:**
- Audit trail: "which version ran?"
- Replay uses correct definition
- Version is immutable for the run

### Version in Registry

**Decision:** Registry stores multiple versions of each workflow.

```go
registry.Register(OrderWorkflowV1)  // version "v1"
registry.Register(OrderWorkflowV2)  // version "v2"

// Lookup by name + version
workflow, err := registry.Get("order-processing", "v2")
```

### Default Version

**Decision:** If no version specified at start, use the latest registered version.

**Rationale:**
- Simple default behavior
- Explicit version for production (recommended)

---

## Safe Changes

The following changes are safe for in-flight workflows:

### 1. Adding Steps at End of Scene

```go
// V1
Scene{Steps: [A, B]}

// V2 - safe
Scene{Steps: [A, B, C]}  // C added at end
```

In-flight V1 runs continue normally. New V2 runs include step C.

### 2. Adding Cue Branches

```go
// V1
Cue{Cases: {"card": chargeCard}}

// V2 - safe
Cue{Cases: {"card": chargeCard, "crypto": chargeCrypto}}
```

Existing runs using "card" branch are unaffected.

### 3. Changing Step Internals

```go
// V1
Act{Name: "send-email", Run: sendEmailV1}

// V2 - safe
Act{Name: "send-email", Run: sendEmailV2}  // Different implementation
```

Step name unchanged, events still match.

### 4. Changing Retry Policies

```go
// V1
Act{Name: "api-call", MaxRetries: 3}

// V2 - safe
Act{Name: "api-call", RetryPolicy: &retry.Policy{MaxAttempts: 5}}
```

In-flight retries may use old or new policy depending on when they resumed.

---

## Unsafe Changes

The following changes break in-flight workflows:

### 1. Removing Steps

```go
// V1
Scene{Steps: [A, B, C]}

// V2 - UNSAFE
Scene{Steps: [A, C]}  // B removed
```

Events for step B exist but B is no longer in definition.

### 2. Reordering Steps

```go
// V1
Scene{Steps: [A, B, C]}

// V2 - UNSAFE
Scene{Steps: [B, A, C]}  // Reordered
```

Events were recorded in order A, B. Replay expects A before B.

### 3. Renaming Steps

```go
// V1
Act{Name: "validate-order"}

// V2 - UNSAFE
Act{Name: "check-order"}  // Renamed
```

Events reference "validate-order" but definition has "check-order".

### 4. Changing Chorus Structure

```go
// V1
Chorus{Steps: [A, B]}

// V2 - UNSAFE
Chorus{Steps: [A, B, C]}  // C added to parallel
```

State merge expectations change. Branch C has no events.

---

## Open Questions

### 1. Version Compatibility Validation

**Question:** Should we validate version compatibility?

**Context:**
- Detect unsafe changes before deployment
- Prevent accidental breaking changes
- Could compare workflow tree structures

**Options:**
- A: No validation (user's responsibility)
- B: Structural comparison (detect removals/reorders)
- C: Event-based validation (check in-flight runs)
- D: Hybrid (structural + event check)

**Leaning toward:** B - structural comparison catches most issues.

### 2. Version Migration

**Question:** Should we support migrating in-flight workflows to new version?

**Context:**
- Long-running workflows (days, weeks)
- Need to apply critical fixes
- Manual migration is error-prone

**Options:**
- A: Not supported (wait for completion)
- B: Manual migration script (user writes)
- C: Migration hooks on workflow definition
- D: Automated migration tool

**Leaning toward:** B - manual script with helper utilities.

### 3. Version Deprecation

**Question:** How to handle deprecated versions?

**Context:**
- Old versions accumulate
- Want to clean up
- But can't remove if in-flight runs exist

**Options:**
- A: Keep all versions forever
- B: Mark deprecated, prevent new starts
- C: Remove after all runs complete
- D: Force-cancel deprecated runs

**Leaning toward:** B + C - deprecate, then clean up when empty.

### 4. Version Naming

**Question:** What version naming scheme?

**Context:**
- Currently: string (user-defined)
- Could enforce semver
- Could auto-increment

**Options:**
- A: Free-form string (current)
- B: Semver required
- C: Integer auto-increment
- D: Timestamp-based

**Leaning toward:** A - free-form is most flexible.

### 5. Default Version Strategy

**Question:** What's the "latest" version?

**Context:**
- Multiple versions registered
- Which is "latest"?

**Options:**
- A: Last registered
- B: Highest by semver sort
- C: Explicit "latest" tag
- D: Require explicit version always

**Leaning toward:** C - explicit "latest" tag avoids confusion.

### 6. Version Rollback

**Question:** How to rollback a bad version?

**Context:**
- V2 has a bug
- Want to use V1 for new runs
- In-flight V2 runs continue or cancel?

**Options:**
- A: Re-register V1 as "latest"
- B: Tag V1 as "rollback"
- C: Version aliases (production → V1)
- D: Cancel V2 runs, rollback

**Leaning toward:** A - simple re-registration. In-flight V2 runs continue.

### 7. Schema Versioning

**Question:** How to handle state schema changes?

**Context:**
- V1 state: `{orderID: string}`
- V2 state: `{orderID: string, priority: int}`
- V2 code expects priority field

**Options:**
- A: State migration in Init
- B: Backward-compatible code (check field existence)
- C: Separate schema version from workflow version
- D: Don't support schema changes

**Leaning toward:** B - backward-compatible code is simplest.

### 8. Parallel Versions in Production

**Question:** Can multiple versions run simultaneously?

**Context:**
- V1 runs exist
- V2 deployed
- Both versions processing

**Options:**
- A: Yes, always (current assumption)
- B: Yes, with warnings
- C: No, drain V1 first
- D: Configurable per workflow

**Leaning toward:** A - parallel versions are normal during transitions.

### 9. Version-Specific Configuration

**Question:** Can versions have different configurations?

**Context:**
- V1 uses 3 retries
- V2 uses 5 retries
- Configuration at version level?

**Options:**
- A: Embedded in workflow definition
- B: Separate version config
- C: Global config, version overrides

**Leaning toward:** A - configuration in workflow definition is explicit.

### 10. Cross-Version Signals

**Question:** Can signals be sent across versions?

**Context:**
- V1 run waiting for "approval"
- SendSignal with payload
- V2 has different payload schema

**Options:**
- A: Accept any payload (OnReceive handles)
- B: Version-specific signal schemas
- C: Validate payload against expected schema

**Leaning toward:** A - payload handling is in OnReceive.

---

## Implementation Status

| Component | Status |
|-----------|--------|
| Version field on Root | Complete |
| Version in events | Complete |
| Multi-version registry | Existing Registry supports versions |
| Version lookup in jobs | Not implemented |
| Compatibility validation | Not implemented |
| Version migration | Not implemented |
| Deprecation | Not implemented |
