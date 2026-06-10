# Tasks: skills-lifecycle-matcher (M1)

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines (code+test+ADR) | 1500-1800 |
| Estimated golden fixture data (inert) | ~100 |
| Total LoC | ~1700 |
| 400-line budget risk | High |
| Chained PRs recommended | No (operator pre-approved size:exception in proposal) |
| Suggested split | None (hard dependency chain: migration ↔ domain ↔ repo ↔ seeder ↔ matcher) |
| Delivery strategy | ask-on-risk |
| Decision needed before apply | No (operator already locked size:exception) |
| Chain strategy | n/a (single PR) |
| Notes | size:exception precedent INIT-0 PR2; ~1700 LoC justified by atomic milestone with hard dep chain |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: size-exception
400-line budget risk: High

## Cross-repo PR strategy

None — single repo.

## Locked design decisions absorbed

1. V4.1 §5.2 enums verbatim: Status (6 values: candidate, validated, active, deprecated, blocked, archived), ActivationSource (5 values: manual, legacy_seed, archive_worker, llm_proposal, imported), RiskLevel (4 values: low, medium, high, critical) — CORRECTED FROM EARLIER SPEC DIVERGENCE
2. StructuralContextRef REUSE from discipline package (M0.5 declared in `prior_context.go`) — DO NOT redeclare
3. `github.com/bmatcuk/doublestar/v4` added to go.mod
4. Seeder backfill Option B: switch InsertIfAbsent → Upsert with V4.1 §7 legacy payload
5. SkillMatcher port in `internal/application/discipline/skill_matcher.go`; PG adapter in `internal/adapters/outbound/pg/skill_matcher.go`
6. Regression snapshot test as BLOCKING gate (D-M1-8)
7. Strict commit ordering inside single PR per D-M1-9
8. Rolling-deploy ordering documented in PR body per D-M1-10
9. Clock injection into seeder per D-M1-12 / CLAUDE.md rule #5

## Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| 1 | All groups A–L | Single PR (size:exception) | Hard dep chain; operator pre-approved |

---

## Group A — Baseline regression snapshot capture (HARD GATE — must be FIRST)

