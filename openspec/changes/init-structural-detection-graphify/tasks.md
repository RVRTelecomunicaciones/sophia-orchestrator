# Tasks: init-structural-detection-graphify (M-KNOW-INIT-0)

## Review Workload Forecast

| Field | Value |
|---|---|
| Estimated changed lines | PR1 ~150 LoC (cli) + PR2 ~350 LoC (orch) |
| 400-line budget risk | Low per PR (PR1 ~150, PR2 ~350) |
| Chained PRs recommended | Yes (2 PRs by repo — independent, not by size) |
| Suggested split | PR1: sophia-cli graphify bootstrap; PR2: sophia-orchestator INIT detector + spawn + cache + persist + phase branch |
| Delivery strategy | ask-on-risk |
| Decision needed before apply | No |
| Chain strategy | stacked-to-main (cached from session) |
| Notes | PRs are fully independent; PR1 can land first or in parallel with PR2. No wire alignment gate. No same-commit-pair. Operator approval required per PR. |

Decision needed before apply: No
Chained PRs recommended: Yes
Chain strategy: stacked-to-main
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Notes |
|------|------|-----------|-------|
| A | GraphifyProber port + adapter + Initializer integration + --auto-bootstrap-graphify flag | PR1 (sophia-cli) | Independent; targets main directly |
| B–G | INIT detector + types + cache + spawner + persister + InitService + phase branch + wire | PR2 (sophia-orchestator) | Independent; targets main directly |

---

## Cross-repo PR strategy

- No wire alignment gate between PR1 and PR2.
- No same-commit-pair.
- Operator approval per PR before push.
- PR1 and PR2 can be reviewed and merged in any order.

---

## Locked design decisions absorbed

1. **SophiaDetectorVer in cache key (7th component)** — cache key Hash() uses 7 components: graphify_version + repo_root + git_head + dirty_tree_hash + sorted(include_globs) + config_hash + SophiaDetectorVer constant. Eliminates stale-cache-after-parser-change risk.
2. **MemoryClient timeout** — configure in `internal/bootstrap/wire.go` explicitly consistent with p95 < 30s budget. Explicit task (F.13).
3. **include_globs default = ["**/*"]** when `.sophia.yaml` absent; documented in code comment and tested.
4. **phase.Deps extension** — new `Init InitService` field added to `appphase.Deps`. Call sites requiring update: `internal/bootstrap/wire.go:274` (production), `internal/application/phase/service_test.go:403,446,646,1268,1351,1412`, `internal/application/phase/archive_event_test.go:102,294`. Tests that don't exercise INIT use `Init: nil` (nil-tolerant — service only calls Init when PhaseInit branch fires).
5. **Angular signals heuristic v1 locked** — "app.config.ts present AND no @ngrx/store in package.json". Documented in code comment. Future refinement out of scope.

---

## Task groups (in dependency order; within each group strict-TDD applies)

