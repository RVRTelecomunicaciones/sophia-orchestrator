# Archive Report: context7-bootstrap (V1)

**Archived**: 2026-06-12T00:00:00Z
**Verdict**: PASS_WITH_WARNINGS (0 CRITICAL, 0 WARNING, 4 SUGGESTION)
**Scope**: SDD change context7-bootstrap — greenfield detection + version-drift bootstrap + deterministic skill importer

## Intent

A greenfield repo (no execution evidence → no skills) receives stale training-data guidance; version drift (manual manifest bump) is invisible. Context7 bootstrap solves both: event-driven (greenfield OR drift), degraded-first, fires async post-INIT, imports docs as `candidate` skill via idempotent `InsertIfAbsent`, preserves D11 (INIT never calls LLM). Scope: PR1 (agent-mcp TOML), PR2 (greenfield + cache fix), PR3a–PR3c-ii (drift + importer + wiring).

## PRs Landed (6)

| PR | Repo | Merged | Commits | LoC | Notes |
|---|---|---|---|---|
| agent-mcp #21 | sophia-agent-mcp | 2026-06-10 | 1 | ~25 | Context7 `[[mcp_providers]]` TOML block; zero proxy code changes |
| orch #90 | sophia-orchestator | 2026-06-10 | 2 | ~390 | Greenfield flag + manifest-hash cache key + async wire |
| orch #91 | sophia-orchestator | 2026-06-11 | 2 | ~300 | Semver helpers + `AppliesWhen.FrameworkMinVersion` + matcher version gate |
| orch #92 | sophia-orchestator | 2026-06-11 | 2 | ~430 | `DocsProvider` port + deterministic `SkillImporter` + PG integration |
| orch #93 | sophia-orchestator | 2026-06-11 | 2 | ~340 | Rate guard + Context7 docs adapter |
| orch #94 | sophia-orchestator | 2026-06-12 | 2 | ~330 | Trigger service + wiring + integration tests |

## 7 Capabilities Delivered

| # | Capability | Evidence | Status |
|---|---|---|---|
| 1 | context7-provider-registration | agent-mcp #21; `configs/example.toml:258-266`; id=context7, npx command, persistent, startup_timeout_s=20, exactly 2 tools, env CONTEXT7_API_KEY | PASS |
| 2 | manifest-hash-cache-invalidation | orch #90; `key_builder.go:75-113`; 8th component, 7-name sorted set, absent sentinel, 16-hex truncation; D-C7-7 acceptance test (identical porcelain + diff manifest bytes → diff key) | PASS |
| 3 | greenfield-detection | orch #90; `context.go` Greenfield bool omitempty; detector sets `len(Frameworks)==0 && len(Languages)==0` as last step; `SophiaDetectorVer="v1.1.0"`; async fire post-persist/advance, traceBackground, 60s default, recover(), nil-safe | PASS |
| 4 | applies-when-version-semantics | orch #91; `skill/semver.go` MajorOf/DriftsForward (stdlib-only, tolerates "22.0.0","go 1.26","^18","v3.2"); `AppliesWhen.FrameworkMinVersion map omitempty`; matcher gate fail-open + WARN, empty-map path unchanged | PASS |
| 5 | skill-importer-deterministic | orch #92; fixed template (REFERENCE-DATA banner + Best practices + Provenance), sanitizeBody escapes `## Rule:/Routine:/Skill:` + fences, 24 KiB truncateBody with rune-boundary, version=full detected, phases explore/proposal/apply, no LLM/MCP; determinism test (not goldens) | PASS |
| 6 | bootstrap-trigger-service | orch #94; key guard → rate guard → greenfield branch → drift branch → resolve/threshold-fallback/GetDocs(tokens=8000)/import; all failures WARN+swallow; never panics; no LLM; 13 spec scenarios SVC-A…M tested | PASS |
| 7 | skill-matcher-structural | orch #91; anyFrameworkMatches — optional gate active only when map non-empty for matched fw; detected_major >= min_major; parse-fail → fail-open + WARN; name-only path byte-unchanged | PASS |

## Design Decisions Verified

All ten decisions from the design are implementable and implemented:

