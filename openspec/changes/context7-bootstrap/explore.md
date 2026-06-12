# Exploration — context7-bootstrap

**Strategy ref:** V4.1 §5.2 enums, §6.1 thresholds, D11 anti-pattern, operator-approved direction.
**Mode:** SDD explore. NO production code changes; investigation + scoping.
**Primary repo:** sophia-orchestator. Secondary: sophia-agent-mcp, sophia-memory-engine.
**Engram artifact:** `sdd/context7-bootstrap/explore`.

---

## 1. Problem: greenfield/cold-start gap

A new repo (e.g., an Angular v22 project) has no execution evidence → no skills →
`SkillMatcher.SkillsForContext` returns empty → PromptBuilder emits no skill guidance →
the LLM defaults to stale training-data knowledge for framework version and patterns.

**Operator-approved solution direction:**

1. **Event-driven triggers only**: Context7 called on (a) greenfield — no skill for detected stack; (b) version drift — INIT-detected version doesn't match active skill's `applies_when`; (c) backstop — `deprecated_api_hits` rises. Zero doc-source calls when versions match.
2. **Source priority chain**: vendor official MCP > Context7 > llms.txt direct fetch.
3. **D11 preserved**: INIT never creates skills. Bootstrap is a separate flow. Imported skills enter as `status=candidate` + `activation_source=imported`; old version stays active until new one is promoted.
4. **Greenfield with no stack signal**: INIT flags greenfield; stack choice escalates to human in PROPOSE; bootstrap fires only after stack decision is recorded.
5. **Context7 constraints**: free tier ~1,000 req/month; ~15s latency; treat output as DATA not instructions; bleeding-edge version entries thin — fallback to main entry.

---

## 2. Current state per item (file:line evidence)

### Item 1 — INIT detector: version capture

**Status: DONE. Versions are already captured.**

- `parser_go.go:27-29` — `parseGoMod` extracts `go X.YY` from `go.mod` → `LanguageInfo.VersionEvidence = "go 1.26"`.
- `parser_node.go:45-48` — `parseNodeManifest` calls `stripSemverPrefix(allDeps["@angular/core"])` → `FrameworkInfo.Version = "22.0.0"` (stripped). File:line: `parser_node.go:46-48`.
- `parser_python.go` — `parsePyprojectToml` and `parseRequirementsTxt` exist; framework versions extracted similarly.
- `structural.FrameworkInfo.Version string` — `internal/domain/structural/context.go:100` — ALREADY holds detected version.
- `structural.LanguageInfo.VersionEvidence string` — `context.go:87` — holds version string from manifest.

**Gap:** `LanguageInfo.VersionEvidence` is a raw string (`"go 1.26"`) not a parsed semver. `FrameworkInfo.Version` is stripped semver (`"22.0.0"`) but not a parsed range. No `MinVersion` / `VersionRange` field in `AppliesWhen` — `internal/domain/skill/lifecycle.go:117-130` — version-drift comparison requires adding a version constraint field to `AppliesWhen` OR doing it in a new service that reads the raw strings.

### Item 2 — StructuralContext: greenfield flag

**Status: NOT PRESENT. Field doesn't exist.**

- `internal/domain/structural/context.go:29-78` — 15 fields; no `Greenfield bool` or `GreenfieldReason string`.
- Greenfield = `len(sc.Frameworks) == 0 && len(sc.Languages) == 0` after detection, OR manifests detected but `SkillMatcher` returns zero skills for that stack. The first form is deterministic and INIT can set it directly; the second form requires a post-matcher check.
- **Cache key impact**: `SophiaDetectorVer = "v1.0.0"` at `detector/types.go:17`. Adding `Greenfield bool` to `StructuralContext` changes what INIT produces. The 7-component cache key (`cache/key.go:32-40`) uses `SophiaDetectorVer` as component 7 — bumping `SophiaDetectorVer` to `"v1.1.0"` invalidates all caches automatically. This is the correct and safe path.
- `SchemaVersion = SchemaV1 = 1` at `context.go:12-13` — checked at `init/service.go:83`. Schema version bump needed when adding fields that change semantics, but adding `Greenfield bool` is additive/backward-compatible JSON (omitempty). A schema version bump is NOT required; only the `SophiaDetectorVer` cache-key component needs bumping.

