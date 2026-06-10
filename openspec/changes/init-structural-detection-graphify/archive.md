# Archive Report: init-structural-detection-graphify (M-KNOW-INIT-0)

**Change**: init-structural-detection-graphify
**Archived**: 2026-06-08
**Mode**: openspec + Engram (hybrid)
**Verification verdict**: PASS_WITH_WARNINGS (0 CRITICAL, 0 WARNING, 4 SUGGESTION)
**Strategy doc**: V4.1 (sophia_hermes_learning_loop_strategy_v4_1.md) §7-bis + §7-ter + §16 M-KNOW-INIT-0

## Intent

INIT-0 delivered deterministic structural detection plugged into the pre-existing `PhaseInit` constant (designed with `ConfidenceThreshold = 0.0` for unconditional, non-LLM transition) via Pattern B Graphify spawn (local `exec.Command`-only, no sidecar) + pure-Go manifest/framework/arch parsers. The `StructuralContext` artifact — containing detected languages, frameworks, package managers, architecture style, and a graph summary from Graphify — is persisted to BOTH memory-engine (for cross-session search) AND a local 24h-TTL cache (for fast re-runs). INIT-0 enforces V4.1 D11 (INIT detects structure deterministically; does NOT create skills; does NOT invoke LLM). This was a prerequisite for M0.5 (PriorContext struct refactor) and M1 (skills schema migration + lifecycle + matcher), which will consume the structural ground truth INIT produces.

## Capabilities delivered (5)

| Capability | Status | Where (main HEAD) |
|---|---|---|
| cli-graphify-bootstrap | DELIVERED | sophia-cli @9d91267: GraphifyProber + adapter + Initializer integration + --auto-bootstrap-graphify flag |
| sophia-structural-detector | DELIVERED | sophia-orchestator @bcf045e: internal/application/init/detector/ (pure Go: manifest parsers, framework fingerprint, arch heuristics) |
| init-graphify-spawn | DELIVERED | sophia-orchestator @bcf045e: internal/adapters/outbound/graphify/spawner.go (Pattern B: `graphify update` → read graph.json) |
| init-phase-orchestration | DELIVERED | sophia-orchestator @bcf045e: internal/application/phase/service.go runAsync branch (INIT short-circuits BEFORE governance/Iron-Law/dispatch) |
| structural-context-persistence | DELIVERED | sophia-orchestator @bcf045e: internal/application/init/persister/dual_persister.go (memory-engine HARD + file cache SOFT) |

## PRs landed (2 PRs across 2 repos)

| Repo | PR | Merged | Lines | Notes |
|---|---|---|---|---|
| sophia-cli | #21 | 2026-06-08T17:03:23Z | 483 LoC | 8 RED-first tests; fix commit 30034e1 (gofmt + unparam); BUILD/TEST/LINT/GOSEC/VULNCHECK all ✅ |
| sophia-orchestator | #79 | 2026-06-08T16:49:10Z | 3488 LoC across 39 files | size:exception justified (Group F phase.Deps prerequisite chain); fix commit 47af90a (.gitkeep + 13 lint issues); UNIT/INT/LINT/WIRE/VULNCHECK/DOCKER all ✅ |

Main HEADs after merge:
- sophia-cli main: 9d91267
- sophia-orchestator main: bcf045e

## Operator-locked decisions (V4.1 D11 + 11 in proposal + apply adaptations)

