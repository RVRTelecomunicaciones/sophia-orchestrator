# Archive Report: loop-hardening

**Change**: loop-hardening
**Archived**: 2026-06-16
**Mode**: openspec (files) + Engram (`sdd/loop-hardening/archive-report`, project 2026)
**Verification verdict**: PASS_WITH_WARNINGS (0 CRITICAL, 2 WARNING, 2 SUGGESTION) — `verify.md`, engram obs #907
**Operator decision**: ARCHIVE NOW with follow-ups tracked
**Ecosystem**: sophia-orchestator (orch) + sophia-memory-engine (ME)
**ADR**: 0014 (supersedes ADR-0013 D-M2-1 fire-and-forget transport)

## Intent

The V4.1 learning loop went live in M2 but had two structural defects: (1) the
orch→ME `phase.archived` webhook was fire-and-forget with no retry — a down/erroring
ME dropped the event permanently (the only unprotected link in the loop); (2) the loop
promoted skills on a constant fake metric (`ApplyAttempts: 0` → always `0.333`), so the
promoter gate always passed and the demoter branch was dead code. loop-hardening hardens
delivery (transactional outbox + relay) and feeds the gates real `tasks.attempts` data,
plus two ME-side slices (digest `unknown` filter, in-memory pipeline benchmark).

## Capabilities delivered (6)

