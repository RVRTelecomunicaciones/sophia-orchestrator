# Archive Report: prereqs-fts-archived-worker-mcp (M-KNOW-PRE-0)

**Change**: prereqs-fts-archived-worker-mcp
**Archived**: 2026-06-08
**Mode**: openspec + Engram (hybrid)
**Verification verdict**: PASS_WITH_WARNINGS (5 non-blocking warnings)
**Strategy doc**: V4.1 (sophia_hermes_learning_loop_strategy_v4_1.md)

---

## Intent

M-KNOW-INIT-0 ("Graphify as Memory") cannot start until four prerequisite gaps in the Sophia platform are closed. V4.1 D12 locks the FTS configuration to `'simple'` so language-agnostic indexing works end-to-end across `memories`, `decisions`, and `heuristics` — preventing silent English content loss when tokenizing through `pg_catalog.spanish`. V4.1 D13 locks `phase.archived` as a first-class orchestrator event so the consolidation worker has a stable, filterable trigger (filtering `phase.completed` by status is explicitly rejected per D13). The worker entrypoint was discovered during explore to exist at `sophia-memory-engine/cmd/workers/main.go` (not in sophia-orchestator as initially supposed), requiring a skeleton implementation to prove the handler shape compiles and runs. Finally, `sophia-agent-mcp` has no schema for declaring MCP providers, mandating a TOML `mcp_providers[]` array with `tools_allowed` allowlist enforcement. Without PRE-0 complete, M-KNOW-INIT-0 has no foundation: no FTS stability, no archive signal, no worker process, and no provider declaration surface.

---

## Capabilities delivered (4)

| Capability | Status | Where (merged HEAD) |
|---|---|---|
| fts-simple-config | DELIVERED | sophia-memory-engine main `9c93527`; migration 005 + code-side defaults |
| phase-archived-event | DELIVERED | sophia-orchestator main `3bfeeaa` + sophia-cli synced; EventPhaseArchived constant + PhaseArchivedPayload + emission |
| consolidation-worker-skeleton | DELIVERED | sophia-memory-engine main `9c93527`; cmd/workers/main.go runnable with graceful lifecycle + stub handler + FakeSubscriber |
| mcp-providers-config | DELIVERED | sophia-agent-mcp main `1678261`; MCPProviderConfig TOML schema + loader validation + AllowlistEnforcer |

---

## PRs landed (4 + 4 zombie closures)

| Repo | PR | Merged | Evidence | Notes |
|---|---|---|---|---|
| sophia-memory-engine | #16 | 2026-06-08T09:19:17Z | `go get` references; migration 005.{up,down}.sql committed | Includes CI fix commit `c172260` after `::text` cast discovered |
| sophia (CLI) | #20 | 2026-06-08T08:52:54Z | EventPhaseArchived mirrored in `pkg/contract/events.go` | ~27min loose before orch #77; tolerable by wire direction |
| sophia-orchestator | #77 | 2026-06-08T09:14:51Z | EventPhaseArchived + PhaseArchivedPayload + service.go emission | Merged commit `bdad617` |
| sophia-agent-mcp | #18 | 2026-06-08T09:19:21Z | MCPProviderConfig + validation + AllowlistEnforcer | Merged commit `34fa0e1` |