- **DG-C7-1**: Context7 TOML block; zero proxy code changes. Verified: wire.go/server.go already loop all providers.
- **DG-C7-2**: 8-component manifest-hash cache key. Verified: D-C7-7 bug (dirty manifest masked by porcelain) confirmed real; acceptance test proves fix works.
- **DG-C7-3**: `Greenfield = len(Frameworks)==0 && len(Languages)==0` set as last detector step; `SophiaDetectorVer` → `v1.1.0`; NO SchemaVersion bump.
- **DG-C7-4**: `FrameworkMinVersion map[string]string` additive JSONB field, no migration.
- **DG-C7-5**: Bootstrap fires AFTER phase persist+advance; injected `Scheduler`; detached ctx + 60s timeout; mandatory `recover()`.
- **DG-C7-6**: In-memory per-process `MemoryRateGuard` (sliding 24h window); default 5/project/day; documented V1 limitation.
- **DG-C7-7**: Name `stack/<framework>-<major>`; version = full detected version (e.g. "22.0.0"); drift = NEW (name,version) row; old row stays active.
- **DG-C7-8**: NEW outbound port `DocsProvider` + adapter reusing dispatcher's `StreamableClientTransport`; per-call Connect→CallTool→Close.
- **DG-C7-9**: `skill.MajorOf` / `skill.DriftsForward` pure helpers; matcher gate fail-open + WARN; drift lives in service, not matcher.
- **DG-C7-10**: Importer = fixed template (header + sanitized body); no LLM/MCP inside importer; 24 KB budget; tokens=8000; MinSnippets=50.

## Test Evidence

| Suite | Result | Notes |
|---|---|---|
| orch unit + lint | PASS | All packages incl. bootstrap, init, skill, phase, pg; forbidigo/wrapcheck/errorlint clean |
| orch PG integration | PASS except R-4 | 12 new T4.6/T5.9 tests PASS; 3 pre-existing R-4 failures (unchanged, outside this change scope) |
| agent-mcp unit + lint | PASS | All packages; -race clean |

**R-4 failures (pre-existing, not introduced):**
- TestMigration010_PreState, TestMigration010_RoundTrip (expect 7 cols, see 16)
- TestSkillRepo_FindByPhase_MatchingRow (expects 2 rows, sees 0)

## Findings

**CRITICAL**: None.

**WARNING**: None.

**SUGGESTION**:

- **S1 — Drift heuristic narrows to consecutive majors (design-bounded).** `service.go:166-216` `runDriftCheck` looks up only `stack/<fw>-<detectedMajor-1>`. A skip-major jump (detected v24 vs active v22, no v23 row) will NOT fire drift. The `SkillLookup.ActiveByName` port only supports exact name lookup (not range), so this is consistent with design DG-C7-9. Spec scenario (22→23, consecutive) is tested (SVC-F). Known V1 boundary; backlog item for multi-major jumps.

- **S2 — `extractMajor` not unified with `skill.MajorOf` (optional refactor).** `importer.go:240` `extractMajor` (returns string major) coexists with domain `skill.MajorOf` (returns int). Both correct and tested; optional unification skipped. Pure cleanup; no functional impact.

- **S3 — Importer goldens via determinism test, not testdata files (accepted deviation).** `importer_test.go:67` asserts byte-identical content via fixed clock + ID, plus structural `Contains` assertions, instead of committed `testdata/*.golden`. Equivalent determinism guarantee; documented deviation in apply reports.

- **S4 — `isDocsUnavailable` via substring vs `errors.Is` (cosmetic).** `service.go:267-269` detects sentinel via substring rather than `errors.Is`. Adapter returns bare sentinel (works today); `errors.Is` would be more robust against future wrapping. Behavior is correct under current wrapping.

## Reconciliation Items (verified, NOT findings)

R1 (in-memory rate guard), R2 (7-name hash), R3 (single sanitized body), R4 (importer signature), R5 (version=full), R6 (nil Bootstrap), R7 (`stack/go-1` from "1.26") — all implemented per design/tasks. Per verify instructions these divergences from spec literal text are authoritative and NOT raised as findings. DG-C7-8 transport reinterpretation (new `DocsProvider` port + adapter) is implemented and honored.

## Notable Patterns

