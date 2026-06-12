# Design: context7-bootstrap (V1)

## Context

A greenfield repo produces a `StructuralContext` with detected frameworks/languages but **zero
skills** (`structuralMatches` returns empty, `skill_matcher.go:237-259`), so the LLM falls back to
stale training-data knowledge. The fix is an **event-driven, degraded-first** bootstrap that queries
Context7 only on (a) greenfield or (b) version drift, imports the docs as a `candidate` skill via the
idempotent `InsertIfAbsent` path, and never touches an LLM or the hot-path matcher. D11 is preserved:
INIT still never creates skills; bootstrap is a separate async service.

This design is split into three PRs matching the proposal:
- **PR1 (agent-mcp)** — register Context7 as a second `[[mcp_providers]]` block (TOML only).
- **PR2 (orch)** — `Greenfield` flag + manifest-hash cache invalidation + async bootstrap wiring.
- **PR3 (orch)** — `AppliesWhen` version semantics + `BootstrapTriggerService` + `SkillImporter` +
  the orch→Context7 transport adapter + rate guard + thin-entry fallback.

> **CHECKS-AND-BALANCES OUTCOME:** the proposal's **D-C7-8 (transport)** is NOT satisfiable as
> written — there is no orchestrator outbound path that invokes an arbitrary agent-mcp proxied tool.
> The only existing MCP path is `AgentDispatcher.Dispatch`, hardcoded to `agent.run`/`agent.health`.
> A correction is recorded in `## Proposal corrections` (DG-C7-8) with a real mechanism. All other
> decisions verified implementable; D-C7-7 verified exactly correct (the bug is real).

---

## Verified code-reality table (drives the decisions below)

| Proposal assumption | Reality (verified, file:line) | Impact |
|---|---|---|
| D-C7-8: orch reaches Context7 "via the existing MCP dispatcher port the phases already use" | Orch's only MCP client is `dispatcher/mcp/dispatcher.go`. Its public surface is `AgentDispatcher.Dispatch`, which calls **only** `agent.run` (`dispatcher.go:213`) and `agent.health` (`dispatcher.go:174`). There is NO generic `CallTool(provider.tool)` anywhere in `internal/` (grep clean). The proxied-tool path on agent-mcp (`server.go:308-334`) registers tools for the **dispatched opencode agent** to call over MCP, not for orch. | D-C7-8 corrected → **DG-C7-8**: new orch outbound port `DocsProvider` + adapter reusing the same go-sdk `StreamableClientTransport` the dispatcher already uses, calling tool `context7.<tool>` on the agent-mcp bridge. |
| D-C7-7: manifest bump masked by `DirtyTreeHash` | CONFIRMED. `key_builder.go:42-45` hashes `git status --porcelain` (paths+status codes, not content). An already-dirty `package.json` bumped v22→v23 keeps the same porcelain line ` M package.json` → same hash → stale cache HIT. Also `CacheKey.Build(ctx, ".", "")` (`service.go:71`) passes empty `graphifyVersion`. | D-C7-7 stands → **DG-C7-2**: add manifest-content-hash as an 8th cache component. |
| Bootstrap fires "async post-InitService.Run inside runInitPhase" | `runInitPhase` (`service.go:689-744`) currently discards the `StructuralContext` (`_, env, err := s.d.Init.Run`). Async scheduling already exists via `Scheduler` (`service.go:55-63`, `AsyncScheduler` = `go work()`). | **DG-C7-5**: capture `sc`, fire bootstrap via injected `Scheduler` AFTER the phase is persisted+terminal, with a detached context + panic guard. |
| `AppliesWhen` is JSONB | CONFIRMED. `applies_when` is a JSONB column; `AppliesWhen` struct serialized whole (`skill_repo.go:140`). `version` is a separate TEXT column default `'v1'` (`010_skills_lifecycle.up.sql:17`). | **DG-C7-6**: `FrameworkMinVersion map[string]string` is an additive JSONB field — **no migration**. |
| `InsertIfAbsent` is idempotent | CONFIRMED. `ON CONFLICT (name, version) DO NOTHING` (`skill_repo.go:160`); `UNIQUE(name, version)` (`010:37`). | Concurrent bootstrap for same `(name,version)` is a safe no-op — DG-C7-7. |
| `FrameworkInfo.Version` shape | `stripSemverPrefix` only trims leading `^~>=<` (`parser_node.go:113-117`) → `"22.0.0"`, NOT a parsed semver. Go is `LanguageInfo.VersionEvidence = "go 1.26"` (`parser_go.go:28`), a raw string. | **DG-C7-9**: normalization (major extraction) lives in a domain helper `skill/semver.go`; tolerates both shapes. |