1. **INIT detects structure deterministically (D11 + V4.1 §7-bis)** — verified in detector package: pure Go, zero `exec.Command` calls in detector code itself; `GraphifySpawner` is isolated in adapters layer.
2. **Surface 2 (AllowlistEnforcer wiring) DEFERRED** — still unwired in main; INIT-0 does not require it (Pattern B uses local exec only, no MCP protocol involved).
3. **Pattern B (CLI per-query, NO sidecar)** — verified in spawner.go: only `graphify update`, no `graphify serve`; no go-sdk client mode.
4. **Detector at sophia-orchestator/internal/application/init/detector/** — verified; pure-domain hexagonal placement.
5. **Dual persistence (memory-engine + local file)** — verified in DualPersister: memory HARD path (failure blocks INIT), file SOFT path (failure logged, INIT continues).
6. **Cache key 7 components (V4.1 §7-ter.8 + SophiaDetectorVer)** — verified in cache/key.go: graphify_version + repo_root + git_head + dirty_tree_hash + sorted(include_globs) + config_hash + SophiaDetectorVer (7th component added during apply per D-INIT-5 scope clarification).
7. **runPhase INIT branch at TOP of runAsync BEFORE governance/Iron-Law/dispatch** — verified at service.go:392-395 (explicit check BEFORE apply-executor branch).
8. **Synthetic envelope respecting Iron Law D1.2 (envelope-before-state)** — verified in init_phase_test.go: call-order recorder pattern asserts Persister.Persist index < PhaseRepo.Save index; envelope validated BEFORE p.Complete.
9. **Angular signals heuristic v1 (app.config.ts + no @ngrx/store)** — verified in parser_node.go comments and test fixtures; locked for INIT-0, refinement deferred.
10. **Degraded-first bootstrap (graphify missing → graph_available=false)** — verified in service.go merge step: spawner error captured, context still persisted, GraphAvailable=false.
11. **StructuralContext.SchemaVersion=1 from day 0** — verified in types.go: const StructuralContextSchemaV1 = 1; InitService checks on cache read; consumers MUST check before deserialize.

## Adaptations approved during apply

| Adaptation | Rationale | Evidence |
|---|---|---|
| AutoBootstrap flag on InitInput (not Deps) | Per-invocation control matches Force pattern; allows operator to toggle per `sophia init` call | apply-progress #788: InitInput{AutoBootstrap bool} wired in initializer_graphify_test.go |
| size:exception on PR2 (3488 vs 350 forecast) | Group F's phase.Deps prerequisite chain forced atomic scope; cannot split without breaking wire graph integrity | tasks.md Review Workload Forecast noted "atomic milestones"; design §7.1 confirms Phase B dependency order (ports → detector → cache → spawner → persister → service → branch → wire) |
| CacheKey kept with //nolint:revive | Package idiom: `cache.CacheKey` reads better than `cache.Key` at call sites; lint exception documented | fix commit 47af90a + verify.md §Adaptations |
| PhaseRepo lookup for phaseID | Carry-over pattern from PRE-0; phaseID derived from Change.ID() via existing machinery | design §4 (Init Service notes); service.go:408 shows `s.d.PhaseRepo.Save(ctx, p)` using existing pattern |
| gosec G304/G204 annotated with `// #nosec` + rationale | Paths are under caller-controlled repoRoot (not user input); exec args from injected interface (not shell) | fix commit 47af90a: spawner.go, detector.go, key_builder.go, file_cache.go all annotated |

## CI failures journey (2 root causes + 13 lint issues)

**PR1 (sophia-cli #21)**:
- **gofmt**: struct field alignment after InitInput addition in cli/init.go and initializer_graphify_test.go. Fixed by reformatting.
- **unparam**: newInitWithProber returned *FakeProjectConfigStore that all callers discarded with `_`. Removed from signature.
- Fix commit: 30034e1
- Result: BUILD/TEST/LINT/GOSEC/VULNCHECK all ✅

**PR2 (sophia-orchestator #79)**:
- **Root cause 1 — flaky TestDetect_HexagonalArch**: testdata/hexagonal/{domain,application,infrastructure}/ were empty directories. Git only tracks files; CI fresh clone did not have them. Detector classified as "monolith" instead of "hexagonal". Solution: added .gitkeep to each directory.
- **Root cause 2 — 13 lint issues** (make test-unit does not run lint; golangci-lint v2.12 stricter than local setup):
  - errorlint: spawner.go x2 (%v → %w in wrapped errors)
  - gosec G304: file_cache.go, key_builder.go, detector.go, spawner.go (4 places) — annotated `// #nosec G304` with rationale (paths under caller root, not user input)
  - gosec G301: file_cache.go (0o755 → 0o750 on dir creation)
  - gosec G204: exec/runner.go (1 place) — annotated `// #nosec G204` (args from injected interface, not shell)
  - revive var-naming: spawner.go (nil_writer → nilWriter)
  - revive exported: cache/key.go (CacheKey kept with //nolint:revive — idiom)
  - unparam: parser_node.go (3rd return reserved; nolint with doc comment)
  - unused: service_test.go (removed unused `mu` field from fakePersisterF)
  - wrapcheck: 5 places (gitrunner.go x2, spawner.go, detector.go, service.go) — all wrapped with %w
- Fix commit: 47af90a
- Result: UNIT/INT/LINT/WIRE/VULNCHECK/DOCKER all ✅

## Process lessons (new)

1. **`make test-unit` ≠ `make lint`** — run both before push OR add `golangci-lint run` pre-push hook. CI config is source of truth.
2. **Empty test fixture directories need `.gitkeep`** — Git only tracks files; CI fresh clone will not have empty directories.
3. **golangci-lint v2.12 stricter** — than older versions; repo CI config is authoritative.
4. **size:exception precedent** — atomic milestones with hard dependency chains (e.g., phase.Deps prerequisites) may legitimately exceed 400 LoC; document reason in PR body + tasks.md workload forecast.
5. **gosec G304/G204 annotations require rationale** — not blanket suppressions; each must document why the checked pattern is safe in this context.
6. **Docker Hub transient outages** — 504 errors on image pulls do not indicate build failure; CI auto-retry resolves.

## Forwarded to M0.5 / M1 / M-LATER (4 non-blocking SUGGESTIONS)

1. **M-LATER (ops follow-up)**: AllowlistEnforcer wiring (Surface 2) — still unwired. INIT-0 does not need it (Pattern B: local exec only). Deferred to follow-on milestone when LLM-phase Graphify queries are added.
2. **M0.5 + M1 integration gap**: StructuralContext persisted to memory-engine + local cache but no consumer yet. Expected: M0.5 will expose it via PriorContext struct; M1 will consume it in SkillMatcher filter logic. Until then, cross-session reads work (via memory-engine), but data is orphaned from orchestrator perspective.
3. **Ops follow-up**: `SOPHIA_GRAPHIFY_TIMEOUT_MS` referenced in wire.go:292 comment but not wired from config (falls back to 30s default). Recommend adding to config schema + env var in next ops task.
4. **Ops follow-up**: `SophiaDetectorVer` manually-bumped const — parser changes without bump could produce stale cached contexts. Recommend CI guard: fail lint if `detector/*.go` changes but Version const unchanged. (Out of scope for INIT-0; tasks.md marked as follow-on.)

## V4.1 status update

Mark **M-KNOW-INIT-0 as DONE and MERGED**. Both PRs landed to main; both HEAD commits clean.

Next milestone in chain per V4.1 §16: **M0.5 (PriorContext refactor)** — convert plain-string `PriorContext` into a struct with `Render()` method that preserves byte-exact output. Prerequisite for M3 enrichment, which will consume `StructuralContext` + skills + episodes. M0.5 expected to also establish consumer path for `StructuralContext` from INIT-0.

After M0.5: M1 (skills schema migration 010 + lifecycle + matcher), M2 (consolidation worker), M3 (PriorContext enrichment).

## SDD cycle complete

Explore → Propose → Spec → Design → Tasks → Apply → Verify → Archive ✅

All 5 capabilities delivered. All operator-locked decisions honored. All risks identified in proposal mitigated by design. All CI failures resolved. All lint/test/build gates cleared. Ready for operations + M0.5 handoff.

---

## Traceability (Engram observations)

- **Proposal**: `sdd/init-structural-detection-graphify/proposal`
- **Specs (5)**: 
  - `sdd/init-structural-detection-graphify/spec` (cli-graphify-bootstrap)
  - `sdd/init-structural-detection-graphify/spec` (sophia-structural-detector)
  - `sdd/init-structural-detection-graphify/spec` (init-graphify-spawn)
  - `sdd/init-structural-detection-graphify/spec` (init-phase-orchestration)
  - `sdd/init-structural-detection-graphify/spec` (structural-context-persistence)
- **Design**: `sdd/init-structural-detection-graphify/design` (#760)
- **Tasks**: `sdd/init-structural-detection-graphify/tasks` (#776)
- **Apply progress**: `sdd/init-structural-detection-graphify/apply-progress` (#788)
- **Verify report**: `sdd/init-structural-detection-graphify/verify-report` (#790)
- **Archive report** (this document): `sdd/init-structural-detection-graphify/archive-report` (ID assigned on save)

All artifacts persisted in hybrid mode (openspec files + Engram observations).
