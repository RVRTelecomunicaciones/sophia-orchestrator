# Exploration — init-structural-detection-graphify (M-KNOW-INIT-0)

**Strategy ref:** V4.1, decisions D11 + D6 + D10, milestone M-KNOW-INIT-0.
**Mode:** SDD explore. NO production code changes; investigation only.
**Cross-repo scope:** sophia-orchestator (primary) + sophia-agent-mcp + sophia-cli. sophia-memory-engine consumed via existing API (no changes).
**Engram artifact:** `sdd/init-structural-detection-graphify/explore`.

---

## 1. Critical architectural discovery

**`PhaseInit` already exists with `ConfidenceThreshold = 0.0`.**

`sophia-orchestator/internal/domain/phase/type.go:12`:
```go
PhaseInit PhaseType = "init"
// ConfidenceThreshold = 0.0
// (comment in type.go: "init carries no agent envelope; threshold 0 means
//  transition is unconditional")
```

Interpretation: in V1 spec, INIT phase was already designed to NOT dispatch to an LLM. It's a marker phase whose transition is unconditional. This perfectly matches V4.1 anti-pattern 7-bis: **INIT detects structure deterministically, no LLM call.**

This means INIT-0 works WITH the existing phase machinery:
- Adds a branch in `runPhase()` that handles `PhaseInit` specially.
- Runs structural detection deterministically.
- Marks the phase DONE directly (no envelope, no dispatch).

We do NOT need to redesign the phase lifecycle. We need to plug deterministic execution into the existing INIT slot.

---

## 2. Current state — per surface

### Surface 1: sophia-orchestator (INIT phase + detector + cache + spawn)

| File | Current state |
|---|---|
| `internal/domain/phase/type.go:12,82` | `PhaseInit` exists; `ConfidenceThreshold = 0.0`; designed for non-LLM |
| `internal/application/phase/service.go` (around L857 `buildPriorContext` + L1057 `eventTypeForStatus`) | Today `runPhase()` dispatches identically for every phase — there is NO INIT branch yet. The INIT must short-circuit BEFORE the LLM path. |
| `internal/bootstrap/wire.go` | Where new detector deps wire in |
| `internal/application/init/` | Does NOT exist — new package |
| `internal/application/init/detector/` | Does NOT exist — new package for SophiaDetector + StructuralContext |

### Surface 2: sophia-agent-mcp (allowlist wiring + Graphify proxy)

| File | Current state |
|---|---|
| `internal/infrastructure/config/config.go:184,306` | `MCPProviderConfig` struct + `Config.MCPProviders` field — LIVE (PRE-0) |
| `internal/infrastructure/mcp/allowlist.go:58` | `AllowlistEnforcer.Authorize(providerID, toolName)` — LIVE but NOT wired into any dispatch path |
| `internal/adapters/inbound/mcp/server.go` (`buildSDKServer`) | Currently handles only `agent.run` and `agent.health`. No proxy for external MCP providers. |
| `internal/application/mcpproxy/` | Does NOT exist — new package for ExternalMCPProxy service |

### Surface 3: sophia-cli (bootstrap detection)

| File | Current state |
|---|---|
| `internal/application/initializer.go:44` | `Initializer.Run()` writes `.sophia.yaml`. No Python/graphify detection. |
| `internal/application/doctor.go` | `DoctorService.Run()` checks Docker, Compose, Git, XDG. No graphify check. |
| `outbound.GraphifyProber` port | Does NOT exist — new port + adapter |

### Surface 4: sophia-memory-engine (no code changes)

| Endpoint | Status |
|---|---|
| `POST /api/v1/memories` with `type=semantic`, `topic_key=init/<change_id>` | LIVE. Migration 004 partial unique index on `topic_key` for active rows ensures idempotent upserts. |

---

## 3. Affected areas (concrete file list)