---

## Design decisions

### DG-C7-1 — PR1: Context7 as a second `[[mcp_providers]]` block (agent-mcp, TOML only)
**Problem.** Orch must reach Context7's two tools; agent-mcp already owns the proxy machinery.
**Options.** (a) new provider block reusing M4 proxy; (b) bespoke Context7 client in agent-mcp.
**Decision.** (a). Add a block to `configs/example.toml` mirroring the graphify block shape
(`example.toml:229-247`):
```toml
[[mcp_providers]]
id                = "context7"
command           = "npx -y @upstash/context7-mcp@latest"
transport         = "stdio"
lifecycle         = "persistent"
startup_timeout_s = 20            # Context7 ~15s latency; > graphify's 10
tools_allowed     = ["resolve-library-id", "get-library-docs"]

[mcp_providers.env]
CONTEXT7_API_KEY = "${CONTEXT7_API_KEY}"
```
**Rationale.** `MCPProviderConfig` already has `ID`, `Command`, `Transport`, `ToolsAllowed`,
`StartupTimeoutS`, `Env` (`config.go:184-225`). `wire.go:273-313` loops ALL `cfg.MCPProviders`,
builds the `AllowlistEnforcer` from them, and the `callerFactory` forwards `provCfg.Env` to
`mcpclient.New` (`wire.go:284-301`). `server.go:308-334` registers each allowed tool as
`<providerID>.<tool>` → `context7.resolve-library-id`, `context7.get-library-docs`. **Zero proxy
code changes** confirmed.
**Evidence.** `wire.go:273-313`, `server.go:308-334`, `config.go:184-225`, `example.toml:229-250`.

### DG-C7-2 — PR2: manifest-content-hash as the 8th cache key component
**Problem.** D-C7-7: an uncommitted manifest bump can be masked by `DirtyTreeHash`, serving a stale
`StructuralContext` (and thus a stale `FrameworkInfo.Version` to drift detection).
**Options.** (a) fold manifest bytes into `DirtyTreeHash`; (b) add a distinct 8th component
`ManifestHash`.
**Decision.** (b) — a distinct component, computed in `KeyBuilder.Build`. Hash the **content bytes**
of the detected manifest set in a deterministic order; for absent files, fold a fixed sentinel so a
file appearing/disappearing also changes the key:
```
ManifestHash = sha256(
   for each name in SORTED ["go.mod","package.json","pyproject.toml","requirements.txt","Cargo.toml","build.gradle","pom.xml"]:
       write(name) ; write(0x00)
       if exists: write(bytes) else: write("\x01<absent>")
       write(0x00)
)[:16 hex]
```
**Rationale.** A distinct component keeps `DirtyTreeHash` semantics intact (commit/working-tree
status) and makes the manifest invalidation auditable on its own. The absent-file sentinel handles
"package.json deleted" without a false cache hit. One-time global cache rebuild on deploy is
accepted (same class as the `SophiaDetectorVer` bump). The manifest set MUST equal the set
`Detector.Detect` reads (`detector.go:34-119`) so the cache reflects exactly the detection inputs.
**Evidence.** `key_builder.go:33-66`, `cache/key.go:30-66`, `detector.go:34-119`.

> Threading note: `Build` runs with `repoRoot="."` (`service.go:71`). `KeyBuilder` already holds a
> `FileReader` (`OSFileReader`, `key_builder.go:99-108`) — reuse `reader.ReadIfExists` for manifest
> bytes; no new dependency. Update the `cache.CacheKey` struct + `Hash()` to carry the 8th field for
> the standalone hasher, and `computeCacheKeyHash` in `key_builder.go` (both code paths must agree).

### DG-C7-3 — PR2: `StructuralContext.Greenfield bool` set deterministically in the detector
**Problem.** Need a deterministic greenfield signal that INIT persists.
**Decision.** Add `Greenfield bool \`json:"greenfield,omitempty"\`` to `structural.StructuralContext`
(after the existing fields, `context.go:77`). The detector sets it as the **last step** of
`Detect`: `sc.Greenfield = len(sc.Frameworks)==0 && len(sc.Languages)==0`. Bump
`detector.SophiaDetectorVer` `"v1.0.0"` → `"v1.1.0"` (`types.go:17`) — this invalidates all caches
(component 7), which is correct because detector OUTPUT changed.
**Rationale.** `omitempty` keeps JSON additive (no `SchemaVersion` bump; `SchemaV1` stays 1 —
verified additive at `context.go:14`, `service.go:81`). The deterministic form (counts==0) is
chosen over the "matcher returned zero skills" form because INIT must stay pure (D11) and must not
call the matcher.
**Evidence.** `context.go:29-78`, `detector.go:27-124`, `types.go:17`, `service.go:81`.

