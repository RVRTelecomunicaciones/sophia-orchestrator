# Delta: phase-archived-event

## Capability

First-class orchestrator event that fires exactly once when a Change transitions into archive completion. The event constant `EventPhaseArchived = "phase.archived"` is introduced in `sophia-orchestator` and immediately mirrored in `sophia-cli`'s wire contract in the same cross-repo PR. `wire_alignment_test` is a non-negotiable CI gate that enforces this mirror.

**Source refs:** proposal §Scope item 2; explore §Item 2 (phase.archived event D13); explore §Cross-repo coupling map.

---

## ADDED Requirements

### Requirement: EventPhaseArchived Constant

The system MUST define `EventPhaseArchived EventType = "phase.archived"` in `sophia-orchestator/internal/ports/inbound/event_types.go`.

#### Scenario: Constant exists with correct string value

- GIVEN `event_types.go` is loaded
- WHEN the value of `EventPhaseArchived` is read
- THEN it equals the string `"phase.archived"`

---

### Requirement: PhaseArchivedPayload Shape

The system MUST define `PhaseArchivedPayload` carrying `change_id`, `phase_type`, an envelope reference, and `archived_at` sourced from the injectable `Clock`.

#### Scenario: Payload carries required fields

- GIVEN a `PhaseArchivedPayload` is constructed at emission time
- WHEN its fields are inspected
- THEN `ChangeID` is the identifier of the Change being archived
- AND `PhaseType` identifies the archive phase
- AND `EnvelopeRef` contains a reference to the persisted envelope
- AND `ArchivedAt` equals the timestamp returned by the injected `Clock` at emission time
- AND `time.Now()` is NOT called directly in domain or application code

---

### Requirement: Exactly-Once Emission at Archive Completion

The system MUST emit `EventPhaseArchived` exactly once when `advanceChange()` detects `completed == phase.PhaseArchive`. The event MUST NOT be emitted before the envelope is persisted (D1.2 Iron Law).

#### Scenario: Happy path — archive completion emits event once

- GIVEN a Change is in a state that causes `advanceChange()` to detect archive completion
- AND the envelope for the archive phase has been persisted
- WHEN `advanceChange()` proceeds past the persist point
- THEN exactly one `EventPhaseArchived` event is emitted to subscribers
- AND the payload contains the correct `ChangeID` and `ArchivedAt`

#### Scenario: Failed phase does not emit phase.archived

- GIVEN a Change phase that fails before reaching archive completion
- WHEN `advanceChange()` processes the failure
- THEN `EventPhaseArchived` is NOT emitted

#### Scenario: Envelope persisted before event emitted

- GIVEN `advanceChange()` is about to emit `EventPhaseArchived`
- WHEN the system is observed at the emission point
- THEN the archive phase envelope has already been written to persistent storage
- AND the event emission happens after the persist call returns without error

---

### Requirement: CLI Wire Contract Mirror

The system MUST include `EventPhaseArchived` in `sophia-cli/pkg/contract/events.go` `knownEvents` in the same PR as the orchestrator constant.

#### Scenario: wire_alignment_test passes after the PR

- GIVEN `EventPhaseArchived` is added to orch `event_types.go`
- AND `EventPhaseArchived` is mirrored in CLI `knownEvents` in the same commit set
- WHEN `TestWireAlignment_OrchEventsMirrored` runs via `make test` in sophia-cli
- THEN the test passes (no missing constant detected)

#### Scenario: wire_alignment_test fails if CLI mirror is absent

- GIVEN `EventPhaseArchived` is present in orch `event_types.go`
- AND the CLI mirror has NOT been added
- WHEN `TestWireAlignment_OrchEventsMirrored` runs
- THEN the test fails, blocking the CI build