| File | Action | Surface |
|---|---|---|
| `sophia-orchestator/internal/application/init/detector/detector.go` | NEW | 1 |
| `sophia-orchestator/internal/application/init/detector/types.go` | NEW (StructuralContext) | 1 |
| `sophia-orchestator/internal/application/init/detector/<lang>_*.go` | NEW (per-language parsers) | 1 |
| `sophia-orchestator/internal/application/init/cache.go` | NEW (cache key + TTL) | 1 |
| `sophia-orchestator/internal/application/init/graphify_spawn.go` | NEW (subprocess lifecycle) | 1 |
| `sophia-orchestator/internal/application/init/service.go` | NEW (orchestrates detect+spawn+merge+persist) | 1 |
| `sophia-orchestator/internal/application/phase/service.go` | MODIFIED — INIT branch in runPhase() | 1 |
| `sophia-orchestator/internal/bootstrap/wire.go` | MODIFIED — wire detector + cache + persister | 1 |
| `sophia-agent-mcp/internal/application/mcpproxy/proxy.go` | NEW (ExternalMCPProxy with AllowlistEnforcer middleware) | 2 |
| `sophia-agent-mcp/internal/adapters/outbound/mcpstdio/client.go` | NEW (Go MCP stdio client) | 2 |
| `sophia-agent-mcp/internal/bootstrap/wire.go` | MODIFIED — wire proxy at boot | 2 |
| `sophia-agent-mcp/internal/adapters/inbound/mcp/server.go` | MODIFIED — expose proxy via SDK or new route | 2 |
| `sophia-cli/internal/adapters/outbound/graphifyprobe/prober.go` | NEW (concrete Python+graphify detector) | 3 |
| `sophia-cli/internal/application/initializer.go` | MODIFIED — InitializerDeps adds GraphifyProber | 3 |
| `sophia-cli/internal/adapters/inbound/cli/init.go` (or equivalent) | MODIFIED — `--auto-bootstrap-graphify` flag | 3 |

---

## 4. Cross-repo coupling

- **No new event constants** → wire_alignment_test does NOT need updating. This is NOT a same-commit-pair scenario.
- 3 independent Go modules; coupling is by data contract:
  - orch persists `StructuralContext` JSON to memory-engine via HTTP → no compile-time coupling
  - orch may call agent-mcp's MCP server for Graphify tools (in EXPLORE/APPLY/VERIFY, not in INIT itself) → out-of-process via stdio MCP
  - sophia-cli is fully independent of orch and agent-mcp; only writes `.sophia.yaml` and probes external CLIs
- PRs across the 3 repos can land independently. Recommended order:
  1. sophia-cli (independent bootstrap)
  2. sophia-orchestator (INIT detector + phase branch + cache + spawn)
  3. sophia-agent-mcp (AllowlistEnforcer + ExternalMCPProxy)

Surface 3 can land first since it has no dependencies. Surface 2 can be deferred to a follow-on if scope creep threatens 400-line PR budget.

---

## 5. WHERE does Sophia structural detector live? — DECISION

**Recommendation: `sophia-orchestator/internal/application/init/detector/`.**

Reasoning:
- INIT phase service is the SINGLE consumer of structural detection output.
- Detector is pure Go file system reads — no need for MCP or runtime-adapters indirection.
- Per orchestator CLAUDE.md, "orchestator coordinates" — coordinating structural detection within the INIT phase IS coordination, not policy or memory storage.
- File system reads of repo's own manifests are NOT a "side effect" in the orchestator's sense (no shell execution, no git mutation). They are read-only inspections of files the orchestator already has access to via the worktree it manages.
- Placing detector in agent-mcp would invert the dependency: orch would call agent-mcp for pure Go work that has no MCP justification.

Alternative considered: place detector in sophia-runtime-adapters. Rejected because:
- Detector does not emit receipts or execute side effects
- Adds inter-process call latency for what is a few file reads

---

## 6. WHERE in sophia-orchestator does INIT phase live? — File map

- `internal/domain/phase/type.go:12` — PhaseInit constant
- `internal/domain/phase/type.go:82` — ConfidenceThreshold = 0.0 for PhaseInit
- `internal/application/phase/service.go` — `runPhase()` and `advanceChange()` (PRE-0 added EventPhaseArchived around L911); INIT branch belongs near the top of `runPhase()` or in a dispatcher map
- `internal/bootstrap/wire.go` — service wiring

The INIT branch must intercept BEFORE any LLM dispatch happens. The cleanest pattern: add an `InitService` dependency to `phase.Service.Deps` and route at the top of `runPhase()` when `p.Type() == PhaseInit`.

---

## 7. WHERE in sophia-agent-mcp does MCP tool dispatch happen?

- `internal/adapters/inbound/mcp/server.go` `buildSDKServer` — registers `agent.run` and `agent.health` only. Both are direct method handlers using the go-sdk's MCP server framework.
- No existing external MCP client code in agent-mcp.

For Surface 2:
- AllowlistEnforcer plugs in BEFORE forwarding to the external provider (e.g., before calling `graphify` MCP tools).
- The dispatch chain for proxied tools needs to be NEW code: receive MCP tool request → resolve provider from request → AllowlistEnforcer.Authorize(providerID, toolName) → forward to external MCP stdio process → return response.
- The new `ExternalMCPProxy` service owns this chain.

---

## 8. HOW does graphify build + serve interact with the Go orchestator?

