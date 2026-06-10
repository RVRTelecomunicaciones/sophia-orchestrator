# Design: skills-lifecycle-matcher (M1)

**Strategy ref:** V4.1 §5.2, §5.3, §5.4, §5.5, §7, §8, §16 (milestone M1).
**Mode:** SDD design. Architectural decisions only — NO production code in this artifact.
**Scope:** Single PR with `size:exception`, schema-only migration + seeder Upsert backfill, deterministic SkillMatcher, regression snapshot gate.
**Engram:** `sdd/skills-lifecycle-matcher/design`.

---

## Approach

Land M1 as a single `size:exception` PR that strictly orders the dependency chain (dep → migration → domain → repo → seeder → matcher port → matcher adapter → deprecation wrapper → regression snapshot). The schema change is a transactional ALTER inside `010_skills_lifecycle.{up,down}.sql` (golang-migrate wraps each `.up.sql` in a transaction by default — explore §8). Backfill is done by the seeder's switch from `InsertIfAbsent` to `Upsert` with a V4.1 §7 legacy payload (Option B from explore §9), which automatically satisfies the "Upsert ≥ 1 non-test caller" criterion. The `SkillQuery.StructuralContext` field is the nil-only opaque marker already in `discipline.PriorContext` (`*discipline.StructuralContextRef`, declared in `prior_context.go` by M0.5), reused verbatim — no redeclare, no new package, no cycle (D-M05-2 precedent). Glob matching for `applies_when.touched_paths` / `exclude_paths` uses `github.com/bmatcuk/doublestar/v4` (pinned in `go.mod`). `SkillsForPhase` becomes a deprecated thin wrapper around `SkillsForContext(SkillQuery{Phase: phase})` so the 3 known callsites in `phase/service.go:396`, `apply/teamlead.go:594/383/482` keep working byte-equivalent. A blocking regression snapshot test on the 9 seeds (`testdata/skill_phase_baseline/*.golden.json`) is captured BEFORE migration is applied via `GOLDEN_UPDATE=1`, committed, and re-asserted byte-exact AFTER migration + backfill to defend the M1 prime directive (regression-zero vs PR #76).

---

## Architecture Decisions

### D-M1-1: Migration 010 schema-only + DROP/ADD UNIQUE in one transaction

**Choice**: `010_skills_lifecycle.up.sql` performs ALTER (add 9 columns with safe defaults) + DROP CONSTRAINT `skills_name_unique` + ADD CONSTRAINT `skills_name_version_unique UNIQUE (name, version)` + 3 CHECK constraints + 3 indexes — all inside a single transaction (golang-migrate auto-wraps each `.up.sql`). Backfill of lifecycle payload happens in the seeder, NOT in SQL.

**Alternatives considered**:
- Option A (SQL `UPDATE skills SET ...` inside migration 010) → rejected: hardcoding JSONB literals (`scope = '{"project_id":"*", "repo_id":"*", "phases": [...]}'`) for 9 phase rows in SQL is fragile, untestable in Go, and requires a per-phase CASE expression that re-creates the seed table's logic at the DB layer.
- Option C (hybrid: SQL fills `status='active'`, seeder fills the rest) → rejected: `activation_source DEFAULT 'manual'` is the WRONG default for legacy seeds; seeder still needs to fix that, so the seeder is required either way — Option C just duplicates work.

**Rationale**: Schema-only ALTER is the smallest reviewable change at the DB boundary. Backfill belongs in testable Go code with the same construction path as `New()` / `NewLegacy()`. golang-migrate transaction wrap means the DROP/ADD UNIQUE swap is atomic for any concurrent reader — there is no constraint-less window.

**Tradeoff**: One boot of the new binary is required before all 9 rows have correct lifecycle data. The migration alone leaves them at `(status='candidate', version='v1', activation_source='manual', scope='{}', applies_when='{}', metrics='{}')` — the seeder upgrades them to `(active, v1, legacy_seed, ...)` on next boot. This is rolling-deploy-safe (see D-M1-10).

Refs: proposal §1, explore §8 §9, specs `skills-schema-migration-010`.

---

### D-M1-2: `SkillQuery.StructuralContext` reuses `discipline.StructuralContextRef` (Option D)

**Choice**: `SkillQuery.StructuralContext` is typed `*discipline.StructuralContextRef` — the same opaque empty-struct marker M0.5 declared in `internal/application/discipline/prior_context.go:64`. M1 never populates this field (always nil). The PG adapter ignores the field. No redeclaration.

**Alternatives considered**:
- Option A (move `StructuralContext` to `internal/domain/structural/`) → rejected: touches INIT-0 stable code; out of M1 scope (same reason M0.5 rejected it).
- Option B (declare a `StructuralCtxView` interface in `discipline`) → rejected: adds vocabulary nobody consumes in M1; over-engineered for a nil field.
- Option C (use `any`) → rejected: loses type safety; M3 has no compile-time signal where to wire.
- Re-declaring a new `StructuralContextRef` inside a matcher package → rejected: would shadow the M0.5 one, force callers to choose which package to import, defeat the M3 single-rename plan.

**Rationale**: M0.5 already locked the forward-compat anchor at `discipline.StructuralContextRef`. Reuse keeps the cycle-avoidance story single-sourced. M3 (PriorContext enrichment + matcher StructuralContext wiring) flips both consumers atomically by renaming/redefining one type.

**Tradeoff**: M1's `applies_when` matcher CANNOT filter by `framework` / `language` / `state_model` (those fields exist on the struct but are not consulted from `StructuralContext`). That's M3 scope, explicit in proposal §29 (Out of Scope).

Refs: proposal §29, explore §6, spec `skill-matcher-port` Requirement 4, M0.5 design D-M05-2.

---

### D-M1-3: `github.com/bmatcuk/doublestar/v4` for glob matching

**Choice**: Pin `github.com/bmatcuk/doublestar/v4` (latest stable tag `v4.x.x`; exact tag pinned in `go.mod` by `go get`). Use `doublestar.Match(pattern, candidate)` for every `applies_when.touched_paths` / `exclude_paths` evaluation.

**Alternatives considered**:
- `path/filepath.Match` (stdlib) → rejected: does NOT support `**` recursive wildcards. `applies_when.touched_paths: ["internal/**/skill_*.go"]` is unrepresentable.
- Custom glob implementation → rejected: NIH violation; non-trivial to get right (escapes, character classes, separator handling); adds maintenance debt for zero gain.
- `gitignore.Match` (go-git) → rejected: imports the entire go-git tree; gitignore semantics differ from V4.1 `applies_when` semantics (negation, directory-relative anchors).

**Rationale**: doublestar/v4 is the de-facto Go glob library for `**` semantics. MIT license, stable API (v4 since 2021), low transitive dep count, used by tools like `mage` / `chezmoi`.

**Tradeoff**: One new external dependency. Documented in ADR; pinned version recorded in proposal §Risks R2.

Refs: proposal §6, explore §7 §14 R8.

---

### D-M1-4: Seeder backfill via Upsert (Option B)

**Choice**: `bootstrap/seed_skills.go:36` switches `repo.InsertIfAbsent(ctx, s)` → `repo.Upsert(ctx, s)`. Seeds are built with a new `skill.NewLegacy` constructor (or via `skill.New` with the V4.1 §7 payload supplied directly) — `Status=active`, `Version=v1`, `ActivationSource=legacy_seed`, `RiskLevel=medium`, `Scope={ProjectID:"*", RepoID:"*", Phases:[<phase>]}`, `AppliesWhen={}` (empty), `Metrics={}` (all zero), `LastUsedAt=nil`, `LastValidatedAt=nil`.