### DG-C7-4 — PR3: `AppliesWhen.FrameworkMinVersion map[string]string` (JSONB, additive)
**Problem.** Drift detection needs the active skill's targeted version, comparable to the detected
version.
**Decision.** Add `FrameworkMinVersion map[string]string \`json:"framework_min_version,omitempty"\``
to `AppliesWhen` (`lifecycle.go:117-129`), keyed by lowercased framework name → min version string
(e.g. `{"angular":"22"}`). Nil/empty = unconstrained (matches all). No migration: it serializes into
the existing `applies_when` JSONB whole-struct (`skill_repo.go:140`).
**Rationale.** Map form (not a flat field) supports multi-framework skills and keeps backward compat:
old rows deserialize with a nil map. The importer sets it on every imported skill so future INITs can
detect drift against it.
**Evidence.** `lifecycle.go:117-129`, `skill_repo.go:140`, `010:17` (version TEXT col).

### DG-C7-5 — PR2: async bootstrap fire inside `runInitPhase`, post-persist, panic-guarded
**Problem.** Bootstrap must run after INIT completes, never block/fail the phase, survive a panic.
**Decision.** In `runInitPhase` (`service.go:689`), capture `sc` (today discarded), and AFTER the
phase is completed+persisted+advanced (after `service.go:728`) schedule the bootstrap via the
injected `Scheduler`:
```go
sc, env, err := s.d.Init.Run(ctx, c)   // sc captured (was _)
// ... existing complete/persist/advance unchanged ...
if s.d.Bootstrap != nil {
    bgCtx := traceBackground(ctx)       // detach from request ctx (service.go:315 pattern)
    s.d.Scheduler(func() {
        defer func() {                  // panic isolation — must not kill the runner
            if r := recover(); r != nil {
                s.d.Logger.Error("bootstrap panic recovered", "panic", r)
            }
        }()
        cctx, cancel := context.WithTimeout(bgCtx, s.d.BootstrapTimeout) // default 60s
        defer cancel()
        s.d.Bootstrap.TriggerIfNeeded(cctx, sc)
    })
}
```
`Bootstrap` is a new optional dep on `phase.Service.Deps` (nil → no-op, preserves all existing tests).
**Rationale.** Fires AFTER caller-visible state change is durable (Iron Law D1.2 already satisfied by
the existing persist at `service.go:718`; bootstrap is a side effect that follows, never precedes).
`Scheduler` indirection makes it synchronous in tests (`SyncScheduler`). The detached context +
timeout mirrors the dispatch path (`service.go:315-321`). Panic guard is mandatory:
`AsyncScheduler` runs a bare `go work()` (`service.go:60`) so an unrecovered panic would crash the
process.
**Evidence.** `service.go:55-63`, `service.go:315-322`, `service.go:689-744`.

### DG-C7-6 — PR3: `BootstrapTriggerService` placement, deps, idempotency, rate guard
**Problem.** Where the greenfield/drift gate + Context7 calls + import live; concurrency; quota.
**Decision.** New package `internal/application/bootstrap`. `Service.TriggerIfNeeded(ctx, sc)`:
1. **Key guard.** If `DocsProvider` is unconfigured (missing key surfaced as `ErrDocsUnavailable`)
   → log WARN, return (degraded-first, DG-C7-3 graphify mirror).
2. **Rate guard.** `RateGuard.Allow(ctx, sc.ProjectID)` — see below. Deny → WARN, return.
3. **Greenfield branch.** `sc.Greenfield == true` → for each intended stack target (V1: the first
   detected framework, or the human-chosen stack recorded post-PROPOSE) call the import flow.
4. **Drift branch.** For each `sc.Frameworks[i]`: look up the active skill `stack/<fw>-<major>` via a
   `SkillLookup` port; compare detected major vs `AppliesWhen.FrameworkMinVersion[fw]` major using
   `skill.MajorOf` (DG-C7-9). detected > active → import the new version.
5. Import via `SkillImporter.ImportFromDocs(...)` → `InsertIfAbsent` (idempotent).
Each Context7/import failure is logged and **discarded** — never propagated to the goroutine.

