# Delta: skills-domain-lifecycle

## Capability

The `Skill` domain aggregate (currently 7 fields + phases/techniques) is extended with 9 lifecycle fields mirroring the schema columns 1:1. New invariants enforce valid enum values for `Status`, `RiskLevel`, and `ActivationSource`. `New()` defaults apply V4.1 §7 candidate values. `Update()` accepts the new payload and re-enforces invariants. `Hydrate()` trusts persistence without revalidation.

Refs: proposal §2, explore §2, V4.1 §5.2 §7.

---

## ADDED Requirements

### Requirement: Skill aggregate carries lifecycle fields

The `Skill` aggregate MUST expose the following unexported fields via public getters:

| Field | Type | Zero/default in New() |
|---|---|---|
| `Status` | `string` (enum) | `"candidate"` |
| `Version` | `string` | `"v1"` |
| `Scope` | value type (JSON-serializable) | zero value |
| `AppliesWhen` | value type (JSON-serializable) | zero value |
| `RiskLevel` | `string` (enum) | `"medium"` |
| `ActivationSource` | `string` (enum) | `"manual"` |
| `Metrics` | value type | zero values (all counts = 0) |
| `LastUsedAt` | `*time.Time` | `nil` |
| `LastValidatedAt` | `*time.Time` | `nil` |

#### Scenario: New() with defaults populates lifecycle fields

- GIVEN a call to `skill.New()` with only the minimum required fields (name, content, phases, techniques)
- WHEN `New()` completes without error
- THEN `Status()` returns `"candidate"`
- AND `Version()` returns `"v1"`
- AND `RiskLevel()` returns `"medium"`
- AND `ActivationSource()` returns `"manual"`
- AND `LastUsedAt()` returns `nil`
- AND `LastValidatedAt()` returns `nil`
- AND `Metrics()` has all numeric counts equal to zero

#### Scenario: New() with all lifecycle fields supplied

- GIVEN a call to `skill.New()` with all 9 lifecycle fields explicitly provided (e.g., Status=`"active"`, Version=`"v2"`, RiskLevel=`"high"`, ActivationSource=`"legacy_seed"`)
- WHEN `New()` completes without error
- THEN all supplied lifecycle getters return the provided values

---

### Requirement: Invariants enforce valid enum values

`New()` and `Update()` MUST return an error if:

- `Status` is not one of: `"candidate"`, `"validated"`, `"active"`, `"deprecated"`, `"blocked"`, `"archived"` (V4.1 §5.2 — 6 values)
- `Version` is empty string
- `RiskLevel` is not one of: `"low"`, `"medium"`, `"high"`, `"critical"`
- `ActivationSource` is not one of: `"manual"`, `"legacy_seed"`, `"archive_worker"`, `"llm_proposal"`, `"imported"` (V4.1 §5.2 — 5 values)

#### Scenario: Update() with invalid status enum is rejected

- GIVEN a valid `Skill` aggregate instance
- WHEN `Update()` is called with `Status = "unknown"`
- THEN an error is returned
- AND the aggregate's `Status` remains unchanged

#### Scenario: New() with empty version is rejected

- GIVEN a call to `skill.New()` with `Version = ""`
- WHEN `New()` is evaluated
- THEN an error is returned indicating version must not be empty

#### Scenario: Update() with valid status transition succeeds

- GIVEN a valid `Skill` aggregate with `Status = "candidate"`
- WHEN `Update()` is called with `Status = "active"`
- THEN no error is returned
- AND `Status()` returns `"active"`

---

### Requirement: Hydrate() trusts persistence without revalidation

`Hydrate()` MUST accept any values from the database without applying invariant checks.

#### Scenario: Hydrate() accepts legacy row without error

- GIVEN a database row with `status = 'candidate'` and `activation_source = 'manual'` (migration 010 defaults)
- WHEN `Hydrate()` is called with that row's data
- THEN no error is returned
- AND all lifecycle getters return the persisted values unchanged

---

### Requirement: Domain unit tests cover lifecycle invariants

All invariant scenarios for `New()`, `Update()`, and `Hydrate()` MUST be covered by unit tests under `internal/domain/skill/`. Tests follow strict TDD: failing test first, then minimal production code.

#### Scenario: Unit test suite green after implementation

- GIVEN the domain unit test file defines RED tests for lifecycle invariants
- WHEN the lifecycle fields and invariant checks are implemented
- THEN `go test ./internal/domain/skill/...` passes with zero failures