- **Fourth consecutive design-correction cycle**: M1 enums, M3 IncludeTypes, M3 callsite count, M4 lifecycle, **M5 transport mechanism** (D-C7-8 corrected mid-design). Spec+design checks-and-balances is now institutional.
- **ZERO auto-activation**: `InsertIfAbsent` alone; no status promotion path anywhere. All 12 new/modified integration tests confirm this.
- **Context7 as dual-mode consumer**: First real test of M4's `ExternalMCPProxy` + `AllowlistEnforcer` multi-provider; graphify unchanged, context7 alongside.
- **Deterministic importer beats LLM-assist**: Context7 v22 Angular entry returns LLM-targeted structured content (standalone, signals, inject, OnPush) that maps 1:1 to skill-body sections. Deterministic assembly sufficient for V1; LLM-assisted draft deferred to V2 (behind governance gate if shipped).

## Forwarded to Backlog (Named Priorities)

### From Verification (New Items)

- **S1 — multi-major drift jump detection** (SUGGESTION, low priority): handle skip-major jumps (v22 → v24 with no v23 row). Requires `SkillLookup.ActiveByVersionRange` or similar. Backlog note; V2 candidate.

- **S2 + S4 — cosmetic cleanups** (SUGGESTION, lowest priority): unify `extractMajor` with `skill.MajorOf`, switch `isDocsUnavailable` to `errors.Is`. No functional impact; polish-only.

### Remaining from M4+ Backlog (Unchanged)

- **Loop hardening** (items 3+4+5+8+9): digest filter hardening, full-pipeline benchmark, webhook outbox, instrumentation (rollback_count, deprecated_api_hits counter producer), retry baseline.
- **Governance + advisory** (items 6+10): LLM critic opt-in, governance-core HTTP surface.
- **Trivial** (item 7): GET /usage skill_id.
- **R-2 (agent-mcp)** — proxy-tool-schemas: hydrate InputSchema from graphify/context7 `tools/list` on first connect.
- **R-3 (agent-mcp)** — proxy-spawn-mutex: per-provider locks instead of single global (more relevant now: multi-provider graphify + context7).
- **R-4 (orch)** — pg-integration-suite repair: fix 3 pre-existing test failures; wire integration tests into Makefile.

### V2 Candidates from context7-bootstrap Design

- Vendor official MCP chain (angular.dev/ai/mcp, llms.txt direct fetch).
- `deprecated_api_hits` backstop trigger (counter producer still missing).
- LLM-assisted importer draft (behind governance gate).
- Durable rate guard (v1 = in-memory; cross-process guard via Postgres).
- LanguageInfo version drift (parse raw VersionEvidence "go 1.26" → semver, currently only framework versions compared).

## SDD Cycle Complete

```
Explore → Propose → Spec → Design (8 decisions) → Tasks (54 items)
→ Apply (6 PRs, stacked-to-main) → Verify (PASS_WITH_WARNINGS, 0 CRITICAL)
→ Archive ✅
```

## Artifact References (Traceability)

| Phase | File/Location |
|---|---|
| Explore | `/openspec/changes/context7-bootstrap/explore.md` |
| Proposal | `/openspec/changes/context7-bootstrap/proposal.md` |
| Spec | specs/*.md (7 specs per SDD v1 contract) |
| Design | `/openspec/changes/context7-bootstrap/design.md` |
| Tasks | `/openspec/changes/context7-bootstrap/tasks.md` |
| Verify | `/openspec/changes/context7-bootstrap/verify-report.md` |
| Archive | This document |

## Closure

**Change context7-bootstrap is CLOSED and ARCHIVED.** All six PRs merged to main. All 7 capabilities shipped. All 54 tasks completed and verified. All 10 design decisions implemented. Strict TDD evidence present. All HARD operator invariants hold. Findings (0 CRITICAL, 0 WARNING, 4 SUGGESTION) feed into backlog and V2 roadmap.

**Next recommended actions:**
1. Close/merge tracking issue if one exists.
2. Update roadmap: context7-bootstrap ✅ DONE (6 PRs, 7 capabilities, 100% task completion).
3. Prioritize from forwarded items: R-4 pg-integration-suite repair (MEDIUM; blocks integration test suite), then R-2/R-3 (agent-mcp DX/perf), then loop hardening (customer-facing observability).
4. Plan V2 candidates for next SDD cycle.