### Item 3 — skill/lifecycle.go: AppliesWhen version semantics

**Status: Framework/Language name matching only; no version range.**

- `lifecycle.go:117-130` — `AppliesWhen` has `Framework []string` and `Language []string` (name lists, case-insensitive equality match). No `FrameworkVersion`, `MinVersion`, `VersionRange`, or semver range.
- `skill_matcher.go:243-259` — `structuralMatches` does name-only comparison via `strings.EqualFold`. Version-drift comparison is NOT implemented.
- **What's needed for version drift**: either (a) add `FrameworkMinVersion map[string]string` to `AppliesWhen` + version comparison logic in matcher, or (b) implement version drift check as a separate gate in the bootstrap trigger service (reads `StructuralContext.Frameworks[].Version` and skill's `AppliesWhen.Framework` + a version annotation stored elsewhere).
- `SourceImported ActivationSource = "imported"` — `lifecycle.go:88` — ALREADY exists. ✓
- `StatusCandidate Status = "candidate"` — `lifecycle.go:31` — ALREADY exists. ✓
- Migration 010 CHECK constraint: `activation_source IN ('manual','legacy_seed','archive_worker','llm_proposal','imported')` — `010_skills_lifecycle.up.sql:30` — `imported` is accepted. ✓
- Migration 010 UNIQUE: `UNIQUE(name, version)` — `010_skills_lifecycle.up.sql:37` — importing a skill as a NEW version creates a new row; old version stays active. ✓

### Item 4 — pg/skill_matcher.go: version-drift hook

**Status: No hook point; structural matching is name-only.**

- `skill_matcher.go:237-259` — `structuralMatches` is the ONLY structural gate. It reads `aw.Framework []string` and `sc.Frameworks []FrameworkInfo` but only compares names. No version comparison at all.
- The matcher receives `q.StructuralContext *structural.StructuralContext` (at `skill_matcher.go:241`) which already carries `FrameworkInfo.Version` — the raw data is present; only the comparison logic is missing.
- **Where version-drift comparison hooks in**: either (a) inside `structuralMatches` — check `FrameworkInfo.Version` vs. a new `AppliesWhen.FrameworkMinVersion` field; or (b) in a new `BootstrapTriggerService` that compares the matcher output with the detected stack independently of the matcher filter itself. Option (b) is cleaner for D11 (INIT stays pure; bootstrap is a separate flow) and doesn't require touching the hot-path matcher.

### Item 5 — Bootstrap routine/worker location

**Options evaluated:**

**Option A — Orchestrator-side service triggered post-INIT:**
- After `InitService.Run` completes and before the phase service returns the INIT envelope, a `BootstrapTriggerService` checks for greenfield/version-drift/backstop conditions and asynchronously fires a routine.
- Access to `StructuralContext` is immediate (already in memory at `service.go:129-145`).
- INIT is already async (goroutine via `Scheduler` at `phase/service.go:316`); bootstrap fires inside the same goroutine AFTER `runInitPhase` completes.
- Orch → Context7 path: requires spawning Context7 as a second `[[mcp_providers]]` entry in sophia-agent-mcp. Orch calls agent-mcp's `context7.resolve-library-id` and `context7.get-library-docs` tools through the existing `ExternalMCPProxy` machinery.
- **Conclusion: RECOMMENDED.** Cleanest separation; triggers on real INIT output; uses existing proxy infra.

**Option B — Memory-engine consolidation worker extension:**
- Would require the worker to have access to StructuralContext per change, which it doesn't today.
- The demoter (`demoter.go:37-63`) already has `deprecated_api_hits` awareness but it's unreachable (never incremented, explicitly noted at `demoter.go:18-19`). This is a valid place for the backstop trigger in the future, NOT for greenfield bootstrap.
- **Conclusion: DEFERRED.** Appropriate only for `deprecated_api_hits` backstop in a later pass.