| Capability | Type | PR | Where |
|---|---|---|---|
| webhook-outbox | New | PR-A (orch) | orch main 98e3430 (#97) |
| phase-archived-webhook | Modified | PR-A (orch) | orch main 98e3430 (#97) |
| skill-usage-tracking | Modified | PR-B (orch) | orch main 98e3430 (#98) |
| skill-retroactive-reevaluation | New | PR-B (orch) | orch main 98e3430 (#98) |
| change-digest-deterministic | Modified | PR-C (ME) | ME main 74fc5b7 (#19) |
| consolidation-pipeline-benchmark | New | PR-C (ME) | ME main 74fc5b7 (#19) |

## Specs synced to source of truth

| Domain | Action | Detail |
|---|---|---|
| webhook-outbox | Created | Full spec; PK + payload wording reconciled (see below) |
| consolidation-pipeline-benchmark | Created | Full spec; fake-helper wording reconciled to shipped `benchMemoryClient`/`benchSkillsClient` |
| skill-retroactive-reevaluation | Created | Full spec; reversal MUST annotated with FOLLOW-UP-1 |
| phase-archived-webhook | Updated | "Outbound POST" (fire-and-forget) requirement replaced by outbox-backed "Outbound delivery"; LLM/persistence-style requirements N/A here |
| change-digest-deterministic | Updated | "Deterministic ChangeDigest structure" requirement replaced (unknown filter); "LLM MUST NOT" + "Digest persistence" requirements preserved untouched |
| skill-usage-tracking | Updated | "GET /usage" requirement replaced (real apply_attempts); "Migration 011" + "Skill injection write path" requirements preserved untouched |

Main specs: `/Users/russell/Documents/2026/sophia-orchestator/openspec/specs/{domain}/spec.md`.

## Spec reconciliations applied (the 2 SUGGESTIONs)

1. **webhook-outbox PK** — spec said `id (UUID PK)`; corrected to **`CHAR(26)` ULID**
   (injectable `IDGenerator`). Reason: every prior migration (009, 011) uses CHAR(26)
   ULID PKs and CLAUDE.md rule 5 forbids `ulid.Make()` in domain/application — repo
   convention wins; functionally equivalent opaque PK. (Decision obs #883.)
2. **webhook-outbox payload** — spec said `payload (jsonb NOT NULL)`; corrected to
   **`BYTEA NOT NULL`**. Reason: JSONB normalizes whitespace/key-order on storage,
   breaking the byte-identical delivery contract that `phase-archived-webhook` asserts
   (caught by `TestOutboxRelay_EndToEnd_MEDownThenUp`). An outbox is an opaque-blob
   carrier; BYTEA is the correct type and is mandated by the byte-identity spec.
   (Decision obs #885.)

Each reconciliation is annotated inline in the merged spec with a one-line "why".

## Tracked follow-ups (the 2 WARNINGs) — recorded in ADR-0014

### FOLLOW-UP-1 — Reeval single-invocation reversal (verify WARNING #1)

`skill-retroactive-reevaluation` spec: the command MUST be able to reverse a confirmed
change. Shipped impl delegates reversal to the existing admin `PATCH /status` multi-hop
chain (validated 6-enum `allowedTransitions`, documented in CLI help + report footer) —
a real recovery path, but multi-hop, not "the same command". Literal MUST not satisfied.
**Action (deferred, operator-chosen)**: a future change MUST either add `reeval --revert`
(single-invocation undo via prior-status snapshot) OR formally amend the spec MUST→SHOULD
to accept the PATCH-chain delegation. Severity WARNING (working path exists).

### FOLLOW-UP-2 — Per-skill ApplyAttempts attribution (verify WARNING #4)

`apply_attempts` is per-change `SUM(tasks.attempts)` applied to all skills of the change
because `tasks` has no `skill_id`. Per-skill attribution needs a schema change (a
`skill_id` on `tasks` or a usage-join) — explicit Non-Goal here. The per-change basis is
honest and activates the gates, but coarser than ideal. **Action (deferred)**: a future
change should add per-skill attribution via a schema change.

Both follow-ups are also written into `docs/adr/0014-webhook-outbox-and-real-apply-attempts.md`
(the repo's authoritative deferred-work tracking convention — numbered ADRs).

## Final delivery (all MERGED to main)

| PR | Repo | Scope | Merge tip |
|---|---|---|---|
| #97 | sophia-orchestator | PR-A: outbox migration 012 + repo + txn INSERT + relay poller + wiring; fire-and-forget deleted | orch main `98e3430` |
| #98 | sophia-orchestator | PR-B: real `apply_attempts` in GET /usage + retroactive reeval CLI | orch main `98e3430` |
| #19 | sophia-memory-engine | PR-C: digest `unknown` filter + golden regen + in-memory `HandlerV2.Handle` benchmark | ME main `74fc5b7` |

Three independent PRs, each < 400 lines (Low budget risk, no chaining). Strict-TDD
(RED→GREEN→VERIFY) per group; work-unit commits; conventional commits; no AI attribution
(verified by scanning commit bodies). All suites re-run locally with `-race` (not CI-only).

## Implementation deviations (all benign / improvements, from apply-progress)

- Migration 012 `payload` changed JSONB→BYTEA mid-apply when an integration test surfaced
  the byte-identity break (now reconciled into spec, D-LH-1b).
- Benchmark used package-local in-memory fakes (no `fakeOrchServer` in consolidation pkg).
- Golden fixture regen produced a byte-identical file (pre-existing golden already had
  exactly the 3 non-unknown skills) — determinism proven, no fixture diff.

## Task completion gate

All implementation tasks checked. The only `[ ]`/`[~]` boxes (E.4, J.4) are commit/PR
boundary tasks the orchestrator owns; all three PRs are merged, so no implementation task
is incomplete. No stale unchecked work. Archive proceeds cleanly.

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

## Artifact references (traceability)

**Engram observations (project 2026)**:
- Explore: `sdd/loop-hardening/explore` — obs #877
- Proposal: `sdd/loop-hardening/proposal` — obs #878
- Spec: `sdd/loop-hardening/spec` — obs #879
- Design: `sdd/loop-hardening/design` — obs #880
- Tasks: `sdd/loop-hardening/tasks` — obs #881
- Verify-report: `sdd/loop-hardening/verify-report` — obs #907
- Decisions: outbox PK ULID obs #883; payload BYTEA obs #885; reeval reversal deferral obs #882
- Archive-report: `sdd/loop-hardening/archive-report` (this file, persisted to engram)

**OpenSpec files**:
- Change folder: `openspec/changes/loop-hardening/` (archived in place via this `archive.md`, per repo convention — prior changes are not moved to an `archive/` dir)
- Main specs (6 capabilities): `openspec/specs/{domain}/spec.md` (synced)
- ADR: `docs/adr/0014-webhook-outbox-and-real-apply-attempts.md`
