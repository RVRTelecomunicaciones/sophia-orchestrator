# Delta: priorcontext-phase-service-migration

## Capability

Migrate the body of `buildPriorContext` in `internal/application/phase/service.go` (line ~960) to construct a `discipline.PriorContext` with `RawMemoryBlob` populated from the memory-engine bundle, then call `pc.Render(discipline.RenderOpts{})` at the callsite boundary. The function's public signature remains `func buildPriorContext(...) string`. All downstream consumers continue to receive a `string`; no method signatures below this callsite change. Output is byte-exact against pre-refactor golden fixtures.

References: proposal §Scope; explore.md §§2,3,11; locked invariants: render-at-boundary, RawMemoryBlob interim field, downstream signatures unchanged.

---

## ADDED Requirements

### Requirement: Migrate buildPriorContext body to struct construction

The system MUST replace the inline `strings.Builder` loop inside `buildPriorContext` with construction of `discipline.PriorContext{RawMemoryBlob: <assembled bundle>}` followed by `return pc.Render(discipline.RenderOpts{})`.

The function signature `func buildPriorContext(ctx context.Context, ...) string` MUST NOT change. The callsite at `service.go:390` MUST continue to receive a `string`. No downstream method signatures (`PromptInput`, `runAllGroups`, etc.) MUST change.

#### Scenario: Empty memory bundle returns empty string

- GIVEN the memory-engine returns an empty bundle (zero records)
- WHEN `buildPriorContext` is called
- THEN the returned string is `""`
- AND the golden fixture `empty_memory_bundle.golden.txt` matches byte-for-byte

#### Scenario: Single memory record renders unchanged content

- GIVEN the memory-engine returns a bundle with exactly 1 record containing content C
- WHEN `buildPriorContext` is called
- THEN the returned string equals C (no extra headers or separators)
- AND the golden fixture `single_memory_record.golden.txt` matches byte-for-byte

#### Scenario: Multiple memory records render concatenated content

- GIVEN the memory-engine returns a bundle with N > 1 records
- WHEN `buildPriorContext` is called
- THEN the returned string equals the records concatenated with `"\n\n"` separators
- AND the golden fixture `multi_memory_record.golden.txt` matches byte-for-byte

#### Scenario: Unicode content is preserved byte-exact

- GIVEN the memory-engine returns a bundle with non-ASCII content
- WHEN `buildPriorContext` is called
- THEN the returned string preserves all non-ASCII bytes without transformation
- AND the golden fixture `memory_with_unicode.golden.txt` matches byte-for-byte

#### Scenario: Error path returns empty string

- GIVEN the memory-engine call returns a non-nil error
- WHEN `buildPriorContext` is called
- THEN the returned string is `""`
- AND the golden fixture `memory_error_returns_empty.golden.txt` matches byte-for-byte

---

### Requirement: Render-at-boundary — no downstream signature changes

The system MUST NOT change the signatures of any method or function that receives the string produced by `buildPriorContext`, including but not limited to: `runAllGroups`, `runTeamLead`, `runImplementWithRetry`, `dispatchImplement`, `dispatchImplementWithOverride`, `runGroupBuildFeedbackLoop`.

#### Scenario: Callsite at service.go still receives string

- GIVEN `buildPriorContext` is migrated to the struct path
- WHEN the callsite at `service.go:390` is compiled
- THEN `priorCtx` remains assigned a `string` value
- AND no type assertion or conversion is required at the callsite

---

### Requirement: TDD — snapshot tests are red before migration

The system MUST capture the 5 phase-service golden fixtures BEFORE any production code change by running the pre-refactor inline path with deterministic fixtures (injected `Clock` + `IDGenerator`). Snapshot tests referencing the migrated `buildPriorContext` MUST fail (red) until the migration is complete (green).

#### Scenario: Red phase — snapshot test fails before migration

- GIVEN golden fixtures are captured from the pre-refactor inline path
- WHEN a snapshot test calls the migrated struct-based `buildPriorContext` before `prior_context.go` exists
- THEN the test fails with a compilation or assertion error

#### Scenario: Green phase — snapshot tests pass after migration

- GIVEN `prior_context.go` and the migrated `buildPriorContext` body exist
- WHEN all 5 phase-service snapshot tests run
- THEN each test produces output that matches its golden fixture byte-for-byte
