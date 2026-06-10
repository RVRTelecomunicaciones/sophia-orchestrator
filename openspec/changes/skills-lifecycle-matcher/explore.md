# Exploration — skills-lifecycle-matcher (M1)

**Strategy ref:** V4.1 §16 milestone M1.
**Mode:** SDD explore. NO production code changes; investigation only.
**Scope:** Single-repo (sophia-orchestator). No cross-repo coupling.
**Engram artifact:** `sdd/skills-lifecycle-matcher/explore`.

---

## 1. Current state — `skills` table (post-PR #76)

**Migration 009** at `migrations/postgres/009_skills.up.sql`:
- 7 columns: `id, name, phases[], content, techniques[], created_at, updated_at`
- Constraint: `skills_name_unique UNIQUE (name)` ← **the one to DROP** for `UNIQUE(name, version)`
- No status / version / scope / applies_when / metrics / etc.

Next migration number: **010**.

---

## 2. Current Skill domain aggregate

`internal/domain/skill/skill.go`:
- Unexported fields with getters (immutable aggregate pattern)
- `New()` enforces: non-empty name + content, ≥1 valid phase, ≥1 technique
- `Hydrate()` reconstitutes from DB without revalidation
- **`Update()` exists but has zero non-test callers** (confirmed against inventory)
- **NO lifecycle modeling today**: no `Status`, `Version`, `Scope`, `AppliesWhen`, `RiskLevel`, `ActivationSource`, `Metrics`, `LastUsedAt`, `LastValidatedAt`

---

## 3. SkillRepo PG adapter

`internal/adapters/outbound/pg/skill_repo.go`:
- **`FindByPhase`** (line 32): `SELECT id, name, phases, content, techniques, created_at, updated_at FROM skills WHERE $1 = ANY(phases)` — NO status filter today
- **`Upsert`** (line 56): conflicts on `id`. **Zero non-test callers confirmed**
- **`InsertIfAbsent`** (line 83): `ON CONFLICT (name) DO NOTHING`. Called by seed bootstrap at `internal/bootstrap/seed_skills.go:36`
- **`List`** (line 103): basic list query

Seed bootstrap iterates 9 hybrid seeds, all via `InsertIfAbsent`.

---

## 4. SkillsForPhase callsites (3 non-test)

1. `internal/adapters/outbound/pg/skill_provider.go:28` — adapter delegates to `FindByPhase`
2. `internal/application/phase/service.go:396` — phase execution path
3. `internal/application/apply/teamlead.go:594` — `hydrateSkills` (also referenced at lines 383 and 482)

All migrate to `SkillsForContext` via deprecated `SkillsForPhase` wrapper.

---

## 5. SkillProvider port

`internal/application/discipline/skill_provider.go` — single method `SkillsForPhase(ctx, phase) ([]Skill, error)`.

M1 adds: deprecated godoc + delegate to `SkillsForContext(SkillQuery{Phase: phase})`.

---

## 6. StructuralContext cycle — design blocker resolution

`StructuralContext` lives at `internal/application/init/detector/types.go` (INIT-0 output).
`discipline` package does NOT import `init/detector`.
`phase/service.go:29` already imports `initdetector` directly.

V4.1 §8 says `SkillQuery.StructuralContext` is `StructuralContext`. Same cycle risk M0.5 hit.

**M0.5 chose Option D**: opaque nil-marker `*StructuralContextRef` in `discipline`.

**M1 inherits the same pattern**: `SkillQuery.StructuralContext` = `*StructuralContextRef`, always nil in M1. M3 wires it when consuming both PriorContext and SkillMatcher.

Consequence: M1's `applies_when` can NOT filter by `framework`/`language` — those filters are M3 anyway. M1 only filters by `phase`, `project_id`, `repo_id`, `feature_type`, `touched_paths`, `exclude_paths`.

---

## 7. SkillMatcher target location

**NEW package**: `internal/application/skill/matcher/`
- Port `SkillMatcher` interface declared in `discipline` package alongside `SkillProvider` for discoverability
- Adapter in `internal/adapters/outbound/pg/skill_matcher.go`
- `SkillQuery` + `SkippedSkill` types in `discipline` (or matcher package — operator confirms)

Algorithm (V4.1 §8 verbatim):
```
1. SELECT * FROM skills WHERE status = 'active'.
2. Filtrar por scope (project_id, repo_id, environment, phases).
3. Filtrar por applies_when (feature_type, framework, touched_paths, exclude_paths).
4. Ordenar por (risk_level asc, last_validated_at desc, usage_count desc).
5. Aplicar token budget máximo (config).
6. Devolver matched + skipped con razón.
```

