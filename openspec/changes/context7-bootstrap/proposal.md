# Proposal: context7-bootstrap (V1)

## Why

A greenfield repo has **no execution evidence → no skills**. `SkillMatcher.structuralMatches`
returns empty for the detected stack (skill_matcher.go:237-259), `PromptBuilder` emits no skill
guidance, and the LLM falls back to **stale training-data knowledge** for the framework version
and patterns (explore §1). For a project that starts on Angular v22, Sophia silently produces v18-era
guidance.

The gap has two faces, and **both ship in V1** (operator-locked):

1. **Greenfield cold-start** — a brand-new stack has zero imported or evidence-backed skills.
2. **Version drift** — the user upgrades `package.json` (v22 → v23) **manually, with no AI involvement**.
   The manifest is the **only source of truth**; Sophia cannot know about a manual bump unless it
   re-reads the manifests on every INIT and compares the detected version against the active skill's
   `applies_when`. Therefore `AppliesWhen` gains version semantics in V1.

The fix is event-driven and degraded-first: Context7 (Upstash MCP) is queried **only** on greenfield
or drift, never when versions match. Imported docs enter as `status=candidate` +
`activation_source=imported` through the existing idempotent `InsertIfAbsent` path
(skill_repo.go:135-181). **D11 is fully preserved**: INIT never creates skills and never calls an LLM;
bootstrap is a separate async flow. Promotion to `active` happens only by governance after usage
evidence (V4.1 §6.1). Mirrors graphify's degraded-mode philosophy: if `CONTEXT7_API_KEY` is absent,
skip with WARN.

## Scope

### In Scope

- **PR1 (sophia-agent-mcp, ~25 LoC, within budget)**: Context7 `[[mcp_providers]]` block in
  `configs/example.toml` (`command = "npx -y @upstash/context7-mcp@latest"`,
  `tools_allowed = ["resolve-library-id","get-library-docs"]`, `env = {CONTEXT7_API_KEY = "..."}`).
  Reuses the M4 `ExternalMCPProxy` + `AllowlistEnforcer` + persistent-per-process lifecycle with
  **zero proxy code changes** (env forwarding already merged via R-1, config.go:225, wire.go:291-300).
- **PR2 (sophia-orchestator, ~250 LoC, within budget)**: `StructuralContext.Greenfield bool` +
  deterministic detection in `Detector.Detect`; `SophiaDetectorVer` bump `v1.0.0`→`v1.1.0`
  (detector/types.go:17); **manifest-hash cache invalidation** (INIT-0 cache key fix, see D-C7-7);
  greenfield trigger wired async post-INIT in `runInitPhase`. Includes strict-TDD tests.
- **PR3 (sophia-orchestator, ~330 LoC, within budget)**: `AppliesWhen.FrameworkMinVersion map[string]string`
  (lifecycle.go:117-130) + semver-range comparison; `BootstrapTriggerService` (drift + greenfield gate);
  `SkillImporter` (deterministic template assembly, D-C7-2); rate guard; thin-entry fallback.
  Includes strict-TDD tests.

### Out of Scope (stay in backlog)

| Item | Reason |
|------|--------|
| Vendor official MCP chain (angular.dev/ai/mcp, llms.txt direct fetch) | Source chain V1 = Context7 only; R-2/R-3 do not ride this change (V2). |
| `deprecated_api_hits` backstop trigger | Counter never incremented; no producer (demoter.go:17-19). M4+. |
| LLM-assisted importer draft | Deterministic assembly is sufficient for V1 (D-C7-2). |
| R-2 InputSchema enrichment / R-3 per-provider mutex (agent-mcp) | DX/perf only; bootstrap is not hot-path (explore §3). |
| PROPOSE-phase question-round server-side wiring | Pure prompt contract; `needs_context` already exists (explore §6). |
| memory-engine changes | None needed — confirmed. No `deprecated_api_hits` producer in V1. |