Two patterns considered:

**Pattern A — Sidecar serve + Go MCP stdio client** (V4.1 7-ter spec):
- orchestator spawns `graphify update` (build) — exec.Command with stdout/stderr capture
- orchestator spawns `graphify serve graphify-out/graph.json` as a sidecar (long-running stdio process)
- Go MCP stdio client (in orch or agent-mcp) sends JSON-RPC messages over stdin/stdout
- Hot-reload: graphify serve watches mtime + size; reloads graph.json on changes
- Process supervision: signal SIGTERM on shutdown; graceful timeout

**Pattern B — CLI per-query (no sidecar)**:
- orchestator spawns `graphify update` once for build
- Each query (e.g., god_nodes, get_community) is a separate `graphify <subcommand>` exec.Command
- No long-running process to supervise
- No MCP client needed in Go
- Tradeoff: cold-start cost per query (~1-2s × few queries)

**Recommendation for INIT-0**: start with **Pattern B** if INIT only needs `graphify update` + reading `graphify-out/graph.json` directly (skip queries entirely; parse JSON file). If INIT needs live tool queries (god_nodes, community), use Pattern A.

V4.1 7-ter.5 spec says: "spawn graphify build + serve". But for INIT alone, the detector can READ graph.json statically. Live MCP queries are only needed for EXPLORE/APPLY/VERIFY phases — that's Surface 2's territory.

**Decision recommended for proposal**: Pattern B for INIT-0; Pattern A deferred to a later milestone when LLM phases need live Graphify queries.

This also defers the `Go MCP stdio client` question — sophia-agent-mcp's existing code uses go-sdk in SERVER mode (`buildSDKServer`). Client mode support must be verified before Surface 2.

---

## 9. WHERE does StructuralContext persist? — DECISION

**Recommendation: BOTH** memory-engine semantic memory AND local file cache.

- `POST /api/v1/memories` body:
  ```json
  {
    "type": "semantic",
    "topic_key": "init/<change_id>",
    "content": "<JSON-serialized StructuralContext>",
    "project_id": "<project>",
    "tags": ["init", "structural_context"]
  }
  ```
- Memory-engine partial unique index (migration 004) on `topic_key` makes this idempotent.
- Local file at `<repo_root>/.sophia/cache/structural/<cache_key>.json` enables fast re-read without network call when cache key matches.
- Cross-session: memory-engine retains it across runs.
- Per-session fast path: local cache.

---

## 10. Bootstrap detection in sophia-cli — entry point

`internal/application/initializer.go:44` Initializer.Run() is the right injection point. The detection runs INSIDE `Run()` after .sophia.yaml is written but BEFORE returning to the user.

Concrete:
- Add `GraphifyProber outbound.GraphifyProber` to `InitializerDeps`
- After `.sophia.yaml` write, call `result, err := prober.Probe(ctx)`
- If `result.Available` → log `info: graphify detected, version=...`
- If `!result.Available` → log `warn: graphify not available; INIT will run in degraded mode` + populate `graph_available=false` in the local cache or config
- Flag `--auto-bootstrap-graphify` triggers `uv tool install "graphifyy[mcp]==0.8.35"` via the prober

---

## 11. Cache strategy concrete impl

Compute cache key in `sophia-orchestator/internal/application/init/cache.go`:

```go
type CacheKey struct {
    GraphifyVersion string
    RepoRoot        string
    GitHead         string
    DirtyTreeHash   string
    IncludeGlobs    []string
    ConfigHash      string
}

func (k CacheKey) Hash() string {
    h := sha256.New()
    h.Write([]byte(k.GraphifyVersion))
    h.Write([]byte(k.RepoRoot))
    h.Write([]byte(k.GitHead))
    h.Write([]byte(k.DirtyTreeHash))
    for _, g := range sortedCopy(k.IncludeGlobs) {
        h.Write([]byte(g))
    }
    h.Write([]byte(k.ConfigHash))
    return hex.EncodeToString(h.Sum(nil))
}
```

- `GraphifyVersion`: from `graphify --version` output captured at prober time
- `RepoRoot`: absolute path
- `GitHead`: `git rev-parse HEAD` via exec.Command
- `DirtyTreeHash`: sha256 of `git status --porcelain` output (modified+untracked files list)
- `IncludeGlobs`: from config or default
- `ConfigHash`: sha256 of `.graphify.yaml` content if exists, else empty

Cache dir: `<repo_root>/.sophia/cache/graphify/<cache_key>/`
TTL: 24h default; configurable.

---

## 12. Test surface