**Option C — sophia-cli command:**
- On-demand only; no automatic trigger. Not event-driven.
- **Conclusion: OUT of scope for V1.**

**How orch reaches Context7:**
- sophia-agent-mcp already has the full pipeline: `MCPProviderConfig` schema (`config.go:184`), `AllowlistEnforcer` (`allowlist.go:31`), `ExternalMCPProxy` (`proxy.go:47`), `StdioClient` (`mcpclient/client.go:34`), and `buildSDKServer` proxy registration (`server.go:311-341`).
- Context7 registers as a SECOND `[[mcp_providers]]` entry in `configs/example.toml` with `command = "npx -y @upstash/context7-mcp@latest"`, `tools_allowed = ["resolve-library-id", "get-library-docs"]`, and `env = {CONTEXT7_API_KEY = "..."}`.
- `mcpclient.New` already supports env forwarding via `Env map[string]string` on `MCPProviderConfig` (`config.go:225`), passed through the `callerFactory` at `wire.go:291-300`. R-1 fix (env forwarding) is already merged. ✓
- The orch calls agent-mcp's proxied tool `context7.resolve-library-id` and `context7.get-library-docs` via the MCP dispatcher — the same path used for graphify tools.

**GraphifySpawner analogy for bootstrap:**
- INIT's `GraphifySpawner` pattern (`init/ports.go` `GraphifySpawner` interface) fires as a parallel goroutine inside `initphase.Service.Run` (`service.go:105-120`).
- Bootstrap is NOT analogous — it must fire AFTER INIT completes (needs the result), not in parallel with it.
- Bootstrap fires as a separate async call: `s.d.Scheduler(bootstrapIfTriggered(...))` in `runInitPhase` after `InitService.Run` returns.

### Item 6 — PROPOSE phase: question rounds for greenfield stack choice

**Status: Question rounds are prompt-contract only, no server-side wiring.**

- `discipline/prompt_builder.go:52-80` — `PromptBuilder.Build` assembles LLM prompts by phase. No server-side "question round" or "ask operator" mechanism exists.
- The PROPOSE phase is dispatched as a standard LLM agent call (`runAsync` path in `phase/service.go:327`). The LLM produces an envelope; if it needs clarification, it sets `status=needs_context` (the `PhaseStatusNeedsContext` handling at `service.go:283-286`).
- `phase.PhaseStatusNeedsContext` exists in the domain — `phase/status.go` — and `service.go:283-286` handles re-runs after a `needs_context` phase.
- **Greenfield stack escalation path**: INIT sets `Greenfield=true` on `StructuralContext`. The PROPOSE agent sees this in `renderStructural` at `prior_context.go:259-281` via the `StructuralCtx` layer. The PROPOSE prompt includes the structural layer. If the operator configures the PROPOSE task description to include greenfield instructions ("if Greenfield=true, ask operator to choose the stack before producing the proposal"), the LLM will emit `needs_context`. The orch pauses; the operator responds with stack choice via the standard retry path.
- **No new server-side code needed for V1 greenfield escalation.** This is purely a PROPOSE prompt-body concern (operator convention). The `Greenfield bool` field on `StructuralContext` is the signal; `renderStructural` already emits all fields.

### Item 7 — Skill creation path for importer

**Status: `SkillRepo.InsertIfAbsent` and `Upsert` are the insertion paths.**

- `skill_repo.go:135-181` — `InsertIfAbsent` inserts a skill row only when `(name, version)` doesn't already exist. Idempotent. This is the correct importer insertion method — D11 constraint: old version stays active; new candidate version is inserted if absent.
- `skill_repo.go:71-130` — `Upsert` fully replaces the row on `(name, version)` conflict. Used by seed/promoter. NOT appropriate for importer (would overwrite active skill row).
- `skill.New` (or `skill.Hydrate` for already-persisted rows) is used to construct the domain object. `New` defaults to `status=candidate`, `activation_source=manual` unless overridden.
- **For an importer**, the insertion path is:
  1. `skill.New(name, version, phases, content, LifecycleInput{Status: StatusCandidate, ActivationSource: SourceImported, ...})`
  2. `SkillRepo.InsertIfAbsent(ctx, skill)` — idempotent no-op if already inserted.
