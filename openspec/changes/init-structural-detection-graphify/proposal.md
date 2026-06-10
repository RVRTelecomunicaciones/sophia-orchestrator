# Proposal: init-structural-detection-graphify (M-KNOW-INIT-0)

**Strategy ref**: V4.1 §2 D11 + §7-bis (INIT anti-pattern) + §7-ter (Graphify hybrid integration) + §16 milestone M-KNOW-INIT-0.
**Prerequisite**: M-KNOW-PRE-0 archived 2026-06-08 (FTS=`simple`, `phase.archived` event, worker skeleton, MCP providers schema).
**Mode**: SDD propose. NO production code in this artifact. Hybrid persistence.
**Exploration**: `openspec/changes/init-structural-detection-graphify/explore.md` (read in full).
**Graphify audit**: `docs/research/graphify-audit.md`.

---

## Intent

Sophia's INIT phase exists today as a `PhaseInit` constant (`internal/domain/phase/type.go:12`) with `ConfidenceThreshold = 0.0`, designed for **non-LLM unconditional transition** — but `runPhase()` has no INIT branch yet, so the phase silently no-ops. V4.1 §7-bis names this the "INIT creates skills" anti-pattern and inverts it: INIT must deterministically detect the project's structural ground truth (manifests, frameworks, arch style, plus the AST graph via Graphify) and persist a `StructuralContext` artifact. Skills are NOT born here; they are emitted by the archive consolidation worker (M2). The deterministic detection requirement aligns precisely with the pre-existing `ConfidenceThreshold = 0.0` design intent — INIT-0 plugs deterministic execution into the INIT slot that the phase machinery already accommodates.

The PRE-0 archive forwarded an explicit non-goal: AllowlistEnforcer remains unwired until INIT needs to call external MCP providers. V4.1 D11 confirms Graphify integration via **degraded-first bootstrap** (no auto-install by default; INIT continues with `graph_available=false` if Python/uv/graphify are missing). Graphify lifecycle is locked to **Pattern B (CLI per-query)** for INIT-0 per the explore decision in §8 — `graphify update` builds the graph once and INIT reads `graphify-out/graph.json` statically; no sidecar serve, no Go MCP stdio client. `StructuralContext` persists to BOTH memory-engine semantic memory (`POST /api/v1/memories` with `topic_key=init/<change_id>`, idempotent via migration 004 partial unique index) AND local file cache at `<repo_root>/.sophia/cache/structural/<cache_key>.json` per the explore §9 decision.

Without this change, M0.5 (PriorContext struct refactor) and M1 (skills schema + matcher) lack the structural ground truth they need to filter skills correctly on legacy projects.

---

## Scope

### In Scope (2 PRs, independent)

**PR1 — sophia-cli bootstrap (~150 LoC)**:
- `outbound.GraphifyProber` port + concrete adapter at `internal/adapters/outbound/graphifyprobe/prober.go` (detects Python 3.10+, `graphify --version`, captures version string).
- `Initializer.Run()` integration: after `.sophia.yaml` write, call `prober.Probe(ctx)` and log result. Populate `graph_available` flag for downstream consumers.
- `--auto-bootstrap-graphify` flag (opt-in): runs `uv tool install "graphifyy[mcp]==0.8.35"` via prober. Default OFF (degraded-first per V4.1 §7-ter.7).
- Unit tests with fake `GraphifyProber` returning Available / NotAvailable / VersionMismatch.

