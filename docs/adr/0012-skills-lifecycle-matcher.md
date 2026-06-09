# ADR-0012: Skills Lifecycle Matcher (M1)

**Status**: Accepted  
**Date**: 2026-06-09  
**Change**: `skills-lifecycle-matcher` (M1)

---

## Context

M1 extends the Skill aggregate with V4.1 §5.2 lifecycle fields and introduces
context-aware skill selection via `SkillMatcher.SkillsForContext`. Prior to M1,
`SkillsForPhase` returned all skills for a phase with no status, scope, or
applies_when filtering.

---

## Decisions

### D-M1-1 — Migration 010: schema-only, single transaction

`010_skills_lifecycle.up.sql` adds 9 lifecycle columns, 3 CHECK constraints,
3 indexes, and swaps the UNIQUE constraint from `(name)` to `(name, version)` in
one file. `golang-migrate` wraps each file in a transaction, providing atomic
rollback on any failure. Down migration uses `IF EXISTS` guards for idempotency.

**Rejected**: separate migrations per column — reduces atomicity and increases
operator intervention risk during partial rollback.

### D-M1-2 — V4.1 §5.2 enum values verbatim

- `Status`: candidate, validated, active, deprecated, blocked, archived (6 values)
- `ActivationSource`: manual, legacy_seed, archive_worker, llm_proposal, imported (5 values)
- `RiskLevel`: low, medium, high, critical (4 values)

Enforced by PG CHECK constraints (migration 010) and `IsValid()` domain methods.
Cross-layer enforcement prevents invalid values from persisting or being returned.

### D-M1-3 — doublestar/v4 for glob matching

`github.com/bmatcuk/doublestar/v4` provides `**` recursive glob matching
(equivalent to .gitignore semantics) for `applies_when.touched_paths` and
`applies_when.exclude_paths`.

**Rejected**: `path/filepath.Match` — no `**` support; `regexp` — overly complex
for path patterns; manual prefix-matching — incomplete for nested paths.

### D-M1-4 — Option B: Upsert seeder (replaces InsertIfAbsent)

`SeedSkills` uses `Upsert` (ON CONFLICT (name, version) DO UPDATE) with the
V4.1 §7 legacy payload: status=active, version=v1, activation_source=legacy_seed,
risk_level=medium, scope={project_id:*, repo_id:*, phases:[<phase>]}.

Running the seeder multiple times is idempotent via the (name, version) conflict
key. Operator customizations are scoped to bumping the version field.

**Rejected**: Option A (keep InsertIfAbsent) — would leave existing rows with
stale lifecycle defaults (status=candidate) and break the D-M1-6 FindByPhase
status='active' invariant on first restart.

### D-M1-5 — SkillMatcher port in discipline package

`discipline.SkillMatcher` declares `SkillsForContext(ctx, SkillQuery)` returning
`([]*skill.Skill, []SkippedSkill, error)`. The skipped list enables full
observability of the filtering trace without business logic in the port.

The port lives in `internal/application/discipline/` alongside `SkillProvider`,
keeping the application layer as the sole boundary for skill-selection contracts.

### D-M1-6 — status='active' filter is a hard-coded invariant

`FindByPhase` and `SkillsForContext` MUST NEVER return non-active skills.
`PGSkillMatcher` enforces this as a defence-in-depth client-side check after
`repo.List()`. Any future SQL pre-filter optimization must preserve this invariant.

### D-M1-7 — StructuralContextRef nil-only in M1

`SkillQuery.StructuralContext *StructuralContextRef` is the M3 wiring point.
`StructuralContextRef` is reused from `prior_context.go` (same package);
no redeclaration. The adapter silently ignores this field in M1.

**Rejected**: introducing M3 filtering logic in M1 — premature complexity with
no spec or test coverage.

### D-M1-8 — Regression golden snapshot as BLOCKING gate

`TestSkillProvider_SkillsForPhase_RegressionZero` compares post-M1 output
against the pre-M1 golden files captured in Group A (commit e83c140).
This test MUST pass before any PR is merged. Any divergence = blocked.

Captures `{skill_id, content_sha256}` sorted by skill_id — minimal stable
format that detects ID or content regressions without embedding full skill text.

### D-M1-9 — Strict commit ordering inside single PR

Commits follow the Group A → L dependency chain exactly. No squash.
Work-unit commits serve as rollback markers if a regression is detected after merge.

### D-M1-10 — Rolling-deploy contract

1. Deploy migration 010 (adds lifecycle columns with safe defaults) before code.
2. Old code continues to read/write the 7 pre-010 columns — new columns are
   optional with default values, invisible to old code.
3. Deploy new code (reads/writes all 16 columns).
4. No backward-incompatible window.

### D-M1-11 — SkillsForPhase deprecated, removal in M3

`SkillProvider.SkillsForPhase` is a thin wrapper around `SkillsForContext(SkillQuery{Phase: pt})`.
It is marked deprecated in godoc. Three existing callsites
(`phase/service.go`, `apply/teamlead.go`) use it unchanged in M1.
M3 will migrate callsites to `SkillsForContext` and remove `SkillsForPhase`.

### D-M1-12 — Clock injection into seeder

`SeedSkills` accepts `clock shared.Clock` per CLAUDE.md rule #5 (no direct
`time.Now()` in domain/application packages). `Wire()` passes `shared.SystemClock{}`.
Tests inject `shared.FixedClock(t)` for deterministic timestamps.

---

## Consequences

- Skills table gains 9 lifecycle columns; existing rows default to safe values.
- `SkillsForPhase` behavior is unchanged for active skills (regression gate passes).
- `SkillsForContext` enables M2 context-aware skill selection (scope, feature_type, paths).
- `SkippedSkill` list supports observability logging without port-level branching.
- `doublestar/v4` added to go.mod (single direct dependency, MIT license).