Groups A and B–G are independent (different repos). Within sophia-orchestator, groups B → C → D → E → F must be sequential (each group depends on the previous group's types/interfaces). G is cross-cutting and runs last.

---

### Group A — sophia-cli graphify bootstrap (PR1)

Spec: `cli-graphify-bootstrap`. Path: `sophia-cli/internal/`.

- [x] A.1 (RED) Write failing unit test: `GraphifyProber.Probe` returns `Available=true` with version string when `graphify --version` exits 0. File: `sophia-cli/internal/adapters/outbound/graphify/prober_test.go`.
- [x] A.2 (RED) Write failing unit test: `GraphifyProber.Probe` returns `Available=false`, `MissingDeps=["python3"]` when `python3 --version` exits non-zero.
- [x] A.3 (RED) Write failing unit test: `GraphifyProber.Probe` returns `Available=false`, `MissingDeps=["graphify"]` when python3 OK but `graphify --version` exits non-zero.
- [x] A.4 (RED) Write failing unit test: `GraphifyProber.Bootstrap` returns nil when `uv tool install "graphifyy[mcp]==0.8.35"` exits 0.
- [x] A.5 (RED) Write failing unit test: `GraphifyProber.Bootstrap` returns wrapped error when uv exits non-zero; error message includes stderr.
- [x] A.6 (RED) Write failing unit test: `Initializer.Run` with prober returning `Available=true` logs INFO and does not call Bootstrap.
- [x] A.7 (RED) Write failing unit test: `Initializer.Run` with `Available=false` AND `--auto-bootstrap-graphify=true` calls Bootstrap once; on success continues without error.
- [x] A.8 (RED) Write failing unit test: `Initializer.Run` with `Available=false` AND `--auto-bootstrap-graphify=false` logs WARN with `missing_deps` and exits 0 (no Bootstrap called).
- [x] A.9 (GREEN) Implement `GraphifyProber` outbound port interface in `sophia-cli/internal/ports/outbound/graphify_prober.go`. Types: `ProberResult{Available, Version, PythonOK, UVOK, MissingDeps, DetectedAt}`. Methods: `Probe(ctx) (ProberResult, error)`, `Bootstrap(ctx) error`.
- [x] A.10 (GREEN) Implement concrete `ExecGraphifyProber` adapter in `sophia-cli/internal/adapters/outbound/graphify/prober.go` using injected `ExecRunner`. Runs `python3 --version` then `graphify --version`; on Bootstrap runs `uv tool install "graphifyy[mcp]==0.8.35"`.
- [x] A.11 (GREEN) Wire prober into `InitializerDeps` in `sophia-cli/internal/application/initializer.go`. `AutoBootstrap` moved to `InitInput` (not `InitializerDeps`) to match the per-call flag pattern. Post-`.sophia.yaml` write: call `Probe`, log result, conditionally call `Bootstrap`. NEVER block on probe result (degraded-first).
- [x] A.12 (GREEN) Add `--auto-bootstrap-graphify` boolean flag (default: false) to CLI init command in `sophia-cli/internal/adapters/inbound/cli/init.go`. Passed through `InitInput.AutoBootstrap`.
- [x] A.13 (VERIFY) Run `go test ./...` in sophia-cli; all tests green.
- [ ] A.14 (CHECKPOINT) Operator approval before commit + push for PR1.

---

### Group B — StructuralContext types + port interfaces (PR2 foundation)

Spec: `sophia-structural-detector`. Path: `internal/application/init/`.

- [ ] B.1 (RED) Write failing unit test: `StructuralContext` JSON marshal/unmarshal round-trip preserves all fields including `SchemaVersion=1`. File: `internal/application/init/detector/types_test.go`.
- [ ] B.2 (RED) Write failing unit test: `StructuralContextSchemaV1` constant equals 1; verifies constant exists and value is stable.
- [ ] B.3 (RED) Write failing unit test: package `internal/application/init/` compiles with `SophiaDetector`, `GraphifySpawner`, `StructuralPersister`, `CacheStore`, `CacheKeyBuilder` interfaces defined (compilation test via blank import or direct usage in fake).
- [ ] B.4 (GREEN) Create `internal/application/init/detector/types.go`: `StructuralContext`, `LanguageInfo`, `FrameworkInfo`, `GraphSummary` structs with JSON tags. Const `StructuralContextSchemaV1 = 1`. Const `SophiaDetectorVer = "v1.0.0"` (7th cache key component — bump when detector logic changes).
- [ ] B.5 (GREEN) Create `internal/application/init/ports.go`: declare `SophiaDetector`, `GraphifySpawner`, `StructuralPersister`, `CacheStore`, `CacheKeyBuilder`, `ExecRunner`, `GitRunner`, `FileReader` interfaces. Sentinel errors: `ErrGraphifyDegraded`, `ErrCacheMiss`, `ErrSchemaVersionMismatch`.

---

### Group C — Structural detector parsers + heuristics (PR2 part 2)

Spec: `sophia-structural-detector`. Path: `internal/application/init/detector/`.

- [ ] C.1 (RED) Write failing unit test: `Detect` on fixture Go project (`go.mod` only) returns `Languages` including `{Name:"Go"}`, `Frameworks` empty. File: `internal/application/init/detector/detector_test.go`.
- [ ] C.2 (RED) Write failing unit test: `Detect` on Angular 17 fixture (`package.json` with `@angular/core@17` + `app.config.ts` present + no `@ngrx/store`) returns `Frameworks` including `{Name:"Angular", Version:"17"}` and signals heuristic noted in `ConventionHints`.
- [ ] C.3 (RED) Write failing unit test: `Detect` on Angular 14 NgRx fixture (`package.json` with `@angular/core@14` + `@ngrx/store`) returns `Frameworks` including `{Name:"Angular", Version:"14"}` with NgRx noted; no signals heuristic.
- [ ] C.4 (RED) Write failing unit test: `Detect` on Spring Boot fixture (`build.gradle` with `spring-boot-starter`) returns `Frameworks` including `{Name:"Spring Boot"}`.
- [ ] C.5 (RED) Write failing unit test: `Detect` on Python FastAPI fixture (`pyproject.toml` with `fastapi`) returns `Frameworks` including `{Name:"FastAPI"}`.
- [ ] C.6 (RED) Write failing unit test: `Detect` on hexagonal layout fixture (dirs `domain/`, `application/`, `infrastructure/`) returns `ArchStyle` including `"hexagonal"`.
- [ ] C.7 (RED) Write failing unit test: `Detect` on monorepo fixture (`pnpm-workspace.yaml` present) returns `ArchStyle` including `"monorepo"`.
- [ ] C.8 (RED) Write failing unit test: `Detect` with no manifests in empty tmpdir returns empty `StructuralContext` with `SchemaVersion=1`, no error, no subprocess spawned.
- [ ] C.9 (GREEN) Implement `go.mod` parser in `internal/application/init/detector/parser_go.go`. Extracts module name and Go version; infers `{Name:"Go"}` LanguageInfo.
- [ ] C.10 (GREEN) Implement `package.json` parser in `internal/application/init/detector/parser_node.go`. Angular signals heuristic locked: `app.config.ts` present AND `@ngrx/store` absent. React, Next.js, Vue fingerprints included. Missing file silently skipped.
- [ ] C.11 (GREEN) Implement `pyproject.toml` + `requirements.txt` + `setup.py` parser in `internal/application/init/detector/parser_python.go`. FastAPI, Django, Flask fingerprints.
- [ ] C.12 (GREEN) Implement `Cargo.toml` parser in `internal/application/init/detector/parser_rust.go`. Extracts package name and edition.
- [ ] C.13 (GREEN) Implement `build.gradle` + `pom.xml` parser in `internal/application/init/detector/parser_jvm.go`. Spring Boot fingerprint from `spring-boot-starter` in dependencies.
- [ ] C.14 (GREEN) Implement arch style heuristics in `internal/application/init/detector/arch.go`. Hexagonal: `domain/` + `application/` + `infrastructure/` dirs. Microservices: `cmd/*/` or `services/*/`. Monorepo: `pnpm-workspace.yaml` or `go.work`. Default fallback: `"monolith"`.
- [ ] C.15 (VERIFY) Run detector unit tests only: `go test ./internal/application/init/detector/...`; all green.

---

### Group D — Cache + GraphifySpawner (PR2 part 3)

Spec: `init-graphify-spawn`. Path: `internal/adapters/outbound/graphify/` + `internal/application/init/cache/`.

- [ ] D.1 (RED) Write failing unit test: `CacheKey.Hash()` is deterministic — same inputs produce identical sha256 hex string across two calls. File: `internal/application/init/cache/key_test.go`.
- [ ] D.2 (RED) Write failing unit test: `CacheKey.Hash()` differs when `SophiaDetectorVer` constant changes (7th component). Use two `CacheKey` structs with different `SophiaDetectorVer` values; assert hashes differ.
- [ ] D.3 (RED) Write failing unit test: `FileCache.Lookup` returns hit + deserialized `StructuralContext` when cache file exists and TTL has not expired. File: `internal/application/init/cache/file_cache_test.go`. Uses `t.TempDir()` + fake Clock.
- [ ] D.4 (RED) Write failing unit test: `FileCache.Lookup` returns `ErrCacheMiss` when cache file exists but TTL (24h default) is exceeded per injected Clock.
- [ ] D.5 (RED) Write failing unit test: `FileCache.Write` writes atomically (temp file + `os.Rename`); verifies final file present and original tempfile absent after write.
- [ ] D.6 (RED) Write failing unit test: `GraphifySpawner.Build` returns `*GraphSummary` with TotalNodes/TotalEdges/GodNodes when `FakeExecRunner` returns valid `graph.json` stdout. File: `internal/adapters/outbound/graphify/spawner_test.go`.
- [ ] D.7 (RED) Write failing unit test: `GraphifySpawner.Build` returns `(nil, "", ErrGraphifyDegraded)` when `graphify --version` step returns exit code 127 (not found).
- [ ] D.8 (RED) Write failing unit test: `GraphifySpawner.Build` returns `(nil, version, ErrGraphifyDegraded)` when `graph.json` is malformed JSON; version still captured from `--version` step.
- [ ] D.9 (GREEN) Implement `CacheKey` struct + `Hash()` in `internal/application/init/cache/key.go`. 7 components: `GraphifyVersion`, `RepoRoot`, `GitHead`, `DirtyTreeHash`, `ConfigHash`, sorted `IncludeGlobs` (joined), `SophiaDetectorVer`. Null byte separator between components. `include_globs` default = `["**/*"]` documented in code comment when field is empty.
- [ ] D.10 (GREEN) Implement `FileCache` in `internal/application/init/cache/file_cache.go`. Path: `<repo_root>/.sophia/cache/structural/<cacheKey>.json`. Atomic write via `os.CreateTemp` + `os.Rename`. TTL check on Lookup using injected Clock. Corrupt JSON → soft miss (no error, return ErrCacheMiss). Auto-creates directory.
- [ ] D.11 (GREEN) Implement `GraphifySpawner` in `internal/adapters/outbound/graphify/spawner.go`. Steps: (1) `graphify --version` via `ExecRunner`; failure → wrap `ErrGraphifyDegraded`. (2) `graphify update <repoRoot>` with configurable timeout (default from `SOPHIA_GRAPHIFY_TIMEOUT_MS`, fallback 30s). (3) Read `<repoRoot>/graphify-out/graph.json`. (4) Parse; GodNodes = top-10 by out_degree. Parse errors wrap `ErrGraphifyDegraded`.
- [ ] D.12 (VERIFY) Run cache + spawner tests: `go test ./internal/application/init/cache/... ./internal/adapters/outbound/graphify/...`; all green.

---

### Group E — Dual persister (PR2 part 4)

Spec: `structural-context-persistence`. Path: `internal/application/init/persister/`.

- [ ] E.1 (RED) Write failing unit test: `DualPersister.Persist` calls `FakeMemoryClient.Ingest` AND `FakeFileCache.Write` when both succeed. File: `internal/application/init/persister/dual_persister_test.go`.
- [ ] E.2 (RED) Write failing unit test: `DualPersister.Persist` when `MemoryClient.Ingest` returns error → returns that error immediately (HARD); still records that file write was NOT attempted (memory-engine is primary HARD path).
- [ ] E.3 (RED) Write failing unit test: `DualPersister.Persist` when `FileCache.Write` returns error → logs WARN but returns nil (SOFT); `MemoryClient.Ingest` was called and succeeded.
- [ ] E.4 (RED) Write failing unit test: `DualPersister.Persist` with same `topic_key` called twice (idempotent re-persist) — both Ingest calls succeed; no panic, no duplicate-key error from fake.
- [ ] E.5 (GREEN) Implement `DualPersister` in `internal/application/init/persister/dual_persister.go`. Uses existing `outbound.MemoryClient.Ingest` with `Type="sdd_init"`, `TopicKey="sdd/<sc.ChangeName>/init"`, tags `["sdd","init","structural_context","schema_v1"]`. On Ingest error: return error (HARD). On FileCache.Write error: WARN log, return nil (SOFT).
- [ ] E.6 (VERIFY) Run persister tests: `go test ./internal/application/init/persister/...`; all green.

---

### Group F — InitService + phase branch + wire (PR2 part 5)

Specs: `init-phase-orchestration`, `sophia-structural-detector`, `structural-context-persistence`. Path: `internal/application/init/service.go`, `internal/application/phase/service.go`, `internal/bootstrap/wire.go`.

- [ ] F.1 (RED) Write failing unit test: `InitService.Run` on cache hit returns cached `StructuralContext` without calling `FakeSophiaDetector` or `FakeGraphifySpawner`. File: `internal/application/init/service_test.go`.
- [ ] F.2 (RED) Write failing unit test: `InitService.Run` on cache miss runs detector + spawner via errgroup in parallel; merges into `StructuralContext{SchemaVersion=1}`; calls `DualPersister.Persist`.
- [ ] F.3 (RED) Write failing unit test: `InitService.Run` when spawner returns `ErrGraphifyDegraded` → `StructuralContext.GraphAvailable=false`, `DegradedReason` populated, phase still completes (no error returned).
- [ ] F.4 (RED) Write failing unit test: `InitService.Run` when detector returns error → propagates as HARD error (non-fatal to INIT means caller logs warn + continues; test asserts error returned and partial context has `SchemaVersion=1`).
- [ ] F.5 (RED) Write failing unit test: `InitService.Run` when persister returns error → logs WARN; INIT completes; returns `StructuralContext` (persister failure is non-fatal to phase completion).
- [ ] F.6 (RED) Write failing unit test: `runInitPhase(PhaseInit)` invokes `InitService.Run` and does NOT call `FakeDispatcher.Dispatch`; `FakeDispatcher.DispatchCalls == 0`. File: `internal/application/phase/service_test.go` (new test function `TestRunInitPhase_DoesNotDispatchToLLM`).
- [ ] F.7 (RED) Write failing unit test: `runInitPhase` asserts `FakeGovernance.EvaluatePhaseCalls == 0` AND `FakeIronLaw.CheckCalls == 0` (INIT skips governance + Iron Law).
- [ ] F.8 (RED) Write failing unit test: `runInitPhase` persists artifact BEFORE phase state change — use `FakePersister` call-order recorder; assert Persist call index < PhaseRepo.Save call index (Iron Law D1.2).
- [ ] F.9 (RED) Write failing unit test: `runPhase` on non-PhaseInit type (e.g., PhaseSpec) still routes to LLM dispatcher (regression guard); `FakeDispatcher.DispatchCalls == 1`.
- [ ] F.10 (GREEN) Implement `InitService.Run` in `internal/application/init/service.go`. Sequence: (1) CacheKey.Build; (2) CacheStore.Lookup — hit returns; (3) errgroup: Detector.Detect + Spawner.Build; (4) merge StructuralContext with SchemaVersion=1, ProjectID/ChangeID/ChangeName from Change; (5) Persister.Persist (WARN on error, continue); (6) return sc, buildEnvelope(c, sc), nil. Inject Clock, IDGen. No direct `time.Now()`.
- [ ] F.11 (GREEN) Add `Init InitService` field to `appphase.Deps` struct in `internal/application/phase/service.go` (nil-tolerant — used only when PhaseInit branch fires). Update `New()` panic guard: Init NOT required (optional dep). Document field with comment matching ApplyExecutor pattern.
- [ ] F.12 (GREEN) Add INIT branch at TOP of `runAsync()` in `internal/application/phase/service.go` (before apply-executor branch at line ~324): `if p.Type() == phase.PhaseInit { s.runInitPhase(ctx, c, p, in); return }`.
- [ ] F.13 (GREEN) Implement `runInitPhase` in `internal/application/phase/service.go`. Mirrors Steps 13-16 of `runAsync`: InitService.Run → Validator.Validate → p.Complete(env, clock.Now()) → PhaseRepo.Save → advanceChange → appendAudit → publishEvent(EventPhaseCompleted). Skips governance, IronLaw, prompt, session, dispatch. `persistArtifactsToMemory` skipped (InitService.Run persists internally).
- [ ] F.14 (GREEN) Configure MemoryClient timeout in `internal/bootstrap/wire.go` for INIT path — verify existing HTTP client timeout is compatible with p95 < 30s budget; add explicit `SOPHIA_MEMORY_TIMEOUT_MS` override if not already present.
- [ ] F.15 (GREEN) Wire InitService in `internal/bootstrap/wire.go`: create `detector`, `gitRunner`, `fileReader`, `keyBuilder`, `fileCache`, `execRunner`, `spawner`, `persister`, `initSvc`; assign `phase.Deps.Init = initSvc`. Update all `appphase.New(appphase.Deps{...})` call sites to include `Init: nil` where INIT is not exercised: `service_test.go:403,446,646,1268,1351,1412`, `archive_event_test.go:102,294`.
- [ ] F.16 (VERIFY) Integration test: full INIT flow with go-hex fixture; guarded by `haveGraphify()` helper (`t.Skip` if absent). File: `test/integration/init_flow_test.go`. Asserts: StructuralContext persisted to memory-engine + local file, GraphAvailable=true (when graphify present), SchemaVersion=1.
- [ ] F.17 (VERIFY) Degraded-mode integration test (no `t.Skip` — runs unconditionally): inject `FakeSpawner` returning ErrGraphifyDegraded; assert GraphAvailable=false, DegradedReason non-empty, Persist called, phase ends DONE.
- [ ] F.18 (VERIFY) Regression: run full existing phase service test suite: `go test ./internal/application/phase/...`; all existing tests still green.
- [ ] F.19 (CHECKPOINT) Operator approval before commit + push for PR2.

---

### Group G — Cross-cutting verification

- [ ] G.1 Run `go test ./...` in sophia-orchestator; all tests green.
- [ ] G.2 Run `go test ./...` in sophia-cli; all tests green.
- [ ] G.3 Confirm no `Co-Authored-By` or AI attribution in any commit message.
- [ ] G.4 Confirm all commit messages follow conventional commits format: `feat(init)`, `test(init)`, `chore(bootstrap)`, etc.
- [ ] G.5 FINAL CHECKPOINT — operator marks M-KNOW-INIT-0 done.

---

## Strict TDD discipline

Every GREEN task (A.9–A.12, B.4–B.5, C.9–C.14, D.9–D.11, E.5, F.10–F.15) is preceded by at least one RED task. Tests run before production code. No exceptions. Clock and IDGenerator are always injected — no direct `time.Now()` in `internal/application/init/`.

## Out of scope

- AllowlistEnforcer wiring (deferred to follow-on milestone)
- Pattern A sidecar + Go MCP stdio client (deferred)
- LLM-phase Graphify queries (deferred)
- SophiaDetectorVer CI lint guard (follow-on ops task; 7th cache key component already mitigates the runtime risk)
- Angular signals heuristic v2 refinement (deferred)
- Migration of existing PhaseInit consumers (none exist — PhaseInit was a stub)