| Repo | Pattern | Notes |
|---|---|---|
| sophia-orchestator | strict TDD; table-driven subtests; mock GraphifyProber interface | Detector tests use embed of fixture manifests for Go/Angular/Python/Rust/Java |
| sophia-agent-mcp | strict TDD; mock external MCP process | AllowlistEnforcer already has 5 unit tests; need ExternalMCPProxy unit tests with fake stdio process |
| sophia-cli | strict TDD; mock GraphifyProber | Initializer test uses fake prober returning Available or NotAvailable |
| sophia-memory-engine | NO changes; no tests added | |

**No CI gate alignment**: wire_alignment_test does not apply (no new event constants).

---

## 13. Risks / blockers

1. **`runPhase()` does NOT short-circuit for PhaseInit today** (HIGH for correctness; LOW risk to fix). Even though `ConfidenceThreshold = 0.0`, the dispatcher may still build a prompt and try to dispatch. Must verify and add explicit INIT branch.
2. **go-sdk client mode unverified** (MEDIUM). If we keep Pattern B (CLI per-query) for INIT-0, this is deferred. If we need Pattern A (sidecar + client), must verify go-sdk client support before spec or design alternative client.
3. **Python/graphify absence on CI** (HIGH for integration tests, LOW for units). Interface-based mocks mandatory. All exec.Command paths behind a Go interface that can be faked. Integration tests guarded with `if !haveGraphify() { t.Skip() }`.
4. **Process leak risk if Pattern A adopted** (deferred unless adopted). Mitigation: process manager with deferred SIGTERM + timeout.
5. **AllowlistEnforcer wiring scope** (LOW but must be locked in proposal). Two scopes:
   - **INIT-only**: Surface 2 deferred entirely. AllowlistEnforcer remains unwired until a later milestone when LLM phases call Graphify.
   - **LLM-phases-too**: Surface 2 IS in INIT-0 scope. Adds ExternalMCPProxy + stdio client.
6. **No clean migration path if StructuralContext shape changes**. Future versions need a `schema_version` field on the persisted JSON to support evolution.

---

## 14. Approaches considered (real forks)

| Question | A | B | C | Recommendation |
|---|---|---|---|---|
| Detector location | orch | agent-mcp | runtime-adapters | **A** (single consumer; pure Go FS reads) |
| StructuralContext persistence | memory-engine only | local cache only | both | **C** (cross-session + fast path) |
| Graphify lifecycle | sidecar serve | CLI per-query | CLI build + parse graph.json | **B or C** for INIT-0; sidecar deferred |
| AllowlistEnforcer dispatch | middleware in proxy | explicit per-handler | not wired in INIT-0 | **A** if proxy is in scope; otherwise defer |
| Go MCP client | go-sdk client mode | custom JSON-RPC stdio | none (CLI only) | **C** for INIT-0; verify go-sdk before adopting A |
| AllowlistEnforcer wiring scope | INIT-only | LLM-phases-too | not wired in INIT-0 | **operator decides in proposal** |

---

## 15. Recommendation: scope split for proposal

Three logical PR groups (all under 400 LoC budget):

**PR1 — sophia-cli bootstrap** (~150 LoC):
- GraphifyProber port + adapter
- Initializer integration
- `--auto-bootstrap-graphify` flag
- Tests

**PR2 — sophia-orchestator INIT detector + spawn + cache + persist** (~350 LoC):
- detector package (Go-only manifest parsers, framework fingerprint, arch heuristics)
- StructuralContext type
- cache key/TTL logic
- graphify spawn (Pattern B: CLI per-query for INIT)
- InitService orchestrates detect+spawn+merge+persist
- phase/service.go INIT branch
- bootstrap wire
- Memory-engine persistence via HTTP (existing endpoint)
- Tests

**PR3 — sophia-agent-mcp AllowlistEnforcer wiring** (only if operator decides LLM-phases-too in proposal) (~250 LoC):
- ExternalMCPProxy service
- Go MCP stdio client (or defer if no LLM-phase Graphify needed yet)
- Wire AllowlistEnforcer into proxy
- Server.go integration
- Tests

If PR3 is deferred to a follow-up milestone, INIT-0 lands in 2 PRs. If included, INIT-0 lands in 3 PRs stacked-to-main.

---

## 16. Skill resolution

No project-specific skill registry found. Standard SDD phase skills apply per `sdd-init/2026`. Apply phase will need:
- go-testing (table-driven + interface mocks)
- persistence-postgres (NOT applicable for INIT-0 — no migrations)
- api-contracts (memory-engine HTTP contract)
- testing-quality

`skill_resolution: none` — standard SDD skills apply.
