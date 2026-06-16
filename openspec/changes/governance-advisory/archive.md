# Archive Report: governance-advisory

**Change**: governance-advisory
**Archived**: 2026-06-16
**Mode**: openspec (files) + Engram (`sdd/governance-advisory/archive-report`, project 2026)
**Verification verdict**: PASS (0 CRITICAL, 0 WARNING, 2 SUGGESTION) — `verify.md`, engram obs #919
**Operator decision**: ARCHIVE NOW (PASS, ready=YES) with follow-ups tracked
**Ecosystem**: sophia-orchestator (orch) + agent-governance-core (govcore)
**ADR**: 0015 (`docs/adr/0015-governance-advisory-contract-lock-and-critic.md`)

## Intent

Two governance-adjacent gaps from Sophia V4.1 §16 M4+:

- **GAP A** — the orch↔govcore governance decision contract. A mid-flight scope
  correction (proposal obs #910 corrected; verification obs #913) overturned the
  explore-phase claim (obs #909) that orch would 404. The `/governance/v1/*`
  facade ALREADY ships in govcore and matches the orch client byte-for-byte. The
  real gap was that each side is tested only in isolation, so the contract could
  silently drift. GAP A was **reframed from "build facade" to "verify + lock"**.
- **GAP B** — an advisory critic. Orch had `PhaseStatusDoneWithConcerns` + the
  wire-v1 `phase.completed_with_concerns` SSE event but nothing produced concerns.
  This wires a strictly-advisory, never-escalating, deterministic-stub critic into
  that channel, per-change opt-in default OFF.

## Capabilities delivered (2)

| Capability | Type | PR | Where |
|---|---|---|---|
| governance-integration-contract | New (reframed from dropped `governance-decision-facade`) | PR-A (orch) | orch main `03cc00e` (#101) |
| advisory-critic | New | PR-B (orch) | orch main `03cc00e` (#100) |

Plus the govcore public `govhttptest` seam (test-support only, no facade change)
— govcore main `5d5648c` (#10).

## Specs synced to source of truth

| Domain | Action | Detail |
|---|---|---|
| governance-integration-contract | Created | Full spec (new capability, no prior main spec); added an "Implementation note (as shipped)" recording the `go.work`+`govhttptest` resolution and the `ECOSYSTEM_REPO_TOKEN`-gated `contract` CI job |
| advisory-critic | Created | Full spec (new capability, no prior main spec); added a "Scope boundary (as shipped — MVP)" section recording single-agent-only coverage, SSE-only persistence, and the deterministic stub |

Main specs: `openspec/specs/governance-integration-contract/spec.md`,
`openspec/specs/advisory-critic/spec.md`.

The stale `governance-decision-facade` capability (the false "build a facade"
premise) was correctly never created under `openspec/specs/` — confirmed ABSENT.
No stale directory to delete.

## The 2 verify SUGGESTIONs (recorded, also in ADR-0015)

1. **Document cross-repo merge-order** — the `contract` CI job depends on
   `ECOSYSTEM_REPO_TOKEN` (now configured) AND govcore main carrying the
   `govhttptest` seam (now merged, #10). A CONTRIBUTING note must state that a
   govcore facade/seam change must land BEFORE the orch `contract` job, so future
   govcore changes do not silently break the orch contract job. (ADR-0015
   follow-up #6.)
2. **Track MVP boundaries as backlog** — `runApplyPhase` critic coverage and
   durable concern persistence are explicit MVP boundaries; track them as visible
   backlog items so the SSE-only single-agent-only boundary stays visible
   post-archive. (ADR-0015 follow-ups #2, #3.)

## Tracked deferred follow-ups (recorded in ADR-0015)

1. **Real LLM critic** — replace the deterministic `StubCritic` via the OpenCode
   dispatcher (non-goal here).
2. **`runApplyPhase` critic coverage** — this change covers single-agent enveloped
   phases only (`runAsync` between `Validator.Validate` and `p.Complete`).
3. **Durable concern persistence** — MVP is SSE-only (`done_with_concerns`
   persists on the phase row; `Concern` detail rides SSE).
4. **Enum mapping `constrain → allow_with_constraints`** — NON-GOAL; no producer
   emits a non-`allow` decision today (identity map). Add only when govcore emits
   non-allow.
5. **govcore module-path mismatch** — `github.com/russellcxl/agent-governance-core`
   vs the `RVRTelecomunicaciones` remote blocks a cross-module `go.mod require`;
   the contract test resolves via `go.work` + the `ECOSYSTEM_REPO_TOKEN`-gated
   `contract` CI job. Ecosystem cleanup, govcore-owned.

## Final delivery (all MERGED to main)

| PR | Repo | Scope | Merge tip |
|---|---|---|---|
| #10 | agent-governance-core | Public `govhttptest` seam (test-support; no facade rebuild) | govcore main `5d5648c` |
| #100 | sophia-orchestator | PR-B: advisory critic (CriticPort + `Concern` + `StubCritic` + opt-in + runAsync insertion + SSE payload + wiring) | orch main `03cc00e` |
| #101 | sophia-orchestator | PR-A: GAP A contract-lock e2e test + `contract` CI job; zero production code in either repo | orch main `03cc00e` |

PR-A and PR-B are INDEPENDENT (no shared code, no chain). Each < 400 lines (Low
budget risk). Strict-TDD (RED→GREEN→VERIFY) per work unit; conventional commits;
no AI attribution (verified — orch range `a63a935..03cc00e` + govcore seam clean;
govcore carries a legitimate HUMAN co-author Russell only).

**CI prerequisite now satisfied**: the `ECOSYSTEM_REPO_TOKEN` secret is configured
and the `contract` job is GREEN in CI run **27607068239** (cross-repo workspace
job).

## Test / build evidence (from verify obs #919)

- `go test -tags=contract -race -count=1 -run TestContractLock ./test/integration/...` → exit 0 (PASS, uncached)
- `go test -tags=integration -count=1 -run TestContractLock ./test/integration/...` → "no tests to run" (contract test excluded from integration job — no CI regression)
- `go test -race -count=1 ./internal/...` (full unit suite) → exit 0, all green
- `make lint` (orch) → 0 issues
- `go test -tags=wirecontract ./test/wirecontract/...` → exit 0 (wire-contract test unchanged + green)
- govcore `go test -race ./govhttptest/...` → exit 0; `golangci-lint` → 0 issues
- CI run 27607068239 (`contract` job) → success

## Task completion gate (archive-time reconciliation — recorded reason)

The tasks artifact for this change lives in Engram (obs #914), not as a
`tasks.md` file on disk (none exists in the change folder). Obs #914 still shows
PR-B groups C–H as "not started" with unchecked bullets — this is a **STALE
artifact**: it was written before `sdd-apply` ran and was not re-synced after
PR-B completed.

Per the sdd-archive Strict-vs-OpenSpec policy, archive-time reconciliation is
permitted ONLY when apply-progress + verify-report prove every task is complete.
That proof exists here:

- `sdd/governance-advisory/apply-progress-pr-b` (obs #916): "All tasks groups
  C,D,E,F,G,H DONE" — committed locally, then merged as PR #100.
- `sdd/governance-advisory/apply-progress-pr-a` (obs #915): PR-A DONE/GREEN
  (commit 8924ad0), merged as PR #101.
- `sdd/governance-advisory/apply-progress-seam` (obs #917): govcore seam DONE,
  merged as PR #10.
- `verify.md` / obs #919: GAP A + GAP B tasks Complete; verdict PASS; all PRs
  MERGED.

**Reconciliation reason**: the stale "not started" text in obs #914 reflects
pre-apply tasks state only; apply-progress (#915/#916/#917) and verify (#919)
prove all implementation work is complete and merged to main. No incomplete
implementation work blocks this archive. The Engram tasks observation is the
stale artifact, not the source of completion truth here.

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

## Artifact references (traceability)

**Engram observations (project 2026)**:
- Explore: `sdd/governance-advisory/explore` — obs #909 (GAP A premise was WRONG; corrected downstream)
- Proposal: `sdd/governance-advisory/proposal` — obs #910 (corrected, overwrites prior false-premise version)
- Spec: `sdd/governance-advisory/spec` — obs #911 (replaced `governance-decision-facade` with `governance-integration-contract`)
- Design: `sdd/governance-advisory/design` — obs #912
- Tasks: `sdd/governance-advisory/tasks` — obs #914 (PR-B section stale; reconciled above)
- Apply-progress PR-A: `sdd/governance-advisory/apply-progress-pr-a` — obs #915
- Apply-progress PR-B: `sdd/governance-advisory/apply-progress-pr-b` — obs #916
- Apply-progress seam: `sdd/governance-advisory/apply-progress-seam` — obs #917
- Verify-report: `sdd/governance-advisory/verify-report` — obs #919
- Archive-report: `sdd/governance-advisory/archive-report` (this file, persisted to engram)

**OpenSpec files**:
- Change folder: `openspec/changes/governance-advisory/` (archived in place via this `archive.md`, per repo convention — changes are not moved to an `archive/` dir)
- Main specs (2 new capabilities): `openspec/specs/governance-integration-contract/spec.md`, `openspec/specs/advisory-critic/spec.md`
- ADR: `docs/adr/0015-governance-advisory-contract-lock-and-critic.md`