## Capabilities

### New Capabilities (for spec phase)

- `greenfield-detection` (PR2): `StructuralContext.Greenfield bool` set deterministically by the
  detector when no frameworks/languages are detected.
- `manifest-hash-cache-invalidation` (PR2): cache key includes a content hash of detected manifests so
  a manual version bump invalidates the INIT cache immediately, not after TTL (D-C7-7).
- `applies-when-version-semantics` (PR3): `AppliesWhen.FrameworkMinVersion` + semver comparison;
  enables drift detection (detected version vs active skill's `applies_when`).
- `bootstrap-trigger-service` (PR3): separate application service that fires on greenfield OR drift,
  calls Context7 via the agent-mcp MCP proxy, and imports a candidate skill.
- `skill-importer-deterministic` (PR3): deterministic template assembly of typed Context7 doc snippets
  into a Sophia skill body — no LLM in the importer (D-C7-2); rate guard + thin-entry fallback.
- `context7-provider-registration` (PR1): Context7 registered as a second `[[mcp_providers]]` entry.

### Modified Capabilities

- `skill-matcher-structural` (PR3): `structuralMatches` (skill_matcher.go:237-259) gains an optional
  version-range gate when `FrameworkMinVersion` is set; name-only behaviour unchanged when absent
  (backward compatible).

## Approach

**Greenfield path (PR2):** `Detector.Detect` sets `Greenfield = len(Frameworks)==0 && len(Languages)==0`.
INIT persists it (additive JSON `omitempty`, no SchemaVersion bump; only `SophiaDetectorVer` bump,
explore §2 item 2). After `InitService.Run` returns in `runInitPhase` (phase/service.go), an async
goroutine fires `BootstrapTriggerService.TriggerIfNeeded(ctx, sc)` with a detached background context
and configurable timeout (default 60s > 15s Context7 latency). Insertion failure is logged and
discarded — it never fails the INIT phase. Greenfield-without-stack-signal ("crea una app de ventas")
is handled at PROPOSE: INIT flags greenfield, the PROPOSE LLM sees it via `renderStructural` and emits
`needs_context` to escalate the stack choice to the human; bootstrap fires only after the stack decision
is recorded (explore §6 — no new server-side code).

**Drift path (PR3):** The version comparison runs inside `BootstrapTriggerService.TriggerIfNeeded`
(NOT the hot-path matcher, D-C7-4). For each detected `FrameworkInfo.Version`, the service finds the
active skill for that framework and compares the detected major against `AppliesWhen.FrameworkMinVersion`.
If detected > active → drift → emit a bootstrap request for the new version. A drift refresh imports a
**NEW skill version row** (`UNIQUE(name,version)`, 010_skills_lifecycle.up.sql:37); the old version
stays `active` until governance promotes the new candidate.

**Importer (PR3):** `SkillImporter.ImportFromDocs` performs deterministic template assembly: typed
Context7 snippets (best-practices, setup, control-flow) are slotted into fixed skill-body sections.
No LLM call (D-C7-2). Output → `skill.New(name, version, phases, body, LifecycleInput{Status:
StatusCandidate, ActivationSource: SourceImported})` → `SkillRepo.InsertIfAbsent` (idempotent).

**Transport:** orchestrator reaches Context7 through agent-mcp's `ExternalMCPProxy` (reuses
`AllowlistEnforcer` + persistent-per-process lifecycle from M4). No direct stdio client in orch.
Calls go through the same MCP dispatcher port the phases already use (D-C7-8).

## Decisions

- **D-C7-1 — Scope = greenfield + version-drift BOTH in V1.** Operator-locked. The manifest is the only
  source of truth; a manual `package.json` bump has no AI involvement, so Sophia must re-read manifests
  every INIT and compare against the active skill's `applies_when`. → `AppliesWhen` gains version
  semantics now (`FrameworkMinVersion`), not in V2.