**Deps (constructor-injected):**
```go
type Deps struct {
    Docs       DocsProvider   // outbound port (DG-C7-8)
    Skills     SkillLookup    // active-skill lookup (drift) — reuses SkillRepo
    Importer   *SkillImporter // wraps SkillRepo.InsertIfAbsent
    Rate       RateGuard
    Clock      shared.Clock   // no time.Now() in application (CLAUDE.md D5)
    IDGen      shared.IDGenerator
    Logger     *slog.Logger
    MinSnippets int           // D-C7-6 threshold, default 50
}
```
**Idempotency/concurrency.** Two changes on the same project racing the SAME `(name,version)` →
`InsertIfAbsent`'s `ON CONFLICT DO NOTHING` makes the loser a no-op (DG-C7-7). No per-project
singleflight is needed for correctness; it is OUT of scope (a quota optimization, not a correctness
requirement) — the rate guard already bounds duplicate Context7 calls.
**Rate guard — in-memory, per-process (V1).** `RateGuard` interface; default impl
`MemoryRateGuard{max int, window time.Duration, clock shared.Clock}` keyed by projectID with a
mutex-guarded sliding counter. **Decision: in-memory, NOT a DB table** — the guard's job is to stop a
CI/CD loop from exhausting the free tier within one process lifetime; a DB-backed cross-process
counter is a V2 concern (documented as a known limitation: a fleet of orch processes each gets its
own budget). Default `max=5/project/24h`, configurable.
**Rationale.** Keeps INIT pure (D11): the service is separate, reads `sc`, never re-enters the
matcher. In-memory guard avoids a migration and a memory-engine round-trip on a non-hot path.
**Evidence.** `skill_repo.go:135-181`, `lifecycle.go:81-98` (SourceImported), explore §5 Option A.

### DG-C7-7 — PR3: naming `stack/<framework>-<major>`; drift = new `(name,version)` row
**Decision.** Name = `"stack/" + lower(framework) + "-" + major` (e.g. `stack/angular-22`,
`stack/go-1.26`). The skill `version` column (TEXT, default `'v1'`) is set to the **full detected
version** (e.g. `"22.0.0"`) so drift to v23 yields a distinct `(name,version)` row
(`stack/angular-23`, `"23.x"`); the old row stays `active` until governance promotes the new
`candidate`.
**Rationale.** `stack/` prefix namespaces imported skills away from seed/evidence skills. Name
encodes the major (stable for `InsertIfAbsent`); version encodes the full string (lets two patch/minor
imports of the same major coexist if ever needed, and feeds promotion provenance).
**Evidence.** `010:37` UNIQUE(name,version); `skill_repo.go:160` ON CONFLICT.

### DG-C7-8 — PR3: orch→Context7 transport = NEW outbound `DocsProvider` port + streamable-HTTP adapter (**CORRECTION of D-C7-8**)
**Problem.** D-C7-8 says reuse "the existing MCP dispatcher port the phases use." Verified false:
the only MCP path is `AgentDispatcher.Dispatch` → `agent.run`/`agent.health` (`dispatcher.go:174,213`).
It cannot call `context7.*`. There is no generic `CallTool` in orch (grep clean).
**Options considered.**
- (A) Extend `AgentDispatcher` with a generic `CallTool` — REJECTED: pollutes a phase-dispatch
  contract with a docs concern; `DispatchRequest`/`DispatchResult` are envelope-shaped, wrong type.
- (B) New orch outbound port `DocsProvider` + a thin adapter that reuses the SAME go-sdk
  `StreamableClientTransport` + `authRoundTripper` the dispatcher already uses (`dispatcher.go:145-162`),
  but calls tools `context7.resolve-library-id` / `context7.get-library-docs` on the agent-mcp bridge —
  ACCEPTED.
- (C) orch-side stdio MCP client spawning Context7 directly (bypass agent-mcp) — REJECTED: duplicates
  the allowlist/lifecycle agent-mcp already owns; violates "agent-mcp is the single external-MCP edge."
- (D) bootstrap as a dispatched routine (opencode calls the proxied tool) — REJECTED: re-introduces an
  LLM into bootstrap, violating D11/D-C7-2 (deterministic, no-LLM importer).
**Decision.** (B). Define the port in `internal/ports/outbound/docs.go`; implement in
`internal/adapters/outbound/docs/context7/`. The adapter reuses the bridge URL/token/origin config the
MCP dispatcher already loads, and the per-call session pattern (`Connect → CallTool → Close`,
`dispatcher.go:305-310`). Missing key / disabled provider → `ErrDocsUnavailable` (degraded-first).
**Rationale.** Reuses the real transport (agent-mcp bridge over Streamable HTTP) WITHOUT abusing the
dispatcher contract. agent-mcp stays the single external-MCP edge; the allowlist (`tools_allowed`,
DG-C7-1) still gates the two tools. This is the minimum correct mechanism.
**Evidence.** `dispatcher.go:145-162,194-262,305-344`; no `CallTool` outside it (grep clean);
`server.go:308-334` registers `context7.*` proxied tools on the bridge.