**Alternatives considered**:
- Option A (SQL backfill in migration 010) → rejected per D-M1-1.
- Option C (hybrid SQL + seeder) → rejected per D-M1-1.

**Rationale**: One code path produces all skill values (boot-time seeder + any future API). The Upsert call uniquely satisfies the success criterion "Upsert ≥ 1 non-test caller" — see explore §9. Idempotent via the new `UNIQUE (name, version)` constraint: re-running the seeder is a no-op on identical content and a content-update on changed content.

**Tradeoff**: The new binary's seeder MUST run after migration 010 has been applied. Migration order is enforced by the existing `bootstrap.Wire()` ordering — migrations run before the seeder. On rolling deploy, see D-M1-10.

Refs: proposal §4, explore §9, spec `skills-seeder-backfill`.

---

### D-M1-5: `SkillMatcher` port in `discipline`, adapter in `adapters/outbound/pg`, algorithm package `internal/application/skill/matcher/` reserved for shared helpers

**Choice**: Port interface `discipline.SkillMatcher` + `SkillQuery` + `SkippedSkill` declared in `internal/application/discipline/skill_matcher.go` (alongside `SkillProvider`). PG adapter at `internal/adapters/outbound/pg/skill_matcher.go`. The shared `scopeMatches` / `appliesWhenMatches` helpers MAY live alongside the adapter to keep the package surface minimal (M1 has exactly one matcher implementation); creating `internal/application/skill/matcher/` as a standalone package is deferred unless test isolation requires it.

**Alternatives considered**:
- Put port in matcher package → rejected: discoverability — operators reading `discipline/` expect both `SkillProvider` and `SkillMatcher` next to each other.
- Put port in `domain/skill` → rejected: ports are an application-layer concern, not a domain one (CLAUDE.md D1.1).
- Force a standalone `internal/application/skill/matcher/` package now → considered; deferred. The proposal §5 mentions the package as the natural home for the algorithm but with one implementation and short helpers, co-location with the PG adapter avoids a 1-package, 1-file boondoggle. If the helper unit tests grow, the package extraction is mechanical (move 2 functions).

**Rationale**: Discoverability + minimum-surface-area. Reviewer reads `discipline/skill_matcher.go` and finds the port; reads `adapters/outbound/pg/skill_matcher.go` and finds the adapter. Standard hexagonal layout.

**Tradeoff**: If a second adapter (e.g., in-memory for fast tests) shows up later, the helpers will need extraction. M1 ships with PG only; in-memory adapter is M2/M3 scope.

Refs: proposal §5, explore §7.

---

### D-M1-6: `FindByPhase` adds `AND status = 'active'` filter

**Choice**: `internal/adapters/outbound/pg/skill_repo.go:32` extends the existing query:

```sql
SELECT <selectColumns>
FROM   skills
WHERE  $1 = ANY(phases)
  AND  status = 'active'
```

Backward compatibility is guaranteed by the seeder backfill: all 9 seeds become `status='active'` on first boot of the new binary, so the regression snapshot test asserts byte-equivalent output to PR #76 baseline.

**Alternatives considered**:
- Leave `FindByPhase` unfiltered → rejected: future `candidate` / `deprecated` / `archived` skills would pollute LLM prompts; the M2 promotion worker would silently degrade prompt quality.
- Take an optional `status` parameter on `FindByPhase` → rejected: violates the "minimum-blast-radius" rule for deprecation; the M3 retirement plan calls for deleting `FindByPhase` entirely.

**Rationale**: `FindByPhase` is the legacy V4.0 contract; it MUST NEVER surface non-active skills. Hard-coded `status='active'` is the invariant. New code uses `SkillsForContext` with explicit status semantics inside the matcher.

**Tradeoff**: The 3 known callsites (`phase/service.go:396`, `apply/teamlead.go:594/383/482`) now get an implicit status filter. Backfill makes this safe; the regression snapshot proves it. The change is documented in the deprecation wrapper godoc.

Refs: proposal §3, explore §3 §10.

---

### D-M1-7: `SkillsForPhase` becomes a deprecated thin wrapper

**Choice**: In `internal/application/discipline/skill_provider.go`, the `SkillProvider` interface gains a sibling `SkillMatcher` reference (constructed at adapter wiring), and `SkillsForPhase` is a thin wrapper:

```
// Deprecated: Use SkillMatcher.SkillsForContext(SkillQuery{Phase: phase}) instead.
// SkillsForPhase will be removed in M3.
func (p *PGSkillProvider) SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
    skills, _, err := p.matcher.SkillsForContext(ctx, SkillQuery{Phase: pt})
    return skills, err
}
```

The 3 known callsites stay byte-equivalent. New callers (M3+) prefer the typed query.

**Alternatives considered**:
- Delete `SkillsForPhase` now and migrate 3 callsites → rejected: increases blast radius; introduces avoidable regression risk in M1 whose prime directive is regression-zero.
- Keep both APIs forever → rejected: V4.1 §16 M3 explicitly retires `SkillsForPhase`. Deprecation godoc is the M1 signal.

**Rationale**: Minimum blast radius + clear retirement signal. The wrapper discards `SkippedSkill` results because the old callers never had access to skip reasons.

**Tradeoff**: One `// Deprecated:` linter signal will start firing for the 3 callsites; that's intentional. The deprecation wrapper is removed in M3 along with the callsite migration to typed queries.

Refs: proposal §7, explore §11.

---

### D-M1-8: Regression snapshot test on the 9 seeds is a BLOCKING gate

**Choice**: A test under `internal/adapters/outbound/pg/` iterates all 9 `phase.PhaseType` values, calls `provider.SkillsForPhase(ctx, pt)`, and asserts the result equals a committed golden file at `testdata/skill_phase_baseline/{phase}.golden.json`. The golden capture flow:

1. **Pre-M1 baseline capture**: a separate test gated by `GOLDEN_UPDATE=1` runs against migration 009 only (no migration 010, no seeder change), captures `provider.SkillsForPhase` output for all 9 phases, writes goldens.
2. **Commit goldens** with conventional `test(pg)` prefix.
3. **Post-M1 assertion**: after migration 010 up + seeder Upsert + adapter changes, the same test runs WITHOUT `GOLDEN_UPDATE=1`, reads goldens, asserts byte-equivalent.

Golden file shape:
```
[
  {"id":"<ULID>", "name":"<name>", "phases":[...], "content_sha256":"<hex>", "techniques":[...]}
]
```
Stored sorted by `id` ascending so insertion order in the DB does not affect comparison. `content_sha256` keeps the goldens compact (`~50 bytes` per skill vs. multi-KB content blob).

**Alternatives considered**:
- Serialize full content instead of sha256 → rejected: bloats goldens to ~50KB+; content is also stored in seeder code (`seed_skills.go:buildSeedSkills`), so a duplicate copy in goldens is redundant maintenance.
- Inline goldens in Go source → rejected: 9 phases × verbose JSON = unreadable test file; testdata/ pattern is standard sophia practice.
- Compare ID+name only → rejected: would not catch content drift (a seed text edit landing in M1 with intent to "improve" the prompt).

**Rationale**: M1's prime directive is regression-zero vs PR #76. A blocking byte-exact gate is the only defensible mechanism. Capturing the baseline BEFORE migration 010 ensures the goldens reflect PR #76 reality, not whatever M1 produces — this is the same risk M0.5 D-M05-5 surfaced for the inline-concat baseline.

