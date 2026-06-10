# Delta: priorcontext-snapshot-golden

## Capability

Create 12 golden fixture files at `internal/application/discipline/testdata/priorcontext/` and the snapshot test infrastructure that drives them. Fixtures capture byte-exact baseline output from the pre-refactor inline concatenation paths using deterministic test doubles (injected `Clock` + `IDGenerator`). The `GOLDEN_UPDATE=1` env-var update pattern from `prompt_builder_test.go:371-394` MUST be reused verbatim. All 12 fixtures MUST be captured BEFORE any production code is changed (strict TDD baseline step). Golden fixture files are excluded from the 400-LoC PR budget per operator decision #3.

References: proposal §Scope; explore.md §§6,7; locked invariant: golden fixtures NOT in 400-LoC budget; strict TDD — failing test first.

---

## ADDED Requirements

### Requirement: 12 golden fixture files at canonical path

The system MUST create exactly the following 12 golden files in `internal/application/discipline/testdata/priorcontext/`:

| File | Callsite shape | Scenario |
|---|---|---|
| `empty_memory_bundle.golden.txt` | phase-service | 0 records → `""` |
| `single_memory_record.golden.txt` | phase-service | 1 record, content preserved |
| `multi_memory_record.golden.txt` | phase-service | 5 records, `\n\n`-joined |
| `memory_with_unicode.golden.txt` | phase-service | non-ASCII content preserved |
| `memory_error_returns_empty.golden.txt` | phase-service | error path → `""` |
| `apply_spec_only.golden.txt` | apply | spec present, design absent |
| `apply_design_only.golden.txt` | apply | design present, spec absent |
| `apply_both_no_progress.golden.txt` | apply | spec + design, no progress |
| `apply_full_three_sections.golden.txt` | apply | spec + design + progress |
| `apply_progress_refresh.golden.txt` | apply | base + progress appended |
| `apply_progress_error_fail_soft.golden.txt` | apply | base unchanged on error |
| `apply_empty_returns_empty.golden.txt` | apply | both absent → `""` |

#### Scenario: Each named golden file exists after baseline capture

- GIVEN `GOLDEN_UPDATE=1 go test ./internal/application/discipline/...` is run against the pre-refactor code
- WHEN the test runner completes without error
- THEN all 12 named files exist under `testdata/priorcontext/` with non-zero content (except the two empty-string fixtures which MAY be empty files)

---

### Requirement: Reuse GOLDEN_UPDATE env-var pattern from prompt_builder_test.go

The system MUST implement the golden update helper in a way that reads `os.Getenv("GOLDEN_UPDATE")` and writes the actual output to disk when set to `"1"`, and compares actual output to the file content when unset. The implementation MUST follow the same read/write helper structure as `prompt_builder_test.go:371-394`. A single shared `readGolden`/`writeGolden` helper MUST be reused — no per-test duplication of the pattern.

#### Scenario: GOLDEN_UPDATE=1 writes fixture files

- GIVEN no golden files exist yet
- WHEN `GOLDEN_UPDATE=1 go test ./internal/application/discipline/...` is run
- THEN all 12 golden files are written with the current inline-concatenation output

#### Scenario: Normal test run compares against golden files

- GIVEN golden files exist from a prior `GOLDEN_UPDATE=1` run
- WHEN `go test ./internal/application/discipline/...` is run without `GOLDEN_UPDATE`
- THEN each snapshot test compares actual output to its golden file byte-for-byte
- AND any mismatch causes the test to fail with a diff showing expected vs actual

---

### Requirement: Deterministic fixtures via injected Clock and IDGenerator

The system MUST drive snapshot tests using injected `Clock` (frozen time) and `IDGenerator` (fixed seed or constant ID) so that golden file content is deterministic across runs and machines. No test MUST call `time.Now()` or `ulid.Make()` directly.

#### Scenario: Frozen clock produces stable golden output

- GIVEN a test double `Clock` returning a fixed timestamp T
- WHEN the snapshot test runs twice on different machines
- THEN both runs produce identical output matching the golden file

#### Scenario: Fixed IDGenerator produces stable golden output

- GIVEN a test double `IDGenerator` returning a constant ID
- WHEN the snapshot test runs twice
- THEN both runs produce identical output matching the golden file

---

### Requirement: Baseline captured before any production code change

The system MUST capture all 12 golden fixtures in the TDD red phase — i.e., by exercising the pre-refactor inline concatenation code directly — BEFORE `prior_context.go` or any callsite migration is written. This establishes the "before" baseline that post-migration snapshot tests must match.

#### Scenario: Baseline capture order enforced

- GIVEN no `prior_context.go` file exists
- WHEN `GOLDEN_UPDATE=1 go test ./internal/application/discipline/...` is run
- THEN golden files are written from the inline-concatenation output
- AND `go build ./internal/application/discipline/...` succeeds (no undefined symbols at this point)