**PR2 — sophia-orchestator INIT detector + spawn + cache + persist (~350 LoC)**:
- NEW package `internal/application/init/detector/` (per explore §5 decision): manifest parsers (Go/TS/Python/Rust/JVM), framework fingerprint (Angular, NgRx, React, Next, Spring Boot, Django, FastAPI, Flask), arch style heuristics (hexagonal directories, monorepo signals).
- NEW `internal/application/init/detector/types.go` defining `StructuralContext` with `schema_version` field (mitigates explore §13 risk 6).
- NEW `internal/application/init/cache.go` — 6-component sha256 cache key (graphify_version + repo_root + git_head + dirty_tree_hash + include_globs + config_hash) with 24h TTL default.
- NEW `internal/application/init/graphify_spawn.go` — Pattern B: `graphify update` via `exec.Command` behind a `GraphifySpawner` interface (testable with fakes); reads `graphify-out/graph.json` directly.
- NEW `internal/application/init/service.go` — `InitService` orchestrates: detect → spawn (if `graph_available`) → merge → persist (dual: memory-engine HTTP + local file).
- MODIFIED `internal/application/phase/service.go` — INIT branch at TOP of `runPhase()` short-circuiting BEFORE LLM dispatch; marks phase DONE directly via the `ConfidenceThreshold = 0.0` unconditional-transition path.
- MODIFIED `internal/bootstrap/wire.go` — wire `GraphifySpawner`, `StructuralPersister`, `InitService` into `phase.Service.Deps`.
- NEW `StructuralPersister` port + adapter calling memory-engine `POST /api/v1/memories` (existing endpoint, no engine changes).
- Unit tests (table-driven, fake interfaces for `GraphifySpawner`/`StructuralPersister`/`Clock`) + integration tests guarded with `if !haveGraphify() { t.Skip() }`.

### Out of Scope (Non-goals — explicit)

