# Delta: golden-rebaseline

## Capability

New enriched-output golden baselines are captured as the FIRST commit in PR3 (M0.5 pattern). Byte-exact comparison is retired as the test contract. Structural assertions replace byte-exact: required sections present in correct order, per-layer budgets respected, attribution headers correct when enabled. Applies to all ~12 existing `priorcontext/*.golden.txt` files and `prompt_builder` fixtures.

## ADDED Requirements

### Requirement: Baseline capture commit is first in PR3

The first commit in PR3 MUST be a test/chore commit that re-captures golden snapshots for the intended enriched output with no production logic changes. This commit is the new diff anchor — reviewers inspect this commit to see what the enriched prompt looks like.

#### Scenario: Baseline commit precedes production changes

- GIVEN PR3 history is inspected
- WHEN commits are listed in order
- THEN the first commit is the golden re-capture commit (conventional commit: `test(discipline): rebaseline golden snapshots for enriched PriorContext output`)
- AND no production file changes appear in that commit

### Requirement: Byte-exact goldens replaced by new enriched-output goldens

All existing `*.golden.txt` files under `testdata/priorcontext/` and related `prompt_builder` fixtures MUST be replaced with new baselines that reflect the enriched output format (skills inside PriorContext, typed layers, attribution headers when enabled). Byte-exact `assert.Equal` on raw golden content MUST be replaced.

#### Scenario: Old byte-exact assertions removed

- GIVEN the rebaseline is complete
- WHEN test files are inspected
- THEN no test uses byte-exact comparison on the full rendered PriorContext string

### Requirement: Structural assertions replace byte-exact

Test assertions MUST verify structure, not byte equality. Required structural assertions:

| Assertion | What is checked |
|---|---|
| Sections present | Skills section, episodes section (if data), digests section (if data) present |
| Ordering | Skills section appears before memory layer sections |
| Budget respected | With non-zero TokenBudget, no layer exceeds its configured limit |
| Attribution format | With EnableAttribution, each skill has `## Skill:` header with required fields |
| No blocked skills | No `status = blocked/deprecated/archived` skill appears in output |

#### Scenario: Structural assertions green on enriched output

- GIVEN the new golden baselines and structural assertions are in place
- WHEN the test suite runs
- THEN all structural assertions pass

#### Scenario: Attribution assertion verifies header format

- GIVEN a test with `RenderOpts.EnableAttribution = true`
- WHEN `Render()` is called
- THEN the test asserts the presence of `## Skill: <name> v<version> (active, source=` prefix in the output

### Requirement: golangci-lint clean after rebaseline

After all PR3 changes including the rebaseline, `golangci-lint run` MUST report no errors. This is a hard gate inherited from the INIT-0 lesson and carried since M0.5.

#### Scenario: Lint gate passes

- GIVEN all PR3 changes are applied
- WHEN `golangci-lint run ./...` executes
- THEN exit code is 0 with no lint errors