### DG-C7-9 — PR3: version normalization in a domain helper; optional matcher gate
**Problem.** `FrameworkInfo.Version` is `"22.0.0"` (Node) or absent; Go is `"go 1.26"` raw. Drift
needs a robust "major" extraction. Where does semver parsing live — domain or adapter?
**Decision.** A pure domain helper `internal/domain/skill/semver.go`:
```go
func MajorOf(version string) (int, bool)   // "22.0.0"→22; "go 1.26"→1; "^18"→18; ""→(_,false)
func DriftsForward(detected, activeMin string) bool // major(detected) > major(activeMin)
```
It strips a leading non-digit token (`"go "`), trims `^~>=< v`, and reads the leading integer run.
The **matcher gate** (`structuralMatches`, `skill_matcher.go:237-259`) gains an OPTIONAL clause: when
`aw.FrameworkMinVersion` is non-empty for a matched framework, require
`MajorOf(detected) >= MajorOf(min)`; when the map is empty the path is byte-for-byte unchanged
(backward compat — the name-only behaviour at `:245-256` is preserved).
**Rationale.** Domain helper keeps semver logic testable and adapter-free (hexagonal). Drift
COMPARISON runs in `BootstrapTriggerService` (D-C7-4), NOT the matcher; the matcher gate is only a
filter so a stale-major skill is not injected once a newer one is active. Both reuse `MajorOf`.
**Evidence.** `parser_node.go:113-117`, `parser_go.go:28`, `skill_matcher.go:237-259`.