**Glob library**: `github.com/bmatcuk/doublestar/v4` for `**` in `touched_paths`. NOT in current `go.mod` — needs adding.

---

## 8. Migration template (zero-downtime)

PRE-0 migration 005 demonstrated zero-downtime ALTER. For M1:
- Migration 010 is **schema-only ALTER + DROP/ADD UNIQUE**: safe defaults so existing 9 seed rows fill in
- The `UNIQUE` swap: `ALTER TABLE skills DROP CONSTRAINT skills_name_unique; ALTER TABLE skills ADD CONSTRAINT skills_name_version_unique UNIQUE (name, version);`
- golang-migrate wraps each migration file in a transaction by default → safe

DROP+ADD UNIQUE inside a transaction is atomic for readers. Brief lock during ALTER but no data corruption window.

---

## 9. Backfill strategy — Option B (seeder Upsert) RECOMMENDED

Three options considered:

| # | Approach | Pros | Cons |
|---|---|---|---|
| A | SQL UPDATE inside migration 010 | atomic w/ schema | hardcoded JSONB in SQL is fragile, untestable |
| B | Seeder switches `InsertIfAbsent` → `Upsert` with lifecycle payload | testable Go code, satisfies "Upsert ≥ 1 non-test caller" automatically | boot-time dependency |
| C | Hybrid: SQL defaults cover most, seeder handles `activation_source` | partial | `activation_source='manual'` default is wrong for seeds; seeder required anyway |

**Recommend B**. The seeder already runs on every boot. Switching to `Upsert` with the V4.1 §7 payload:

```json
{
  "status": "active",
  "version": "v1",
  "activation_source": "legacy_seed",
  "risk_level": "medium",
  "scope": {"project_id": "*", "repo_id": "*", "phases": ["<phase>"]},
  "applies_when": {},
  "metrics": {usage_count: 0, success_count: 0, failure_count: 0, ...}
}
```

This satisfies:
- "9 seeds migradas con status=active, activation_source=legacy_seed" ✅
- "Upsert tiene al menos 1 caller no-test" ✅ (seeder is the caller)
- "0 seeds duplicadas en BD" ✅ (Upsert idempotent via `name,version`)

---

## 10. Regression-zero test strategy

V4.1 acceptance: `SkillsForPhase(phase)` must return the SAME skills as PR #76 for each of the 9 phase types.

**Snapshot test plan**:
- BEFORE migration: query `FindByPhase` for each phase, dump skill IDs + content sha256 → golden file
- AFTER migration + backfill: same query, same fixture, compare against golden
- Failure = regression vs PR #76 (M1 broke the live contract)

Goldens land at `internal/adapters/outbound/pg/testdata/skill_phase_baseline/*.golden.json`.

---

## 11. `SkillsForPhase` deprecation mechanism

```go
// Deprecated: Use SkillsForContext(SkillQuery{Phase: phase}) instead.
// SkillsForPhase will be removed in M3.
func (p *SkillProvider) SkillsForPhase(ctx context.Context, phase PhaseType) ([]Skill, error) {
    skills, _, err := p.matcher.SkillsForContext(ctx, SkillQuery{Phase: phase})
    return skills, err
}
```

Old callers continue working; new callers prefer the typed query.

---

## 12. Affected areas (file list)

| File | Action | LoC est. |
|---|---|---|
| `migrations/postgres/010_skills_lifecycle.up.sql` | NEW | ~80 |
| `migrations/postgres/010_skills_lifecycle.down.sql` | NEW | ~30 |
| `internal/domain/skill/skill.go` | MODIFIED — lifecycle fields + invariants + Update sig change | ~150 |
| `internal/adapters/outbound/pg/skill_repo.go` | MODIFIED — extend SQL + scanSkill + status filter | ~120 |
| `internal/adapters/outbound/pg/skill_matcher.go` | NEW — SkillMatcher PG adapter | ~150 |
| `internal/adapters/outbound/pg/testdata/skill_phase_baseline/*.golden.json` | NEW — 9 regression fixtures | ~100 (inert) |
| `internal/bootstrap/seed_skills.go` | MODIFIED — Upsert with lifecycle payload | ~50 |
| `internal/application/skill/matcher/` | NEW package — algorithm + tests | ~250 |
| `internal/application/discipline/skill_provider.go` | MODIFIED — Deprecated godoc | ~10 |
| `internal/application/discipline/skill_matcher.go` | NEW — port interface + types (SkillQuery, SkippedSkill) | ~80 |
| `internal/ports/outbound/repository.go` | MODIFIED — SkillRepository interface extended | ~30 |
| `go.mod` + `go.sum` | MODIFIED — add `github.com/bmatcuk/doublestar/v4` | ~10 |
| Unit tests for matcher (scope/applies_when/sort/skip) | NEW | ~250 |
| Integration test (testcontainers PG, full flow) | NEW | ~200 |
| Regression snapshot test | NEW | ~80 |
| ADR (skills-lifecycle-matcher) | NEW | ~80 |