- The importer lives in the orchestrator application layer as a new service (e.g., `internal/application/bootstrap/` or `internal/application/skillimport/`), NOT in INIT.

---

## 3. sophia-agent-mcp: R-2 and R-3 assessment

### R-3: Single global mutex serializing first-call spawns

**Status: CONFIRMED. Single `sync.Mutex` in `proxy.go`.**

- `proxy.go:46-54` — `Proxy` has one `mu sync.Mutex` protecting `callers map[string]ToolCaller`.
- `proxy.go:120-141` — `getOrSpawn` holds `mu` for the ENTIRE spawn-and-connect duration (`p.factory(ctx, providerID)` runs under lock at line 135).
- With two providers (graphify + context7), a first-call to context7 (which has ~15s latency) while a first-call to graphify is in progress would serialize: graphify blocks for 10s startup, then context7 blocks for 15s, total 25s for both. With providers initialized lazily on first call, this only affects cold-start.
- **Impact for context7-bootstrap**: bootstrap fires post-INIT, not on the hot-path. The 15s context7 latency is on a background goroutine. However, if the bootstrap goroutine and a simultaneous graphify first-call race, one will wait. Acceptable for V1. For V2 a per-provider lock map would be cleaner.
- **Decision**: R-3 should NOT ride this change. It's a performance improvement, not a correctness issue. Flag for V2.

### R-2: Placeholder InputSchema on proxy tools

**Status: CONFIRMED PRESENT.**