**Tradeoff**: The capture step is a one-time manual ritual gated by `GOLDEN_UPDATE=1`. The PR author must run it, eyeball the diff, commit. CI will fail loudly if the post-migration assertion drifts. Goldens are inert and outside the 400-LoC review budget (M0.5 precedent — proposal §25 / operator decision 10).

**Failure semantics**: snapshot mismatch = M1 broke PR #76 behavior → STOP, do not merge. The test reports per-phase diff so the breaking change is named.

Refs: proposal §8, explore §10, success criterion #4.

---

### D-M1-9: Strict ordering inside the single PR

**Choice**: Each commit is reviewable as a self-contained slice that compiles + tests green. The order is:

1. `chore(deps): add doublestar/v4 to go.mod` — dep first, no code uses it yet.
2. `feat(pg): migration 010 skills lifecycle up/down + integration test` — migration only; no domain/repo change yet.
3. `feat(domain/skill): add lifecycle fields + invariants + Update signature` — domain layer; unit tests RED-first.
4. `feat(pg): extend skill_repo for lifecycle columns + status filter + scanSkill JSONB` — adapter layer; integration test RED-first.
5. `feat(bootstrap): switch seeder to Upsert with V4.1 §7 legacy payload` — seeder; integration test RED-first.
6. `feat(discipline): declare SkillMatcher port + SkillQuery + SkippedSkill` — port + types only.
7. `feat(pg): implement SkillMatcher PG adapter (scope + applies_when + sort + skip)` — adapter; unit + integration tests RED-first.
8. `refactor(discipline): SkillsForPhase deprecated wrapper around SkillsForContext` — deprecation only.
9. `test(pg): regression snapshot for SkillsForPhase on 9 phases` — blocking gate.

**Alternatives considered**:
- One commit with everything → rejected: unreviewable; CI cannot bisect failures.
- Split into 3 PRs (per explore §13 Option A) → rejected by operator (proposal §13 decision 1): tightly coupled domain ↔ repo ↔ matcher invariants make intermediate states non-functional.

**Rationale**: Each commit compiles + tests green = bisectable. Reviewers can land mental snapshots at each layer. The dependency chain is explicit in the commit log.

**Tradeoff**: PR is ~9 commits; review takes longer per commit but less cumulative cognitive load.

Refs: proposal §16-§18.

---

### D-M1-10: Rolling-deploy contract

**Choice**: The new binary cannot run against the old schema (missing columns break `scanSkill`); the old binary CAN run against the new schema (extra columns ignored by old SELECT). Deploy order:

1. Apply migration 010 (via `golang-migrate` job, NOT via the application binary). Verify success via `pg_constraint` / `information_schema.columns` queries.
2. Roll out the new binary. As each instance boots, its seeder upserts the 9 seeds with the V4.1 §7 payload (`status=active`, `activation_source=legacy_seed`, etc.). Idempotent — multiple instances upserting in parallel is safe due to `UNIQUE (name, version)`.
3. Smoke-test the regression snapshot in CI before merge → blocks bad deploys.
4. The old binary, if still running during rollout, continues serving traffic with its `InsertIfAbsent`-by-name path. The new lifecycle columns get filled by DB defaults during any old-binary inserts (none happen in steady state — only the seeder inserts), so even old-binary writes are safe.

**Alternatives considered**:
- Apply migration AND binary in same deploy step → rejected: any binary failure leaves the DB at a state the old binary CANNOT read (old SELECT does not list new columns; pgx tolerates this, but Upsert with old SQL would now miss the lifecycle defaults). Safer to decouple migration from binary rollout.
- Deploy binary BEFORE migration → rejected: new binary's `scanSkill` requires the new columns; would crash on every read.

**Rationale**: Standard zero-downtime rolling-deploy pattern. The migration is the precondition; the binary is the consumer. No special coordination required during the binary rollout window because old binary's writes are limited to `InsertIfAbsent` paths that the schema defaults already cover.

**Tradeoff**: Migration job + binary deploy is two steps. Operator must apply migration first. This is the standard operational discipline in `sophia-orchestator` (see migrations 005-009 precedent).

Refs: proposal §Risks R4 §R7, explore §14 R4 §R7.

---

### D-M1-11: Domain enum types are unexported-fields-with-getters mirroring the existing pattern

**Choice**: `Status`, `RiskLevel`, `ActivationSource` are public `string`-based types with `String()` methods and module-private constructors that validate against the closed enum set. Example:

```
// Status is a closed enum of skill lifecycle states.
type Status string

const (
    StatusCandidate  Status = "candidate"
    StatusActive     Status = "active"
    StatusDeprecated Status = "deprecated"
    StatusArchived   Status = "archived"
)

func (s Status) IsValid() bool { ... }
func (s Status) String() string { return string(s) }
```

`Scope`, `AppliesWhen`, `Metrics` are public value-struct types (JSON-serializable) declared in the `skill` package — Go structs at the domain boundary, JSONB at the SQL boundary. `scanSkill` decodes JSONB columns directly into the struct fields via pgx scanners.

**Alternatives considered**:
- Use `string` directly with constants → rejected: no type safety; downstream callers could pass `"random"` and compile; the `IsValid()` predicate disappears.
- Define enums in a sub-package → rejected: forces a circular import (domain/skill needs them for invariants).
- Store `Scope` / `AppliesWhen` / `Metrics` as `json.RawMessage` → rejected: loses type safety; matcher would re-parse on every call.

**Rationale**: Mirrors the existing `phase.PhaseType` + `skill.Technique` pattern. CHECK constraints at the DB layer + `IsValid()` at the domain layer is defense-in-depth. Go struct values for nested JSON are the standard pgx pattern.

**Tradeoff**: The Skill aggregate's field count grows from 7 to 16. Tests get noisier (more constructor params). Acceptable: every field has a clear V4.1 §7 origin.

Refs: spec `skills-domain-lifecycle`, V4.1 §5.2 §7.

---

### D-M1-12: All time via injected Clock; no direct `time.Now()` in domain/application

**Choice**: `SkillRepo.Upsert` / `SkillRepo.scanSkill` use `time.Time` values passed from the aggregate (created/updated/lastUsed/lastValidated). The seeder receives `clock shared.Clock` and passes `clock.Now()` into `skill.New` / `skill.NewLegacy`. The matcher's sort by `last_validated_at` uses persisted timestamps (no clock dependency). Migration uses `DEFAULT now()` for `created_at` / `updated_at` only (existing precedent in migration 009) — `last_used_at` / `last_validated_at` default to NULL.

**Alternatives considered**:
- Direct `time.Now()` calls in seeder → rejected: CLAUDE.md rule #5 forbids it in application packages.

**Rationale**: Aligns with sophia-orchestator CLAUDE.md rule #5 (no direct `time.Now()` / `ulid.Make()` in domain/application — use injectable `Clock` and `IDGenerator`). Test fixtures use a frozen clock for deterministic timestamps in unit/integration tests.

**Tradeoff**: Seeder signature gains a `clock` parameter. Wiring change is local to `bootstrap.Wire()`.

Refs: CLAUDE.md rule #5.

---

## Components

### 1. Migration 010 — `010_skills_lifecycle.{up,down}.sql`

**Up (`010_skills_lifecycle.up.sql`)**:

```sql
-- Migration 010: skills lifecycle + metrics + scope + applies_when (V4.1 §5.2).
-- Extends the schema from migration 009 with lifecycle metadata and atomically
-- swaps the UNIQUE constraint from (name) to (name, version) so the same name
-- can carry multiple versioned variants (V4.1 §5.5).
--
-- Safe defaults populate the 9 existing seed rows so the DB stays consistent
-- between this migration applying and the seeder Upsert (Option B, D-M1-4)
-- running on the next boot of the new binary.

ALTER TABLE skills
  ADD COLUMN status             TEXT        NOT NULL DEFAULT 'candidate',
  ADD COLUMN version            TEXT        NOT NULL DEFAULT 'v1',
  ADD COLUMN scope              JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN applies_when       JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN risk_level         TEXT        NOT NULL DEFAULT 'medium',
  ADD COLUMN activation_source  TEXT        NOT NULL DEFAULT 'manual',
  ADD COLUMN metrics            JSONB       NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN last_used_at       TIMESTAMPTZ,
  ADD COLUMN last_validated_at  TIMESTAMPTZ,
  ADD CONSTRAINT skills_status_check
    CHECK (status IN ('candidate','validated','active','deprecated','blocked','archived')),
  ADD CONSTRAINT skills_risk_check
    CHECK (risk_level IN ('low','medium','high','critical')),
  ADD CONSTRAINT skills_activation_source_check
    CHECK (activation_source IN ('manual','legacy_seed','archive_worker','llm_proposal','imported'));

CREATE INDEX IF NOT EXISTS idx_skills_status      ON skills (status);
CREATE INDEX IF NOT EXISTS idx_skills_scope_gin   ON skills USING GIN (scope);
CREATE INDEX IF NOT EXISTS idx_skills_applies_gin ON skills USING GIN (applies_when);

ALTER TABLE skills DROP CONSTRAINT skills_name_unique;
ALTER TABLE skills ADD CONSTRAINT skills_name_version_unique UNIQUE (name, version);
```

**Down (`010_skills_lifecycle.down.sql`)**:

```sql
-- Migration 010 rollback: drop lifecycle/metrics columns, swap UNIQUE back.
-- Idempotent via IF EXISTS guards (spec: skills-schema-migration-010).

ALTER TABLE skills DROP CONSTRAINT IF EXISTS skills_name_version_unique;
ALTER TABLE skills ADD CONSTRAINT skills_name_unique UNIQUE (name);

DROP INDEX IF EXISTS idx_skills_applies_gin;
DROP INDEX IF EXISTS idx_skills_scope_gin;
DROP INDEX IF EXISTS idx_skills_status;

ALTER TABLE skills
  DROP CONSTRAINT IF EXISTS skills_activation_source_check,
  DROP CONSTRAINT IF EXISTS skills_risk_check,
  DROP CONSTRAINT IF EXISTS skills_status_check,
  DROP COLUMN     IF EXISTS last_validated_at,
  DROP COLUMN     IF EXISTS last_used_at,
  DROP COLUMN     IF EXISTS metrics,
  DROP COLUMN     IF EXISTS activation_source,
  DROP COLUMN     IF EXISTS risk_level,
  DROP COLUMN     IF EXISTS applies_when,
  DROP COLUMN     IF EXISTS scope,
  DROP COLUMN     IF EXISTS version,
  DROP COLUMN     IF EXISTS status;
```

**Notes**:
- No explicit `BEGIN; ... COMMIT;` — golang-migrate wraps each `.up.sql` / `.down.sql` in a transaction by default (see migrations 005-008 precedent).
- Enum sets EXACTLY mirror the spec: 4 statuses, 4 risk levels, 4 activation sources. (The proposal scaffolding mentioned 5-6 status values; the spec is authoritative — 4 only.)
- `DROP CONSTRAINT skills_name_unique` then `ADD CONSTRAINT skills_name_version_unique` is atomic because the wrapping transaction holds an `ACCESS EXCLUSIVE` lock for the duration of the file.

---

### 2. Domain — `internal/domain/skill/skill.go`

**Aggregate shape after M1**:

```go
type Skill struct {
    id               ids.SkillID
    name             string
    phases           []phase.PhaseType
    content          string
    techniques       []Technique

    // M1 lifecycle (V4.1 §5.2 §7)
    status           Status
    version          string
    scope            Scope
    appliesWhen      AppliesWhen
    riskLevel        RiskLevel
    activationSource ActivationSource
    metrics          Metrics
    lastUsedAt       *time.Time
    lastValidatedAt  *time.Time

    createdAt        time.Time
    updatedAt        time.Time
}
```

**Enum types** (declared in `internal/domain/skill/lifecycle.go`):

```go
type Status string
const (
    StatusCandidate  Status = "candidate"
    StatusValidated  Status = "validated"
    StatusActive     Status = "active"
    StatusDeprecated Status = "deprecated"
    StatusBlocked    Status = "blocked"
    StatusArchived   Status = "archived"
)
func (s Status) IsValid() bool { /* closed-set check */ }
func (s Status) String() string { return string(s) }

type ActivationSource string
const (
    SourceManual        ActivationSource = "manual"
    SourceLegacySeed    ActivationSource = "legacy_seed"
    SourceArchiveWorker ActivationSource = "archive_worker"
    SourceLLMProposal   ActivationSource = "llm_proposal"
    SourceImported      ActivationSource = "imported"
)

type RiskLevel string
const (
    RiskLow      RiskLevel = "low"
    RiskMedium   RiskLevel = "medium"
    RiskHigh     RiskLevel = "high"
    RiskCritical RiskLevel = "critical"
)
func (r RiskLevel) IsValid() bool { ... }
func (r RiskLevel) String() string { return string(r) }

type ActivationSource string
const (
    SourceLegacySeed   ActivationSource = "legacy_seed"
    SourceManual       ActivationSource = "manual"
    SourceLLMSuggested ActivationSource = "llm_suggested"
    SourcePromoted     ActivationSource = "promoted"
)
func (a ActivationSource) IsValid() bool { ... }
func (a ActivationSource) String() string { return string(a) }
```

**Scope / AppliesWhen / Metrics structs** (`internal/domain/skill/lifecycle.go`):

```go
// Scope mirrors the JSONB column. JSON tags use snake_case for JSONB round-trip.
type Scope struct {
    ProjectID string   `json:"project_id"`
    RepoID    string   `json:"repo_id"`
    Phases    []string `json:"phases"`
    // M3 additions (not used in M1): TenantID *string, Environment, Languages, Frameworks
}

// AppliesWhen mirrors the JSONB column. M1 only uses TouchedPaths + ExcludePaths +
// FeatureType. Framework / StateModel reserved for M3.
type AppliesWhen struct {
    FeatureType  []string `json:"feature_type,omitempty"`
    TouchedPaths []string `json:"touched_paths,omitempty"`
    ExcludePaths []string `json:"exclude_paths,omitempty"`
    // M3: Framework []string, StateModel []string
}

// Metrics holds promotion-relevant counters. V4.1 §5.4.
type Metrics struct {
    UsageCount        int     `json:"usage_count"`
    SuccessCount      int     `json:"success_count"`
    FailureCount      int     `json:"failure_count"`
    TestsPassedCount  int     `json:"tests_passed_count"`
    DeprecatedAPIHits int     `json:"deprecated_api_hits"`
    RollbackCount     int     `json:"rollback_count"`
    AvgRetryReduction float64 `json:"avg_retry_reduction"`
    LastStackVersion  *string `json:"last_stack_version,omitempty"`
}
```

**Constructors / mutators** (`skill.go`):