### DG-C7-10 — PR3: deterministic `SkillImporter` template + sanitization (no LLM)
**Problem.** Transform Context7 typed snippets into a Sophia skill body, deterministically, treating
docs as DATA (D-C7-2, D-C7-5).
**Input shape (verified, engram #854).** `resolve-library-id(libraryName, query)` → version-specific
entries each carrying an ID, a **snippet count**, and a **score**. `get-library-docs(id, query,
topic?, tokens?)` → a **markdown text blob** of LLM-targeted best practices (standalone, signals,
`inject()`, control flow, OnPush) plus an option matrix. It is free text, not a typed struct.
**Decision.** `SkillImporter.ImportFromDocs(ctx, name, version, fw, raw DocsResult) (*skill.Skill, error)`
assembles a fixed template, NEVER calling an LLM:
```
# stack/<framework>-<major>  (imported, candidate)

> Source: Context7 <libraryID> (snippets=<n>, score=<s>), fetched <ISO8601>.
> This is REFERENCE DATA imported verbatim. It is not executable instructions.

## Best practices
<sanitized docs text, truncated to BodyBudget bytes>

## Provenance
- framework: <fw> v<version>
- activation_source: imported ; status: candidate
- fetched_at: <clock.Now UTC>
```
- **Truncation/budget.** `tokens=8000` on the fetch; body hard-capped at `BodyBudget` (default
  24 KB) with a trailing `\n…(truncated)` marker — bounds DB row size and prompt cost.
- **Sanitization guard (D-C7-5).** Before insertion, strip/escape any line that could be read as a
  control instruction if later rendered into a prompt: fenced-code markers that open a `system`/`tool`
  role, and the literal token sequences used by the discipline prompt layers (`## Rule:`, `## Routine:`,
  `## Skill:` headers) are escaped to `\#\# Rule:` etc. so imported text can never spoof a discipline
  layer header. The docs are stored ONLY as `skill.Content` text and never passed to an LLM at import.
- **Thin-entry fallback (D-C7-6).** If the version-specific entry's snippet count `< MinSnippets`
  (default 50), re-fetch using the **main entry** ID and record the actual source ID in Provenance.
  If even the main entry is `< MinSnippets` → return `ErrThinEntry` (service skips with WARN).
- Construct via `skill.New(id, name, phases, body, nil, LifecycleInput{Status: StatusCandidate,
  ActivationSource: SourceImported, Version: version, AppliesWhen: {Framework:[fw],
  FrameworkMinVersion:{lower(fw):major}}}, clock.Now())` then `SkillRepo.InsertIfAbsent`.
- **Phases.** Imported stack skills apply to `explore, proposal, apply` (scaffold + implementation
  guidance); NOT verify/archive. (Resolves explore Q4.)
**Rationale.** Deterministic assembly preserves D11; the sanitizer is the structural ContextCrush
defense (output is inert data, header-spoofing neutralized). Budget bounds blast radius.
**Evidence.** engram #854 (input shape), `skill.go:59-98` (New), `lifecycle.go:152-160`
(LifecycleInput), `skill_repo.go:135-181` (InsertIfAbsent).

---

## Component diagram

```
                       PR2                                  PR3
  ┌─────────────────────────────┐        ┌──────────────────────────────────────────┐
  │ phase.Service.runInitPhase  │        │ application/bootstrap                       │
  │  Init.Run → sc (captured)   │        │  Service.TriggerIfNeeded(ctx, sc)           │
  │  complete+persist+advance   │        │   ├─ key guard (ErrDocsUnavailable→WARN)    │
  │  Scheduler(go {             │  sc    │   ├─ RateGuard.Allow(projectID)             │
  │    recover(); WithTimeout;  ├───────►│   ├─ greenfield? → import flow              │
  │    Bootstrap.TriggerIfNeeded│        │   └─ drift? (MajorOf detected>active)→import│
  │  })                         │        │        │                                    │
  └─────────────────────────────┘        │        ▼                                    │
                                          │  SkillImporter.ImportFromDocs (no LLM)      │
                                          │   ├─ DocsProvider.ResolveLibrary (thin?)    │
   ┌─────────── orch outbound ────────┐   │   ├─ DocsProvider.GetDocs (DATA)            │
   │ ports/outbound/docs.go           │◄──┤   ├─ sanitize + template + truncate         │
   │ DocsProvider (DG-C7-8)           │   │   └─ skill.New(candidate,imported)          │
   └──────────────┬───────────────────┘   │            │ InsertIfAbsent (idempotent)    │
                  │ streamable-HTTP        └────────────┼────────────────────────────────┘
                  │ (reuses dispatcher's                ▼
                  │  transport+auth)              SkillRepo (PG, ON CONFLICT DO NOTHING)
                  ▼
   adapters/outbound/docs/context7  ──MCP──►  agent-mcp bridge
                                              context7.resolve-library-id / get-library-docs
                                              (proxy → stdio npx @upstash/context7-mcp)   ← PR1
```

---

## Port / interface signatures (Go)

```go
// internal/ports/outbound/docs.go  (PR3)
package outbound

import "context"

// ErrDocsUnavailable signals the docs provider is not configured/usable
// (missing CONTEXT7_API_KEY, provider disabled). Callers degrade with WARN.
var ErrDocsUnavailable = errors.New("docs: provider unavailable")

// ErrThinEntry signals every candidate entry is below the snippet threshold.
var ErrThinEntry = errors.New("docs: entry below snippet threshold")

type LibraryEntry struct {
    ID       string // context7-compatible library id, e.g. "/websites/angular_dev"
    Snippets int
    Score    float64
    IsMain   bool   // true for the framework's main (non-version-pinned) entry
}

type DocsResult struct {
    LibraryID string
    Snippets  int
    Score     float64
    Body      string // raw markdown — treated as DATA, never instructions
}

// DocsProvider is the orchestrator's outbound port to an external docs source
// (V1: Context7 via the agent-mcp bridge). Implementations MUST be safe for
// concurrent use.
type DocsProvider interface {
    // ResolveLibrary returns candidate entries ranked by relevance for a
    // framework name. Returns ErrDocsUnavailable when unconfigured.
    ResolveLibrary(ctx context.Context, framework, query string) ([]LibraryEntry, error)
    // GetDocs fetches the docs body for a resolved library id.
    GetDocs(ctx context.Context, libraryID, query, topic string, tokens int) (DocsResult, error)
}
```

```go
// internal/application/bootstrap/ports.go  (PR3)
package bootstrap

// SkillLookup finds the currently-active skill for a stack name (drift compare).
type SkillLookup interface {
    ActiveByName(ctx context.Context, name string) (*skill.Skill, bool, error)
}

// RateGuard bounds bootstrap calls per project. Allow returns false when the
// project has exhausted its window budget.
type RateGuard interface {
    Allow(ctx context.Context, projectID string) bool
}

type Service struct{ d Deps }
func (s *Service) TriggerIfNeeded(ctx context.Context, sc structural.StructuralContext)

type SkillImporter struct { repo SkillRepoInserter; clock shared.Clock; idgen shared.IDGenerator; budget int }
func (i *SkillImporter) ImportFromDocs(ctx context.Context, name, version, fw string, r outbound.DocsResult) (*skill.Skill, error)
```

```go
// internal/domain/skill/semver.go  (PR3 — pure domain)
func MajorOf(version string) (int, bool)
func DriftsForward(detected, activeMin string) bool

// internal/domain/skill/lifecycle.go  (PR3 — additive JSONB field)
type AppliesWhen struct {
    // ... existing fields ...
    FrameworkMinVersion map[string]string `json:"framework_min_version,omitempty"`
}

// internal/domain/structural/context.go  (PR2 — additive JSON field)
type StructuralContext struct {
    // ... existing fields ...
    Greenfield bool `json:"greenfield,omitempty"`
}
```

---

## Data flow

### Greenfield (PR2 fire + PR3 import)
```
INIT detects stack → sc.Greenfield=true → runInitPhase persists+advances phase
  → Scheduler(go, recover, 60s timeout): Bootstrap.TriggerIfNeeded(sc)
    → key ok? rate ok? greenfield branch
      → DocsProvider.ResolveLibrary("Angular","best practices")  [via agent-mcp bridge]
        → pick best entry; if version entry snippets<50 → use main entry (record id)
      → DocsProvider.GetDocs(id, "best practices", topic, tokens=8000)
      → SkillImporter: sanitize+template+truncate → skill.New(stack/angular-22,
            "22.0.0", candidate, imported, FrameworkMinVersion{angular:22})
      → SkillRepo.InsertIfAbsent  (no-op if already imported)
```

### Drift (PR3, on a subsequent INIT after manual bump)
```
operator bumps package.json v22→v23 (even uncommitted)
  → next INIT: ManifestHash component changes (DG-C7-2) → cache MISS → re-detect
  → sc.Frameworks[Angular].Version="23.0.0", sc.Greenfield=false
  → Bootstrap.TriggerIfNeeded: drift branch
    → Skills.ActiveByName("stack/angular-22") → active, FrameworkMinVersion{angular:22}
    → DriftsForward("23.0.0","22") == true
    → import stack/angular-23 (candidate); stack/angular-22 stays active until governance
```

---

## Error / degraded taxonomy

| Condition | Detection | Behaviour |
|---|---|---|
| `CONTEXT7_API_KEY` absent / provider disabled | `DocsProvider` returns `ErrDocsUnavailable` | WARN, return; INIT unaffected; agent-mcp still serves graphify (DG-C7-1) |
| Rate budget exhausted | `RateGuard.Allow == false` | WARN, return (no Context7 call) |
| Context7 transport/timeout error | adapter wraps error | logged + discarded in goroutine; INIT already terminal |
| Version entry thin (`<MinSnippets`) | snippet count from `ResolveLibrary` | fall back to main entry (DG-C7-10) |
| All entries thin | `ErrThinEntry` | skip with WARN |
| `InsertIfAbsent` conflict (concurrent / re-run) | `ON CONFLICT DO NOTHING` | silent no-op (idempotent) |
| Panic inside bootstrap goroutine | `recover()` (DG-C7-5) | logged; phase runner unaffected |
| docs contain spoofed discipline headers | importer sanitizer (DG-C7-10) | headers escaped; stored as inert text |

---

## Test strategy

Strict TDD ACTIVE — tests first, no Standard Mode fallback. Test command: `go test ./...` (1.26.2).

| Layer | What | Approach |
|---|---|---|
| Unit (PR2) | manifest-hash invalidation: dirty `package.json` v22→v23 with UNCHANGED porcelain → DIFFERENT key (the D-C7-7 acceptance test) | fake `GitRunner` returns identical porcelain; fake `FileReader` returns different manifest bytes; assert `Build` keys differ. **Write FIRST.** |
| Unit (PR2) | `Greenfield` true iff no frameworks AND no languages; false otherwise | table-driven on `Detector.Detect` with testdata fixtures |
| Unit (PR2) | `Greenfield` is omitempty additive; `SophiaDetectorVer=="v1.1.0"`; SchemaV1 unchanged | marshal assertion + constant check |
| Unit (PR2) | bootstrap fires post-persist; nil `Bootstrap` dep → no-op; panic in bootstrap recovered, phase still terminal | `SyncScheduler` + fake `Bootstrap` that panics; assert phase saved + no crash |
| Unit (PR3) | `MajorOf` / `DriftsForward`: "22.0.0", "go 1.26", "^18", "", "v3.2" | table-driven, pure |
| Unit (PR3) | `structuralMatches` gate: empty `FrameworkMinVersion` → name-only path byte-identical (backward compat); set + detected major ≥ min → pass; < min → SkipReasonStructuralMismatch | table-driven |
| Unit (PR3) | `SkillImporter`: deterministic body (golden); NO LLM dependency (no LLM port in `Deps` — compile-time guarantee + assert via fake); docs stored as content only; header-spoof sanitized (golden) | golden file + fake `SkillRepoInserter` |
| Unit (PR3) | thin-entry fallback to main; all-thin → `ErrThinEntry` | fake `DocsProvider` with controllable snippet counts |
| Unit (PR3) | `TriggerIfNeeded`: missing key→WARN; rate deny→no call; greenfield→1 import; drift→new version row; non-drift→no import; import error discarded | fake `DocsProvider`/`SkillLookup`/`RateGuard` |
| Unit (PR3) | `MemoryRateGuard`: sliding window per project; injected `Clock` advances window | pure, fake clock |
| Integration (PR3) | `InsertIfAbsent` candidate `(name,version)` row; second insert no-op; old version stays active | **testcontainers** Postgres (matches repo `testcontainers-go`) |
| Integration (PR3) | `context7` `DocsProvider` adapter round-trip | skip-guarded (`testing.Short`/env) against a fake MCP server via `mcp.NewInMemoryTransports` — NO real Context7 in CI |
| Integration (PR1) | `context7.*` callable through agent-mcp proxy; missing key degrades but graphify still served | agent-mcp repo; skip-guarded real `npx`, or fake stdio caller via `WithCallerFactory` |

**testcontainers** is used ONLY for the PG `InsertIfAbsent`/drift-row integration test. All MCP/Docs
paths use SDK in-memory transports or injected fakes. No real Context7 or real `npx` in CI default.

---

## Migration / rollout

- **No DB migration.** `FrameworkMinVersion` rides existing `applies_when` JSONB; `Greenfield` rides
  the cached/persisted `StructuralContext` JSON (omitempty).
- **Cache rebuild (one-time):** `SophiaDetectorVer` v1.0.0→v1.1.0 invalidates all INIT caches on
  deploy (DG-C7-3); the new `ManifestHash` component (DG-C7-2) also shifts every key — single
  acceptable one-time rebuild.
- **Rollback:** PR1 revert → single-provider proxy (graphify only); PR2 revert → `Greenfield` drops
  (omitempty, no migration), detector ver reverts, bootstrap fire removed; PR3 revert → service +
  importer + `DocsProvider` port + `FrameworkMinVersion` + matcher gate + semver helper removed
  (name-only matching restored). Any already-imported `candidate` rows are inert (never promoted
  without governance) — no orphan cleanup needed.

---

## Proposal corrections

### DG-C7-8 supersedes D-C7-8 — transport mechanism (CRITICAL)
**D-C7-8 claim:** "Orch → Context7 via the existing MCP dispatcher port, not a new outbound HTTP port…
the same path the phases already use to reach agent-mcp tools."
**Reality (verified):** The orchestrator has exactly ONE MCP client (`dispatcher/mcp/dispatcher.go`),
exposed only as `AgentDispatcher.Dispatch`, which calls **only** `agent.run` (`dispatcher.go:213`) and
`agent.health` (`dispatcher.go:174`). There is no generic `CallTool(provider.tool)` anywhere in orch
`internal/` (grep clean). The proxied-tool registration on agent-mcp (`server.go:308-334`) exposes
`context7.*` for the **dispatched opencode agent** to call over MCP — it does NOT give the
orchestrator process a way to invoke a proxied tool without dispatching an LLM agent. Therefore
"reuse the existing dispatcher port" is unimplementable: that port's contract is envelope-shaped
phase dispatch, not arbitrary tool calls.
**Correction (DG-C7-8):** add a **new orchestrator outbound port `DocsProvider`** plus a thin
adapter `adapters/outbound/docs/context7` that REUSES the dispatcher's transport machinery (go-sdk
`StreamableClientTransport` + `authRoundTripper` + per-call `Connect→CallTool→Close`,
`dispatcher.go:145-162,305-344`) to call `context7.resolve-library-id` / `context7.get-library-docs`
on the **same agent-mcp bridge**. This honours D-C7-8's INTENT (agent-mcp is the single external-MCP
edge; the `tools_allowed` allowlist still gates the two tools; no second transport stack) while being
actually buildable. It does NOT add a raw HTTP port and does NOT spawn Context7 from orch directly.

### Clarifications (not corrections — proposal stands, refined)
- **D-C7-7 (cache key):** verified exactly correct; the porcelain-masking bug is real. Refined to a
  distinct 8th component with an absent-file sentinel and the manifest set pinned to the detector's
  read set (DG-C7-2).
- **Rate guard placement:** proposal left "DB table vs in-memory" open; decided **in-memory
  per-process** for V1 with a documented cross-process limitation (DG-C7-6).
- **Singleflight:** proposal risk row implies `InsertIfAbsent` suffices for concurrency; confirmed —
  per-project singleflight is explicitly OUT (correctness handled by `ON CONFLICT`, DG-C7-6/7).
- **Imported-skill phases:** explore Q4 left open; decided `explore, proposal, apply` (DG-C7-10).
- **Version major extraction:** pinned to a pure domain helper handling both `"22.0.0"` and
  `"go 1.26"` shapes (DG-C7-9), since `FrameworkInfo.Version` is NOT a parsed semver
  (`parser_node.go:113-117`).

No other proposal decisions diverge. D-C7-1/2/3/4/5/6/9 are implementable as written.