- `server.go:321` — `InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`)` — every proxied tool gets an empty schema. This means MCP clients that use the schema for validation/autocomplete get no benefit.
- For Context7 specifically: `resolve-library-id` expects `libraryName` (required) and `query` (required); `get-library-docs` expects `context7CompatibleLibraryID` (required), `query` (required), `topic` (optional), `tokens` (optional). These are lost.
- **Impact**: The bootstrap service calling `context7.resolve-library-id` via the proxy constructs its own `map[string]any` args; the schema isn't used server-side for validation. Functionally fine for V1.
- **Decision**: R-2 is a DX improvement. Should NOT ride this change. Flag for future improvement.

---

## 4. sophia-memory-engine: deprecated_api_hits path

**Status: CONFIRMED DEAD PATH.**

- `demoter.go:17-19` — explicit comment: "M4+ unreachable paths (instrumentation gaps): active→deprecated: deprecated_api_hits >= 1 (always 0 in M2)".
- `demoter.go:53` — `shouldDeprecate` condition checks only `AvgRetryReduction`, not `DeprecatedAPIHits`.
- `skill.Metrics.DeprecatedAPIHits int` — `lifecycle.go:139` — field exists in domain struct. `skill_repo.go:322` — `PatchMetrics` increments it additively. But no producer calls `PatchMetrics` with a non-zero `DeprecatedAPIHits`.
- **What would instrument it**: a watcher in the phase service that detects deprecated API patterns in the LLM's output (via regex or LLM-as-judge). This is an M4+ concern. For context7-bootstrap, `deprecated_api_hits` as a backstop trigger is deferred — the counter is structurally present but never incremented, so the trigger can't fire.

---

## 5. Context7 constraints summary (from prior research, live-tested)

| Property | Value |
|---|---|
| Transport | stdio via `npx -y @upstash/context7-mcp@latest` |
| Tools | `resolve-library-id`, `get-library-docs` |
| Auth | `CONTEXT7_API_KEY` env var (free at context7.com/dashboard) |
| Rate limit | ~1,000 req/month free tier (cut 83% Jan 2026 without notice) |
| Latency | ~15s avg per docs call (after 2026 reranking improvement) |
| Bleeding-edge coverage | Angular v22: 7 snippets, score 34.7 — must fall back to main entry |
| Security risk | ContextCrush (Feb 2026): community registry injected AI instructions — patched but structural risk |
| Mitigation | `tools_allowed` pin + treat output as DATA, never instructions |

**Live test result** (from engram obs #854): Angular's main entry (`/websites/angular_dev`) returned LLM-ready best-practices (standalone, signals, inject(), OnPush) — maps directly to skill body content. Quality confirmed.

---

## 6. The scoping proposal

### V1 — IN (tight cut, ~650-750 LoC across 2 repos)

| Component | Repo | LoC | Notes |
|---|---|---|---|
| `StructuralContext.Greenfield bool` field | sophia-orchestator | ~5 | `context.go`; additive JSON field |
| `SophiaDetectorVer` bump to `"v1.1.0"` | sophia-orchestator | ~1 | `detector/types.go:17`; cache invalidation |
| Greenfield detection in `Detector.Detect` | sophia-orchestator | ~15 | `detector/detector.go`; set when no frameworks/languages detected |
| `BootstrapTriggerService` port interface | sophia-orchestator | ~20 | `internal/application/bootstrap/ports.go` |
| `BootstrapTriggerService` implementation | sophia-orchestator | ~80 | `internal/application/bootstrap/service.go`; checks greenfield flag; calls context7 tools via agent-mcp MCP proxy port; inserts candidate skill |
| `SkillImporter` (wraps `SkillRepo.InsertIfAbsent`) | sophia-orchestator | ~40 | `internal/application/bootstrap/importer.go`; D11 safe: `status=candidate`, `activation_source=imported` |
| Wire `BootstrapTriggerService` into `runInitPhase` | sophia-orchestator | ~25 | `phase/service.go`; fire async after `InitService.Run` |
| Tests (strict TDD) | sophia-orchestator | ~200 | Unit + integration; fake context7 responses |
| Context7 `[[mcp_providers]]` entry in `example.toml` | sophia-agent-mcp | ~15 | New block in `configs/example.toml` |
| **Total** | | **~400** | |

### V1 — OUT (explicitly deferred)

| Item | Reason for deferral |
|---|---|
| Version-drift trigger (INIT version vs. skill `applies_when`) | Requires `AppliesWhen` version range field — design decision needed; deferred to V2 |
| `deprecated_api_hits` backstop trigger | Counter never incremented; no producer; M4+ concern |
| Vendor official MCP chain (angular.dev/ai/mcp, etc.) | Additional provider registration per framework; deferred to V2 |
| llms.txt direct-fetch fallback | Engineering complexity; deferred to V2 |
| PROPOSE-phase question-round server-side wiring | Pure prompt contract; no server-side code needed for V1 |
| R-2 InputSchema enrichment on proxy tools | DX improvement; not blocking |
| R-3 per-provider mutex optimization | Performance; not blocking correctness |
| Memory-engine demoter `deprecated_api_hits` instrumentation | No producer; M4+ |

---

## 7. Recommended architecture for V1 bootstrap flow

```
INIT phase completes (InitService.Run returns sc)
    │
    └── runInitPhase (phase/service.go)
           │
           ├── [greenfield check] sc.Greenfield == true?
           │       YES → async goroutine: BootstrapTriggerService.TriggerIfNeeded(ctx, sc)
           │                                │
           │                                ├── call agent-mcp MCP proxy:
           │                                │     context7.resolve-library-id(libraryName=sc.Frameworks[0].Name)
           │                                │     → get best library ID (fallback to main entry if v-specific thin)
           │                                │     context7.get-library-docs(libraryID, query="best practices", tokens=8000)
           │                                │     → raw docs text (treat as DATA)
           │                                │
           │                                └── SkillImporter.ImportFromDocs(name, version, docs)
           │                                      → skill.New(..., LifecycleInput{
           │                                            Status: StatusCandidate,
           │                                            ActivationSource: SourceImported,
           │                                         })
           │                                      → SkillRepo.InsertIfAbsent(ctx, skill) — idempotent
           │
           └── NO → nothing (versions match or no stack detected)
