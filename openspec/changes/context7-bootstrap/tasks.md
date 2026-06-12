# Tasks: context7-bootstrap (V1)

## Review Workload Forecast

| Field | Value |
|-------|-------|
| PR1 agent-mcp (Context7 TOML provider) | ~120 LoC incl. tests | within budget |
| PR2 orch (greenfield + manifest hash + async wire) | ~390 LoC incl. tests | borderline (proposal said ~250 impl-only; tests push toward 400) |
| PR3a orch (semver + AppliesWhen + matcher gate) | ~300 LoC incl. tests | within budget |
| PR3b orch (DocsProvider port + SkillImporter + PG integration) | ~430 LoC incl. tests | borderline-over |
| PR3c-i orch (rate guard + Context7 adapter) | ~340 LoC incl. tests | within budget |
| PR3c-ii orch (trigger service + wiring + integration) | ~330 LoC incl. tests | within budget |
| 400-line budget risk | Resolved by sub-split (PR2/PR3b remain borderline) |
| Chained PRs recommended | Yes — 6 stacked PRs (DG-C7-8 adds port+adapter LoC the proposal's ~330 did not count) |
| Chain order | PR1 → PR2 → PR3a → PR3b → PR3c-i → PR3c-ii (stacked-to-main; each merges before next starts) |
| Delivery strategy | ask-on-risk |
| Chain strategy | stacked-to-main |

Decision needed before apply: No (both resolved — see below)
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: Resolved by sub-split

**Operator decisions (RESOLVED 2026-06-11):**
1. **PR3c boundary** — RESOLVED: sub-split. PR3c-i = Groups N+O (rate guard + Context7 adapter, ~340). PR3c-ii = Groups P+Q+R (trigger service + wiring + integration, ~330). No `size:exception` in this change.
2. **Imported-skill `version` column** — RESOLVED: full detected version per design DG-C7-7 (e.g. `"22.0.0"`). T4.4 stands as written; skill-importer spec's literal `"v1"` is superseded (see reconciliation table).

### Suggested Work Units

| Unit | Goal | PR | Notes |
|------|------|----|-------|
| 1 | Context7 `[[mcp_providers]]` TOML block + config/registration/degraded tests | PR1 (sophia-agent-mcp) | zero proxy code changes (DG-C7-1) |
| 2 | Manifest-content-hash 8th cache-key component (D-C7-7 acceptance test FIRST) | PR2 | DG-C7-2 |
| 3 | `Greenfield` flag + `SophiaDetectorVer` v1.1.0 + async bootstrap wiring | PR2 | DG-C7-3, DG-C7-5 |
| 4 | `skill.MajorOf`/`DriftsForward` + `AppliesWhen.FrameworkMinVersion` + matcher gate | PR3a | DG-C7-4, DG-C7-9 |
| 5 | `DocsProvider` outbound port + deterministic `SkillImporter` + PG integration | PR3b | DG-C7-8 (types), DG-C7-10, DG-C7-7 |
| 6 | `MemoryRateGuard` + Context7 adapter | PR3c-i | DG-C7-6, DG-C7-8 |
| 7 | `BootstrapTriggerService` + wiring + integration | PR3c-ii | DG-C7-5, DG-C7-6 |

## Locked Decisions Absorbed

- **DG-C7-1**: Context7 = second `[[mcp_providers]]` block; `startup_timeout_s = 20`; exactly 2 tools allowed; env-forwarding already merged (R-1) — zero proxy code changes.
- **DG-C7-2**: manifest hash = distinct 8th cache component; sorted 7-name set pinned to the detector read set (`go.mod`, `package.json`, `pyproject.toml`, `requirements.txt`, `Cargo.toml`, `build.gradle`, `pom.xml`); absent-file sentinel; 16-hex truncation; `CacheKey.Hash()` and `computeCacheKeyHash` must agree.
- **DG-C7-3**: `Greenfield = len(Frameworks)==0 && len(Languages)==0`, set as last step of `Detect`; `omitempty`; NO SchemaVersion bump; `SophiaDetectorVer` → `"v1.1.0"`.
- **DG-C7-4**: `FrameworkMinVersion map[string]string` — additive JSONB field, NO migration.
- **DG-C7-5**: bootstrap fires via injected `Scheduler` AFTER persist+advance; detached ctx (`traceBackground`); 60s default timeout; mandatory `recover()`; `Bootstrap` is an optional nil-safe dep on `phase.Deps`.
- **DG-C7-6**: rate guard is IN-MEMORY per-process (`MemoryRateGuard`, sliding window, default 5/project/24h, injected `Clock`). No singleflight (out of scope).
- **DG-C7-7**: name `stack/<framework>-<major>`; `version` column = full detected version; drift = NEW `(name,version)` row; old row stays active.
- **DG-C7-8 (supersedes proposal D-C7-8)**: NEW outbound port `internal/ports/outbound/docs.go` (`DocsProvider`, `ErrDocsUnavailable`, `ErrThinEntry`, `LibraryEntry`, `DocsResult`) + adapter `internal/adapters/outbound/docs/context7/` reusing the dispatcher's `StreamableClientTransport` + `authRoundTripper`, per-call `Connect→CallTool→Close`, calling `context7.*` on the agent-mcp bridge.
- **DG-C7-9**: `skill.MajorOf` / `skill.DriftsForward` pure domain helpers tolerate `"22.0.0"`, `"go 1.26"`, `"^18"`, `"v3.2"`. Matcher gate fail-open with WARN (spec WHAT). Drift comparison lives in the service, never the matcher.
- **DG-C7-10**: importer = fixed template (header + provenance + sanitized body), header-spoof sanitization (`## Rule:`/`## Routine:`/`## Skill:` escaped), `BodyBudget` 24 KB truncation, `tokens=8000`, `MinSnippets` 50, phases `explore, proposal, apply`. NO LLM, NO MCP call inside the importer.
- Strict TDD ACTIVE: every group is RED → GREEN → VERIFY; no Standard Mode fallback.
- Conventional commits; NEVER Co-Authored-By / AI attribution; golangci-lint (forbidigo/wrapcheck/errorlint) clean per checkpoint.
- No `time.Now()`/`ulid.Make()` in application/domain — injected `Clock`/`IDGenerator` (repo CLAUDE.md D5).

## Spec/Design Reconciliation Notes (design authoritative for HOW — verify phase must read this)

| # | Spec says | Tasks follow (design) |
|---|-----------|------------------------|
| R1 | bootstrap-trigger-service: rate guard "MUST persist counter in a durable store (Postgres)" keyed `(project_id, date_UTC)` | DG-C7-6: in-memory `MemoryRateGuard`, sliding 24h window, per-process (documented V1 limitation). Day-boundary spec scenario is satisfied by window expiry semantics, not calendar reset. |
| R2 | manifest-hash spec: concat content of FOUND manifests (4 names); empty repo → sha256("") constant `e3b0c4...` | DG-C7-2: 7-name detector set, name+sentinel framing (absence changes the key), 16-hex truncation. Tests assert determinism/stability/difference — NOT the `e3b0c4` constant. |
| R3 | skill-importer spec: 3-section template (Setup/Best Practices/Control Flow) via heading extraction | DG-C7-10: single sanitized body under `## Best practices` + `## Provenance`; unstructured-docs behavior (full content in body) preserved. |
| R4 | skill-importer spec: `ImportFromDocs(ctx, framework, detectedVersion, docs, sourceLibraryID)` | Design signature: `ImportFromDocs(ctx, name, version, fw, r outbound.DocsResult)`. Resolve/threshold/fallback selection lives in the SERVICE (spec bootstrap-trigger-service tool sequence steps 1–4); importer makes zero MCP calls (spec honored). |
| R5 | skill-importer spec: `version` column = `"v1"` | DG-C7-7: full detected version (e.g. `"22.0.0"`). **Operator decision item #2 above.** |
| R6 | greenfield spec: "Bootstrap fires if and only if `sc.Greenfield == true`; when false no goroutine is spawned" | DG-C7-5 + drift requirement: scheduler fires whenever `Bootstrap != nil`; greenfield/drift gating happens INSIDE `TriggerIfNeeded` (drift needs non-greenfield INITs). In PR2 prod wiring `Bootstrap` is nil → no-op until PR3c. |
| R7 | skill-importer spec scenario: `stack/go-1` from `"1.26"` | Followed (name major = `MajorOf` int). Design's `stack/go-1.26` example is illustrative only and contradicts its own `MajorOf` signature. |

---

## PR1 Task Group (sophia-agent-mcp)

**Branch:** `feat/context7-provider` (off `main`) · **Commit prefix:** `feat(config)` / `test(config)` · **Merges to main BEFORE PR2 starts** (stacked-to-main; PR1 is independent but ordered first — zero-risk TOML).

### Group A — Context7 provider registration
Spec: context7-provider-registration (DG-C7-1)

- [x] T1.1 **RED**: In `infrastructure/config/config_test.go`, add test parsing `configs/example.toml`: exactly one entry with `id == "context7"`; `command == "npx -y @upstash/context7-mcp@latest"`; `transport == "stdio"`; `lifecycle == "persistent"`; `startup_timeout_s == 20`; `tools_allowed == ["resolve-library-id","get-library-docs"]` (exactly 2 — no `list_libraries`); env map declares `CONTEXT7_API_KEY`; graphify entry retains its original 8 tools and command unchanged. Run; confirm FAIL.
- [x] T1.2 **RED**: In `adapters/inbound/mcp/server_test.go`, add tests building the SDK server with BOTH provider configs: `context7.resolve-library-id` and `context7.get-library-docs` registered; no other `context7.*` tool present; `agent.run`/`agent.health` + all 8 `graphify.*` tools unaffected; `AllowlistEnforcer.Authorize("context7","list_libraries")` → `ErrToolNotAllowed` with no subprocess interaction. Run; confirm FAIL (config fixtures lack context7).
- [x] T1.3 **GREEN**: Add the `[[mcp_providers]]` block to `configs/example.toml` exactly per DG-C7-1 (id, command, transport, lifecycle persistent, `startup_timeout_s = 20`, two tools, `[mcp_providers.env] CONTEXT7_API_KEY = "${CONTEXT7_API_KEY}"`). Zero proxy code changes — registration is the existing `wire.go:273-313` / `server.go:308-334` loop. T1.1/T1.2 green.
- [x] T1.4 **RED→GREEN**: Degraded-first test (fake stdio caller via `WithCallerFactory`): missing `CONTEXT7_API_KEY` / failed spawn → context7 tool call returns provider-level error (not panic); graphify call in the same process still served; health check unaffected. Optional skip-guarded real-`npx` test behind env flag (NOT in CI default).
- [x] T1.5 **VERIFY**: `make test` + `make lint` clean in sophia-agent-mcp.
- [x] T1.6 **COMMIT+PR**: `feat(config): register context7 as second mcp provider with two-tool allowlist`. Open PR1 → main. Merge before PR2 work starts. (committed locally; push+PR pending operator checkpoint)

---

## PR2 Task Groups (sophia-orchestator)

**Branch:** `feat/greenfield-cache-key` (off `main` after PR1 merges) · **Commit prefixes:** `feat(init)`, `feat(phase)`, `test(init)` · **Merges to main BEFORE PR3a starts.**

> Golden/snapshot caution: the 8th component + `SophiaDetectorVer` bump touch existing expectations.
> Exact files to update: `internal/application/init/cache/key_test.go` (CacheKey struct literals — add `ManifestHash` coverage), `internal/application/init/detector/types_test.go:45` (asserts `SophiaDetectorVer: "v1.0.0"` → `"v1.1.0"`). No `key_builder_test.go` exists today — Group B creates it. `service_test.go`/`ports_test.go` use fake `CacheKeyBuilder`s → unaffected.

### Group B — Manifest-content-hash 8th cache-key component
Spec: manifest-hash-cache-invalidation (DG-C7-2, D-C7-7). FIRST group of PR2 — the acceptance test leads.

- [x] T2.1 **RED**: Create `internal/application/init/key_builder_test.go`. The D-C7-7 acceptance test: fake `GitRunner` returning IDENTICAL `git status --porcelain` output across both builds; fake `FileReader` returning `package.json` bytes with `^22.0.0` then `^23.0.0` → `Build` produces DIFFERENT keys. Plus: unrelated non-manifest edit → manifest component unchanged; no manifests → key stable/deterministic across two builds; multi-manifest (`package.json`+`go.mod`) deterministic order; manifest deleted between builds → key differs (absent sentinel). Run; confirm FAIL.
- [x] T2.2 **RED**: In `internal/application/init/cache/key_test.go`, add: `Hash()` differs when only `ManifestHash` differs; deterministic with the 8th field set; serialized key string includes the manifest component (loggable/debuggable).
- [x] T2.3 **GREEN**: Add `ManifestHash string` to `cache.CacheKey` (`cache/key.go`) and fold into `Hash()` (null-byte separated, after component 7). In `internal/application/init/key_builder.go`: compute per DG-C7-2 — SORTED fixed name set `["Cargo.toml","build.gradle","go.mod","package.json","pom.xml","pyproject.toml","requirements.txt"]` (== detector read set, `detector.go:34-119`), per name write `name\x00` + (`bytes` | `"\x01<absent>"`) + `\x00`, sha256, first 16 hex. Reuse the existing `FileReader.ReadIfExists` — no new dependency. Keep `computeCacheKeyHash` and `CacheKey.Hash()` in agreement.
- [x] T2.4 **VERIFY**: `make test-unit` + `make lint` clean.

### Group C — Greenfield flag + SophiaDetectorVer bump
Spec: greenfield-detection (DG-C7-3). Sequential after B (both touch cache-key expectations; keep one rebuild story).

- [x] T2.5 **RED**: In `internal/application/init/detector/detector_test.go`, table-driven: no frameworks AND no languages → `Greenfield == true`; framework detected (`@angular/core` fixture) → `false`; language-only (`go.mod`, no framework) → `false`. In a marshal test: `Greenfield == false` → `"greenfield"` key ABSENT (omitempty); `true` → present. Backward-compat: JSON without the key deserializes to `false`, no error. Constant test: `detector.SophiaDetectorVer == "v1.1.0"`. Update `internal/application/init/detector/types_test.go:45` (`"v1.0.0"` → `"v1.1.0"`). Run; confirm FAIL.
- [x] T2.6 **GREEN**: Add `Greenfield bool \`json:"greenfield,omitempty"\`` to `structural.StructuralContext` (`internal/domain/structural/context.go`, after existing fields). Set as the LAST step of `Detector.Detect`: `sc.Greenfield = len(sc.Frameworks)==0 && len(sc.Languages)==0`. Bump `SophiaDetectorVer` to `"v1.1.0"` (`detector/types.go:17`). NO SchemaVersion bump; detector MUST NOT call the matcher.
- [x] T2.7 **VERIFY**: `make test-unit` + `make lint` clean.

### Group D — Async bootstrap wiring in runInitPhase
Spec: greenfield-detection "Async Bootstrap Fire Post-INIT" (DG-C7-5; see reconciliation note R6 — gating lives in the service, PR3c).

- [x] T2.8 **RED**: In `internal/application/phase/` tests (new `service_bootstrap_test.go` or extend `service_test.go`), with `SyncScheduler` + fake `Bootstrap`: (a) `TriggerIfNeeded` receives the captured `sc` and runs AFTER the phase is persisted+terminal (assert persist-before-fire ordering via fake persister call log); (b) nil `Bootstrap` dep → no-op, all existing behavior unchanged; (c) fake Bootstrap that PANICS → recovered, logged, phase still terminal, no crash; (d) fake Bootstrap returning error → swallowed, INIT result SUCCESS; (e) context passed to Bootstrap is detached (not the request ctx) and carries a deadline (`BootstrapTimeout`, default 60s). Run; confirm FAIL.
- [x] T2.9 **GREEN**: In `internal/application/phase/service.go`: add to `Deps` an optional `Bootstrap` (local interface `{ TriggerIfNeeded(context.Context, structural.StructuralContext) }`) + `BootstrapTimeout time.Duration` (default 60s when zero, configurable via `bootstrap.timeout`). In `runInitPhase`: capture `sc` (today discarded as `_`); AFTER complete+persist+advance, if `Bootstrap != nil` schedule via `s.d.Scheduler` with `traceBackground(ctx)` + `context.WithTimeout` + `defer recover()` per the DG-C7-5 snippet. Do NOT add `Bootstrap` to the `Deps` nil-validation gate (it is optional).
- [x] T2.10 **VERIFY**: `make test-unit` + `make lint` clean.

### Group E — PR2 Checkpoint
- [x] T2.11 **CHECKPOINT**: `make test-unit` all green (race); `make lint` 0 issues; `make test-integration` green (no new integration tests, but cache-key change must not break existing ones).
- [x] T2.12 **COMMIT+PR**: work-unit commits — `feat(init): add manifest content hash as 8th cache key component`, `feat(init): detect greenfield and bump detector version to v1.1.0`, `feat(phase): fire optional bootstrap async after init persists`. (committed locally; push+PR pending operator checkpoint)

---

## PR3a Task Groups (sophia-orchestator)

**Branch:** `feat/skill-version-semantics` (off `main` after PR2 merges) · **Commit prefixes:** `feat(skill)`, `feat(pg)` · **Merges to main BEFORE PR3b starts.** (No PR2 dependency in code, but stacked order keeps one-merge-at-a-time review flow.)

### Group F — Domain semver helper
Spec: applies-when-version-semantics "Semver Comparison — Major Only" (DG-C7-9)

- [x] T3.1 **RED**: Create `internal/domain/skill/semver_test.go`, table-driven: `MajorOf` — `"22.0.0"`→(22,true); `"go 1.26"`→(1,true); `"^18"`→(18,true); `"v3.2"`→(3,true); `">=22.0.0"`→(22,true); `""`→(0,false); `"edge"`→(0,false). `DriftsForward("23.0.0","22")`→true; `("22.3.1","22.0.0")`→false; `("edge","22")`→false (unparseable never drifts). Run; confirm FAIL.
- [x] T3.2 **GREEN**: Create `internal/domain/skill/semver.go` — pure functions, no imports beyond stdlib: strip leading non-digit token (`"go "`), trim `^~>=< v` prefixes, read leading integer run.
- [x] T3.3 **VERIFY**: `make test-unit` + `make lint`.

### Group G — AppliesWhen.FrameworkMinVersion field
Spec: applies-when-version-semantics "AppliesWhen FrameworkMinVersion Field" (DG-C7-4)

- [x] T3.4 **RED**: In `internal/domain/skill/lifecycle_test.go`: JSON without `framework_min_version` → nil map, no error (backward compat); JSON with `{"framework":["angular"],"framework_min_version":{"angular":"22.0.0"}}` → correct map + `Framework` intact; marshal with nil/empty map → key ABSENT (omitempty). Run; confirm FAIL.
- [x] T3.5 **GREEN**: Add `FrameworkMinVersion map[string]string \`json:"framework_min_version,omitempty"\`` to `AppliesWhen` (`internal/domain/skill/lifecycle.go:117-129`). NO migration (rides `applies_when` JSONB, `skill_repo.go:140`). Do not rename/remove `Framework`/`Language`.
- [x] T3.6 **VERIFY**: `make test-unit` + `make lint`.

### Group H — Matcher version gate
Spec: skill-matcher-structural + applies-when-version-semantics "Matcher Version Gate" (DG-C7-9)

- [x] T3.7 **RED**: In `internal/adapters/outbound/pg/skill_matcher_structural_test.go` (or new `skill_matcher_versiongate_test.go`), table-driven: (a) nil map → name-only result IDENTICAL to pre-change (angular skill matches `Version:"22.0.0"` AND `Version:""`); (b) empty map `{}` → gate inactive, skill returned even when detected major < anything; (c) gate pass: detected `22.0.0` vs min `22.0.0`; detected `23.1.0` vs min `22.0.0`; (d) gate fail: detected `18.2.0` vs min `22.0.0` → NOT returned (SkipReasonStructuralMismatch); (e) name mismatch → gate never evaluated; (f) per-framework selectivity: map has only `"react"` → angular matches name-only; (g) fail-open: detected `"edge"` → returned + WARN logged; unparseable min value → returned + WARN; (h) no DB/I-O during comparison (fake-backed); (i) legacy seeds (no map) all still returned. Run; confirm FAIL.
- [x] T3.8 **GREEN**: In `internal/adapters/outbound/pg/skill_matcher.go` `structuralMatches` (:237-259): after the existing name match, if `aw.FrameworkMinVersion` non-empty AND has an entry for the matched lowercased framework name → require `skill.MajorOf(detected) >= skill.MajorOf(min)`; parse failure on either side → fail open + `slog` WARN. Empty/nil map path byte-for-byte unchanged. Signature unchanged; never returns an error; in-memory only.
- [x] T3.9 **VERIFY**: `make test-unit` + `make lint`.

### Group I — PR3a Checkpoint
- [x] T3.10 **CHECKPOINT**: `make test-unit` green; `make lint` 0 issues.
- [x] T3.11 **COMMIT+PR**: `feat(skill): add semver major helpers and FrameworkMinVersion to AppliesWhen` + `feat(pg): optional version gate in structuralMatches`. (committed locally; push+PR pending operator checkpoint)

---

## PR3b Task Groups (sophia-orchestator)

**Branch:** `feat/skill-importer` (off `main` after PR3a merges) · **Commit prefixes:** `feat(bootstrap)`, `test(bootstrap)`, `test(pg)` · **Merges to main BEFORE PR3c starts.** Depends on PR3a (`skill.MajorOf`, `FrameworkMinVersion`).

### Group J — DocsProvider outbound port (types only)
Spec: bootstrap-trigger-service transport banner (DG-C7-8 port half)

- [x] T4.1 **GREEN (declarations — no behavior, no RED needed)**: Create `internal/ports/outbound/docs.go` exactly per the design signature block: `ErrDocsUnavailable`, `ErrThinEntry`, `LibraryEntry{ID, Snippets, Score, IsMain}`, `DocsResult{LibraryID, Snippets, Score, Body}`, `DocsProvider{ResolveLibrary, GetDocs}` with doc comments (Body is DATA, never instructions; implementations concurrency-safe). `go build ./...` + `make lint` clean.

### Group K — Deterministic SkillImporter
Spec: skill-importer-deterministic (DG-C7-10, DG-C7-7; reconciliation notes R3/R4/R5/R7)

- [x] T4.2 **RED**: Create `internal/application/bootstrap/importer_test.go` + `testdata/` goldens: (a) GOLDEN — fixed `DocsResult` + fake clock → byte-identical body across runs (template: title, `> Source: Context7 <id> (snippets=, score=), fetched <ISO8601>` + REFERENCE-DATA banner, `## Best practices` sanitized body, `## Provenance` with framework/version/activation_source/status/fetched_at); (b) GOLDEN sanitization — input containing `## Rule:`, `## Routine:`, `## Skill:` headers and role-opening fence markers → escaped (`\#\# Rule:`), never stored raw; (c) truncation — body > `BodyBudget` (24 KB default) → hard cap + trailing `\n…(truncated)`; (d) name: (`"Angular"`, `"22.0.0"`) → `stack/angular-22` (lowercased); (`"go"`, `"1.26"`) → `stack/go-1`; (e) lifecycle: `StatusCandidate`, `SourceImported`, risk `medium`, phases exactly `explore, proposal, apply`, `AppliesWhen{Framework:[fw], FrameworkMinVersion:{fw: "<major>"}}`; (f) version column = full detected version `"22.0.0"` (DESIGN DG-C7-7 — flagged decision item #2); (g) "already exists" from repo → DEBUG log + nil error; (h) NO-LLM guarantee — constructor deps are exactly `{repo, clock, idgen, budget}` (compile-time: no LLM/dispatcher port) + fake repo spy proving only `InsertIfAbsent` is invoked; (i) fallback provenance — `DocsResult.LibraryID` ≠ version-specific entry → body notes main-entry fallback. Run; confirm FAIL.
- [x] T4.3 **GREEN**: Create `internal/application/bootstrap/importer.go`: `SkillImporter{repo SkillRepoInserter; clock shared.Clock; idgen shared.IDGenerator; budget int}` + `ImportFromDocs(ctx, name, version, fw string, r outbound.DocsResult) (*skill.Skill, error)` per DG-C7-10 — pure sanitize → template → truncate → `skill.New(... LifecycleInput{StatusCandidate, SourceImported, Version: version, AppliesWhen{...}}, clock.Now())` → `repo.InsertIfAbsent`. No MCP calls, no LLM, no `time.Now()`.
- [x] T4.4 **FLAGGED**: confirm operator decision item #2 (version column value). If operator chooses spec's `"v1"`, change one constant + golden; tasks default = design. CONFIRMED: design value used (full detected version per DG-C7-7).
- [x] T4.5 **VERIFY**: `make test-unit` + `make lint`.

### Group L — PG integration: idempotent candidate rows
Spec: skill-importer-deterministic "Idempotent InsertIfAbsent" (DG-C7-7; testcontainers)

- [x] T4.6 **RED→GREEN**: In `internal/adapters/outbound/pg/skill_repo_integration_test.go`, add testcontainers tests (no repo code change expected — `InsertIfAbsent` reused as-is): (a) insert `stack/angular-22` candidate/imported → row present with correct lifecycle fields, NOT returned by active-only matcher queries; (b) second identical insert → no-op, row unchanged; (c) concurrent double-insert same `(name,version)` → exactly one row, no error surfaced; (d) drift row `stack/angular-23` coexists; `stack/angular-22` untouched and still `active` when pre-seeded active.
- [x] T4.7 **VERIFY**: `make test-integration` green.

### Group M — PR3b Checkpoint
- [x] T4.8 **CHECKPOINT**: `make test-unit` + `make test-integration` green; `make lint` 0 issues.
- [x] T4.9 **COMMIT+PR**: `feat(bootstrap): add DocsProvider port and deterministic skill importer`. Open PR3b → main. Merge before PR3c starts. (committed locally; push+PR pending operator checkpoint)

---

## PR3c-i Task Groups (sophia-orchestator)

**Branch:** `feat/bootstrap-rateguard-docs-adapter` (off `main` after PR3b merges) · **Commit prefixes:** `feat(bootstrap)`, `test(bootstrap)` · Groups N+O (~340 LoC). **Merges to main BEFORE PR3c-ii starts.** Depends on PR3b (`DocsProvider` port types).

### Group N — MemoryRateGuard
Spec: bootstrap-trigger-service "Rate Guard" (DG-C7-6; reconciliation note R1 — in-memory supersedes durable store)

- [x] T5.1 **RED**: Create `internal/application/bootstrap/rateguard_test.go` with fake `shared.Clock`: limit 5 default — calls 1..5 allowed, 6th denied; counter is per-project (project B unaffected by A's exhaustion); advancing the clock past the 24h window re-allows; concurrent `Allow` calls race-safe (`-race`). Run; confirm FAIL.
- [x] T5.2 **GREEN**: Create `internal/application/bootstrap/rateguard.go`: `RateGuard` interface + `MemoryRateGuard{max int, window time.Duration, clock shared.Clock, mu sync.Mutex, ...}` sliding-window counter keyed by projectID; defaults max=5, window=24h; configurable via `bootstrap.max_calls_per_project_per_day`.
- [x] T5.3 **VERIFY**: `make test-unit` (with `-race` via the target) + `make lint`.

### Group O — Context7 DocsProvider adapter
Spec: bootstrap-trigger-service "Context7 MCP Tool Calls" transport half (DG-C7-8 adapter)

- [x] T5.4 **RED**: Create `internal/adapters/outbound/docs/context7/client_test.go` using a fake MCP server over `mcp.NewInMemoryTransports` (NO real Context7/npx in CI): (a) `ResolveLibrary` calls tool `context7.resolve-library-id` and maps entries → `[]LibraryEntry` (ID, snippet count, score, IsMain); (b) `GetDocs` calls `context7.get-library-docs` with id/query/topic/tokens and returns `DocsResult` with raw markdown body; (c) unconfigured (missing bridge URL/token or disabled) → `ErrDocsUnavailable` WITHOUT any transport dial; (d) transport/timeout error → wrapped error (wrapcheck-clean), session closed; (e) per-call session: Connect→CallTool→Close, no session leak across calls. Run; confirm FAIL.
- [x] T5.5 **GREEN**: Create `internal/adapters/outbound/docs/context7/client.go` implementing `outbound.DocsProvider`: reuse the dispatcher's `StreamableClientTransport` + `authRoundTripper` construction and per-call session pattern (`dispatcher.go:145-162, 305-344`) against the SAME agent-mcp bridge URL/token/origin config; concurrency-safe.
- [x] T5.6 **VERIFY**: `make test-unit` + `make lint`. Optional skip-guarded (`testing.Short`/env) round-trip kept out of CI default.

### Group N+O — PR3c-i Checkpoint
- [x] T5.6a **CHECKPOINT**: `make test-unit` (race) green; `make lint` 0 issues.
- [x] T5.6b **COMMIT+PR**: work-unit commits — `feat(bootstrap): in-memory per-project rate guard`, `feat(bootstrap): context7 docs provider adapter over agent-mcp bridge`. (committed locally; push+PR pending operator checkpoint)

## PR3c-ii Task Groups (sophia-orchestator)

**Branch:** `feat/bootstrap-trigger` (off `main` after PR3c-i merges) · **Commit prefixes:** `feat(bootstrap)`, `feat(pg)`, `test(bootstrap)` · Groups P+Q+R (~330 LoC). **Final PR of the chain.** Depends on PR2 (`Greenfield`, phase wiring hook), PR3a/PR3b, and PR3c-i (`RateGuard`, Context7 adapter).

### Group P — BootstrapTriggerService
Spec: bootstrap-trigger-service (DG-C7-6, DG-C7-9; D11 + ContextCrush requirements)

- [x] T5.7 **RED**: Create `internal/application/bootstrap/service_test.go` with fake `DocsProvider`/`SkillLookup`/`RateGuard`/importer-spy: (a) `DocsProvider` returns `ErrDocsUnavailable` → WARN, return, zero further calls, zero inserts; (b) rate guard denies → WARN, NO docs call; (c) greenfield + 1 framework → exactly 1 import with framework+version; (d) greenfield + zero frameworks → WARN `greenfield but no framework detected`, no docs call; (e) non-greenfield + versions satisfy active mins → no import; (f) drift: active `stack/angular-22` min `{angular:"22"}` + detected `23.0.0` → import angular v23, existing row never written; (g) no active skill with min set → drift never fires (no baseline); (h) unparseable detected or stored min → no drift + WARN; (i) thin version entry (7 < MinSnippets 50) + fat main entry → main entry selected, actual ID recorded in `DocsResult` passed to importer; (j) ALL entries thin → WARN skip, `get-library-docs` never called; (k) docs/import error → WARN, swallowed, continue to next framework, method returns nil; (l) D11: constructor has NO LLM/dispatcher dep (compile-time) and fakes record zero LLM interaction; (m) quota/429 surfaced by provider → WARN skip. Run; confirm FAIL.
- [x] T5.8 **GREEN**: Create `internal/application/bootstrap/ports.go` (`SkillLookup{ActiveByName}`, importer-facing `SkillRepoInserter` if not already in importer.go) and `internal/application/bootstrap/service.go`: `Service{d Deps}` with `Deps{Docs, Skills, Importer, Rate, Clock, IDGen, Logger, MinSnippets}`; `TriggerIfNeeded(ctx, sc)` = key guard → rate guard → greenfield branch → drift branch (`skill.DriftsForward`) → per-framework fetch (resolve → threshold/fallback select → GetDocs `query="best practices"`, `tokens=8000`) → `Importer.ImportFromDocs`; every failure logged WARN and discarded; never panics out; never calls an LLM; only `InsertIfAbsent` mutates.
- [x] T5.9 **RED→GREEN**: `SkillLookup` PG side — if `skill_repo.go` lacks an active-by-name query, add minimal `ActiveByName(ctx, name)` to the repo (`feat(pg)`) with a testcontainers test in `skill_repo_integration_test.go`; if an equivalent query exists, adapt and skip the new method.
- [x] T5.10 **VERIFY**: `make test-unit` + `make lint` + `make test-integration`. (unit+lint PASS; integration BLOCKED — Docker daemon down, retried once per policy; code compiles clean)

### Group Q — Wiring
Spec: greenfield-detection async-fire + bootstrap-trigger-service (DG-C7-5/6/8)

- [x] T5.11 **GREEN**: In `internal/bootstrap/wire.go`: construct `MemoryRateGuard`, context7 `DocsProvider` adapter (bridge config reuse), `SkillImporter`, `bootstrap.Service`; inject as `phase.Deps.Bootstrap` + `BootstrapTimeout`; add config keys `bootstrap.timeout` (60s), `bootstrap.max_calls_per_project_per_day` (5), `bootstrap.min_snippets` (50), `bootstrap.body_budget` (24576). `go build ./...`; existing wire/config tests extended for the new keys' defaults.
- [x] T5.12 **VERIFY**: `make test-unit` + `make lint` green repo-wide.

### Group R — PR3c-ii Checkpoint
- [x] T5.13 **CHECKPOINT**: `make test-unit` (race) green; `make lint` 0 issues; `make test-integration` BLOCKED (Docker daemon unresponsive — retried once per policy).
- [x] T5.14 **COMMIT+PR**: work-unit commits — `feat(bootstrap): trigger service for greenfield and version drift` (fd75d7b), `feat(bootstrap): wire bootstrap service into init phase` (2c79390). Committed locally on feat/bootstrap-trigger. Push+PR pending operator checkpoint.

---

## Strict TDD + Checkpoints Discipline

- Every group is RED → GREEN → VERIFY. No GREEN before its RED tests exist and FAIL (exceptions: T4.1 pure type declarations; T5.11 wiring smoke-tested via build + config-default tests).
- T2.1 (D-C7-7 cache-invalidation acceptance test) MUST be the first test written in PR2 — before touching `key_builder.go` (proposal Strict TDD Note).
- T5.7 drift/no-LLM/thin-entry tests MUST exist and fail before `service.go` is written (proposal Strict TDD Note).
- Goldens (T4.2) are committed fixtures; regenerate only deliberately with a documented diff.
- `make lint` (forbidigo/wrapcheck/errorlint) clean at every checkpoint before commit.
- No `Co-Authored-By`, no AI attribution, conventional commits only.
- `types_test.go:45` version-constant update (T2.5) MUST land in the same commit as the `types.go` bump (T2.6).

## Out of Scope — apply MUST NOT touch

- R-2 InputSchema enrichment / R-3 per-provider mutex (agent-mcp) — V2 DX/perf.
- Vendor official MCP chain (angular.dev/ai/mcp, llms.txt direct fetch) — source chain V1 = Context7 only.
- `deprecated_api_hits` backstop trigger (no producer exists; M4+).
- memory-engine: ZERO changes in any PR.
- LLM-assisted importer draft (V2; V1 is deterministic-only).
- PROPOSE-phase question-round server-side wiring (prompt contract only).
- Per-project singleflight (correctness covered by `ON CONFLICT DO NOTHING`).
- DB-backed/cross-process rate guard (V1 = in-memory; documented limitation).
- `SkillRepo.InsertIfAbsent` internals (`skill_repo.go:135-181`) — reused, never modified.
- `SchemaVersion`/`SchemaV1` — NO bump; no DB migrations anywhere in this change.
- `AgentDispatcher` contract — no generic `CallTool` added to it (DG-C7-8 option A rejected).
- `GreenfieldReason` or any companion field (spec MUST NOT, V1).
