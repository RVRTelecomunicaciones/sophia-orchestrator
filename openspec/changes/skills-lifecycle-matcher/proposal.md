# Proposal: skills-lifecycle-matcher (M1)

## Intent

M1 extends the live `skills` table (PR #76, migration 009) with lifecycle + metrics + scope + applies_when columns (V4.1 §5.2), backfills the 9 legacy seeds as `active` / `legacy_seed` / `v1` via the seeder, and introduces a deterministic `SkillMatcher` (V4.1 §8) capable of resolving skills by `SkillQuery` instead of just `phase`. This is THE FIRST learning-loop milestone in V4.1 §16 — every subsequent milestone (M2 promotion/demotion worker, M3 enrichment + StructuralContext wiring) depends on it. **Regression-zero vs PR #76 is the prime directive**: `SkillsForPhase(phase)` MUST return byte-equivalent skills before and after migration.

## Scope

### In Scope (single PR, `size:exception`)

1. **Migration 010 (schema-only, transactional)** — `migrations/postgres/010_skills_lifecycle.{up,down}.sql`. ALTER TABLE adds V4.1 §5.2 columns (`status`, `version`, `scope` JSONB, `applies_when` JSONB, `risk_level`, `activation_source`, `metrics` JSONB, `last_used_at`, `last_validated_at`) with safe defaults so the 9 existing rows survive. Same migration DROP `skills_name_unique` and ADD `skills_name_version_unique UNIQUE (name, version)` atomically inside the golang-migrate transaction wrapper (V4.1 §5.2; explore §8). Reversible down migration restores the pre-M1 shape.

2. **Domain aggregate extension** — `internal/domain/skill/skill.go`. Add unexported fields with getters for `Status`, `Version`, `Scope`, `AppliesWhen`, `RiskLevel`, `ActivationSource`, `Metrics`, `LastUsedAt`, `LastValidatedAt`. Extend invariants (valid status, non-empty version, valid risk_level). Change `Update` signature to accept the new lifecycle payload (currently zero non-test callers — explore §2, so signature change is safe).

3. **SkillRepo PG adapter** — `internal/adapters/outbound/pg/skill_repo.go`. Extend `FindByPhase` (line 32) with `AND status = 'active'` filter (operator decision 7; explore §3). Extend `Upsert` (line 56) and `InsertIfAbsent` (line 83) SQL + `scanSkill` to read/write all new columns. Extend `internal/ports/outbound/repository.go` `SkillRepository` interface accordingly.

4. **Seeder backfill (Option B)** — `internal/bootstrap/seed_skills.go:36` switches `InsertIfAbsent` → `Upsert` with V4.1 §7 legacy payload (`status=active`, `version=v1`, `activation_source=legacy_seed`, `risk_level=medium`, `scope={project_id:"*", repo_id:"*", phases:[<phase>]}`, `applies_when={}`, zeroed metrics). Idempotent via `UNIQUE (name, version)`. Satisfies "Upsert ≥ 1 non-test caller" criterion automatically (explore §9).

5. **SkillMatcher package** — NEW `internal/application/skill/matcher/`. Port `SkillMatcher` interface + `SkillQuery` + `SkippedSkill` + opaque `StructuralContextRef` types declared in `internal/application/discipline/skill_matcher.go` (operator decision 5; explore §7). PG adapter at `internal/adapters/outbound/pg/skill_matcher.go` implements V4.1 §8 algorithm: status filter, scope filter (project_id, repo_id, phases), applies_when filter (feature_type, touched_paths via `doublestar/v4`, exclude_paths), sort by (risk_level asc, last_validated_at desc, usage_count desc), skip-with-reason.

6. **`doublestar/v4` dependency** — `go.mod` + `go.sum` add `github.com/bmatcuk/doublestar/v4` (operator decision 2; explore §7). Required for `**` semantics in `applies_when.touched_paths`/`exclude_paths`.

7. **`SkillsForPhase` deprecation wrapper** — `internal/application/discipline/skill_provider.go`. Add `// Deprecated:` godoc; delegate to `SkillsForContext(SkillQuery{Phase: phase})`. Old API stays for back-compat in M1; retires in M3 (operator decision 8; explore §11).

8. **Regression snapshot tests** — `internal/adapters/outbound/pg/testdata/skill_phase_baseline/*.golden.json` (9 phase fixtures). Test asserts `FindByPhase` returns byte-equivalent skills before vs after migration + backfill (operator decision 9; explore §10). Goldens NOT counted toward 400-LoC budget (M0.5 precedent; operator decision 10).

9. **Integration test** — testcontainers PG, full flow: apply 010, run seeder, query matcher with representative `SkillQuery`, assert matched + skipped sets.

### Out of Scope

- **StructuralContext consumption in `applies_when`** (framework/language filters) — deferred to M3. `SkillQuery.StructuralContext = *StructuralContextRef` is nil-only in M1, mirroring M0.5 Option D (operator decision 6; explore §6).
- **Promotion / demotion policy logic** — M2 worker.
- **LLM-driven skill creation** — anti-pattern per V4.1 D11; never applies to INIT.
- **AllowlistEnforcer wiring** — PRE-0 / INIT-0 / M-LATER scope.
- **`skill_provider` / `skill_matcher` cross-repo coupling** — single-repo scope; no event constants emitted to other services.
- **Removing `SkillsForPhase` API** — deferred to M3; M1 deprecates only.
- **Migration of non-seed skills** — none exist today; backfill targets the 9 hybrid seeds.

## Capabilities

### New Capabilities

- `skills-schema-migration-010`: `ALTER TABLE skills` adding V4.1 §5.2 lifecycle/metrics/scope/applies_when columns + DROP/ADD UNIQUE swap, applied atomically inside the migrate transaction; idempotent up/down.
- `skills-domain-lifecycle`: Skill aggregate extended with `Status`, `Version`, `Scope`, `AppliesWhen`, `RiskLevel`, `ActivationSource`, `Metrics`, `LastUsedAt`, `LastValidatedAt` + invariants + new `Update` signature.
- `skills-seeder-backfill`: `seed_skills.go` switches `InsertIfAbsent` → `Upsert` with V4.1 §7 legacy payload; 9 seeds become `active` / `legacy_seed` / `v1` on next boot.
- `skill-matcher-port`: `SkillMatcher` interface + `SkillQuery` + `SkippedSkill` + `StructuralContextRef` nil-only opaque marker types in `internal/application/discipline/`.
- `skill-matcher-pg-adapter`: deterministic algorithm (scope filter, `applies_when` filter via `doublestar/v4`, sort, skip-with-reason); `FindByPhase` adds `status = 'active'` filter.
- `skills-for-phase-deprecation`: `SkillsForPhase` becomes wrapper around `SkillsForContext(SkillQuery{Phase: phase})` with `// Deprecated:` godoc pointing at M3 retirement.
- `skills-regression-snapshot`: 9 phase-baseline golden files + snapshot test ensuring `SkillsForPhase` regression-zero vs PR #76.

### Modified Capabilities

None at the spec level. All seven entries above are new capability surfaces; the PR #76 skills capability is extended additively without changing its existing behavior.

## Approach (high level)

- **Strict ordering** inside the single PR (each layer compiles + tests green before the next): `doublestar/v4` to `go.mod` → migration 010 up/down → domain extension → repo extension → seeder backfill → matcher port + types → matcher PG adapter → `SkillsForPhase` deprecation wrapper → regression snapshot tests → integration test.
- **Strict TDD per task** (operator decision 11): failing test FIRST for every capability. Migration 010 has its own test pattern: integration test applies up → verifies schema, applies down → verifies revert.
- **Backfill via seeder Upsert (Option B)** — automatic on next boot after deploy. Rolling-deploy-safe: old binary's `InsertIfAbsent` keeps working (SQL defaults fill new columns); new binary's seeder upserts the proper V4.1 §7 payload on restart (explore §14 R7).
- **Regression snapshot**: capture 9 phases' `SkillsForPhase` output BEFORE migration (from PR #76 baseline) into golden JSON; assert byte-equivalent AFTER migration + backfill.
- **One commit per logical layer** for review clarity (conventional commits, NO `Co-Authored-By`, NO AI attribution — operator decision 12; sophia CLAUDE.md output style).

## Affected Areas

| Area | Impact | Description |
|---|---|---|
| `migrations/postgres/010_skills_lifecycle.{up,down}.sql` | NEW | Schema-only ALTER + UNIQUE swap; reversible |
| `internal/domain/skill/skill.go` | MODIFIED | Lifecycle fields + invariants + new `Update` signature |
| `internal/adapters/outbound/pg/skill_repo.go` | MODIFIED | Extend SQL + `scanSkill` + `status='active'` filter in `FindByPhase` |
| `internal/adapters/outbound/pg/skill_matcher.go` | NEW | `SkillMatcher` PG adapter (algorithm + doublestar glob) |
| `internal/adapters/outbound/pg/testdata/skill_phase_baseline/*.golden.json` | NEW | 9 regression snapshots (inert; outside LoC budget) |
| `internal/bootstrap/seed_skills.go` | MODIFIED | `InsertIfAbsent` → `Upsert` with V4.1 §7 payload |
| `internal/application/skill/matcher/` | NEW package | Algorithm + tests |
| `internal/application/discipline/skill_provider.go` | MODIFIED | `// Deprecated:` godoc; delegate to `SkillsForContext` |
| `internal/application/discipline/skill_matcher.go` | NEW | Port interface + `SkillQuery` + `SkippedSkill` + `StructuralContextRef` types |
| `internal/ports/outbound/repository.go` | MODIFIED | `SkillRepository` interface extended |
| `go.mod` + `go.sum` | MODIFIED | Add `github.com/bmatcuk/doublestar/v4` |
| Unit tests for matcher (scope/applies_when/sort/skip) | NEW | Strict TDD; RED-first per capability |
| Integration test (testcontainers PG, full flow) | NEW | End-to-end migration → seeder → matcher |
| Regression snapshot test | NEW | `SkillsForPhase` byte-equivalent pre/post M1 |
| ADR (skills-lifecycle-matcher) | NEW | Records doublestar choice, Option B backfill, nil-only `StructuralContextRef` |

## Risks

| # | Risk | Likelihood | Mitigation |
|---|---|---|---|
| R1 | `StructuralContext` import cycle (`init/detector` ↔ `discipline`) | Med | Option D (nil-only opaque `*StructuralContextRef` in `discipline`) — mirror M0.5; operator decision 6 |
| R2 | `doublestar/v4` not in `go.mod` | Low | `go get github.com/bmatcuk/doublestar/v4`; standard procedure; explore §7 |
| R3 | UNIQUE constraint swap window during ALTER | Low | golang-migrate auto-wraps each `.up.sql` in a transaction; DROP+ADD atomic; explore §8 |
| R4 | `scanSkill` must extend atomically with migration deploy | Med | Standard rolling-deploy: migration BEFORE new binary serves traffic; explore §14 R4 |
| R5 | `FindByPhase` polluted by future non-active skills | Low | Add `AND status = 'active'` filter; seeds backfilled as `active`; operator decision 7 |
| R6 | ~1670 LoC (`size:exception`) | High | Declared explicitly in PR body; INIT-0 PR2 precedent; operator decision 1; explore §13 |
| R7 | Backfill of 9 seeds via Upsert depends on bootstrap of new binary | Low | Old binary's `InsertIfAbsent` keeps working with defaults during rolling deploy; new binary upserts correct payload on next restart; explore §14 R7 |
| R8 | `doublestar/v4` `**` semantics differ from `filepath.Match` | Low | Documented in matcher package; unit-tested per pattern shape; explore §14 R8 |
| R9 | Regression vs PR #76 in `SkillsForPhase` | High | Snapshot tests on 9 phases assert byte-equivalence; CI gate; operator decision 9 |
| R10 | INIT-0 push-time lint/test surprises | Low | `make lint` + `make test-unit` pre-push; `.gitkeep` for empty testdata dirs; INIT-0 lessons #1+ |

## Rollback Plan

- Single PR revert reverses everything atomically:
  - Migration 010 down restores the pre-M1 schema (drops lifecycle/metrics/scope/applies_when columns; DROP `skills_name_version_unique`; ADD `skills_name_unique`).
  - Go code reverts to pre-M1 (no lifecycle fields, no matcher package, no doublestar dependency).
  - `seed_skills.go` reverts to `InsertIfAbsent`.
  - 9 seeds revert to pre-lifecycle shape (no metrics, no scope, no applies_when).
- M2 / M3 work blocked until M1 is re-applied.
- Coordination: revert PR + run down migration on staging first, then production. No data loss because seeds are reproducible from `seed_skills.go`.

## Dependencies

- PR #76 (migration 009) already merged and live — REQUIRED baseline.
- `github.com/bmatcuk/doublestar/v4` — NEW external dependency added in this PR.
- INIT-0 PR2 precedent for `size:exception` workflow — already accepted by operator.
- No cross-repo coupling (sophia-memory-engine, governance, runtime-adapters unchanged).

## Success Criteria

V4.1 §16 verbatim (operator decisions 1-12 + operator-added 9-12):

- [ ] Migration aplicable y reversible (down migration funcional).
- [ ] 9 seeds migradas con `status=active`, `activation_source=legacy_seed`.
- [ ] 0 seeds duplicadas en BD (idempotente via `UNIQUE (name, version)`).
- [ ] `SkillsForPhase(phase)` sigue devolviendo los mismos skills que antes (regresión cero vs PR #76).
- [ ] `SkillsForContext(query)` funciona con filtros `scope` + `applies_when`.
- [ ] `go test` verde en todos los packages tocados.
- [ ] Sin downtime en deploy.
- [ ] `Upsert` tiene al menos 1 caller no-test (seeder).
- [ ] `golangci-lint` clean (INIT-0 lesson #1).
- [ ] `github.com/bmatcuk/doublestar/v4` añadido a `go.mod`.
- [ ] `SkillQuery.StructuralContext` nil-only (mirror M0.5 Option D).
- [ ] Regression snapshot tests on 9 seeds pass byte-exact.

## Open Questions

None. All 12 operator decisions are locked (single PR with `size:exception`, `doublestar/v4`, schema-only migration 010, seeder Upsert backfill, matcher package location, nil-only `StructuralContextRef`, `status='active'` filter, `SkillsForPhase` deprecation wrapper, regression snapshots, goldens outside LoC budget, strict TDD, conventional commits without attribution).

## Strict TDD Note

`strict_tdd` is TRUE for this repo. Spec MUST define test-first acceptance per capability. Apply phase follows `strict-tdd.md`: failing test FIRST, then minimal production code. Migration 010 has its own pattern — integration test applies up → verifies schema (columns + UNIQUE swap), applies down → verifies revert (columns dropped, original UNIQUE restored).
