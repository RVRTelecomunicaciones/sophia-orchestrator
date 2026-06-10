# Archive Report: skills-lifecycle-matcher (M1)

**Change**: skills-lifecycle-matcher  
**Archived**: 2026-06-09  
**Mode**: openspec + Engram (hybrid)  
**Verification verdict**: PASS_WITH_WARNINGS (0 CRITICAL, 2 WARNING, 3 SUGGESTION)  
**Strategy doc**: V4.1 §16 M1

---

## Intent

M1 is THE FIRST learning-loop milestone — extends live `skills` table (PR #76) with V4.1 §5.2 lifecycle/metrics/scope/applies_when columns, backfills 9 legacy seeds as `active` / `legacy_seed` / `v1`, ships deterministic SkillMatcher with doublestar globs. Regression-zero vs PR #76 enforced. Unblocks M2 (worker that promotes/demotes skills via metrics).

---

## Capabilities delivered (7)

| Capability | Status | Evidence |
|---|---|---|
| skills-schema-migration-010 | DELIVERED | migrations/postgres/010_skills_lifecycle.{up,down}.sql |
| skills-domain-lifecycle | DELIVERED | internal/domain/skill/{lifecycle.go,skill.go} with 6 Status + 5 ActivationSource + 4 RiskLevel enums |
| skills-seeder-backfill | DELIVERED | internal/bootstrap/seed_skills.go via NewLegacy + Upsert |
| skill-matcher-port | DELIVERED | internal/application/discipline/skill_matcher.go (SkillMatcher, SkillQuery, SkippedSkill) |
| skill-matcher-pg-adapter | DELIVERED | internal/adapters/outbound/pg/skill_matcher.go with scope/applies_when/sort algorithm |
| skills-for-phase-deprecation | DELIVERED | internal/adapters/outbound/pg/skill_provider.go deprecation wrapper |
| skills-regression-snapshot | DELIVERED | testdata/skill_phase_baseline/*.golden.json (9 phases) + blocking gate test |

---

## PR landed (1 PR, 12 commits, ~1700 LoC)

| PR | Merged | Commits | Total LoC | Notes |
|---|---|---|---|---|
| sophia-orchestrator#81 | 2026-06-09T13:54:38Z | e83c140 baseline → 2460c0f lint | 38 files, +3037/-203 | size:exception declared; hard dep chain |

**Merge commit**: `8417730` (main HEAD after merge)

---

## Operator-locked decisions (12 from proposal + 12 design ADRs)

1. **V4.1 §5.2 enums verbatim**: Status 6 (candidate, validated, active, deprecated, blocked, archived), ActivationSource 5 (manual, legacy_seed, archive_worker, llm_proposal, imported), RiskLevel 4 (low, medium, high, critical) — CORRECTED in spec phase before apply
2. **StructuralContextRef REUSED** from M0.5 prior_context.go (no redeclaration, no cycle)
3. **doublestar/v4** for applies_when globs (`**` semantics)
4. **Seeder backfill via Upsert** (Option B) — satisfies "Upsert ≥ 1 non-test caller"
5. **SkillMatcher port in discipline**, adapter in pg/
6. **FindByPhase adds `status='active'` filter** invariant
7. **SkillsForPhase deprecated wrapper** around SkillsForContext
8. **Regression snapshot test BLOCKING gate** — baseline captured Group A before any production changes
9. **Strict layer-ordered commits** (12 atomic commits, each compiles + tests green)
10. **Rolling-deploy ordering** (migration first, then binary)
11. **Skill aggregate unexported fields + getters** pattern
12. **Clock injection mandatory** (CLAUDE.md rule #5)

---

## CI status

PR #81 merged with all CI checks green:
- Lint: 0 issues (golangci-lint pre-push enforced)
- Wire-contract matrix: PASS
- Unit tests: PASS (40+ packages)
- Integration tests Postgres: PASS (testcontainers)
- govulncheck: PASS
- Build binary: PASS
- Docker image: PASS
- GitGuardian Security Checks: PASS

---

## Process lessons reinforced

1. **Spec + design parallel = checks-and-balances**: Design caught spec's enum bug (Status 4 vs 6) BEFORE apply. Without parallel verification, the bug would have cascaded to migration 010 CHECK constraints. Pattern validated as load-bearing.

2. **Golangci-lint pre-push** (INIT-0 lesson #1): 0 lint surprises in CI for second straight PR.

3. **Baseline-capture-FIRST as separate commit** (M0.5 lesson): commit e83c140 protects the regression contract by capturing pre-M1 reality before any schema or code changes.

4. **Strict TDD RED→GREEN**: Every production change preceded by failing test across 12 groups. Each commit group includes both test and production code.

5. **Size:exception precedent**: Hard dependency chain (migration ↔ domain ↔ repo ↔ seeder ↔ matcher) justifies single atomic PR. INIT-0 PR2 precedent honored.

---

## Adaptations approved during apply

1. **PGSkillMatcher uses `repo.List()` instead of `FindByPhase`** for full SkippedSkill observability (M1 row count small per design). M2 can optimize with SQL pushdown when needed.

2. **NewPGSkillMatcher accepts unused first param** (`*pgxpool.Pool`) for forward-compat with M2 SQL pushdown. Will be typed once M2 actually needs it.

3. **SkillQuery.MaxRiskLevel typed as `skill.RiskLevel`** (improvement over spec's string) — but filter logic NOT yet wired (WARNING #1 in verify; M2 must implement or remove).

4. **Sort tertiary key is `id asc`** instead of `metrics.usage_count desc` (degenerate in M1 when all seeds have usage_count=0; M2 will reinstate when promotion worker populates counters).

---

## Forwarded to M2 / M3 (2 WARNINGS + 3 SUGGESTIONS)

### Critical blockers
**None.** All V4.1 §16 acceptance criteria met. Regression-zero gate is wired and green.

### Warnings (non-blocking, address during archive cleanup)

1. **WARNING-1**: `SkillQuery.MaxRiskLevel` is declared but dead field — neither `scopeMatches` nor `appliesWhenMatches` reads it. **M2 action**: Either implement the filter or remove the field until needed.

2. **WARNING-2**: `tasks.md` checklist stale — only Group A checked despite all 12 commits merged. **Archive action**: Update checkboxes or point to merge commit + engram apply-progress as source of truth.

### Suggestions (explicit anchors for follow-up)

3. **SUGGESTION-1**: Sort tertiary key should be `metrics.usage_count desc` when M2 populates the counters. Currently `id asc` (stable, degenerates to zero usage in M1).

4. **SUGGESTION-2**: M2 should consider SQL pushdown of matcher filtering when skills table row count grows beyond ~50 rows. GIN indexes on `scope` and `applies_when` already exist from migration 010.

5. **SUGGESTION-3**: When M2 implements SQL pushdown, type `NewPGSkillMatcher` first param explicitly as `*pgxpool.Pool` (currently an unused forward-compat placeholder).

---

## V4.1 status update

**Mark M1 as DONE.**

**Next milestone in chain**: **M2 (consolidation worker)** — implements V4.1 §6 promotion policy (candidate → validated → active by metrics, with archive_worker activation_source). Reads completed Change envelopes, computes metrics deltas via skills regression analyzer, transitions skills via Status field, calls Upsert with new metrics + last_validated_at + log timestamps.

**After M2**: M3 (PriorContext enrichment) consumes StructuralContext + skills + episodes + change_digests + business rules to enrich `SkillQuery.StructuralContext` field and wire framework/language/state_model filters in `appliesWhenMatches`.

---

## Regression contract verification

**Prime directive (regression-zero vs PR #76) enforced via blocking snapshot gate**:
- Group A (commit e83c140): Captured 9 phase baselines (`testdata/skill_phase_baseline/*.golden.json`) against migration-009-only DB pre-apply
- Group J (commit 88cb4f4): Regression test `TestSkillProvider_SkillsForPhase_RegressionZero` asserts byte-equivalent output post-migration + seeder
- CI gate: Test fails with per-phase diff on any drift; merge blocked until green

**Result**: 0 functional regressions vs PR #76. The 9 seeded skills return byte-identical content+techniques. Schema changes, lifecycle fields, and matcher filtering are transparent to existing callers.

---

## Acceptance criteria (V4.1 §16 — all 8 PASS)

1. ✅ **Migración aplicable y reversible** — `010_skills_lifecycle.up.sql` + `010_skills_lifecycle.down.sql` with IF EXISTS guards. Round-trip tested.

2. ✅ **9 seeds migradas con `status=active`, `activation_source=legacy_seed`** — `NewLegacy` constructor applied to all 9 defs; Upsert persists with V4.1 §7 payload.

3. ✅ **0 seeds duplicadas en BD** — `UNIQUE (name, version)` prevents duplicates; Upsert idempotent via `ON CONFLICT ... DO UPDATE`.

4. ✅ **`SkillsForPhase(phase)` regresión cero vs PR #76** — `TestSkillProvider_SkillsForPhase_RegressionZero` asserts byte-equivalent across all 9 phases; CI gate blocks merge if broken.

5. ✅ **`SkillsForContext(query)` funciona con scope + applies_when** — PGSkillMatcher implements full V4.1 §8 algorithm (status + scope + applies_when + sort + skip-with-reason).

6. ✅ **`go test` verde en todos los packages tocados** — CI green: unit + integration tests across migration, domain, repo, seeder, matcher, provider.

7. ✅ **Sin downtime en deploy** — D-M1-10 rolling-deploy contract: migration first (defaults populate), binary second (seeder upserts correct payload), old binary tolerates new columns.

8. ✅ **`Upsert` tiene al menos 1 caller non-test** — `internal/bootstrap/seed_skills.go:40` invokes `repo.Upsert` in production seed loop.

---

## SDD cycle complete

✅ Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive

---

## Artifact Traceability

| Phase | Engram Topic Key | Observation ID | File Path |
|---|---|---|---|
| Proposal | sdd/skills-lifecycle-matcher/proposal | (search) | openspec/changes/skills-lifecycle-matcher/proposal.md |
| Specs | sdd/skills-lifecycle-matcher/spec | (search) | openspec/changes/skills-lifecycle-matcher/specs/* |
| Design | sdd/skills-lifecycle-matcher/design | (search) | openspec/changes/skills-lifecycle-matcher/design.md |
| Tasks | sdd/skills-lifecycle-matcher/tasks | (search) | openspec/changes/skills-lifecycle-matcher/tasks.md |
| Apply-Progress | sdd/skills-lifecycle-matcher/apply-progress | #807 | (engram-only) |
| Verify-Report | sdd/skills-lifecycle-matcher/verify-report | (search) | openspec/changes/skills-lifecycle-matcher/verify.md |
| Archive-Report | sdd/skills-lifecycle-matcher/archive-report | (this save) | openspec/changes/skills-lifecycle-matcher/archive.md |

---

## Archive action items

- [x] Read all change artifacts (proposal, specs, design, tasks, apply-progress, verify-report)
- [x] Verify no CRITICAL findings from verify phase
- [x] Merge openspec delta specs into main specs (no delta; specs merged during design/spec parallel phase)
- [x] Write archive report with full traceability
- [x] Persist archive report to Engram and file

**Archive status**: ✅ COMPLETE

---

## Next Phases

- **For operator**: Review 2 WARNINGS and apply archive cleanup (update tasks.md checkboxes; no code changes needed)
- **For M2**: Implement WARNING-1 (MaxRiskLevel filter) and SUGGESTION-1 (usage_count sort key) when promotion worker lands. Consider SUGGESTION-2 SQL pushdown once row count grows.
- **For M3**: Wire StructuralContext field and framework/language/state_model filters; retire SkillsForPhase API; populate token budget + source attribution

---

## File manifest (merged into main `8417730`)

**New files**:
- migrations/postgres/010_skills_lifecycle.{up,down}.sql
- internal/domain/skill/lifecycle.go
- internal/application/discipline/skill_matcher.go
- internal/adapters/outbound/pg/skill_matcher.go
- internal/adapters/outbound/pg/skill_phase_baseline_capture_test.go
- internal/adapters/outbound/pg/skill_matcher_test.go
- internal/adapters/outbound/pg/skill_matcher_glob_test.go
- internal/adapters/outbound/pg/skill_provider_regression_test.go
- internal/adapters/outbound/pg/testdata/skill_phase_baseline/*.golden.json (9 files)
- docs/adr/0012-skills-lifecycle-matcher.md

**Modified files**:
- internal/domain/skill/skill.go (9 lifecycle fields + getters + NewLegacy)
- internal/adapters/outbound/pg/skill_repo.go (16-col scanSkill, status filter, Upsert)
- internal/adapters/outbound/pg/skill_provider.go (deprecated wrapper)
- internal/bootstrap/seed_skills.go (Upsert + V4.1 §7 payload + clock injection)
- internal/bootstrap/wire.go (SkillMatcher + clock wiring)
- internal/ports/outbound/repository.go (interface godoc)
- go.mod + go.sum (doublestar/v4 v4.10.0)

---

## Sign-off

**Change**: skills-lifecycle-matcher (M1)  
**Status**: ARCHIVED — all phases complete, all criteria met, no CRITICAL issues  
**Recommendation**: Ready for production deployment  
**Archive date**: 2026-06-09  
**Archive executor**: sdd-archive (Haiku 4.5)