**Total production+test+ADR**: ~1670 LoC
**Total including goldens (inert)**: ~1770 LoC

---

## 13. PR delivery options

Option A — **3 chained PRs (stacked-to-main)**:
- PR1 (~110 LoC): migration 010 up/down + ADR
- PR2 (~410 LoC): domain + repo + seed + regression test
- PR3 (~1150 LoC): matcher + tests + integration

Option B — **Single PR with `size:exception`** (like INIT-0 PR2):
- All ~1670 LoC in one PR. Atomic. Easier review of cross-cutting changes (domain ↔ repo ↔ matcher are tightly coupled).

**Recommend B (single PR)** because:
- Domain extension and matcher implementation share the same struct invariants — splitting risks intermediate non-compiling states
- Migration + backfill seeder change MUST land together (without seeder Upsert, the migration leaves all 9 seeds without metrics/scope JSONB → broken state)
- INIT-0 precedent: atomic-milestone PRs with `size:exception` are operator-accepted when there's a hard dependency chain

Operator decides in proposal.

---

## 14. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | StructuralContext cycle | Option C (nil-only opaque marker) — same as M0.5 |
| R2 | `doublestar/v4` not in go.mod | Add via `go get`; standard procedure |
| R3 | UNIQUE constraint swap window | golang-migrate auto-wraps each .up.sql in a transaction; DROP+ADD atomic |
| R4 | `scanSkill` must extend atomically with migration deploy | Standard rolling-deploy: migration BEFORE new binary serves traffic |
| R5 | `FindByPhase` must add `status = 'active'` filter or future non-active skills pollute prompts | Seeds are backfilled as `active`; filter is correct invariant |
| R6 | 1670 LoC total (size:exception) | Operator confirms in proposal |
| R7 | Backfill of 9 seeds via Upsert depends on bootstrap running on the new binary; if old binary serves traffic during rolling deploy, it sees only the old schema | Acceptable: old binary's `InsertIfAbsent` keeps working (defaults fill new columns); new binary's seeder upserts the proper payload on next restart |
| R8 | `applies_when` glob evaluation: `doublestar/v4` semantics differ from `filepath.Match` (specifically `**`) | Standard pattern; documented in matcher package; unit-tested |

---

## 15. Recommendation

**Proceed to sdd-propose** with these locked recommendations:

1. **Migration 010 schema-only** + DROP/ADD UNIQUE in transaction (V4.1 §5.2 verbatim)
2. **Backfill via seeder Upsert** (Option B) — satisfies the "Upsert ≥ 1 non-test caller" criterion automatically
3. **SkillMatcher in `internal/application/skill/matcher/`** + port in `discipline`
4. **`SkillQuery.StructuralContext` = `*StructuralContextRef` nil-only** (Option C, mirrors M0.5)
5. **`doublestar/v4` for glob** in `touched_paths`
6. **`FindByPhase` adds `AND status = 'active'`** filter
7. **`SkillsForPhase` deprecated wrapper** — old API stays for back-compat in M1, retire in M3
8. **Single PR with `size:exception`** (operator decides; recommend B)
9. **Regression snapshot tests** on the 9 seeds: byte-exact baseline vs post-migration `FindByPhase`
10. **Goldens NOT counted toward LoC budget** (M0.5 precedent)

Open questions for proposal/design:
- **Q-M1-1**: single PR (size:exception) vs 3-PR chain?
- **Q-M1-2**: confirm `doublestar/v4` as glob lib?

---

## 16. Skill resolution

No project-specific skill registry. Standard SDD phase skills. Apply will need:
- persistence-postgres (golang-migrate)
- go-testing (testcontainers + table-driven + golden files)
- domain-modeling (aggregate invariants)
- api-contracts (matcher port)

`skill_resolution: none` — standard SDD skills apply.
