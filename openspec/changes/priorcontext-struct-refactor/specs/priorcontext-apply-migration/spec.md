# Delta: priorcontext-apply-migration

## Capability

Migrate the bodies of `loadPriorContext` (line ~844) and `refreshApplyProgress` (line ~807) in `internal/application/apply/run.go` to build a `discipline.PriorContext` with named section fields (spec, design, progress), then call `pc.Render(discipline.RenderOpts{})` at the callsite boundary. Both functions' public signatures remain `string`-returning. Fail-soft behaviour on apply-progress fetch error is preserved. All six downstream method signatures are unchanged. Output is byte-exact against pre-refactor golden fixtures including `##` section headers.

References: proposal §Scope; explore.md §§2,3,11; locked invariants: render-at-boundary, downstream signatures unchanged, fail-soft refresh.

---

## ADDED Requirements

### Requirement: Migrate loadPriorContext body to struct construction

The system MUST replace the inline section-building logic in `loadPriorContext` with construction of `discipline.PriorContext` populated from the fetched spec and design content, followed by `pc.Render(discipline.RenderOpts{})` at the callsite. The function's return type MUST remain `string`. The `## spec` and `## design` headers MUST be preserved byte-exact in the rendered output.

#### Scenario: Spec only — no design

- GIVEN only the spec topic key resolves to content S, design topic key is absent
- WHEN `loadPriorContext` is called
- THEN the returned string contains the spec section with `## spec` header and content S
- AND the golden fixture `apply_spec_only.golden.txt` matches byte-for-byte

#### Scenario: Design only — no spec

- GIVEN only the design topic key resolves to content D, spec topic key is absent
- WHEN `loadPriorContext` is called
- THEN the returned string contains the design section with `## design` header and content D
- AND the golden fixture `apply_design_only.golden.txt` matches byte-for-byte

#### Scenario: Both spec and design, no progress

- GIVEN both spec and design topic keys resolve to content
- WHEN `loadPriorContext` is called (before `refreshApplyProgress`)
- THEN the returned string contains both `## spec` and `## design` sections in order
- AND the golden fixture `apply_both_no_progress.golden.txt` matches byte-for-byte

#### Scenario: Both topics absent returns empty string

- GIVEN neither spec nor design topic key resolves to content
- WHEN `loadPriorContext` is called
- THEN the returned string is `""`
- AND the golden fixture `apply_empty_returns_empty.golden.txt` matches byte-for-byte

---

### Requirement: Migrate refreshApplyProgress body to struct construction

The system MUST replace the inline append logic in `refreshApplyProgress` with construction that appends a `## Recent progress` section to the existing `PriorContext` struct, then calls `pc.Render(discipline.RenderOpts{})`. On memory-engine error, the function MUST return the base string unchanged (fail-soft). The function's return type MUST remain `string`.

#### Scenario: Full three sections — spec, design, and progress

- GIVEN spec, design, and apply-progress topic keys all resolve to content
- WHEN `loadPriorContext` then `refreshApplyProgress` are called in sequence
- THEN the returned string contains `## spec`, `## design`, and `## Recent progress` sections in order
- AND the golden fixture `apply_full_three_sections.golden.txt` matches byte-for-byte

#### Scenario: Progress refresh success appends section

- GIVEN a base string from `loadPriorContext` and a non-empty apply-progress result
- WHEN `refreshApplyProgress` is called
- THEN the returned string appends the `## Recent progress` section to the base
- AND the golden fixture `apply_progress_refresh.golden.txt` matches byte-for-byte

#### Scenario: Progress refresh error returns base unchanged (fail-soft)

- GIVEN a base string from `loadPriorContext` and the apply-progress fetch returns a non-nil error
- WHEN `refreshApplyProgress` is called
- THEN the returned string is identical to the base string (no progress section appended)
- AND the golden fixture `apply_progress_error_fail_soft.golden.txt` matches byte-for-byte

---

### Requirement: Render-at-boundary — no downstream signature changes

The system MUST NOT change the signatures of any method that receives the string produced by `loadPriorContext` or `refreshApplyProgress`, including: `runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop`.

#### Scenario: Downstream chain compiles without modification

- GIVEN `loadPriorContext` and `refreshApplyProgress` are migrated to the struct path
- WHEN the apply package is compiled
- THEN all six downstream methods compile without any signature change
- AND no type assertion or conversion is introduced in the call chain

---

### Requirement: TDD — snapshot tests are red before migration

The system MUST capture the 7 apply golden fixtures BEFORE any production code change. Snapshot tests referencing the migrated functions MUST fail (red) until the migration is complete (green).

#### Scenario: Red phase — snapshot test fails before migration

- GIVEN golden fixtures are captured from the pre-refactor inline path
- WHEN snapshot tests call the migrated struct-based functions before `prior_context.go` exists
- THEN tests fail with compilation or assertion errors

#### Scenario: Green phase — all 7 apply snapshot tests pass

- GIVEN `prior_context.go` and both migrated function bodies exist
- WHEN all 7 apply snapshot tests run
- THEN each test produces output that matches its golden fixture byte-for-byte

---

### Requirement: Exhaustive consumer enumeration before migration

The system MUST enumerate ALL callsites that receive the string from `loadPriorContext` or `refreshApplyProgress` via `rg 'PriorContext\|loadPriorContext\|refreshApplyProgress' internal/application/` before touching any production file, to confirm no undiscovered consumers exist.

#### Scenario: No undiscovered callsites

- GIVEN the grep/rg search across `internal/application/{phase,apply,discipline}` is run
- WHEN results are reviewed
- THEN only the documented callsites in `service.go:390` and `run.go` chain are found
- AND no additional callers require signature changes
