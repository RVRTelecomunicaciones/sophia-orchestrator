# Tasks: Apply Skill Injection

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | 700-950 |
| 400-line budget risk | High |
| Chained PRs recommended | Yes |
| Suggested split | PR1 Mechanism → PR2 Integration → PR3 Content |
| Delivery strategy | auto-chain |
| Chain strategy | feature-branch-chain |

Decision needed before apply: Yes
Chained PRs recommended: Yes
Chain strategy: feature-branch-chain
400-line budget risk: High

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|---|---|---|---|
| 1 | Mechanism foundation | PR 1 | base=tracker branch; ADR-0011 + migration 009 + repo/provider + tests |
| 2 | Prompt hydration integration | PR 2 | base=PR1 branch; pure builder + flag + backward-compat goldens |
| 3 | Seeded content rollout | PR 3 | base=PR2 branch; 9 skills + NOTICE + idempotent seeder |

## Phase 1: Slice 1 — Mechanism

- [x] 1.1 Extend `internal/domain/ids/ids.go`; create `internal/domain/skill/{skill.go,technique.go}` with `SkillID`, constructors/hydrate/update, canonical phase order, allowed-technique validation.
- [x] 1.2 Add unit tests for invalid name/content, zero phases, invalid tags, dedupe/order, hydrate/update in `internal/domain/skill/*_test.go` to keep domain at 100%.
- [x] 1.3 Add `migrations/postgres/009_skills.{up,down}.sql`; confirm latest existing migration is `008_*`; create `docs/adr/0011-skill-injection.md` for persisted entity + insert-only seeding.
- [x] 1.4 Add `SkillRepository` in `internal/ports/outbound/repository.go` and `SkillProvider` port in `internal/application/discipline/skill_provider.go`.
- [x] 1.5 Create `internal/adapters/outbound/pg/skill_repo.go` with `FindByPhase`, `Upsert`, `InsertIfAbsent`, `List`; add integration tests (`//go:build integration`) for migration 009 + repo queries.
- [x] 1.6 Verify slice 1: `gofmt ./... && go test ./... && go test -tags=integration ./internal/adapters/outbound/pg/... && golangci-lint config verify && golangci-lint run`.

## Phase 2: Slice 2 — Integration

- [ ] 2.1 Update `internal/application/discipline/prompt_builder.go` to accept `PromptInput.Skills` and render one `# Skill` block after HARD-GATE and before Prior Context; keep `Build` pure/deterministic.
- [ ] 2.2 Update `internal/application/phase/service.go`, `internal/application/apply/{run.go,teamlead.go}` to hydrate via `SkillProvider` before `Prompts.Build`; fail-soft on flag off, empty result, or provider error.
- [ ] 2.3 Add `SOPHIA_SKILLS_ENABLED` default `true` in `internal/infrastructure/config/config.go`; wire repo/provider in `internal/bootstrap/wire.go` without new SSE events or CLI mirror.
- [ ] 2.4 Add/refresh golden + app tests in `internal/application/discipline/testdata/`, `prompt_builder_test.go`, `phase/service_test.go`, `apply/*test.go` proving flag-off/empty/error are byte-identical to pre-change prompts.
- [ ] 2.5 Verify slice 2: `gofmt ./... && go test ./... -cover ./internal/application/... && golangci-lint config verify && golangci-lint run`.

## Phase 3: Slice 3 — Content

- [ ] 3.1 Add boot seeder in `internal/bootstrap/seed_skills.go` (not migration data) for 9 named hybrid skills, adapted content, and `InsertIfAbsent` by `name` only.
- [ ] 3.2 Add MIT attribution in `NOTICE`; keep inline-why inside persisted `content`, not schema.
- [ ] 3.3 Add seeder tests covering empty-table seed, idempotent restart, and edited-row no-clobber behavior; reuse integration-tag PG tests when DB assertions are required.
- [ ] 3.4 Verify slice 3: `gofmt ./... && go test ./... && go test -tags=integration ./... && golangci-lint config verify && golangci-lint run`.
