# Design: Apply Skill Injection

## Technical Approach

Implement operator-decided **Sabor B** as a persisted `Skill` aggregate stored in PostgreSQL, seeded at boot, hydrated by the application before prompt assembly, and rendered as an additive `# Skill` section in SDD prompts. The mechanism stays orch-only: no runtime-adapter changes, no dispatcher changes, and no prompt changes when skills are disabled, missing, or fail to load.

## Architecture Decisions

| Decision | Choice | Alternatives considered | Rationale |
|---|---|---|---|
| Aggregate shape | New `internal/domain/skill/` aggregate root with `ids.SkillID`, `name`, `phases []phase.PhaseType`, `content`, `techniques []Technique`, `createdAt`, `updatedAt` | Embedded prompt constants; join-table model | Matches existing aggregate/hydrate conventions (`change`, `apply`), keeps runtime edits possible, and avoids schema over-modeling. |
| Storage model | `skills` table with `phases TEXT[]`, `techniques TEXT[]`, `content TEXT`, timestamps, `UNIQUE(name)`, `GIN(phases)` | JSONB columns; phase/technique join tables | `ANY(phases)` is the only query path. `TEXT[]` + GIN is the simplest correct shape with low adapter complexity. |
| Hydration boundary | **Caller hydrates** via a `SkillProvider` port and passes `[]skill.Skill` in `discipline.PromptInput` | `PromptBuilder.Build(ctx)` reaches provider directly | `Build` is currently pure/synchronous and called from both `phase/service.go` and `apply/teamlead.go`. Keeping it pure avoids pushing `context.Context` and DB behavior into 20+ prompt tests while still keeping DB access behind a fakeable port. |
| Seeding rule | Boot-time seeder in `internal/bootstrap`, using insert-if-absent by `name` | SQL data migration; upsert-overwrite | Startup seeding can reuse domain validation, is easier to test, and the insert-only rule guarantees operator runtime edits are never clobbered. |

## Data Flow

`Wire()` → `SkillRepo` + `SkillProvider` + optional `SeedSkills()` → phase/apply service loads `SkillsForPhase(ctx, phase)` when `SOPHIA_SKILLS_ENABLED=true` → fail-soft on error/empty → `PromptInput.Skills` → `PromptBuilder.Build()` renders `# Skill` after `# HARD-GATE Markers` and before `# Prior Context`.

If flag OFF, provider error, or no rows: services pass `nil` skills and the prompt stays byte-identical to today.

## File Changes

| File | Action | Description |
|---|---|---|
| `internal/domain/ids/ids.go` | Modify | Add `SkillID` typed ULID parser/string helpers. |
| `internal/domain/skill/skill.go` | Create | Aggregate, constructor, hydrate, update, invariants. |
| `internal/domain/skill/technique.go` | Create | Allowed technique tags + validation helpers. |
| `internal/ports/outbound/repository.go` | Modify | Add `SkillRepository`. |
| `internal/application/discipline/prompt_builder.go` | Modify | Add `PromptInput.Skills` and deterministic `# Skill` renderer. |
| `internal/application/discipline/skill_provider.go` | Create | Port/service contract `SkillsForPhase(ctx, phase.PhaseType) ([]*skill.Skill, error)`. |
| `internal/application/phase/service.go` | Modify | Caller-side hydration before `Prompts.Build`. |
| `internal/application/apply/run.go`, `teamlead.go` | Modify | Apply-phase hydration reused for implement prompts. |
| `internal/adapters/outbound/pg/skill_repo.go` | Create | PG save/find-by-phase/find-by-name implementation. |
| `internal/bootstrap/wire.go` | Modify | Wire repo/provider, config gate, and seeder. |
| `internal/infrastructure/config/config.go` | Modify | Add `SOPHIA_SKILLS_ENABLED` default `true`. |
| `migrations/postgres/009_skills.{up,down}.sql` | Create | New table and indexes. |
| `docs/adr/0011-skill-injection.md` | Create | Persisted-skill mechanism decision. |
| `NOTICE` | Create | MIT attribution for adapted Cortex content. |

## Interfaces / Contracts

```go
type Skill struct { /* id, name, phases, content, techniques, timestamps */ }

func New(id ids.SkillID, name string, phases []phase.PhaseType, content string, techniques []Technique, now time.Time) (*Skill, error)
func Hydrate(id ids.SkillID, name string, phases []phase.PhaseType, content string, techniques []Technique, createdAt, updatedAt time.Time) *Skill
func (s *Skill) Update(name string, phases []phase.PhaseType, content string, techniques []Technique, now time.Time) error

type SkillProvider interface {
    SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error)
}
```

Invariants: non-empty `name`/`content`; at least one valid phase; deduped canonical phase order; at least one valid technique; techniques limited to `constitutional-self-critique`, `chain-of-verification`, `extended-thinking`, `skeleton-of-thought`, `react`, `step-back`, `inline-why`.

`skills` schema: `id CHAR(26) PK`, `name TEXT NOT NULL UNIQUE`, `phases TEXT[] NOT NULL`, `content TEXT NOT NULL`, `techniques TEXT[] NOT NULL`, `created_at TIMESTAMPTZ NOT NULL`, `updated_at TIMESTAMPTZ NOT NULL`; indexes: `GIN(phases)`, unique `name`.

Render format:

```text
# Skill
Use the following runtime skill guidance for this phase. It augments Iron Laws and HARD-GATE markers; it does not override them.

## <name>
Techniques: tag-a, tag-b
<content>
```

`inline-why` is authored inside `content` (e.g. short “Why:” clauses), not a separate column.

## Testing Strategy

| Layer | What to Test | Approach |
|---|---|---|
| Unit | Skill invariants, update rules, tag/phase validation | Table-driven domain tests. |
| Unit | Prompt rendering with nil/empty/seeded skills | Golden tests in `internal/application/discipline/testdata/`. |
| Integration | `009_skills` migration + `SkillRepo.FindByPhase/Save` | Postgres integration tests matching existing repo style. |
| Integration | Boot seeder idempotency and no-clobber behavior | Start with absent/existing-edited rows; verify existing content survives. |

## Migration / Rollout

Migration number is **009** (latest existing is 008). Seed 9 rows: `init-bootstrap-context`, `explore-investigate`, `proposal-draft-options`, `spec-write-requirements`, `design-architect-system`, `tasks-decompose-work`, `apply-implement-safely`, `verify-chain-validation`, `archive-finalize-deltas`. Technique mapping: init=`step-back`; explore=`react`; proposal/spec=`skeleton-of-thought`; design=`extended-thinking,step-back`; tasks=`extended-thinking`; apply=`constitutional-self-critique`; verify=`chain-of-verification`; archive=`step-back`; all also include `inline-why`.

## Open Questions

- [ ] None blocking.