- **D-C7-2 — Importer is deterministic template assembly, NO LLM (recommendation, not open question).**
  Raw Context7 docs ≠ Sophia skill body. The importer slots **typed doc snippets** (best-practices,
  setup, control-flow sections) into fixed skill-body template sections. This preserves D11 spirit:
  bootstrap never invokes an LLM. **Rationale:** the live test (engram #854) showed Context7's main
  Angular entry already returns LLM-targeted, structured best-practices content (standalone, signals,
  `inject()`, OnPush) that maps 1:1 to skill-body sections — deterministic assembly is sufficient.
  *Fallback if a future framework's docs are unstructured:* an LLM-assisted draft MAY be added in V2,
  but it MUST still produce only a `candidate` gated like `llm_proposal` (governance review before
  activation). V1 ships deterministic-only.

- **D-C7-3 — Naming convention: `stack/<framework>-<major>`** (e.g. `stack/angular-22`,
  `stack/go-1.26`). **Rationale:** the name must be stable for idempotent `InsertIfAbsent`. Framework
  name is lowercased; major version comes from the parsed `FrameworkInfo.Version`/`VersionEvidence`.
  Drift to v23 produces `stack/angular-23` as a distinct `(name, version)` row — the old
  `stack/angular-22` stays active until promotion. The `stack/` prefix namespaces imported skills away
  from evidence-backed and seed skills.

- **D-C7-4 — Drift comparison runs in `BootstrapTriggerService`, not the matcher.** The matcher
  (`structuralMatches`) gains an *optional* version gate (used only when `FrameworkMinVersion` is set),
  but the **drift trigger** (detected vs active) runs in the bootstrap service. **Rationale:** keeps the
  hot-path matcher cheap and keeps INIT pure (D11) — the bootstrap flow stays a separate service. The
  greenfield trigger emits on `Greenfield==true`; the drift trigger emits when detected major > active
  skill's `FrameworkMinVersion` major.

- **D-C7-5 — Fetched docs are DATA, never instructions (ContextCrush guard).** Context7 output is stored
  **only** as skill-body text. It is never injected as system/tool instructions during import, and never
  passed to an LLM at import time (D-C7-2 already guarantees this). `tools_allowed` pins the two safe
  tools. **Rationale:** ContextCrush (Feb 2026) showed community-registry injection of AI instructions;
  treating output as inert data is the structural defense.

- **D-C7-6 — Thin-entry fallback rule.** Bleeding-edge Context7 entries can be thin (Angular v22: 7
  snippets, score 34.7 vs 14,850 for the main entry — engram #854). `resolve-library-id` returns
  snippet counts; if the version-specific entry's snippet count < threshold (config
  `bootstrap.min_snippets`, default 50), the importer queries the **main entry** instead and records the
  actual source ID in the skill body provenance. If even the main entry is below threshold → skip with
  WARN (degraded-first). **Rationale:** a thin entry yields a low-quality skill; the main entry plus a
  recorded version note is more useful than a 7-snippet stub.

- **D-C7-7 — Manifest content hash MUST be in the cache key (CRITICAL — operator scenario).**
  **Finding (file:line, read directly):** the 7-component cache key (cache/key.go:30-66) does NOT
  include manifest content. Component 4 `DirtyTreeHash = sha256(git status --porcelain)`
  (key_builder.go:43-45). Porcelain lists changed file **paths + status codes, not content**. So if
  `package.json` is already dirty and the user bumps v22→v23 **without committing**, the porcelain line
  stays ` M package.json` → **same DirtyTreeHash → cache HIT → stale version served**. The committed
  case is covered (changes `GitHead`), but the operator's exact scenario (manual manifest edit) can be
  masked. **Decision:** add a manifest-content-hash component to the cache key — hash the bytes of the
  detected manifests (`package.json`, `go.mod`, `pyproject.toml`, `requirements.txt`) and fold it into
  the key. This is IN scope for PR2. **Rationale:** the manifest is the source of truth (D-C7-1); the
  cache must reflect manifest changes immediately, not after TTL. *(Folding into `DirtyTreeHash` or
  adding an 8th component are both acceptable; spec phase picks the exact shape.)*

- **D-C7-8 — Orch → Context7 via the existing MCP dispatcher port, not a new outbound HTTP port.**
  **Rationale:** reuses the same path the phases already use to reach agent-mcp tools; no duplicate
  transport logic.

- **D-C7-9 — Quota: free tier + per-project rate guard; skip on missing key.** If `CONTEXT7_API_KEY` is
  absent → skip bootstrap with WARN (degraded-first, mirrors graphify). A per-project rate guard caps
  bootstrap calls (free tier ~1,000 req/month, cut 83% Jan 2026 without notice — engram #854).
  **Rationale:** the free-tier rug-pull risk is real; the guard prevents quota exhaustion from a
  CI/CD loop.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| agent-mcp `configs/example.toml` | Modified | Context7 `[[mcp_providers]]` block (PR1) |
| orch `structural/context.go:29-78` | Modified | `Greenfield bool` field (PR2) |
| orch `detector/types.go:17` | Modified | `SophiaDetectorVer` → `v1.1.0` (PR2) |
| orch `detector/detector.go` | Modified | Set `Greenfield` deterministically (PR2) |
| orch `init/key_builder.go:43-66` | Modified | Manifest content hash in cache key (PR2, D-C7-7) |
| orch `init/cache/key.go:30-66` | Modified | Cache key component (PR2, D-C7-7) |
| orch `phase/service.go` `runInitPhase` | Modified | Async bootstrap fire post-INIT (PR2) |
| orch `skill/lifecycle.go:117-130` | Modified | `AppliesWhen.FrameworkMinVersion` (PR3) |
| orch `pg/skill_matcher.go:237-259` | Modified | Optional version gate in `structuralMatches` (PR3) |
| orch `internal/application/bootstrap/` | New | `BootstrapTriggerService` + `SkillImporter` + ports (PR3) |
| orch `pg/skill_repo.go:135-181` | Reused | `InsertIfAbsent` (no change — importer calls it) |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Manual uncommitted manifest bump masked by `DirtyTreeHash` → stale version | High (operator's scenario) | D-C7-7: manifest content hash in cache key (PR2) |
| Context7 15s latency / quota exhaustion | Med | Fully async (never blocks INIT); per-project rate guard; skip on missing key (D-C7-9) |
| ContextCrush: injected AI instructions in docs | Med | D-C7-5: output stored as DATA only, never instructions, never LLM-fed at import |
| Bleeding-edge entry too thin (Angular v22: 7 snippets) | Med | D-C7-6: fallback to main entry above threshold; skip below |
| `FrameworkMinVersion` version gate breaks name-only matching | Low | Gate is optional — only active when field is set; name-only path unchanged (PR3 test) |
| `SophiaDetectorVer` bump invalidates all INIT caches on deploy | Low-Med | Acceptable one-time operational cost; intentional |
| Concurrent bootstrap for same stack | Low | `UNIQUE(name,version)` → second `InsertIfAbsent` is a no-op |

## Rollback Plan

- **PR1**: TOML-only; revert the block. `ExternalMCPProxy` returns to single-provider (graphify). No state.
- **PR2**: revert. `Greenfield` field drops (omitempty — no migration); `SophiaDetectorVer` reverts;
  cache-key component reverts (caches rebuild once). Bootstrap fire removed.
- **PR3**: revert. `BootstrapTriggerService`/`SkillImporter` removed; `AppliesWhen.FrameworkMinVersion`
  drops; matcher version gate removed (name-only restored). No imported skills means no orphaned rows;
  any already-imported `candidate` rows are inert (never promoted without governance).

## Dependencies

- M4 `ExternalMCPProxy` + `AllowlistEnforcer` + env forwarding (R-1) — delivered (explore §3).
- `SourceImported` / `StatusCandidate` enums + migration 010 CHECK + `UNIQUE(name,version)` — delivered.
- `FrameworkInfo.Version` / `LanguageInfo.VersionEvidence` already captured by the detector — delivered.
- PR order: PR1 (TOML, zero-risk) first; PR2 (greenfield + cache fix) second; PR3 (drift + importer)
  depends on PR2's `Greenfield` field. PR1/PR2 are independent Go modules.

## Acceptance Criteria

**PR1**
- [ ] Context7 registered as a second `[[mcp_providers]]` entry; `tools_allowed` pins only the two tools.
- [ ] `context7.resolve-library-id` and `context7.get-library-docs` callable through agent-mcp proxy.
- [ ] Missing `CONTEXT7_API_KEY` → provider degrades, agent-mcp still serves graphify.

**PR2**
- [ ] `StructuralContext.Greenfield == true` when no frameworks/languages detected; `false` otherwise.
- [ ] `Greenfield` is additive JSON (`omitempty`); no SchemaVersion bump; `SophiaDetectorVer == v1.1.0`.
- [ ] **Cache invalidation test (D-C7-7): an uncommitted manual `package.json` version bump (v22→v23
      while already dirty) produces a DIFFERENT cache key** — manifest hash component changes even though
      `git status --porcelain` is unchanged.
- [ ] Greenfield bootstrap fires async post-INIT; INIT phase returns before bootstrap completes; bootstrap
      failure never fails INIT.

**PR3**
- [ ] `AppliesWhen.FrameworkMinVersion` parsed; detected major > active skill major → drift trigger emits.
- [ ] Drift import creates a NEW `(name, version)` row; the old version stays `active`.
- [ ] Imported skill has `status=candidate`, `activation_source=imported`, name `stack/<framework>-<major>`.
- [ ] `SkillImporter` produces a skill body via deterministic template assembly — NO LLM call (verified by
      a no-LLM-dependency test); fetched docs appear only as body text, never as instructions.
- [ ] Thin-entry fallback: snippet count < threshold → query main entry; record actual source; below main
      threshold → skip with WARN.
- [ ] Rate guard: bootstrap calls capped per project; missing key → skip with WARN.
- [ ] `structuralMatches` name-only path unchanged when `FrameworkMinVersion` is unset (backward compat).

## PR Delivery Sketch

| PR | Repo | Scope | Est. LoC | Budget |
|----|------|-------|----------|--------|
| PR1 | sophia-agent-mcp | Context7 `[[mcp_providers]]` TOML entry | ~25 | within 400 |
| PR2 | sophia-orchestator | Greenfield flag + `SophiaDetectorVer` bump + manifest-hash cache fix + async wire + tests | ~250 | within 400 |
| PR3 | sophia-orchestator | `AppliesWhen` version semantics + `BootstrapTriggerService` + `SkillImporter` + matcher gate + rate guard + thin-entry fallback + tests | ~330 | within 400 |

No `size:exception` required — the original ~400 LoC single-PR estimate is split into PR2 (greenfield +
cache) and PR3 (drift + importer), each comfortably under the 400-line review budget. PR1 merges first
(zero-risk TOML). PR3 depends on PR2's `Greenfield` field.

## Strict TDD Note

strict_tdd ACTIVE. PR2 and PR3: test-first per strict-tdd.md (no Standard Mode fallback). PR2 MUST write
the cache-invalidation test (D-C7-7) before touching the key builder. PR3 MUST write the drift-detection,
no-LLM-importer, and thin-entry-fallback tests before implementation. golangci-lint pre-push; conventional
commits, no Co-Authored-By / AI attribution.

## Open Questions

None. All operator decisions are locked (D-C7-1 scope, transport, quota, lifecycle, source chain,
greenfield-without-signal). The six explore open issues are resolved by D-C7-2 through D-C7-7.