```go
// New constructs a validated Skill with V4.1 §7 defaults applied to lifecycle fields
// that the caller did not provide. Signature documented in spec skills-domain-lifecycle.
func New(
    id ids.SkillID,
    name string,
    phases []phase.PhaseType,
    content string,
    techniques []Technique,
    lifecycle LifecycleInput, // optional payload; zero value → defaults
    now time.Time,
) (*Skill, error) { ... }

// LifecycleInput is an optional construction payload. Zero-value fields fall back
// to V4.1 §7 defaults (Status=candidate, Version=v1, RiskLevel=medium,
// ActivationSource=manual, Scope/AppliesWhen/Metrics zero).
type LifecycleInput struct {
    Status           Status
    Version          string
    Scope            Scope
    AppliesWhen      AppliesWhen
    RiskLevel        RiskLevel
    ActivationSource ActivationSource
    Metrics          Metrics
    LastUsedAt       *time.Time
    LastValidatedAt  *time.Time
}

// NewLegacy is a convenience constructor for the boot seeder. Equivalent to
// New() with lifecycle = {Status: StatusActive, Version: "v1",
// ActivationSource: SourceLegacySeed, RiskLevel: RiskMedium,
// Scope: {ProjectID: "*", RepoID: "*", Phases: [<derived from phases arg>]}}.
func NewLegacy(
    id ids.SkillID,
    name string,
    phases []phase.PhaseType,
    content string,
    techniques []Technique,
    now time.Time,
) (*Skill, error) { ... }

// Update mutates a runtime-editable Skill. Accepts the same LifecycleInput as
// New(); zero values preserve existing fields. Re-enforces invariants.
func (s *Skill) Update(
    name string,
    phases []phase.PhaseType,
    content string,
    techniques []Technique,
    lifecycle LifecycleInput,
    now time.Time,
) error { ... }

// Hydrate reconstitutes from the DB without re-validating invariants.
func Hydrate(
    id ids.SkillID,
    name string,
    phases []phase.PhaseType,
    content string,
    techniques []Technique,
    status Status,
    version string,
    scope Scope,
    appliesWhen AppliesWhen,
    riskLevel RiskLevel,
    activationSource ActivationSource,
    metrics Metrics,
    lastUsedAt, lastValidatedAt *time.Time,
    createdAt, updatedAt time.Time,
) *Skill { ... }
```

**Invariants added in M1** (in addition to existing name/content/phases/techniques):
- `Status.IsValid()` MUST be true (enforced in `New` / `Update`).
- `Version` MUST NOT be empty after trim.
- `RiskLevel.IsValid()` MUST be true.
- `ActivationSource.IsValid()` MUST be true.

`Hydrate` trusts the DB and does NOT re-check invariants.

**Getters added**: `Status()`, `Version()`, `Scope()`, `AppliesWhen()`, `RiskLevel()`, `ActivationSource()`, `Metrics()`, `LastUsedAt()`, `LastValidatedAt()`. All read-only (return copies for slices/maps inside Scope / AppliesWhen / Metrics).

---

### 3. Repo adapter — `internal/adapters/outbound/pg/skill_repo.go`

**Updated `selectColumns` constant**:

```go
const selectColumns = `id, name, phases, content, techniques,
    status, version, scope, applies_when, risk_level,
    activation_source, metrics, last_used_at, last_validated_at,
    created_at, updated_at`
```

**`FindByPhase` after M1**:

```go
func (r *SkillRepo) FindByPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
    q := `SELECT ` + selectColumns + `
          FROM   skills
          WHERE  $1 = ANY(phases)
            AND  status = 'active'`
    // ...
}
```

**`Upsert` after M1**:

```go
const upsertSQL = `
INSERT INTO skills (
    id, name, phases, content, techniques,
    status, version, scope, applies_when, risk_level,
    activation_source, metrics, last_used_at, last_validated_at,
    created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5,
        $6, $7, $8, $9, $10,
        $11, $12, $13, $14,
        $15, $16)
ON CONFLICT (name, version) DO UPDATE SET
    id                = EXCLUDED.id,
    phases            = EXCLUDED.phases,
    content           = EXCLUDED.content,
    techniques        = EXCLUDED.techniques,
    status            = EXCLUDED.status,
    scope             = EXCLUDED.scope,
    applies_when      = EXCLUDED.applies_when,
    risk_level        = EXCLUDED.risk_level,
    activation_source = EXCLUDED.activation_source,
    metrics           = EXCLUDED.metrics,
    last_used_at      = EXCLUDED.last_used_at,
    last_validated_at = EXCLUDED.last_validated_at,
    updated_at        = EXCLUDED.updated_at`
```

Note: `ON CONFLICT` now keys on `(name, version)` per migration 010 D-M1-1.

**`scanSkill` after M1** — extends current 7-column scan to read 16 columns and decode JSONB into structs via pgx default scanners (`pgtype.JSONB` or direct `json.Unmarshal` on `[]byte`):

```go
func scanSkill(rows pgx.Rows) (*skill.Skill, error) {
    var (
        rawID                                   string
        name, content                           string
        phases, techniques                      []string
        status, version, riskLevel, actSource   string
        scopeBytes, appliesBytes, metricsBytes  []byte
        lastUsedAt, lastValidatedAt             *time.Time
        createdAt, updatedAt                    time.Time
    )
    if err := rows.Scan(&rawID, &name, &phases, &content, &techniques,
        &status, &version, &scopeBytes, &appliesBytes, &riskLevel,
        &actSource, &metricsBytes, &lastUsedAt, &lastValidatedAt,
        &createdAt, &updatedAt); err != nil {
        return nil, err
    }

    var scope skill.Scope
    if err := json.Unmarshal(scopeBytes, &scope); err != nil { return nil, fmt.Errorf("scope: %w", err) }
    var appliesWhen skill.AppliesWhen
    if err := json.Unmarshal(appliesBytes, &appliesWhen); err != nil { return nil, fmt.Errorf("applies_when: %w", err) }
    var metrics skill.Metrics
    if err := json.Unmarshal(metricsBytes, &metrics); err != nil { return nil, fmt.Errorf("metrics: %w", err) }
    // ... ID / phase / technique conversions identical to existing code ...

    return skill.Hydrate(
        skillID, name, phaseTypes, content, techTags,
        skill.Status(status), version, scope, appliesWhen,
        skill.RiskLevel(riskLevel), skill.ActivationSource(actSource), metrics,
        lastUsedAt, lastValidatedAt,
        createdAt, updatedAt,
    ), nil
}
```

`List` extends similarly; `InsertIfAbsent` is left in place (not removed in M1; seeder no longer uses it but other code might in the future) with its SQL updated to write the new columns from the aggregate.

---

### 4. SkillRepository port — `internal/ports/outbound/repository.go`

Interface signatures stay the same (`FindByPhase` / `Upsert` / `InsertIfAbsent` / `List`); only the underlying `*skill.Skill` shape changes, so the interface compiles unchanged. Godoc is updated:

```go
// SkillRepository persists Skill aggregates with V4.1 §5.2 lifecycle fields.
//
// FindByPhase returns Skills whose phases array contains pt AND status='active'.
// The status filter is hard-coded as an invariant: legacy FindByPhase callers
// (phase/service.go:396, apply/teamlead.go) MUST NEVER receive non-active
// skills. New code uses SkillMatcher.SkillsForContext.
//
// Upsert conflicts on (name, version) per migration 010. Idempotent.
// ...
```

---

### 5. Seeder — `internal/bootstrap/seed_skills.go`

**Signature change**: `SeedSkills` accepts an injected `clock shared.Clock` parameter.

**Implementation**:

```go
func SeedSkills(ctx context.Context, repo outbound.SkillRepository, clock shared.Clock, logger *slog.Logger) error {
    seeds, err := buildSeedSkills(clock.Now().UTC())
    if err != nil { ... }

    for _, s := range seeds {
        if err := repo.Upsert(ctx, s); err != nil {
            return fmt.Errorf("bootstrap: SeedSkills: upsert %q: %w", s.Name(), err)
        }
    }
    logger.Info("bootstrap: skill seeder complete",
        slog.Int("seeds_attempted", len(seeds)))
    return nil
}
```

