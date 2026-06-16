# consolidation-pipeline-benchmark Specification

## Purpose

Provide an in-memory Go benchmark of the ME consolidation pipeline (`HandlerV2.Handle`) that exposes the per-skill loop cost across varying `skill_usage` row counts, runs in CI without a Docker dependency, and reports allocations. This is test-only code with no production behavior change.

## Requirements

### Requirement: In-memory benchmark of HandlerV2.Handle

A benchmark MUST exercise `HandlerV2.Handle` end-to-end using the existing in-memory `fakeOrchServer` (no real Postgres, no Docker, no testcontainers in the default CI path). It MUST mirror the `memory_pg_bench_test.go` pattern: appropriate build tag, `b.ReportAllocs()`, and `b.ResetTimer()` before the measured loop. It MUST parameterise the number of `skill_usage` rows so the per-skill cost of the pipeline loop is observable across row counts.

#### Scenario: Benchmark runs without Docker in default CI

- GIVEN a CI environment with no Docker daemon available
- WHEN the consolidation benchmark target is invoked in the default path
- THEN it completes successfully using only in-memory fakes
- AND it reports per-operation allocations via `ReportAllocs`

#### Scenario: Cost scales observably with skill_usage row count

- GIVEN benchmark cases for increasing `skill_usage` row counts (e.g. 1, 10, 100)
- WHEN the benchmark runs each case
- THEN each case produces a distinct ns/op and allocs/op measurement
- AND the per-skill loop cost is attributable from the row-count progression

### Requirement: Benchmark is test-only and isolated from production

The benchmark MUST NOT introduce or modify any production code path and MUST NOT be required for normal build/test success. Deleting the benchmark file MUST leave production behavior and the standard test suite unchanged.

#### Scenario: Removing the benchmark leaves the build green

- GIVEN the benchmark file is deleted
- WHEN the standard build and unit-test suite runs
- THEN the build succeeds and no production behavior changes
