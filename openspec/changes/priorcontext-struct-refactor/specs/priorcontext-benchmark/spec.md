# Delta: priorcontext-benchmark

## Capability

Add 4 benchmarks in `internal/application/discipline/prior_context_bench_test.go` that measure `Render()` latency against inline-concatenation baselines for both callsite shapes, and assert that `Render()` does not exceed 2x the baseline latency. A tolerance band (median of ≥10 benchmark runs) MUST be used to absorb CI runner noise. Benchmark output MUST be captured in the PR body for reviewer visibility.

References: proposal §Scope / §Success Criteria; explore.md §8; locked invariants: Benchmark target `Render() <= 2x` baseline; benchmark variance tolerance band.

---

## ADDED Requirements

### Requirement: 4 benchmarks at canonical path

The system MUST declare the following 4 benchmark functions in `internal/application/discipline/prior_context_bench_test.go`:

| Function | Purpose |
|---|---|
| `BenchmarkPriorContext_Render_PhaseService` | Measures `Render()` for the phase-service shape (RawMemoryBlob) |
| `BenchmarkPriorContext_Render_ApplyThreeSections` | Measures `Render()` for the apply three-section shape |
| `BenchmarkInlineConcat_PhaseService` | Baseline: measures the pre-refactor inline concat for phase-service shape |
| `BenchmarkInlineConcat_ApplyThreeSections` | Baseline: measures the pre-refactor inline concat for apply shape |

#### Scenario: All 4 benchmarks compile and run

- GIVEN `prior_context_bench_test.go` exists with all 4 functions
- WHEN `go test -bench=. ./internal/application/discipline/...` is run
- THEN all 4 benchmarks run to completion and report ns/op values without error

---

### Requirement: Render() <= 2x baseline latency assertion

The system MUST assert that the median `ns/op` of `BenchmarkPriorContext_Render_PhaseService` is at most 2× the median `ns/op` of `BenchmarkInlineConcat_PhaseService`, AND that the median `ns/op` of `BenchmarkPriorContext_Render_ApplyThreeSections` is at most 2× the median `ns/op` of `BenchmarkInlineConcat_ApplyThreeSections`.

The assertion MUST use a tolerance band computed from ≥10 benchmark iterations (e.g., `b.N` loops that run enough to produce a stable median), not a single-run measurement. This guards against CI runner noise.

#### Scenario: Phase-service Render within 2x baseline

- GIVEN `BenchmarkPriorContext_Render_PhaseService` and `BenchmarkInlineConcat_PhaseService` are run with `-benchtime=10x` or equivalent
- WHEN median ns/op values are compared
- THEN `Render()` median MUST be ≤ 2× baseline median
- AND the test suite MUST fail if the ratio exceeds 2.0 by more than the tolerance band

#### Scenario: Apply three-section Render within 2x baseline

- GIVEN `BenchmarkPriorContext_Render_ApplyThreeSections` and `BenchmarkInlineConcat_ApplyThreeSections` are run
- WHEN median ns/op values are compared
- THEN `Render()` median MUST be ≤ 2× baseline median

---

### Requirement: Benchmark output captured in PR body

The system MUST include the raw `go test -bench` output in the PR description so reviewers can verify the ratio without re-running locally. This is an observability requirement, not a code requirement.

#### Scenario: PR body contains benchmark output

- GIVEN the benchmarks are run as part of the PR workflow
- WHEN the PR is submitted
- THEN the PR body contains a fenced code block with the `go test -bench` output showing all 4 ns/op values

---

### Requirement: TDD — benchmark assertion is red before Render() exists

The system MUST write the benchmark file (and any ratio assertion helper) BEFORE `prior_context.go` is created, causing a compilation failure (red). The benchmarks and assertion turn green only after `Render()` is implemented.

#### Scenario: Red phase — benchmarks reference undefined symbol

- GIVEN `prior_context_bench_test.go` exists referencing `discipline.PriorContext` and `Render`
- WHEN `go build ./internal/application/discipline/...` is run without `prior_context.go`
- THEN the build fails with an undefined symbol error

#### Scenario: Green phase — benchmarks pass after implementation

- GIVEN `prior_context.go` is created with the `Render` method
- WHEN `go test -bench=. ./internal/application/discipline/...` is run
- THEN all 4 benchmarks complete and the 2x ratio assertion passes