**`buildSeedSkills` change**: each of the 9 `defs` entries calls `skill.NewLegacy(...)` instead of `skill.New(...)`. `NewLegacy` provides the V4.1 §7 payload (`StatusActive`, `SourceLegacySeed`, `v1`, etc.) and constructs `Scope` from each def's phase list automatically. No def field changes — the existing 9-element table stays byte-equivalent at the level of (name, phases, content, techniques).

`bootstrap.Wire()` updated to pass the configured `shared.Clock` into `SeedSkills`.

---

### 6. SkillMatcher port — `internal/application/discipline/skill_matcher.go` (NEW)

```go
package discipline

import (
    "context"

    "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
    "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// SkillMatcher resolves the set of Skills applicable to a context (phase,
// project/repo scope, applies_when filters). V4.1 §8.
//
// Contract:
//   - Returns (matched, skipped, nil) on success. Skipped reports the
//     deterministic reason each non-matching active skill was filtered out.
//   - Returns (nil, nil, err) only on infrastructure failure.
//   - Filters out skills with status != 'active' implicitly (status_not_active
//     is recorded in skipped only for caller visibility, not for normal
//     production paths where the SELECT already restricts to active).
//   - Sort order: risk_level asc, last_validated_at desc (NULLs last),
//     metrics.usage_count desc. Stable within ties via id asc.
type SkillMatcher interface {
    SkillsForContext(ctx context.Context, q SkillQuery) ([]*skill.Skill, []SkippedSkill, error)
}

// SkillQuery is the typed request. Zero-value is valid input (matches every
// active skill — useful for tests and tooling).
//
// StructuralContext is the M3 wiring anchor. Always nil in M1 per D-M1-2 /
// D-M05-2. The PG adapter MUST NOT dereference this field. Reuses the M0.5
// StructuralContextRef opaque marker declared in prior_context.go.
type SkillQuery struct {
    Phase             phase.PhaseType
    ProjectID         string
    RepoID            string
    StructuralContext *StructuralContextRef
    FeatureType       string
    TouchedPaths      []string
    RiskLevel         skill.RiskLevel
}

// SkippedSkill explains why a candidate was filtered out. Reason is one of
// the closed string constants below.
type SkippedSkill struct {
    SkillID string
    Reason  string
}

const (
    SkipReasonScopeMismatch     = "scope_mismatch"
    SkipReasonAppliesWhenFailed = "applies_when_failed"
    SkipReasonStatusNotActive   = "status_not_active"
)
```

**Important**: `StructuralContextRef` is NOT re-declared — it lives in `discipline/prior_context.go:64` from M0.5. M1 imports it from the same package (no import statement needed; same package).

---

### 7. SkillMatcher PG adapter — `internal/adapters/outbound/pg/skill_matcher.go` (NEW)

```go
package pg

import (
    "context"
    "sort"

    "github.com/bmatcuk/doublestar/v4"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
    "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// PGSkillMatcher implements discipline.SkillMatcher against Postgres.
// It performs scope + applies_when filtering in Go after SELECT'ing all active
// skills. M1 keeps the algorithm deterministic and explicit; index-pushdown
// (GIN(applies_when), GIN(scope)) is a future optimization once the row count
// rises beyond the seed set.
type PGSkillMatcher struct {
    pool *pgxpool.Pool
    repo *SkillRepo // reuse scanSkill via a List-by-status helper
}

func NewPGSkillMatcher(pool *pgxpool.Pool, repo *SkillRepo) *PGSkillMatcher { ... }

func (m *PGSkillMatcher) SkillsForContext(
    ctx context.Context,
    q discipline.SkillQuery,
) ([]*skill.Skill, []discipline.SkippedSkill, error) {
    // 1. SELECT every active skill (status filter is the production-correctness
    //    invariant — non-active rows never reach the matcher).
    all, err := m.selectAllActive(ctx)
    if err != nil { return nil, nil, fmt.Errorf("PGSkillMatcher.SkillsForContext: %w", err) }

    matched := make([]*skill.Skill, 0, len(all))
    skipped := make([]discipline.SkippedSkill, 0)

    for _, s := range all {
        // Status check is redundant here (SELECT already filtered) but kept
        // as an explicit defense if a future caller bypasses selectAllActive.
        if s.Status() != skill.StatusActive {
            skipped = append(skipped, discipline.SkippedSkill{
                SkillID: s.ID().String(), Reason: discipline.SkipReasonStatusNotActive})
            continue
        }
        if !scopeMatches(s.Scope(), q) {
            skipped = append(skipped, discipline.SkippedSkill{
                SkillID: s.ID().String(), Reason: discipline.SkipReasonScopeMismatch})
            continue
        }
        if !appliesWhenMatches(s.AppliesWhen(), q) {
            skipped = append(skipped, discipline.SkippedSkill{
                SkillID: s.ID().String(), Reason: discipline.SkipReasonAppliesWhenFailed})
            continue
        }
        matched = append(matched, s)
    }

    sortSkills(matched)
    return matched, skipped, nil
}

// scopeMatches implements the scope filter:
//   - phase: SkillQuery.Phase MUST appear in Scope.Phases (when q.Phase != "").
//   - project_id: "*" matches any; otherwise exact match required.
//   - repo_id: same as project_id.
// Empty q.Phase is treated as "no filter" for tooling.
func scopeMatches(s skill.Scope, q discipline.SkillQuery) bool {
    if q.Phase != "" {
        found := false
        for _, p := range s.Phases {
            if p == string(q.Phase) { found = true; break }
        }
        if !found { return false }
    }
    if q.ProjectID != "" && s.ProjectID != "*" && s.ProjectID != q.ProjectID { return false }
    if q.RepoID    != "" && s.RepoID    != "*" && s.RepoID    != q.RepoID    { return false }
    return true
}

// appliesWhenMatches implements V4.1 §5.3 semantics:
//   - FeatureType (if set on aggregate): q.FeatureType MUST be in the list.
//   - TouchedPaths (if set on aggregate): at least one q.TouchedPaths element
//     MUST match at least one TouchedPaths glob via doublestar.
//   - ExcludePaths (if set on aggregate): NO q.TouchedPaths element may match
//     any ExcludePaths glob — exclude wins.
// Empty applies_when sub-fields mean "no constraint" for that dimension.
func appliesWhenMatches(a skill.AppliesWhen, q discipline.SkillQuery) bool {
    if len(a.FeatureType) > 0 {
        if !contains(a.FeatureType, q.FeatureType) { return false }
    }
    if len(a.TouchedPaths) > 0 && len(q.TouchedPaths) > 0 {
        if !anyGlobMatch(a.TouchedPaths, q.TouchedPaths) { return false }
    }
    if len(a.ExcludePaths) > 0 && len(q.TouchedPaths) > 0 {
        if anyGlobMatch(a.ExcludePaths, q.TouchedPaths) { return false }
    }
    return true
}

// anyGlobMatch returns true iff any (pattern, candidate) pair matches via
// doublestar.Match. Errors from invalid patterns are treated as no-match
// (defensive against operator-edited skills).
func anyGlobMatch(patterns, candidates []string) bool {
    for _, pat := range patterns {
        for _, cand := range candidates {
            if ok, _ := doublestar.Match(pat, cand); ok { return true }
        }
    }
    return false
}

// sortSkills orders matched skills per V4.1 §8:
//   1. risk_level ASC (low < medium < high < critical).
//   2. last_validated_at DESC, NULLs last.
//   3. metrics.usage_count DESC.
//   4. id ASC (stable tiebreaker).
func sortSkills(skills []*skill.Skill) { sort.SliceStable(skills, less(skills)) }
```

