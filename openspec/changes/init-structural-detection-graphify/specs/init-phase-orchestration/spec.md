# Delta: init-phase-orchestration

## Capability

`runPhase()` in `sophia-orchestator/internal/application/phase/service.go` MUST branch on `PhaseInit` at the top of the function, before any LLM dispatch path is reached. The branch invokes `InitService.Run(ctx, change)`, which runs `SophiaDetector` and `GraphifySpawner` in parallel, merges their outputs into a `StructuralContext`, persists it, and marks the phase DONE via a synthetic envelope — all without ever calling an LLM provider. Detector or spawner failures are non-fatal and result in a partial `StructuralContext`. Persistence failure is also non-fatal but MUST be logged.

---

## ADDED Requirements

### Requirement: PhaseInit Short-Circuit in runPhase()

`runPhase()` MUST check `if phase.Type() == PhaseInit` as the FIRST conditional branch and MUST route to `InitService.Run(ctx, change)` when true. No LLM dispatcher call MUST occur for any `PhaseInit` invocation. This invariant is testable: a unit test MUST assert that `Dispatcher.Dispatch` is never called when `phase.Type() == PhaseInit`.

#### Scenario: happy path with graphify available

- GIVEN a change whose current phase is `PhaseInit` and graphify is installed
- WHEN `runPhase()` is invoked
- THEN `InitService.Run` is called (not any LLM dispatcher)
- AND `StructuralContext` is populated with detector + graph results
- AND the phase is marked DONE
- AND the phase envelope is persisted BEFORE the phase status is updated (Iron Law D1.2)

#### Scenario: happy path degraded (graphify absent)

- GIVEN a change whose current phase is `PhaseInit` and graphify is not installed
- WHEN `runPhase()` is invoked
- THEN `InitService.Run` is called
- AND `StructuralContext.graph_available=false` with `degraded_reason` populated
- AND the phase is still marked DONE
- AND no LLM dispatcher is called

---

### Requirement: Parallel Detector and Spawner Execution

`InitService.Run` MUST execute `SophiaDetector.Detect` and `GraphifySpawner.Spawn` concurrently (e.g., using `errgroup`). The merged `StructuralContext` MUST reflect results from both. Each component MUST have an independent timeout so a slow graphify build does not block the detector result indefinitely.

#### Scenario: detector and spawner run concurrently

- GIVEN both detector and spawner are configured with fake implementations that each complete after a simulated delay
- WHEN `InitService.Run` is called
- THEN both complete before `InitService.Run` returns (observable via elapsed time being less than the sum of individual delays)

---

### Requirement: Non-Fatal Component Failures

A failure in `SophiaDetector.Detect` (e.g., `repoRoot` is unreadable) MUST produce a partial `StructuralContext` with an empty or zero-value detector section; it MUST NOT abort INIT. A failure in `GraphifySpawner.Spawn` MUST set `graph_available=false`; it MUST NOT abort INIT. In both cases the failure MUST be logged at WARN level.

#### Scenario: detector failure is non-fatal

- GIVEN `SophiaDetector.Detect` returns an error (simulated via fake)
- WHEN `InitService.Run` is called
- THEN `StructuralContext` has empty language and framework fields
- AND the phase is still marked DONE
- AND an error is logged at WARN level

#### Scenario: spawn failure is non-fatal

- GIVEN `GraphifySpawner.Spawn` returns a degraded result with `graph_available=false`
- WHEN `InitService.Run` is called
- THEN `StructuralContext.graph_available=false`
- AND the phase is still marked DONE

---

### Requirement: Synthetic Envelope and Iron Law D1.2 Compliance

`InitService.Run` MUST mark the phase DONE by persisting a synthetic envelope consistent with the `ConfidenceThreshold=0` unconditional-transition design. The envelope MUST be persisted BEFORE any caller-visible state change (Iron Law D1.2). No LLM-produced envelope content is required or expected.

#### Scenario: envelope persisted before state change

- GIVEN `InitService.Run` is executing
- WHEN the phase transitions to DONE
- THEN the synthetic envelope is written to the store before the phase status field is updated
- AND a subsequent read of phase state reflects DONE only after the envelope write has completed

---

### Requirement: No LLM Dispatch for PhaseInit

`runPhase()` MUST NOT call `Dispatcher.Dispatch` or any LLM provider when `phase.Type() == PhaseInit`. This MUST be enforced structurally (early return / guard clause), not by documentation alone.

#### Scenario: LLM dispatcher never called for INIT

- GIVEN a fake `Dispatcher` is injected that records all calls
- WHEN `runPhase()` is called with `phase.Type() == PhaseInit`
- THEN the fake dispatcher records zero calls