- **Surface 2 (AllowlistEnforcer wiring + `ExternalMCPProxy` in sophia-agent-mcp)** — DEFERRED to a follow-on milestone. AllowlistEnforcer stays unwired (already flagged in PRE-0 archive warning #4). INIT-0 archive will re-confirm this gap.
- **Pattern A sidecar serve + Go MCP stdio client** — DEFERRED. go-sdk client mode unverified (explore §13 risk 2); not needed when INIT reads `graph.json` statically.
- **LLM-phase Graphify tool queries** (EXPLORE/APPLY/VERIFY using `query_graph`, `god_nodes`, `get_community`, etc.) — DEFERRED. INIT-0 does not need live MCP queries.
- **New event constants** — none introduced. `wire_alignment_test` does NOT apply. No cross-repo same-commit-pair.
- **Migration of existing INIT consumers** — `PhaseInit` already exists with `ConfidenceThreshold = 0.0`; consumers already accept unconditional transition.
- **`StructuralContext` consumption by `SkillMatcher`** — M1's responsibility.
- **`PriorContext` struct refactor** — M0.5's responsibility.
- **Auto-bootstrap on by default** — V4.1 §7-ter.7 mandates degraded-first; auto-install is opt-in via `--auto-bootstrap-graphify`.
- **`affected_nodes` MCP tool / upstream Graphify PR** — deferred per graphify-audit §Risk 3.
- **Docker sidecar for Graphify (Q-G1 Option C)** — out of scope per V4.1 §7-ter.7.

---

## Capabilities

> Contract with `sdd-spec`. Each item below becomes a new `openspec/specs/<name>/spec.md`.

### New Capabilities

- `cli-graphify-bootstrap`: sophia-cli `init` command detects Python 3.10+ and `graphify --version` via `GraphifyProber` port; writes `graph_available` flag and detected version; `--auto-bootstrap-graphify` flag triggers `uv tool install "graphifyy[mcp]==0.8.35"` (opt-in, OFF by default).
- `sophia-structural-detector`: Go package at `internal/application/init/detector/` that parses manifests (`go.mod`, `package.json`, `tsconfig.json`, `pyproject.toml`, `requirements.txt`, `Cargo.toml`, `build.gradle*`, `pom.xml`), fingerprints frameworks + versions, applies arch style heuristics, and emits `StructuralContext` (with `schema_version`).
- `init-graphify-spawn`: orchestator runs `graphify update` via `GraphifySpawner` interface; parses `graphify-out/graph.json`; cache check via 6-component sha256 key with 24h TTL at `<repo_root>/.sophia/cache/graphify/<cache_key>/`.
- `init-phase-orchestration`: `runPhase()` branches on `PhaseInit` to invoke `InitService.Run(ctx, change)` and mark phase DONE deterministically; NO LLM dispatch for `PhaseInit`.
- `structural-context-persistence`: dual persistence — memory-engine semantic memory (`POST /api/v1/memories`, `type=semantic`, `topic_key=init/<change_id>`, idempotent via migration 004 partial unique index) AND local file at `<repo_root>/.sophia/cache/structural/<cache_key>.json` for fast re-runs.

### Modified Capabilities

- None at spec level. `PhaseInit` already exists in the domain spec; INIT-0 adds runtime semantics to a constant that was already designed for unconditional transition.

---

## Approach

Order of work within PR2 (each step is test-first per strict TDD):

1. **Detector types** — define `StructuralContext` struct (with `schema_version`) and per-language manifest fixtures embedded as test data.
2. **Detector parsers** — manifest parsers per ecosystem (Go/TS/Python/Rust/JVM) + framework fingerprint regex/JSON walk + arch-style heuristics. Pure Go file system reads; no exec, no network.
3. **Cache layer** — `CacheKey.Hash()` deterministic over 6 components (see explore §11); read/write local cache file with TTL check via injected `Clock`.
4. **Graphify spawn** — `GraphifySpawner` interface; concrete impl runs `exec.Command("graphify", "update", repoRoot)` and reads `graphify-out/graph.json`. Tests use fake spawner returning canned JSON or `ErrNotAvailable`.
5. **InitService** — orchestrates: compute cache key → check local cache → if miss, detect + spawn + merge → persist (dual) → return `StructuralContext`. All deps injected via `Deps` struct (Detector, Spawner, Cache, Persister, Clock, IDGenerator).
6. **runPhase() branch** — top-of-function check: `if phase.Type() == PhaseInit { ctx := initService.Run(...); markDone(...); return }`. Short-circuits BEFORE any LLM dispatch path.
7. **wire.go** — bootstrap detector + spawner + cache + persister; inject into `phase.Service.Deps`.
8. **Memory-engine persister** — HTTP POST to `/api/v1/memories` with `type=semantic`, `topic_key=init/<change_id>`. Existing endpoint, idempotent.

PR1 (sophia-cli) is independent of PR2 and can land in any order. Both use the same `GraphifyProber` contract semantics so the orch can later trust the cli-written `.sophia.yaml` if it needs to (not required for INIT-0).

Interface boundaries (all behind Go interfaces in the `Deps` struct so unit tests fake them):
- `GraphifyProber` (cli) → detects Python/uv/graphify availability.
- `GraphifySpawner` (orch) → runs `graphify update` and reads `graph.json`.
- `StructuralPersister` (orch) → writes to memory-engine + local cache.
- `Clock`, `IDGenerator` — already exist in shared per Sophia CLAUDE.md D1.5 + Iron Laws.

Degraded-first bootstrap (V4.1 §7-ter.7): if `graphify --version` fails, `StructuralContext.graph_available = false` and `StructuralContext.degraded_reason = "graphify not installed"`. `InitService` still runs the Go detector and persists the partial context. M1's `SkillMatcher` is responsible for filtering skills with `applies_when.graph_required = true`.

---

## Affected Areas

Concrete file list (from explore §3, scoped to In-Scope only):

| Area | Impact | Description |
|------|--------|-------------|
| `sophia-cli/internal/adapters/outbound/graphifyprobe/prober.go` | NEW | Python+graphify detection; `--version` capture |
| `sophia-cli/internal/application/initializer.go` | MODIFIED | `InitializerDeps` adds `GraphifyProber`; call after `.sophia.yaml` write |
| `sophia-cli/internal/adapters/inbound/cli/init.go` | MODIFIED | `--auto-bootstrap-graphify` flag plumb |
| `sophia-orchestator/internal/application/init/detector/types.go` | NEW | `StructuralContext` struct with `schema_version` |
| `sophia-orchestator/internal/application/init/detector/detector.go` | NEW | Detector entrypoint + dispatch |
| `sophia-orchestator/internal/application/init/detector/go_parser.go` | NEW | `go.mod` parser |
| `sophia-orchestator/internal/application/init/detector/node_parser.go` | NEW | `package.json` + `tsconfig.json` parser + framework fingerprint |
| `sophia-orchestator/internal/application/init/detector/python_parser.go` | NEW | `pyproject.toml`, `requirements.txt`, `setup.py` |
| `sophia-orchestator/internal/application/init/detector/rust_parser.go` | NEW | `Cargo.toml` |
| `sophia-orchestator/internal/application/init/detector/jvm_parser.go` | NEW | `build.gradle*`, `pom.xml` |
| `sophia-orchestator/internal/application/init/detector/arch_heuristics.go` | NEW | Hexagonal directories, monorepo signals |
| `sophia-orchestator/internal/application/init/cache.go` | NEW | 6-component sha256 cache key + TTL |
| `sophia-orchestator/internal/application/init/graphify_spawn.go` | NEW | `GraphifySpawner` interface + concrete impl |
| `sophia-orchestator/internal/application/init/service.go` | NEW | `InitService` orchestrates detect+spawn+merge+persist |
| `sophia-orchestator/internal/application/init/persister.go` | NEW | `StructuralPersister` port + memory-engine adapter |
| `sophia-orchestator/internal/application/phase/service.go` | MODIFIED | INIT branch at top of `runPhase()` |
| `sophia-orchestator/internal/bootstrap/wire.go` | MODIFIED | Wire init service deps |

NOT touched in INIT-0: any file under `sophia-agent-mcp/` (Surface 2 deferred). Any file under `sophia-memory-engine/` (engine endpoint already LIVE per PRE-0).

---

## Risks

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `runPhase()` does NOT short-circuit for `PhaseInit` today (explore §13 #1) | HIGH (confirmed) | Correctness | Strict TDD: first failing test asserts NO LLM dispatch when `phase.Type()==PhaseInit`; explicit branch at top of `runPhase()` |
| Python/graphify absent on dev machines (graphify-audit Risk 1) | HIGH | Integration test isolation; INIT functionality | Interface-based mocks for unit tests; `if !haveGraphify() { t.Skip() }` for integration; degraded-first runtime (`graph_available=false` does not break INIT) |
| Graphify version drift (graphify-audit Risk 2) | MEDIUM | Edge-case schema changes between releases | Hard-pin `graphifyy[mcp]==0.8.35`; `StructuralContext.graphify_version` field tracks which version produced each graph |
| `StructuralContext` shape evolution (explore §13 #6) | MEDIUM | Future migration cost | `schema_version` field in v1 from day 0; consumers must check before deserialize |
| AllowlistEnforcer remains unwired through INIT-0 | MEDIUM (security/policy gap) | No external MCP authorization | Explicit non-goal; documented in PRE-0 archive warning #4 and re-confirmed in INIT-0 archive; no production path calls external MCP in INIT-0 (Pattern B = local `exec.Command` only, no MCP protocol involved) |
| Pattern B cold-start cost (~1-2s per `graphify update`) (explore §8) | LOW | p95 budget on cache-miss INIT | 24h TTL local cache; second execution on same change/HEAD is cache hit; target p95<30s cache-miss, <5s cache-hit |
| go-sdk client mode unverified (explore §13 #2) | LOW (deferred risk) | Future Surface 2 work | Out-of-scope for INIT-0; verification block listed for follow-on milestone |
| Process leak risk if Pattern A adopted (explore §13 #4) | N/A | N/A | Pattern A is OUT of scope; risk does not apply to INIT-0 |

---

## Rollback Plan

Per PR (independent rollbacks):

- **PR1 (sophia-cli)**: `git revert` the single commit. `.sophia.yaml` writing behavior reverts to pre-INIT-0 state (no `graph_available` field written). No data migration. No downstream breakage — orch reads `graph_available` defensively (default `false` if absent).
- **PR2 (sophia-orchestator)**: `git revert` the single commit. `runPhase()` reverts to pre-INIT-0 dispatcher (which today is a silent no-op for `PhaseInit` per the explore §1 finding — the constant exists but no branch handled it). Iron Law D1.2 (envelope-before-state) is unaffected because `InitService` never persists state before envelope; revert just removes the new path. memory-engine rows persisted under `topic_key=init/*` remain as orphan records (idempotent upsert means re-running after revert+re-apply just overwrites them). M1/M0.5 not yet implemented → no consumer is reading these records, so orphaning is harmless.

No combined-rollback orchestration needed; PRs are independent and order-agnostic.

---

## Dependencies

- **PRE-0 capabilities (all LIVE per archive 2026-06-08)**:
  - `mcp-providers-config` — schema for declaring providers (consumed in spec by future Graphify MCP wiring; not consumed in INIT-0 runtime).
  - `phase-archived-event` — emission happens after INIT-0 completes its phase work (deferred consumption by M2 worker).
  - `fts-simple-config` — language-agnostic FTS for memory-engine searches over `StructuralContext` records.
  - `consolidation-worker-skeleton` — entrypoint exists at `sophia-memory-engine/cmd/workers/main.go`; not modified by INIT-0.
- **External**: `graphifyy[mcp]==0.8.35` (PyPI, MIT, audited at commit `8a04560` per graphify-audit). Sophia does NOT vendor or fork.
- **Python**: 3.10+ on dev machines (degraded if absent).
- **`uv`** (optional, only if `--auto-bootstrap-graphify` used).

---

## Success Criteria

Mapped to V4.1 §16 M-KNOW-INIT-0 acceptance criteria:

- [ ] `graphify update` build + reading `graphify-out/graph.json` works on 3 fixture repos (Go, Angular, Python) — integration test.
- [ ] Cache hit on second execution of same change/HEAD (no rebuild) — table-driven cache test.
- [ ] `StructuralContext` persisted to memory-engine semantic memory (`topic_key=init/<change_id>`, idempotent) AND local file cache — dual persistence test asserts both writes.
- [ ] Sophia structural detector identifies framework + version in fixture repos (e.g. Angular 17 detected from `@angular/core@17.x` in `package.json` + presence of `app.config.ts`).
- [ ] Degraded mode does not break INIT — when `graphify --version` fails, `StructuralContext.graph_available=false` and `degraded_reason` is populated; `InitService` still returns and persists.
- [ ] p95 INIT < 30s on 500-file repo (cache miss, Pattern B cold-start).
- [ ] p95 INIT < 5s on cache hit.
- [ ] NO LLM dispatch from `runPhase()` when `phase.Type()==PhaseInit` — test asserts `dispatcher.Dispatch` is never called for INIT phase.
- [ ] PR1 (sophia-cli) and PR2 (sophia-orchestator) land independently; either order acceptable; no same-commit-pair coupling.
- [ ] All production logic preceded by failing test per strict TDD (per `sdd-init/2026`).
- [ ] Conventional commits, no Co-Authored-By, no AI attribution.

---

## Strict TDD Note

`strict_tdd: true` per `sdd-init/2026` (cached). Every production change in PR1 and PR2 is preceded by a failing test. All `exec.Command` paths (Python detection, `graphify --version`, `graphify update`) live behind Go interfaces (`GraphifyProber`, `GraphifySpawner`) so unit tests use fakes — no real subprocess in unit tests. Integration tests are guarded:

```go
func haveGraphify() bool {
    _, err := exec.LookPath("graphify")
    return err == nil
}

func TestInitService_Integration(t *testing.T) {
    if !haveGraphify() { t.Skip("graphify not available") }
    // ...
}
```

`Clock` and `IDGenerator` injected per Sophia CLAUDE.md D1.5 + Iron Laws; no direct `time.Now()` or `ulid.Make()` in `internal/application/init/`.

---

## Open Questions

None after the 11 operator-locked decisions:

1. Scope = 2 PRs (Surface 2 deferred). ✅
2. Graphify lifecycle = Pattern B (CLI per-query). ✅
3. `StructuralContext` persistence = BOTH (memory-engine + local cache). ✅
4. `PhaseInit` branch in `runPhase()` (short-circuit BEFORE LLM). ✅
5. Detector location = `sophia-orchestator/internal/application/init/detector/`. ✅
6. No cross-repo wire alignment (no new event constants). ✅
7. PRs are independent (no stacked-to-main coupling). ✅
8. Strict TDD applies. ✅
9. Conventional commits, no AI attribution. ✅
10. Degraded-first bootstrap (no auto-install by default; opt-in flag). ✅
11. Cache key = 6-component sha256 + 24h TTL. ✅

Spec phase may surface secondary clarifications (e.g., exact JSON shape of `StructuralContext`, exact set of framework signatures), which is expected and within `sdd-spec` scope.