**Wiring** (in `bootstrap.Wire()`): the existing `pg.NewSkillProvider` continues to construct the deprecated provider; a new `pg.NewPGSkillMatcher(pool, skillRepo)` wires up the matcher and is injected into both the `SkillProvider` (so the deprecation wrapper can delegate) and any future M2/M3 callers.

---

### 8. Deprecation wrapper — `internal/application/discipline/skill_provider.go`

Update the existing port doc and adapter:

```go
// SkillProvider is the legacy phase-only port. SkillsForPhase is preserved for
// backward compatibility with the 3 known callsites (phase/service.go:396,
// apply/teamlead.go:594/383/482) and is BYTE-EQUIVALENT to PR #76 behavior.
//
// Deprecated: Use SkillMatcher.SkillsForContext instead. SkillProvider will be
// removed in M3 once the callsites are migrated to typed queries.
type SkillProvider interface {
    SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error)
}
```

```go
// pg.SkillProvider adapter after M1
type SkillProvider struct {
    matcher discipline.SkillMatcher
}

func NewSkillProvider(matcher discipline.SkillMatcher) *SkillProvider {
    if matcher == nil { panic("pg.SkillProvider: nil matcher") }
    return &SkillProvider{matcher: matcher}
}

// Deprecated: SkillsForPhase delegates to SkillsForContext and discards the
// skipped set. Equivalent to SkillsForContext(SkillQuery{Phase: pt}) modulo
// the dropped (skipped, err) tuple.
func (p *SkillProvider) SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
    skills, _, err := p.matcher.SkillsForContext(ctx, discipline.SkillQuery{Phase: pt})
    return skills, err
}
```

The wrapper now depends on `SkillMatcher` instead of `SkillRepo` directly. `bootstrap.Wire()` reorders: construct `SkillRepo` → construct `PGSkillMatcher(pool, repo)` → construct `SkillProvider(matcher)` → inject into `phase.Service` / `apply.RunService` deps.

---

### 9. Regression snapshot test — `internal/adapters/outbound/pg/skill_provider_regression_test.go`

```go
//go:build integration

func TestSkillProvider_SkillsForPhase_RegressionZero(t *testing.T) {
    // setupTestPG runs all migrations including 010, then runs the seeder.
    ctx, pool := setupTestPG(t)
    require.NoError(t, bootstrap.SeedSkills(ctx, pg.NewSkillRepo(pool), fixedClock, slog.Default()))

    repo := pg.NewSkillRepo(pool)
    matcher := pg.NewPGSkillMatcher(pool, repo)
    provider := pg.NewSkillProvider(matcher)

    for _, pt := range phase.AllPhaseTypes() {
        t.Run(string(pt), func(t *testing.T) {
            skills, err := provider.SkillsForPhase(ctx, pt)
            require.NoError(t, err)

            got := serializeForGolden(skills)
            golden := readGolden(t, fmt.Sprintf("testdata/skill_phase_baseline/%s.golden.json", pt))

            if os.Getenv("GOLDEN_UPDATE") == "1" {
                writeGolden(t, fmt.Sprintf("testdata/skill_phase_baseline/%s.golden.json", pt), got)
                return
            }

            require.JSONEq(t, golden, got, "regression on phase %s", pt)
        })
    }
}

// serializeForGolden produces a stable JSON shape:
//   [{"id": "...", "name": "...", "phases": [...], "techniques": [...], "content_sha256": "<hex>"}, ...]
// sorted by id ascending. Content is hashed (sha256-hex) rather than embedded
// to keep goldens compact; any content drift surfaces as a hash diff.
```

**Baseline capture procedure** (operator-facing, ONE-TIME):

1. Check out the M1 PR branch.
2. Locally revert ONLY the M1 migration + seeder change (`git checkout main -- migrations/postgres/010* internal/bootstrap/seed_skills.go internal/adapters/outbound/pg/skill_repo.go`).
3. Run `GOLDEN_UPDATE=1 make test-integration` for the regression test only.
4. Inspect the captured goldens (9 files), confirm they reflect PR #76 reality.
5. `git checkout HEAD -- migrations/postgres/010* ...` to restore M1 changes.
6. Commit goldens with `test(pg): capture pre-M1 regression baseline for SkillsForPhase`.
7. Run `make test-integration` again WITHOUT `GOLDEN_UPDATE=1` — it MUST pass.

This rigor matches the M0.5 D-M05-5 baseline-capture-FIRST pattern.

---

## Data Flow

```
┌──────────────────────────────────────────────────────────────────────┐
│                       BOOT-TIME (per binary instance)               │
└──────────────────────────────────────────────────────────────────────┘
  bootstrap.Wire()
       │
       ├─→ golang-migrate apply 010 (idempotent; usually run as separate step)
       │
       ├─→ pg.NewSkillRepo(pool)
       │
       ├─→ pg.NewPGSkillMatcher(pool, skillRepo)
       │
       ├─→ pg.NewSkillProvider(matcher)
       │
       └─→ bootstrap.SeedSkills(ctx, skillRepo, clock, logger)
              │
              for each of 9 hybrid seeds:
              │
              skill.NewLegacy(id, name, phases, content, techniques, clock.Now())
                   │
                   produces *skill.Skill with V4.1 §7 lifecycle payload
              │
              skillRepo.Upsert(ctx, s)
                   │
                   INSERT ... ON CONFLICT (name, version) DO UPDATE SET ...

┌──────────────────────────────────────────────────────────────────────┐
│                       RUNTIME (per prompt build)                    │
└──────────────────────────────────────────────────────────────────────┘
  Legacy path (phase/service.go:396, apply/teamlead.go:594/383/482):
       │
       deps.Skills.SkillsForPhase(ctx, p.Type())  ← SkillProvider port
              │
              SkillProvider.SkillsForPhase wraps:
              │
              matcher.SkillsForContext(ctx, SkillQuery{Phase: pt})
                     │
                     SELECT * FROM skills WHERE status='active'
                     │
                     scopeMatches(s.Scope, q)
                     appliesWhenMatches(s.AppliesWhen, q) [no-op when q.TouchedPaths empty]
                     sortSkills(matched)
              │
              returns ([]*skill.Skill, []SkippedSkill, err)
       │
       (skipped discarded by wrapper; legacy path receives []*skill.Skill only)
       │
       Skills → PromptInput.Skills → PromptBuilder → dispatcher

  Future M3 path (post-deprecation retirement):
       │
       matcher.SkillsForContext(ctx, SkillQuery{
           Phase:             p.Type(),
           ProjectID:         change.Project(),
           RepoID:            change.Repo(),
           FeatureType:       overrides.FeatureType,
           TouchedPaths:      task.FilesPattern(),
           StructuralContext: ...,  // M3 wires real value
       })
```

---

## Test Strategy

Strict TDD enabled for the project. Every component below ships with a failing test FIRST, then minimal production code.

### Layer-by-layer test coverage