- [x] A.1 Verify `git status` shows no uncommitted tracked file changes (clean tree before capture)
- [x] A.2 Read `migrations/postgres/009_skills.up.sql` + grep `SkillsForPhase` callsites to confirm pristine pre-M1 state
- [x] A.3 Create `internal/adapters/outbound/pg/testdata/skill_phase_baseline/` + add `.gitkeep` to commit the empty dir (INIT-0 lesson #2)
- [x] A.4 Write `internal/adapters/outbound/pg/skill_phase_baseline_capture_test.go`: test gated by `GOLDEN_UPDATE=1` env var; calls `provider.SkillsForPhase` for each of 9 phases; writes `testdata/skill_phase_baseline/<phase>.golden.json` with `[{"skill_id":"...","content_sha256":"<hex>"}]` sorted by `skill_id` asc
- [x] A.5 Run `GOLDEN_UPDATE=1 go test -tags=integration -run TestSkillPhaseBaselineCapture ./internal/adapters/outbound/pg/ -count=1` against migration-009-only DB to write 9 golden files
- [x] A.6 Inspect all 9 golden files — confirm they reflect PR #76 reality (non-empty, correct phase membership)
- [x] A.7 Commit baseline: `test(pg): capture pre-M1 SkillsForPhase regression baseline`
- [ ] A.8 CHECKPOINT — operator eyeballs goldens and approves before any production file changes

---

## Group B — Foundation: doublestar/v4 dep + StructuralContextRef confirmation

- [ ] B.1 Run `go get github.com/bmatcuk/doublestar/v4@latest` in repo root
- [ ] B.2 Verify `go.mod` and `go.sum` updated with the new dependency
- [ ] B.3 Read `internal/application/discipline/prior_context.go` line ~64 and confirm `StructuralContextRef` is declared there; record exact import path for use in Group G — DO NOT redeclare
- [ ] B.4 Commit: `chore(deps): add github.com/bmatcuk/doublestar/v4 to go.mod`

---

## Group C — Migration 010 SQL (spec: skills-schema-migration-010)

- [ ] C.1 **(RED)** Write `internal/adapters/outbound/pg/migration_010_test.go` (build tag `integration`): test asserts pre-migration schema has 7 columns + `skills_name_unique` constraint + no lifecycle columns
- [ ] C.2 Write `migrations/postgres/010_skills_lifecycle.up.sql` per design D-M1-1: ALTER TABLE adds 9 columns with safe defaults; 3 CHECK constraints (Status 6 values, RiskLevel 4, ActivationSource 5); 3 indexes (idx_skills_status, idx_skills_scope_gin GIN, idx_skills_applies_gin GIN); DROP `skills_name_unique`; ADD `skills_name_version_unique UNIQUE (name, version)` — all in one file (golang-migrate wraps in transaction)
- [ ] C.3 Write `migrations/postgres/010_skills_lifecycle.down.sql` per design D-M1-1: DROP `skills_name_version_unique`; ADD `skills_name_unique`; DROP 3 indexes IF EXISTS; ALTER TABLE DROP all 3 CHECK constraints + 9 lifecycle columns — all with IF EXISTS guards (spec: idempotent down)
- [ ] C.4 **(GREEN)** Extend `migration_010_test.go`: assert post-up has 9 new columns in `information_schema.columns`, `skills_name_version_unique` in `pg_constraint`, `skills_name_unique` absent, 3 indexes present in `pg_indexes`
- [ ] C.5 **(GREEN)** Extend `migration_010_test.go`: assert up+down round-trip leaves schema identical to post-009 baseline (9 lifecycle columns absent, `skills_name_unique` restored, 3 indexes dropped)
- [ ] C.6 **(GREEN)** Extend `migration_010_test.go`: assert idempotent re-run of down migration raises no error
- [ ] C.7 **(GREEN)** Extend `migration_010_test.go`: assert all 6 status values accepted by CHECK; assert invalid value `'unknown'` rejected; assert all 5 activation_source values accepted; assert all 4 risk_level values accepted
- [ ] C.8 Run `go test -tags=integration ./internal/adapters/outbound/pg/ -run TestMigration010` — verify all green via testcontainers
- [ ] C.9 Commit: `feat(pg): migration 010 skills lifecycle up/down + integration test`

---

## Group D — Domain Skill aggregate extension (spec: skills-domain-lifecycle)

- [ ] D.1 **(RED)** Write `internal/domain/skill/lifecycle_test.go`: unit tests asserting `Status.IsValid()` returns true for all 6 valid values and false for an invalid value; same for `RiskLevel` (4 values) and `ActivationSource` (5 values)
- [ ] D.2 **(RED)** Extend `internal/domain/skill/skill_test.go`: unit test `New()` with zero lifecycle input sets Status=candidate, Version=v1, RiskLevel=medium, ActivationSource=manual; LastUsedAt=nil; Metrics counts=0
- [ ] D.3 **(RED)** Extend `skill_test.go`: unit test `New()` with explicit lifecycle fields all returned correctly via getters
- [ ] D.4 **(RED)** Extend `skill_test.go`: unit test `New()` rejects invalid Status (one invalid value); rejects empty Version; rejects invalid RiskLevel; rejects invalid ActivationSource
- [ ] D.5 **(RED)** Extend `skill_test.go`: unit test `Update()` rejects invalid Status; aggregate state unchanged on error
- [ ] D.6 **(RED)** Extend `skill_test.go`: unit test `Update()` valid status transition succeeds (candidate → active)
- [ ] D.7 **(RED)** Extend `skill_test.go`: unit test `Hydrate()` accepts any persisted values without error (including migration-default values)
- [ ] D.8 **(RED)** Extend `skill_test.go`: unit test JSON marshal round-trip for `Scope`, `AppliesWhen`, `Metrics` value types (json.Marshal → json.Unmarshal → field-equal)
- [ ] D.9 **(GREEN)** Write `internal/domain/skill/lifecycle.go`: declare `Status` (6 consts), `ActivationSource` (5 consts), `RiskLevel` (4 consts) string-based types each with `IsValid()` and `String()` methods; declare `Scope`, `AppliesWhen`, `Metrics` structs with JSON tags; declare `LifecycleInput` struct
- [ ] D.10 **(GREEN)** Extend `internal/domain/skill/skill.go`: add 9 unexported lifecycle fields + public getters (`Status()`, `Version()`, `Scope()`, `AppliesWhen()`, `RiskLevel()`, `ActivationSource()`, `Metrics()`, `LastUsedAt()`, `LastValidatedAt()`); update invariant checks in `New()` and `Update()`; update `Hydrate()` signature to accept 9 new params; implement `NewLegacy()` constructor (status=active, source=legacy_seed, v1, medium, scope derived from phases arg)
- [ ] D.11 Run `go test ./internal/domain/skill/...` — verify all unit tests green
- [ ] D.12 Commit: `feat(domain/skill): add lifecycle fields + invariants + Update signature + NewLegacy`

---

## Group E — SkillRepo PG adapter extension (spec: skill-matcher-pg-adapter §FindByPhase)

- [ ] E.1 **(RED)** Extend `internal/adapters/outbound/pg/skill_repo_test.go` (integration): assert `FindByPhase` returns only skill with `status='active'` when table also contains a `status='deprecated'` skill for the same phase
- [ ] E.2 **(RED)** Extend `skill_repo_test.go` (integration): assert `Upsert` ON CONFLICT (name, version) DO UPDATE merges all lifecycle fields correctly; assert idempotent re-run
- [ ] E.3 **(RED)** Extend `skill_repo_test.go` (integration): assert `scanSkill` correctly round-trips non-empty `Scope`, `AppliesWhen`, `Metrics` structs through JSONB columns
- [ ] E.4 **(GREEN)** Update `selectColumns` constant in `skill_repo.go` to include 9 new columns in correct order matching `scanSkill` scan position
- [ ] E.5 **(GREEN)** Implement `scanSkill` JSONB decoders: scan `scopeBytes`, `appliesBytes`, `metricsBytes` as `[]byte`; `json.Unmarshal` into `skill.Scope`, `skill.AppliesWhen`, `skill.Metrics`; pass all 16 params to `skill.Hydrate()`
- [ ] E.6 **(GREEN)** Update `FindByPhase` SQL in `skill_repo.go` line ~32: add `AND status = 'active'` to WHERE clause
- [ ] E.7 **(GREEN)** Update `Upsert` SQL: change ON CONFLICT target from `(id)` to `(name, version)`; add all 9 lifecycle columns to INSERT columns list and DO UPDATE SET clause (16 total cols)
- [ ] E.8 **(GREEN)** Update `InsertIfAbsent` SQL: change ON CONFLICT target from `(name)` to `(name, version)` per migration 010 constraint swap; add 9 lifecycle columns to INSERT list with values from aggregate
- [ ] E.9 **(GREEN)** Update `internal/ports/outbound/repository.go` `SkillRepository` interface godoc: document `FindByPhase` status='active' invariant; document `Upsert` conflicts on (name, version)
- [ ] E.10 Run `go test -tags=integration ./internal/adapters/outbound/pg/ -run TestSkillRepo` — verify all green
- [ ] E.11 Commit: `feat(pg): extend skill_repo for lifecycle columns + status filter + scanSkill JSONB`

---

## Group F — Seeder backfill via Upsert (spec: skills-seeder-backfill)

- [ ] F.1 **(RED)** Write/extend `internal/bootstrap/seed_skills_test.go` (integration): assert seeder against fresh testcontainers PG (with migration 010) inserts exactly 9 rows all with `status='active'` and `activation_source='legacy_seed'`
- [ ] F.2 **(RED)** Extend `seed_skills_test.go`: assert second seeder run is idempotent (count stays 9, no error)
- [ ] F.3 **(RED)** Extend `seed_skills_test.go`: assert each of 9 seeds has `scope.project_id='*'`, `scope.repo_id='*'`, `scope.phases` contains the skill's designated phase
- [ ] F.4 **(RED)** Extend `seed_skills_test.go`: assert seeder fails with an error when lifecycle columns are absent (migration 009 only DB — confirms migration-order dependency)
- [ ] F.5 **(GREEN)** Update `SeedSkills` signature in `internal/bootstrap/seed_skills.go` to accept `clock shared.Clock` parameter
- [ ] F.6 **(GREEN)** Update `buildSeedSkills` in `seed_skills.go`: replace `skill.New(...)` calls with `skill.NewLegacy(id, name, phases, content, techniques, clock.Now().UTC())` for all 9 seeds
- [ ] F.7 **(GREEN)** Switch `seed_skills.go` loop from `repo.InsertIfAbsent(ctx, s)` to `repo.Upsert(ctx, s)` with error wrapping
- [ ] F.8 **(GREEN)** Update `internal/bootstrap/wire.go` `Wire()` function: pass configured `shared.Clock` into `SeedSkills(ctx, skillRepo, clock, logger)` call
- [ ] F.9 Run `go test -tags=integration ./internal/bootstrap/ -run TestSeedSkills` — verify all green
- [ ] F.10 Commit: `feat(bootstrap): switch seeder to Upsert with V4.1 §7 legacy payload`

---

## Group G — SkillMatcher port + types (spec: skill-matcher-port)

- [ ] G.1 **(RED)** Write `internal/application/discipline/skill_matcher_test.go`: unit test `SkillQuery{}` zero-value compiles and is constructable; unit test `SkippedSkill{SkillID: "x", Reason: "scope_mismatch"}` passes `go vet`; unit test confirms `StructuralContextRef` is NOT redeclared (same type as `prior_context.go`)
- [ ] G.2 **(GREEN)** Write `internal/application/discipline/skill_matcher.go`: declare `SkillMatcher` interface (`SkillsForContext(ctx, SkillQuery) ([]*skill.Skill, []SkippedSkill, error)`); declare `SkillQuery` struct with 7 fields (Phase `phase.PhaseType`, ProjectID, RepoID `string`, StructuralContext `*StructuralContextRef`, FeatureType, TouchedPaths `[]string`, RiskLevel `skill.RiskLevel`); declare `SkippedSkill` struct (SkillID, Reason string); declare 3 reason constants (`SkipReasonScopeMismatch`, `SkipReasonAppliesWhenFailed`, `SkipReasonStatusNotActive`) — REUSE `StructuralContextRef` from same package (prior_context.go), do NOT redeclare
- [ ] G.3 Run `go build ./internal/application/discipline/...` — verify zero build errors and no import cycle
- [ ] G.4 Commit: `feat(discipline): declare SkillMatcher port + SkillQuery + SkippedSkill`

---

## Group H — SkillMatcher PG adapter algorithm (spec: skill-matcher-pg-adapter)

- [ ] H.1 **(RED)** Write `internal/adapters/outbound/pg/skill_matcher_test.go` (integration): assert `SkillsForContext` with empty query returns only active skill when DB has one active + one candidate; candidate appears in skipped with `status_not_active`
- [ ] H.2 **(RED)** Extend `skill_matcher_test.go`: assert scope wildcard `project_id="*"` matches any ProjectID in query
- [ ] H.3 **(RED)** Extend `skill_matcher_test.go`: assert scope mismatch (exact project_id ≠ query.ProjectID) returns skipped with `scope_mismatch`
- [ ] H.4 **(RED)** Extend `skill_matcher_test.go`: assert phase filter — skill with phases=["apply","verify"] skipped when query.Phase="spec"
- [ ] H.5 **(RED)** Extend `skill_matcher_test.go`: assert `applies_when.feature_type` mismatch returns skipped with `applies_when_failed`
- [ ] H.6 **(RED)** Write `internal/adapters/outbound/pg/skill_matcher_glob_test.go` (unit, no build tag): assert `doublestar.Match("internal/domain/**", "internal/domain/skill/skill.go")` = true; assert `doublestar.Match("**/*_test.go", "a/b/c_test.go")` = true; assert `doublestar.Match("vendor/**", "vendor/lib/foo.go")` = true (for exclude_paths)
- [ ] H.7 **(RED)** Extend `skill_matcher_test.go` (integration): assert `touched_paths = ["internal/domain/**"]` matches `SkillQuery.TouchedPaths = ["internal/domain/skill/skill.go"]`
- [ ] H.8 **(RED)** Extend `skill_matcher_test.go`: assert `exclude_paths` wins over include — skill with `touched_paths=["**"]` + `exclude_paths=["vendor/**"]` skipped for query `TouchedPaths=["vendor/lib/foo.go"]`
- [ ] H.9 **(RED)** Extend `skill_matcher_test.go`: assert sort order — 3 active skills with risk_level high/low/medium returned in order low, medium, high
- [ ] H.10 **(RED)** Extend `skill_matcher_test.go`: assert full integration query `SkillQuery{Phase:"apply", ProjectID:"*", RepoID:"*"}` against 9 seeded skills returns ≥1 matched; total matched+skipped = total active skills
- [ ] H.11 **(GREEN)** Write `internal/adapters/outbound/pg/skill_matcher.go`: implement `PGSkillMatcher` struct (pool + repo fields); `NewPGSkillMatcher(pool, repo)` constructor; `selectAllActive(ctx)` helper using `LIST`-style query with `WHERE status='active'`; `SkillsForContext` orchestrates status check + `scopeMatches` + `appliesWhenMatches` + `sortSkills`
- [ ] H.12 **(GREEN)** Implement `scopeMatches(scope skill.Scope, q discipline.SkillQuery) bool`: phase membership check (when q.Phase non-empty); project_id wildcard/exact; repo_id wildcard/exact
- [ ] H.13 **(GREEN)** Implement `appliesWhenMatches(a skill.AppliesWhen, q discipline.SkillQuery) bool`: feature_type list inclusion; touched_paths glob via `doublestar.Match`; exclude_paths wins via `anyGlobMatch`
- [ ] H.14 **(GREEN)** Implement `sortSkills(skills []*skill.Skill)`: custom RiskLevel enum ordering (low=0 < medium=1 < high=2 < critical=3); then last_validated_at desc NULLs last; then usage_count desc; then id asc (stable tiebreaker)
- [ ] H.15 Run `go test -tags=integration ./internal/adapters/outbound/pg/ -run TestPGSkillMatcher` and `go test ./internal/adapters/outbound/pg/ -run TestSkillMatcherGlob` — verify all green
- [ ] H.16 Commit: `feat(pg): implement SkillMatcher PG adapter (scope + applies_when + sort + skip)`

---

## Group I — SkillsForPhase deprecation wrapper (spec: skills-for-phase-deprecation)

- [ ] I.1 **(RED)** Write/extend `internal/adapters/outbound/pg/skill_provider_test.go` (integration): assert `SkillsForPhase(ctx, "apply")` result equals `SkillsForContext(ctx, SkillQuery{Phase: "apply"})` result's `[]*skill.Skill` slice
- [ ] I.2 **(RED)** Extend `skill_provider_test.go`: assert error from underlying `SkillsForContext` propagates unchanged through `SkillsForPhase`
- [ ] I.3 **(GREEN)** Update `internal/adapters/outbound/pg/skill_provider.go`: change `SkillProvider` struct to hold `discipline.SkillMatcher` instead of `*SkillRepo` directly; update `NewSkillProvider(matcher discipline.SkillMatcher)` constructor; implement `SkillsForPhase` as thin deprecated wrapper: `skills, _, err := p.matcher.SkillsForContext(ctx, discipline.SkillQuery{Phase: pt}); return skills, err`
- [ ] I.4 **(GREEN)** Add `// Deprecated: Use SkillMatcher.SkillsForContext(SkillQuery{Phase: phase}) instead. SkillsForPhase will be removed in M3.` godoc to the `SkillsForPhase` method
- [ ] I.5 Verify the 3 known callsites (`internal/application/phase/service.go:396`, `internal/application/apply/teamlead.go:594/383/482`) still compile unchanged: `go build ./internal/application/phase/... ./internal/application/apply/...`
- [ ] I.6 Commit: `refactor(discipline): SkillsForPhase deprecated wrapper around SkillsForContext`

---

## Group J — Regression snapshot test BLOCKING gate (spec: skills-regression-snapshot)

- [ ] J.1 Write `internal/adapters/outbound/pg/skill_provider_regression_test.go` (build tag `integration`): iterates all 9 `phase.AllPhaseTypes()` values; for each calls `provider.SkillsForPhase(ctx, pt)`; computes `serializeForGolden` result (sorted by skill_id asc; each entry: `{"skill_id":"...","content_sha256":"<hex>"}`) ; compares against `testdata/skill_phase_baseline/<phase>.golden.json` via `require.JSONEq`; fails fast with phase name + diff if missing golden file
- [ ] J.2 **(GREEN)** Run the regression test WITHOUT `GOLDEN_UPDATE=1` against the fully-migrated testcontainers DB (migration 010 applied + seeder run): `go test -tags=integration -run TestSkillProvider_SkillsForPhase_RegressionZero ./internal/adapters/outbound/pg/ -count=1`
- [ ] J.3 IF any of the 9 phases diverges — STOP; do NOT proceed; report phase name + diff to operator; M1 has broken PR #76 behavior
- [ ] J.4 Verify all 9 snapshot tests pass green
- [ ] J.5 Commit: `test(pg): regression snapshot for SkillsForPhase on 9 phases (BLOCKING gate)`

---

## Group K — Wire + ADR

- [ ] K.1 **(GREEN)** Update `internal/bootstrap/wire.go`: construct `pg.NewSkillRepo(pool)` → construct `pg.NewPGSkillMatcher(pool, skillRepo)` → construct `pg.NewSkillProvider(matcher)` → inject `matcher` and `provider` into `phase.Service` / `apply.RunService` deps as required
- [ ] K.2 Verify full project compiles: `go build ./...` — zero errors
- [ ] K.3 Write `docs/adr/0011-skills-lifecycle-matcher.md` (or next sequential number): record rationale for D-M1-1 through D-M1-12 (migration schema-only, doublestar/v4 choice, Option B backfill, nil-only StructuralContextRef, status='active' filter, deprecated wrapper, rolling-deploy contract, Clock injection)
- [ ] K.4 Commit: `docs(adr): 0011 skills-lifecycle-matcher design decisions`

---

## Group L — Cross-cutting verification

- [ ] L.1 Run `make test-unit` — assert green (all domain + unit tests)
- [ ] L.2 Run `make test-integration` — assert green (all integration tests including regression snapshot)
- [ ] L.3 Run `golangci-lint run` — assert 0 issues (INIT-0 lesson #1 — do not defer lint)
- [ ] L.4 Run `rg "\.Upsert\(" internal/` — confirm ≥1 non-test caller (seed_skills.go)
- [ ] L.5 Run `git log --format="%s" | head -15` — confirm no `Co-Authored-By` or AI attribution in any commit message
- [ ] L.6 Confirm each commit message follows conventional commit format (`feat(scope):`, `test(scope):`, `chore(scope):`, `refactor(scope):`, `docs(scope):`)
- [ ] L.7 Draft PR body and confirm `size:exception` is declared with justification (precedent: INIT-0 PR2; hard dep chain; ~1700 LoC)
- [ ] L.8 FINAL CHECKPOINT — operator approves then: `git push origin <branch> && gh pr create`

---

## Strict TDD discipline

Every GREEN step must be preceded by a RED step. Group A (baseline capture) is the OUTER hard gate — no production file changes until A.8 is operator-approved. Within Groups C–I each capability follows RED (failing test) → GREEN (minimal production code) → commit.

## Dependency order summary

```
A (baseline capture, hard gate)
  ↓
B (doublestar/v4 dep)
  ↓
C (migration 010 SQL + integration test)
  ↓
D (domain lifecycle + unit tests)
  ↓
E (skill_repo extension + integration tests)
  ↓
F (seeder Upsert backfill + integration tests)
  ↓
G (SkillMatcher port + types)
  ↓
H (SkillMatcher PG adapter + unit + integration tests)
  ↓
I (SkillsForPhase deprecation wrapper)
  ↓
J (regression snapshot BLOCKING gate)
  ↓
K (wire + ADR)
  ↓
L (cross-cutting verification + PR)
```

## Out of scope reminders

- StructuralContext consumption beyond nil-only marker (M3)
- Migration of non-seed skills (none exist today)
- Promotion / demotion policy logic (M2)
- LLM-driven skill creation (anti-pattern V4.1 D11)
- AllowlistEnforcer wiring (M-LATER)
- Token budget enforcement (M3)
- Source attribution rendering (M3)
- Episodes / digests / business rules / routines population (M3)
- SkillsForPhase removal (M3)
- In-memory SkillMatcher adapter (M2/M3)
- Index pushdown optimizations for GIN(scope) / GIN(applies_when) (M2+)