```

**Async safety**: the bootstrap goroutine outlives the phase-run goroutine. It uses a detached context (background) with a configurable timeout (default 60s > 15s context7 latency). Skill insertion failure is logged + discarded (never fails the INIT phase).

**D11 preserved**: INIT still never creates skills. The `BootstrapTriggerService` is a SEPARATE application service. Skills enter as `candidate`. Old active skills are untouched. Governance promotes candidates; INIT orchestrates nothing.

---

## 8. PR delivery proposal

| PR | Repo | Scope | Est. LoC | Chain |
|---|---|---|---|---|
| PR1 | sophia-agent-mcp | Context7 `[[mcp_providers]]` TOML entry | ~15 | standalone, mergeable first |
| PR2 | sophia-orchestator | Greenfield flag + BootstrapTriggerService + SkillImporter + wire + tests | ~400 | depends on PR1 merged OR TOML-only change means PR2 can proceed independently |

PRs are in separate Go modules. Either order works. PR1 first recommended (TOML-only, zero risk, merges fast). PR2 may be within 400-line budget or slightly over — scoping will determine split need.

---

## 9. Operator decisions needed for proposal phase

**Q1 (BLOCKING):** V1 scope cut — confirm IN list as stated above, or expand to include version-drift trigger in V1?
- Option A (recommended): V1 = greenfield only. Version-drift in V2 (requires AppliesWhen design).
- Option B: include version-drift in V1 (adds ~100 LoC for AppliesWhen extension + matcher update, adds design complexity).

**Q2 (BLOCKING):** Where does the bootstrap service call agent-mcp?
- Option A: bootstrap service calls agent-mcp via the existing MCP dispatcher port (same path as phases dispatch). Cleanest; no new port.
- Option B: bootstrap service calls agent-mcp HTTP directly (new outbound port). More explicit but duplicates transport logic.

**Q3 (DESIGN):** Skill naming convention for imported skills.
- How should the importer name the skill? e.g., `"angular-best-practices-v22"` vs. `"angular-scaffold"` vs. content-hash-based?
- The name + version must be stable for idempotent `InsertIfAbsent`.

**Q4 (DESIGN):** Which phases does an imported skill apply to?
- Proposal only? All phases? Depends on whether the skill is "scaffold guidance" (explore/propose) or "implementation guidance" (apply).

**Q5 (OPS):** Free tier budget strategy.
- 1,000 req/month is thin for CI/CD usage. Strategy for prod?
- Options: (a) paid tier (~$10/mo); (b) open-context7 self-hosted; (c) hard cap on bootstrap calls per project per day.

**Q6 (DESIGN):** Fallback when Context7 returns thin results (< N snippets or score < threshold)?
- Fall back to main entry if version-specific entry has <50 snippets (based on live-test evidence: Angular v22 had 7 snippets vs 14,850 for main)?
- Or silently skip bootstrap if quality threshold not met?

---

## 10. Risks

| Risk | Severity | Mitigation |
|---|---|---|
| Context7 15s latency blocks INIT phase response | LOW | Bootstrap is fully async; INIT returns before bootstrap fires |
| Free tier 1,000 req/month exhaustion | MEDIUM | Rate guard in `BootstrapTriggerService`; per-project-per-day cap; skip if token not configured |
| ContextCrush: injected AI instructions in docs output | MEDIUM | Treat output as DATA; never pass raw docs as prompt instructions; extract to structured format first |
| Bleeding-edge version entry too thin (e.g. Angular v22: 7 snippets) | MEDIUM | Fallback to main entry when snippet count < threshold (needs Config field for threshold) |
| `InsertIfAbsent` idempotency: concurrent bootstrap calls for same stack | LOW | `UNIQUE(name, version)` constraint in PG; second insert is a no-op |
| `SophiaDetectorVer` bump invalidates all INIT caches on deploy | LOW-MEDIUM | Acceptable operational cost; communicates intent via `SophiaDetectorVer` bump |
| Skill content from Context7 doesn't map to Sophia skill body format | MEDIUM | Importer must transform raw docs into skill body format (single-step LLM summarization, OR structured extraction from docs); design decision needed |

---

## 11. Skill resolution

`skill_resolution: paths-injected` (skills loaded before work):
- `go-testing` — for strict TDD apply phase
- `api-contracts` — for MCP tool call contracts in bootstrap service
- `background-workers` — for async bootstrap goroutine lifecycle
- `domain-modeling` — for `BootstrapTriggerService` port + domain invariant design