| Component | Test type | Test file | What it asserts |
|---|---|---|---|
| Migration 010 | Integration | `internal/adapters/outbound/pg/migration_010_test.go` | up adds 9 cols + 3 CHECKs + 3 indexes + UNIQUE swap; down reverts cleanly; round-trip restores baseline |
| Domain enum types | Unit | `internal/domain/skill/lifecycle_test.go` | `IsValid()` rejects unknown values; `String()` returns the underlying string |
| `Skill.New` | Unit | `internal/domain/skill/skill_test.go` | Default lifecycle payload applied; invalid status/risk/source rejected; empty version rejected |
| `Skill.NewLegacy` | Unit | `internal/domain/skill/skill_test.go` | Produces (active, v1, legacy_seed, medium) with Scope.Phases populated |
| `Skill.Update` | Unit | same | Invariants re-enforced; valid transitions succeed; aggregate state unchanged on error |
| `Skill.Hydrate` | Unit | same | Accepts any persisted values without re-validation |
| `SkillRepo.FindByPhase` | Integration | `internal/adapters/outbound/pg/skill_repo_test.go` | Status='active' filter rejects non-active rows; scanSkill correctly decodes JSONB columns |
| `SkillRepo.Upsert` | Integration | same | Conflict on (name, version) updates; idempotent re-run |
| `SkillRepo.scanSkill` | Integration | same | Round-trips Scope / AppliesWhen / Metrics through JSONB |
| `SeedSkills` | Integration | `internal/bootstrap/seed_skills_test.go` | 9 seeds present with (active, legacy_seed, v1); idempotent re-run; clock-injection passes |
| `SkillMatcher` port | Compile | `internal/application/discipline/skill_matcher_test.go` | Zero-value SkillQuery compiles; SkippedSkill constructable; StructuralContextRef nil-only |
| `PGSkillMatcher.SkillsForContext` | Integration | `internal/adapters/outbound/pg/skill_matcher_test.go` | Scope filter (project/repo/phase wildcards + exact match); applies_when filter (feature_type / touched_paths / exclude_paths via doublestar); sort order; skipped reasons; status_not_active short-circuit |
| Glob matching | Unit | `internal/adapters/outbound/pg/skill_matcher_glob_test.go` | `**/foo.go` matches nested; `internal/**/*_test.go` matches deep; exclude wins |
| `SkillProvider.SkillsForPhase` deprecation wrapper | Integration | `internal/adapters/outbound/pg/skill_provider_test.go` | Delegates to matcher; discards skipped; returns same shape as PR #76 |
| Regression snapshot | Integration (BLOCKING) | `internal/adapters/outbound/pg/skill_provider_regression_test.go` | Byte-equivalent goldens vs PR #76 across all 9 phases |

### TDD sequencing inside the single PR

For each commit listed in D-M1-9, the strict TDD rule is:
1. Write the failing test FIRST against the API the commit will introduce.
2. `make test-unit` (or `make test-integration` for adapter commits) shows RED.
3. Write the minimal production code to make the test green.
4. `make test-unit && make lint` shows GREEN.
5. Commit BOTH the test AND the production code together (one commit per layer).

The regression snapshot test capture (golden write) happens BEFORE migration 010 lands — see the procedure in §9.

---

## Risks Revisited (concrete mitigation per proposal)

| # | Risk (from proposal) | M1 design mitigation |
|---|---|---|
| R1 | `StructuralContext` import cycle | D-M1-2: reuse `discipline.StructuralContextRef` from M0.5; no new declaration; no import of `init/detector`. |
| R2 | `doublestar/v4` not in `go.mod` | First commit in the PR sequence (D-M1-9) is `chore(deps): add doublestar/v4`. Pinned version recorded in `go.sum`. |
| R3 | UNIQUE constraint swap window | golang-migrate transaction wrap (D-M1-1). DROP+ADD inside the same transaction = no constraint-less window for concurrent inserts. |
| R4 | `scanSkill` extends atomically with migration | D-M1-10 rolling-deploy: migration runs FIRST, separate job; binary rollout strictly after migration success. Old binary tolerates extra columns (SELECT lists explicit subset in pre-M1 code). |
| R5 | `FindByPhase` polluted by non-active skills | D-M1-6: hard-coded `AND status='active'`. Backfill makes all 9 seeds active immediately. |
| R6 | ~1670 LoC `size:exception` | Single PR + 9 layered commits per D-M1-9 keeps each commit reviewable. Operator approved per proposal §13. |
| R7 | Seeder Upsert depends on new-binary boot | D-M1-10: old binary's `InsertIfAbsent` keeps working with SQL defaults during the rollout window. New binary's seeder corrects the payload on first boot. Idempotent. |
| R8 | doublestar `**` differs from `filepath.Match` | Glob behavior is unit-tested at `skill_matcher_glob_test.go` with explicit `**`, `*`, and exclude cases. Documented in the matcher package godoc. |
| R9 | Regression vs PR #76 | D-M1-8 blocking snapshot gate; baseline captured BEFORE migration; CI re-asserts after. |
| R10 | INIT-0 lint/test surprises | `make lint && make test-unit` pre-push enforced; `.gitkeep` placed in `testdata/skill_phase_baseline/` to keep the directory committed even before goldens land. |

---

## Out of Scope (reaffirmed from proposal)

- **StructuralContext consumption in `applies_when`** (framework/language/state_model filtering) — deferred to M3.
- **Promotion / demotion policy logic** — M2 worker.
- **LLM-driven skill creation** — anti-pattern per V4.1 D11; never applies to INIT.
- **AllowlistEnforcer wiring** — PRE-0 / INIT-0 scope.
- **Cross-repo coupling** with `sophia-memory-engine`, governance, runtime-adapters — single-repo M1.
- **Removal of `SkillsForPhase` API** — deferred to M3.
- **Migration of non-seed skills** — none exist today.
- **Index pushdown** (using GIN(scope) / GIN(applies_when) inside the matcher SELECT) — M1 filters in Go for clarity; pushdown is an optimization for M2+ when row count grows.
- **In-memory `SkillMatcher` adapter** — single PG adapter in M1; in-memory variant is M2/M3 scope.

---

## Rolling-Deploy Contract (D-M1-10 recap)

1. **Apply migration 010** via the standalone `golang-migrate` job. Verify via `pg_constraint` + `information_schema.columns` queries that:
   - `skills_name_version_unique` exists; `skills_name_unique` is gone.
   - 9 new columns present with correct defaults.
   - 3 CHECK constraints + 3 indexes present.
2. **Roll out the new binary** instance-by-instance. Each instance's `bootstrap.Wire()`:
   - Runs `bootstrap.SeedSkills(ctx, repo, clock, logger)` → upserts 9 seeds with V4.1 §7 payload.
   - Wires `PGSkillMatcher` into `SkillProvider` (deprecation wrapper) and into `phase.Service` / `apply.RunService` dependencies.
3. **Regression snapshot test runs in CI** for every PR; merge is blocked on the gate.
4. **Rollback procedure**: revert PR → run migration 010 down → run prior binary. Down is reversible because seed content is reproducible from `seed_skills.go`.

---

## Open Questions

None. All 12 operator decisions from the proposal are locked. This design implements them mechanically.

---

## References

- Proposal: `openspec/changes/skills-lifecycle-matcher/proposal.md`
- Exploration: `openspec/changes/skills-lifecycle-matcher/explore.md`
- Specs: `openspec/changes/skills-lifecycle-matcher/specs/{skills-schema-migration-010,skills-domain-lifecycle,skills-seeder-backfill,skill-matcher-port}/spec.md`
- Strategy: V4.1 §5.2, §5.3, §5.4, §5.5, §7, §8, §16
- M0.5 precedent: `openspec/changes/priorcontext-struct-refactor/design.md` (D-M05-2 for StructuralContextRef, D-M05-5 for baseline-capture-FIRST)
- CLAUDE.md rule #5 (Clock / IDGenerator injection)
- Migrations 005, 008, 009 as schema-ALTER + index patterns