**Zombie closures** (recovery PRs #72 + #76 carried commits to main, originals closed):
- sophia-orchestator #71 (apply-quota-breaker) — CLOSED
- sophia-orchestator #73 (skill-domain-mechanism) — CLOSED
- sophia-orchestator #74 (skill-prompt-integration) — CLOSED
- sophia-orchestator #75 (skill-content-seed) — CLOSED

---

## Operator-locked decisions

### Strategy decisions locked in V4.1 D12 + D13:

1. **D12 — FTS Language = 'simple'** (widened to 3 tables during explore, approved)
   - Rationale: English content indexing + language-agnostic searches require tokenization through `pg_catalog.simple`, not `spanish`.
   - Evidence in code: `migrations/postgres/005_fts_simple.up.sql` updates all 3 tables; `postgres_fts.go` and `memory.go` constants match.

2. **D13 — `phase.archived` as distinct event** (not filtering `phase.completed`)
   - Rationale: Worker must subscribe to a specific event; filtering by status is rejected as fragile.
   - Evidence in code: EventPhaseArchived constant exists; emission gated on `completed == phase.PhaseArchive`; CLI mirrors the constant via wire_alignment_test gate.

### Design-phase decisions (locked in SDD design):

3. **D-PRE-1: FTS migration via per-row UPDATE + ALTER DEFAULT** (idempotent)
   - No down-migration FTS config dependency in up. Down migration has operator-approved DO block guard for rollback safety.
   - Evidence: `005_fts_simple.up.sql:26-32` with `WHERE fts_language::text = 'spanish'`; down with EXISTS guard on pg_ts_config.

4. **D-PRE-2: `phase.archived` emission AFTER envelope persist + state durable** (Iron Law D1.2)
   - Emission happens inside `advanceChange`, after `c.MarkCompleted` + `ChangeRepo.Save` returns nil.
   - Evidence: `service.go:913-930` nested-if guards; test B.2 asserts no emission on Save error.

5. **D-PRE-3: Cross-repo PR pairing for orch + CLI** (hard CI gate via wire_alignment_test)
   - Single PR pattern enforced; both repos land together with operator approval.
   - Evidence: Both orch and CLI changes present on mains; same-commit-pair observed with tolerable 27-min window.

6. **D-PRE-4: Worker EventSubscriber interface for M2-agnostic transport**
   - Interface is transport-neutral; production wiring is TODO(M2) typed nil; tests use FakeSubscriber.
   - Evidence: `consolidation/subscriber.go` defines interface; `main.go:23-27` has TODO comment; no SSE/webhook/MQ imports.

7. **D-PRE-5: TOML `mcp_providers[]` via existing BurntSushi/toml** (no new parser)
   - Struct tags handle array-of-tables; validation in loader adds sentinel constants + validation block.
   - Evidence: `config.go:306` with `toml:"mcp_providers"`; `loader.go:88-142` validation; no new imports in go.mod.

### Apply-phase adaptations approved during execution:

8. **Migration 005 `::text` cast on WHERE** (postgres:16-alpine FTS dictionary discovery)
   - Problem: Without cast, Postgres tries to resolve `'spanish'` as regconfig via pg_ts_config; Alpine CI image lacks `spanish` config → SQLSTATE 22P02.
   - Fix: Cast LHS to `text` in both up and down WHERE clauses; down also wrapped in DO block with EXISTS guard.
   - Evidence: Commit `c172260 fix(fts): use ::text cast...`; CI now green.

9. **`PhaseRepo.FindByChangeAndType` instead of new `advanceChange` parameter**
   - Minimum-diff: repo method already exposed; no function signature change required.
   - Evidence: `service.go:921` uses PhaseRepo lookup; design specified this as locked decision.

10. **`ErrUnknownProvider` sentinel added** (beyond original spec)
    - Distinguishes "provider not found" from "tool not in allowlist" for better diagnostics.
    - Evidence: `allowlist.go:21-23` defines both; `allowlist_test.go` covers both sentinels; operator-approved during apply.

11. **Makefile `test-unit` expanded to include `adapters/...`**
    - New `postgres_fts_literal_test.go` runs under unit target without testcontainers.
    - Evidence: Test exists and passes; Makefile rule confirmed.

12. **`shared.RealClock` instead of design's `SystemClock`** (codebase convention)
    - Memory-engine codebase uses RealClock; consistency over design sketch.
    - Evidence: `cmd/workers/main.go:16` uses `shared.RealClock{}`; matches `internal/domain/shared/temporal.go:11`.

13. **`example.toml` includes `[[mcp_providers]]` example**
    - Guard test `TestConfigStructTagsCoveredByExampleTOML` requires every struct tag in at least one example.
    - Evidence: `configs/example.toml:222-228` has commented example; test passes.

---

## Forwarded to M2 / INIT-0

5 non-blocking warnings from verify that are explicit non-goals of PRE-0 and become input for next milestones:

1. **M2 (worker EventSubscriber transport)**: Choose and implement real transport (SSE / webhook / message bus); current `cmd/workers/main.go:23` is typed nil with TODO(M2) marker.

2. **M2 (consolidation logic)**: Implement episodic→semantic promotion, heuristic emission, ProjectDNA update inside the stub handler; PRE-0 only logs receipt.

3. **M2 (wire-conformance test)**: Add integration test verifying `PhaseArchivedPayload` (orch) ≈ `PhaseArchivedReceived` (worker) over the chosen transport; current duplication is intentional (independent modules) but can drift silently.

4. **INIT-0 (AllowlistEnforcer wiring)**: Wire `AllowlistEnforcer.Authorize` into the MCP tool dispatch path; enforcer exists + is tested but not yet called by any production code path.

5. **Deploy runbook (migration 005 environment note)**: Document that `migration 005.down` is conditional on Postgres image having a `spanish` FTS config in `pg_ts_config`; postgres:16-alpine lacks it (DO block fallthrough to NOTICE). Production rollback procedures must account for this branching.

---

## Process lessons

### Lesson 1: Same-commit-pair timing requires tight merge windows

**Observation**: CLI PR #20 merged ~27 minutes before orch PR #77. Design mandated atomic "single PR pairing", but the commits landed separately across a real-time window.

**Why it worked**: The wire_alignment_test enforces orch ⊆ CLI direction. CLI's `knownEvents` map is allowed to contain constants not yet defined in orch (they are whitelisted separately). The 27-minute window was therefore technically safe — by the time the test ran on orch #77, CLI #20's mirror was already upstream.

**Recommendation for M2**: When landing cross-repo PRs, use git merge queue or single-step atomic merge to tighten the window. The pattern works but should not drift into multi-minute lags.

### Lesson 2: postgres:16-alpine FTS dictionary gotcha

**Discovery**: Any migration that compares a REGCONFIG column to a non-built-in language literal MUST use `column::text = 'lang'` to avoid triggering Postgres's regconfig resolution lookup.

**Context**: `'spanish'` is a non-built-in FTS config. When Postgres encounters `WHERE fts_language = 'spanish'`, it tries to resolve `'spanish'` as a regconfig OID by looking it up in `pg_ts_config`. The postgres:16-alpine container image does NOT include `spanish` configuration → SQLSTATE 22P02 invalid input syntax for type oid.

**Mitigation**: Cast LHS to `text` — `WHERE fts_language::text = 'simple'`. The `'simple'` config is always built-in and safe to compare directly, but for consistency and defense against future configs, apply the cast universally.

**Why it matters**: Future migrations or queries touching FTS columns must follow this pattern. The gotcha is not documented in postgres:16 release notes prominently enough.

### Lesson 3: Zombie PR cleanup pattern

**Observation**: When using recovery-PR merge pattern (engram #747 describes it), the original slice PRs remain OPEN after their commits are merged via recovery PR. Manual closure is required.

**Pattern discovered**: 
- Original PR #71, #73, #74, #75 are authored; commits authored but not merged.
- Recovery PR #72 cherry-picks commits and merges to main.
- Original PRs still show state=OPEN with old commits.
- Manual `gh pr close` required for each zombie.

**Recommendation for M2+**: Add a post-merge cleanup step to the recovery-merge runbook. Script: `for pr in orig_prs; do gh pr close $pr --comment "Merged via recovery PR #XYZ"; done`.

---

## V4.1 status update

M-KNOW-PRE-0 is **COMPLETE** and **CLOSED**. All four capabilities are live on origin/main across the 4 repos (sophia-orchestator, sophia-memory-engine, sophia-cli, sophia-agent-mcp).

**Next milestone in chain**: M-KNOW-INIT-0 (Graphify structural detector + Sophia Go adapter layer + INIT phase spawn-and-merge for detecting Graphify edges in decision DAGs).

**Next phase after archive**: Begin SDD exploration for INIT-0 scope; start codebase assessment of Graphify Python submodule (already cloned; commit ready per apply-progress).

---

## SDD cycle complete

```
Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅
```

**Cycle metadata**:
- Proposal ID: M-KNOW-PRE-0
- Strategy ref: sophia_hermes_learning_loop_strategy_v4_1.md (V4.1 §11 + §16)
- Strict TDD: ACTIVE (per sdd-init/2026); all production logic preceded by RED tests
- Cross-repo coordination: Enforced by wire_alignment_test CI gate
- Operator approvals: 6 checkpoints (A.8, B.10, C.7, D.7, E.1–E.6)
- Total changed lines across 4 repos: ~650–720 (chained into 3 PRs per delivery strategy)
- Artifact store: Hybrid (openspec/changes/prereqs-fts-archived-worker-mcp + engram topic_keys)

---

## Artifacts and traceability

All artifacts indexed for recovery across sessions:

| Artifact | Topic Key | Location |
|---|---|---|
| Proposal | `sdd/prereqs-fts-archived-worker-mcp/proposal` | /openspec/changes/.../proposal.md |
| Spec (fts-simple-config) | `sdd/prereqs-fts-archived-worker-mcp/spec-fts-simple-config` | /openspec/changes/.../specs/fts-simple-config/spec.md |
| Spec (phase-archived-event) | `sdd/prereqs-fts-archived-worker-mcp/spec-phase-archived-event` | /openspec/changes/.../specs/phase-archived-event/spec.md |
| Spec (consolidation-worker-skeleton) | `sdd/prereqs-fts-archived-worker-mcp/spec-consolidation-worker-skeleton` | /openspec/changes/.../specs/consolidation-worker-skeleton/spec.md |
| Spec (mcp-providers-config) | `sdd/prereqs-fts-archived-worker-mcp/spec-mcp-providers-config` | /openspec/changes/.../specs/mcp-providers-config/spec.md |
| Design | `sdd/prereqs-fts-archived-worker-mcp/design` | /openspec/changes/.../design.md |
| Tasks | `sdd/prereqs-fts-archived-worker-mcp/tasks` | /openspec/changes/.../tasks.md |
| Apply progress | `sdd/prereqs-fts-archived-worker-mcp/apply-progress` | Engram #779 (merged state + 3 issue resolutions) |
| Verify report | `sdd/prereqs-fts-archived-worker-mcp/verify-report` | /openspec/changes/.../verify.md |
| Archive report | `sdd/prereqs-fts-archived-worker-mcp/archive-report` | /openspec/changes/.../archive.md (this file) + Engram |

---

## Sign-off

**Archive executed**: 2026-06-08 23:59:59 UTC by sdd-archive executor (Haiku 4.5)
**Verification verdict**: PASS_WITH_WARNINGS (0 CRITICAL, 5 non-blocking warnings, 5 suggestions for M2/INIT-0)
**Status**: READY FOR NEXT MILESTONE

All change artifacts persisted. No blockers for M-KNOW-INIT-0 launch. Five warnings and five suggestions documented and forwarded to successor phases.
