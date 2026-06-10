# Design: init-structural-detection-graphify (M-KNOW-INIT-0)

**Strategy ref**: V4.1 §7-bis (INIT anti-pattern) + §7-ter (Graphify hybrid integration) + §16 M-KNOW-INIT-0.
**Proposal**: `openspec/changes/init-structural-detection-graphify/proposal.md` (read first).
**Exploration**: `openspec/changes/init-structural-detection-graphify/explore.md` (read first).
**Graphify audit**: `docs/research/graphify-audit.md`.
**Mode**: SDD design. NO production code in this artifact. Architecture only.
**Strict TDD**: every interface listed here gets a fake impl in tests; no real subprocess or HTTP in unit tests.

---

## Approach

Two independent PRs land M-KNOW-INIT-0. **PR1 (sophia-cli, ~150 LoC)** introduces a `GraphifyProber` outbound port so the `Initializer` can detect Python 3.10+ and `graphify --version` after writing `.sophia.yaml`; an opt-in `--auto-bootstrap-graphify` flag installs `graphifyy[mcp]==0.8.35` via `uv tool install`. **PR2 (sophia-orchestator, ~350 LoC)** plugs deterministic execution into the pre-existing `PhaseInit` constant (`internal/domain/phase/type.go:12`, `ConfidenceThreshold = 0.0`) via a new `InitService`: an INIT branch sits at the top of `runAsync()` and short-circuits BEFORE governance/Iron-Law/dispatch (mirroring the existing apply-executor branch at `service.go:324`). `InitService` runs the new `internal/application/init/detector/` package (pure Go FS reads — manifests, frameworks, arch heuristics) in parallel with `GraphifySpawner.Build()` (Pattern B: `exec.Command("graphify", "update", ...)` then parse `graphify-out/graph.json`), merges into a `StructuralContext` carrying `SchemaVersion = 1`, persists to BOTH memory-engine (via the existing `outbound.MemoryClient.Ingest`, `topic_key=sdd/<change_name>/init`, idempotent via migration 004 partial unique index) AND a local file at `<repo_root>/.sophia/cache/structural/<cache_key>.json` (atomic write). A 24h TTL local cache keyed on a 6-component sha256 (graphify_version + repo_root + git_head + dirty_tree_hash + include_globs + config_hash) gates the spawn path so cache-hits stay <5s. Degraded-first: if `graphify` is absent, `graph_available=false` and `degraded_reason` is populated; INIT still detects + persists. All subprocess and HTTP calls live behind Go interfaces (`GraphifyProber`, `GraphifySpawner`, `StructuralPersister`, `CacheStore`, `SophiaDetector`) injected through `Deps`; unit tests use fakes, integration tests are guarded with `if !haveGraphify() { t.Skip() }`. Iron Law D1.2 is honored: `InitService.Run` returns a `StructuralContext` AND a synthetic `*envelope.Envelope`; `runInitPhase` persists artifacts to memory-engine, then constructs the envelope, then calls `p.Complete(env, clock.Now())` + `PhaseRepo.Save(ctx, p)` — envelope-before-state, identical contract to LLM phases.

---

## Decisions

### D-INIT-1: Pattern B (CLI per-query) for INIT-0

- **What**: `graphify update` runs once via `exec.Command` behind `GraphifySpawner`; INIT then reads `graphify-out/graph.json` directly. No sidecar `graphify serve`, no Go MCP stdio client.
- **Why**: INIT-0 only needs the static graph summary (nodes/edges/god_nodes/communities). No live tool queries (`query_graph`, `get_community`) are needed in INIT — those are EXPLORE/APPLY/VERIFY territory (deferred milestone).
- **Tradeoff**: cold-start cost (~1-2s per `graphify update`) on cache miss. Mitigated by 24h TTL local cache; second execution on same change/HEAD is a cache hit (<5s).
- **Alternatives rejected**:
  - Pattern A (sidecar serve + Go MCP stdio client): adds process supervision, signal handling, hot-reload watcher, and depends on go-sdk client mode which is unverified (explore §13 #2). Deferred to LLM-phase milestone.
  - No graphify at all: loses cross-language structural ground truth (per V4.1 §7-ter motivation); INIT-0 would collapse to manifest detection only.
- **Locked**: operator decision 2 (proposal §Open Questions).

### D-INIT-2: Dual persistence (memory-engine + local file)

- **What**: `StructuralPersister` writes the `StructuralContext` to memory-engine via `outbound.MemoryClient.Ingest` (`type="sdd_init"`, `topic_key="sdd/<change_name>/init"`) AND to `<repo_root>/.sophia/cache/structural/<cache_key>.json` atomically.
- **Why**: cross-session retrievability (memory-engine survives across runs and is searchable via FTS) + fast local re-runs (file cache avoids network round-trip when cache key matches).
- **Idempotency**: memory-engine migration 004 partial unique index on `topic_key` for active rows handles upsert; re-running INIT on the same change_name overwrites the prior record cleanly.
- **Alternatives rejected**: single source (operator chose both per explore §9).
- **Locked**: operator decision 3.

### D-INIT-3: PhaseInit branch at TOP of `runAsync()`, BEFORE governance/Iron-Law/dispatch

- **What**: in `internal/application/phase/service.go`, the existing `runAsync(ctx, c, p, in)` (line 271) gets a new top-of-function branch:
  ```go
  if p.Type() == phase.PhaseInit {
      s.runInitPhase(ctx, c, p, in)
      return
  }
  ```
  placed BEFORE the existing apply-executor branch (line 324) — INIT must short-circuit before governance evaluates, before Iron Laws check, before any LLM prompt is built.
- **Why**:
  - `PhaseInit` already exists with `ConfidenceThreshold = 0.0` (`type.go:82`) and the codebase comment confirms intent: "init carries no agent envelope; threshold 0 means transition is unconditional." We are plugging deterministic execution INTO the pre-designed slot, not redesigning the lifecycle.
  - INIT must not be subject to governance approval gates (no LLM agent involved) or Iron Law prompt-building (no prompt to build).
  - Placing the branch in `runAsync` (not in `Run`) preserves the 202 Accepted + SSE response pattern — INIT still runs asynchronously and emits `phase.started`/`phase.completed` events via the same machinery (Sophia CLAUDE.md D1.5).
- **Implementation sketch**: `runInitPhase` mirrors the bottom half of `runAsync` (Steps 13-16) for envelope persistence, phase save, audit, advanceChange, terminal event emission — but skips Steps 4-12 (governance, Iron Law, prompt, session, dispatch, validate). The synthetic envelope is built from `InitService.Run`'s output.
- **Alternatives rejected**:
  - Branch inside the dispatcher: no — dispatcher is for LLM providers (opencode/aider/ollama/mcp), and INIT must never hit that path (proposal Iron-Law concern).
  - Skip the envelope entirely: violates Iron Law D1.2; every phase produces a validated envelope before any state change.
- **Locked**: operator decision 4.

### D-INIT-4: Detector location = `sophia-orchestator/internal/application/init/detector/`

- **What**: new package at `internal/application/init/detector/` for manifest parsing, framework fingerprint, arch-style heuristics.
- **Why**: single consumer (`InitService`); pure Go FS reads (no shell, no HTTP, no MCP); hexagonal-correct (application layer coordinates pure-domain work; no infrastructure dependency). Placing in `runtime-adapters` would add inter-process latency for what is a few `os.ReadFile` calls.
- **Alternatives rejected**: place in agent-mcp (inverts dependency), place in runtime-adapters (adds IPC overhead).
- **Locked**: operator decision 5.

### D-INIT-5: `StructuralContext.SchemaVersion = 1` from day 0

- **What**: every `StructuralContext` carries `SchemaVersion int` with constant `StructuralContextSchemaV1 = 1`. Consumers (future M0.5 `PriorContext`, M1 `SkillMatcher`) MUST check this field before deserialize.
- **Why**: explore §13 risk 6 identifies migration cost when `StructuralContext` shape evolves. Adding the version field on day 0 is cheap insurance; renaming/restructuring later without it is expensive.
- **Alternatives rejected**: defer version field until first migration is needed (rejected — retrofitting a version field requires re-reading every prior record).

### D-INIT-6: Degraded mode default = warn + continue

- **What**: if `GraphifySpawner.Build()` returns `ErrGraphifyNotAvailable` (or any non-zero exit / JSON parse failure), `InitService` sets `StructuralContext.GraphAvailable = false`, `DegradedReason = "<concrete cause>"`, omits `GraphSummary`, and continues. `SophiaDetector.Detect()` results are still merged + persisted.
- **Why**:
  - Dev machines may lack Python 3.10+ / uv / graphify; CI must not block on optional dependencies.
  - V4.1 §7-ter.7 mandates degraded-first.
- **Operator opt-in**: `--auto-bootstrap-graphify` flag on sophia-cli `init` (PR1) calls `GraphifyProber.Bootstrap()` which runs `uv tool install "graphifyy[mcp]==0.8.35"`. Default OFF.
- **Alternatives rejected**: fail INIT when graphify absent (blocks dev workflow); auto-install by default (security/footgun risk per V4.1).
- **Locked**: operator decision 10.

### D-INIT-7: Cache key = 6-component sha256 with null separator

- **What**: cache key = `sha256(graphify_version || \0 || repo_root || \0 || git_head || \0 || dirty_tree_hash || \0 || sorted(include_globs) || \0 || config_hash)`. TTL = 24h default (configurable).
- **Why**:
  - Null separator prevents ambiguity (e.g., `"go" + "mod"` vs `"go" + "" + "mod"`).
  - Sorted `include_globs` ensures determinism (map iteration order doesn't matter).
  - `dirty_tree_hash` = sha256 of `git status --porcelain` output captures uncommitted work.
  - `config_hash` = sha256 of `.graphify.yaml` content if present, else empty.
- **Locked**: V4.1 §7-ter.8 + operator decision 11.

### D-INIT-8: Memory-engine persistence via existing `outbound.MemoryClient.Ingest`

- **What**: `MemoryEnginePersister` adapter wraps the already-wired `outbound.MemoryClient`. The new port `StructuralPersister` is a thin orchestration layer (it ALSO writes the local file via `CacheStore.Write`); the HTTP transport is the existing memory-engine client. No new HTTP client, no new auth wiring.
- **Why**:
  - `internal/ports/outbound/memory.go:27-36` already exposes `Ingest(ctx, IngestMemoryInput) (*MemoryRecord, error)` against `POST /api/v1/memories`. Reusing it preserves tenant-scope handling (`SOPHIA_MEMORY_TENANT_ID`), retries, observability, and obs metrics that the existing path already has.
  - `IngestMemoryInput.TopicKey` is the upsert key per ADR-0003; supplying `sdd/<change_name>/init` matches the topic_key format already in use for other SDD artifacts (proposal/spec/design/tasks/apply/verify).
- **Concrete shape**:
  ```
  Type:        "sdd_init"
  Content:     <StructuralContext JSON>
  Summary:     "INIT structural detection for <change_name>"
  Tags:        ["sdd", "init", "structural_context", "schema_v1"]
  TopicKey:    "sdd/<change_name>/init"
  Scope:       MemoryScope{ProjectID: change.Project(), SessionID: change.ID().String(), AgentID: "sophia-orchestator"}
  Provenance:  MemoryProvenance{Source: "sophia-orchestator", Method: "sdd-phase-output"}
  ```
- **Alternatives rejected**:
  - Introduce a new HTTP client just for structural persistence: duplicates retry/auth/observability code; violates Sophia CLAUDE.md "store memory via outbound port" (point 2 of Never-do list).

### D-INIT-9: Local cache path = `<repo_root>/.sophia/cache/structural/<cache_key>.json`

- **What**: `FileCache` writes atomically via temp-file + `os.Rename`. Reads check `DetectedAt + TTL` against `clock.Now()` before returning a hit.
- **Why**:
  - `<repo_root>/.sophia/` is already used by sophia-cli for `.sophia.yaml`; co-locating the cache means a single `.sophia/` is gitignored by the user once.
  - Atomic rename prevents partially-written cache files from being read as valid hits if a process crashes mid-write.
- **TTL semantics**: a stale file (`DetectedAt + TTL < clock.Now()`) is a cache MISS but is NOT deleted on read — `Write` overwrites it. This makes cache reads pure (no side effect on disk) and matches stdlib idioms.
- **Locked**: operator decision 11.

### D-INIT-10: All subprocess and HTTP calls behind interfaces (testability)

- **What**: every `exec.Command` and HTTP call lives behind a Go interface listed in `Deps`. Unit tests inject fakes; only integration tests touch real subprocesses or networks.
- **Interfaces**: `GraphifyProber` (cli only), `GraphifySpawner`, `StructuralPersister`, `CacheStore`, `SophiaDetector`. Plus the existing `outbound.MemoryClient` (already an interface).
- **Why**: explore §13 #3 — Python/graphify absence on CI is HIGH risk for integration tests. With fakes, every detector / cache / persister / spawner unit test runs in <100ms with zero environment dependencies.
- **Concrete rule**: NO direct `exec.Command`, `http.Client`, or `os.Open` of repo files in `internal/application/init/*` outside of the adapter packages. The `InitService` orchestrates pure interfaces.

### D-INIT-11: Clock + IDGenerator injection per Sophia CLAUDE.md D1.5

- **What**: every `time.Now()` in `internal/application/init/` becomes `s.d.Clock.Now()`. Every `ulid.Make()` (none expected; the change has its own id) becomes `s.d.IDGen.NewID()` if needed.
- **Why**: deterministic tests + golangci-lint `forbidigo` rule already blocks direct `time.Now()` in `domain`/`application` layers.

---

## Component design

### 1. `StructuralContext` type (`internal/application/init/detector/types.go`)

```go
package detector

import "time"

// StructuralContextSchemaV1 is the wire-version of the StructuralContext JSON
// shape persisted to memory-engine and the local cache file. Consumers MUST
// check this field before deserialize; version mismatches must be handled
// explicitly (re-detect or fail).
const StructuralContextSchemaV1 = 1

// StructuralContext is the deterministic ground-truth produced by INIT phase.
// Persisted to memory-engine (topic_key=sdd/<change_name>/init) AND to
// <repo_root>/.sophia/cache/structural/<cache_key>.json.
type StructuralContext struct {
    SchemaVersion     int              `json:"schema_version"`        // = StructuralContextSchemaV1
    ProjectID         string           `json:"project_id"`            // from change.Project()
    ChangeID          string           `json:"change_id"`             // from change.ID().String()
    ChangeName        string           `json:"change_name"`           // from change.Name()
    Languages         []LanguageInfo   `json:"languages"`
    Frameworks        []FrameworkInfo  `json:"frameworks"`
    PackageManagers   []string         `json:"package_managers"`      // npm | pnpm | yarn | go | uv | pip | poetry | gradle | maven | cargo
    ArchStyle         []string         `json:"arch_style"`            // hexagonal | layered | mvc | monorepo | nx-workspace | turbo-workspace
    GraphSummary      *GraphSummary    `json:"graph_summary,omitempty"`
    AffectedModules   []string         `json:"affected_modules,omitempty"`
    ConventionHints   []string         `json:"convention_hints"`      // e.g. "package-by-feature", "test-files-collocated"
    GraphAvailable    bool             `json:"graph_available"`
    DegradedReason    string           `json:"degraded_reason,omitempty"`
    DetectedAt        time.Time        `json:"detected_at"`           // = clock.Now() at merge
    GraphifyVersion   string           `json:"graphify_version,omitempty"`
    SophiaDetectorVer string           `json:"sophia_detector_version"` // semver of detector package
}

type LanguageInfo struct {
    Name            string `json:"name"`             // go | typescript | python | rust | java | kotlin
    VersionEvidence string `json:"version_evidence,omitempty"` // e.g. "go.mod: go 1.26.2"
    FilesCount      int    `json:"files_count"`
}

type FrameworkInfo struct {
    Name         string `json:"name"`           // angular | ngrx | react | next | spring-boot | django | fastapi | flask | ...
    Version      string `json:"version,omitempty"`
    EvidencePath string `json:"evidence_path"`  // file that proved presence (e.g. package.json, requirements.txt)
}

type GraphSummary struct {
    TotalNodes     int      `json:"total_nodes"`
    TotalEdges     int      `json:"total_edges"`
    GodNodes       []string `json:"god_nodes,omitempty"`        // top-K out-degree
    CommunityCount int      `json:"community_count,omitempty"`  // from graphify community detection
}
```

**Notes**:
- `DetectedAt` uses `clock.Now().UTC()` (Sophia CLAUDE.md D1.5).
- `SophiaDetectorVer` is a const in the detector package (e.g. `"0.1.0"`) bumped manually when the parser logic changes meaningfully.
- `GraphSummary` is `*GraphSummary` (nullable) so JSON omits it cleanly in degraded mode.

### 2. Port interfaces

```go
// --- internal/application/init/ports.go ---
package init // package name may collide with go keyword; use `initphase` if needed

import (
    "context"
    "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
)

// SophiaDetector runs pure Go FS reads to fingerprint languages, frameworks,
// architecture style, and convention hints from the repo at repoRoot. No
// shell, no network. Returns a DetectorResult merged later by InitService.
type SophiaDetector interface {
    Detect(ctx context.Context, repoRoot string) (DetectorResult, error)
}

type DetectorResult struct {
    Languages       []detector.LanguageInfo
    Frameworks      []detector.FrameworkInfo
    PackageManagers []string
    ArchStyle       []string
    ConventionHints []string
}

// GraphifySpawner runs `graphify update` (Pattern B) and parses
// graphify-out/graph.json into a GraphSummary. Implementations live in
// internal/adapters/outbound/graphify/spawner.go. Unit tests inject a fake.
// On non-zero exit OR JSON parse error, returns (nil, ErrGraphifyDegraded(reason)).
type GraphifySpawner interface {
    Build(ctx context.Context, repoRoot string) (*detector.GraphSummary, string /*version*/, error)
}

// StructuralPersister writes StructuralContext to BOTH memory-engine
// (via outbound.MemoryClient.Ingest) AND the local cache file (via CacheStore).
// The two writes are sequenced: memory-engine first (the source of truth for
// cross-session reads), local file second (fast path).
type StructuralPersister interface {
    Persist(ctx context.Context, sc detector.StructuralContext, cacheKey string) error
}

// CacheStore is the local file cache abstraction. Used by both InitService
// (Lookup before Build, Write after Persist) AND by StructuralPersister
// (Write as the file half of dual persistence).
type CacheStore interface {
    Lookup(ctx context.Context, cacheKey string) (*detector.StructuralContext, bool, error)
    Write(ctx context.Context, cacheKey string, sc detector.StructuralContext) error
}

// CacheKeyBuilder computes the 6-component sha256 cache key. Behind an
// interface only so tests can supply a deterministic builder; the real
// impl reads git head + porcelain output via injected git exec wrapper.
type CacheKeyBuilder interface {
    Build(ctx context.Context, repoRoot, graphifyVersion string) (string, error)
}
```

**Sentinel errors** (package `init` / `initphase`):
```go
var (
    ErrGraphifyDegraded    = errors.New("init: graphify degraded")  // wraps cause via %w
    ErrCacheMiss           = errors.New("init: cache miss")
    ErrSchemaVersionMismatch = errors.New("init: cached schema_version mismatch")
)
```

### 3. `InitService` design (`internal/application/init/service.go`)

```go
type Deps struct {
    Detector       SophiaDetector
    Spawner        GraphifySpawner
    Persister      StructuralPersister
    Cache          CacheStore
    CacheKey       CacheKeyBuilder
    Clock          shared.Clock
    IDGen          shared.IDGenerator
    Logger         *slog.Logger
    CacheTTL       time.Duration  // default 24h
}

type Service struct {
    d Deps
}

func New(d Deps) *Service { /* nil-checks + defaults */ }

// Run is the single entrypoint called from phase.Service.runInitPhase().
// Returns (StructuralContext, *envelope.Envelope, error). The envelope is
// constructed here so phase.Service can persist it BEFORE state change.
func (s *Service) Run(ctx context.Context, c *change.Change) (detector.StructuralContext, *envelope.Envelope, error) {
    // 1. Compute cache key.
    cacheKey, err := s.d.CacheKey.Build(ctx, /*repoRoot=*/ ".", /*graphifyVersion=*/ "")
    if err != nil { /* hard fail: cannot even key the cache */ }

    // 2. Cache lookup; if hit and not expired AND schema_version matches → skip detect+spawn.
    if cached, hit, err := s.d.Cache.Lookup(ctx, cacheKey); err == nil && hit {
        if cached.SchemaVersion == detector.StructuralContextSchemaV1 &&
           s.d.Clock.Now().Sub(cached.DetectedAt) < s.d.CacheTTL {
            return *cached, s.buildEnvelope(c, *cached), nil
        }
        // schema mismatch or expired → fall through to rebuild
    }

    // 3. Parallel: Detector.Detect + Spawner.Build (errgroup).
    var dRes DetectorResult
    var gSum *detector.GraphSummary
    var gVer string
    var degradedReason string

    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error {
        r, err := s.d.Detector.Detect(gctx, ".")
        if err != nil { return fmt.Errorf("detector: %w", err) }
        dRes = r
        return nil
    })
    g.Go(func() error {
        sum, ver, err := s.d.Spawner.Build(gctx, ".")
        if err != nil {
            // graphify degraded — NOT a hard fail; record reason and continue
            degradedReason = err.Error()
            return nil
        }
        gSum = sum
        gVer = ver
        return nil
    })
    if err := g.Wait(); err != nil {
        // ONLY detector errors are hard fails; spawner errors are absorbed above.
        return detector.StructuralContext{}, nil, err
    }

    // 4. Merge.
    sc := detector.StructuralContext{
        SchemaVersion:     detector.StructuralContextSchemaV1,
        ProjectID:         c.Project(),
        ChangeID:          c.ID().String(),
        ChangeName:        c.Name(),
        Languages:         dRes.Languages,
        Frameworks:        dRes.Frameworks,
        PackageManagers:   dRes.PackageManagers,
        ArchStyle:         dRes.ArchStyle,
        ConventionHints:   dRes.ConventionHints,
        GraphSummary:      gSum,
        GraphAvailable:    gSum != nil,
        DegradedReason:    degradedReason,
        DetectedAt:        s.d.Clock.Now().UTC(),
        GraphifyVersion:   gVer,
        SophiaDetectorVer: detector.Version,
    }

    // 5. Persist (dual: memory-engine + local file).
    //    Persister is responsible for the order: memory-engine first, file second.
    //    A failure on memory-engine is HARD; a failure on file is SOFT (logged, not returned).
    if err := s.d.Persister.Persist(ctx, sc, cacheKey); err != nil {
        return detector.StructuralContext{}, nil, fmt.Errorf("persist: %w", err)
    }

    return sc, s.buildEnvelope(c, sc), nil
}

func (s *Service) buildEnvelope(c *change.Change, sc detector.StructuralContext) *envelope.Envelope {
    raw, _ := json.Marshal(sc) // best-effort; structurally always valid
    return &envelope.Envelope{
        SchemaVersion:    "sophia-wire-v1",
        Phase:            string(phase.PhaseInit),
        ChangeName:       c.Name(),
        Project:          c.Project(),
        Status:           envelope.StatusOK,
        Confidence:       1.0,                       // INIT is deterministic; threshold is 0.0; we report full confidence
        ExecutiveSummary: fmt.Sprintf("init: detected %d languages, %d frameworks, graph_available=%t",
                              len(sc.Languages), len(sc.Frameworks), sc.GraphAvailable),
        ArtifactsSaved:   []envelope.ArtifactRef{
            {TopicKey: fmt.Sprintf("sdd/%s/init", c.Name()), Type: "sdd_init"},
        },
        NextRecommended:  envelope.NextRecommended{Phase: string(phase.PhaseExplore)},
        Risks:            nil,
        Data:             raw,
    }
}
```

**Notes**:
- `Run` returns the envelope so `phase.Service.runInitPhase` can call `p.Complete(env, clock.Now())` + `PhaseRepo.Save(ctx, p)` immediately after the persister succeeds — keeping Iron Law D1.2 ordering identical to LLM phases.
- `Persister.Persist` succeeding is the boundary that makes the artifact visible cross-session; the synthetic envelope is built AFTER persist succeeds.
- Spawner errors are absorbed into `DegradedReason`; only detector errors hard-fail INIT (detector is the floor — if we cannot read manifests, we cannot produce a useful context).

### 4. `runPhase()` branch (in `internal/application/phase/service.go`)

```go
// runAsync executes steps 10-16 of the phase flow.
func (s *Service) runAsync(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) {
    // INIT phase short-circuit: deterministic detection, no LLM, no governance,
    // no Iron Law check, no prompt build, no dispatcher. Iron Law D1.2 still
    // applies: envelope is built+persisted BEFORE the Change.CurrentPhase advance.
    if p.Type() == phase.PhaseInit {
        s.runInitPhase(ctx, c, p, in)
        return
    }

    // Step 4: governance.
    decision, err := s.d.Governance.EvaluatePhase(ctx, outbound.EvaluatePhaseInput{
        // ... unchanged
    })
    // ... existing path continues
}

// runInitPhase handles the PhaseInit branch — deterministic structural
// detection via InitService. Mirrors Steps 13-16 of runAsync for envelope
// persistence, audit, advanceChange, terminal event.
func (s *Service) runInitPhase(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) {
    sc, env, err := s.d.Init.Run(ctx, c)
    if err != nil {
        s.failPhase(ctx, p, fmt.Sprintf("init: %v", err))
        return
    }

    // Iron Law D1.2: envelope persisted BEFORE state change. The envelope is
    // already validated structurally (we built it from a known-good shape),
    // but we still pass it through the Validator for uniformity — failing
    // here means the envelope shape we constructed is wrong, which is a code
    // bug, not a runtime degradation.
    if _, vErr := s.d.Validator.Validate(mustMarshal(env), p.Type()); vErr != nil {
        s.failPhase(ctx, p, fmt.Sprintf("envelope validation (init): %v", vErr))
        return
    }

    // Step 13: complete phase + persist (Iron Law #1: persisted-before-return).
    if err := p.Complete(env, s.d.Clock.Now()); err != nil {
        s.failPhase(ctx, p, fmt.Sprintf("phase complete: %v", err))
        return
    }
    if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
        s.failPhase(ctx, p, fmt.Sprintf("save phase: %v", err))
        return
    }
    s.recordPhaseTerminal(p, env)
    s.recordPhaseEnded(p)

    // Step 13b: persist envelope.ArtifactsSaved to memory-engine. For INIT,
    // the artifact (StructuralContext) was already persisted INSIDE
    // InitService.Run via the StructuralPersister. persistArtifactsToMemory
    // is a no-op here because the artifact is already written; calling it
    // would be a double-write. Skip.
    // (Note: future M0.5 PriorContext refactor may consume sc directly.)
    _ = sc // sc is the in-memory copy; consumers read from memory-engine via topic_key

    // Step 14: advance Change.CurrentPhase.
    if p.Status().AdvanceAllowed() {
        s.advanceChange(ctx, c, p.Type())
    }

    // Step 15-16: audit + terminal lifecycle event.
    cidLocal := c.ID()
    pidLocal := p.ID()
    eventType := eventTypeForStatus(p.Status())
    s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventType, env)
    s.publishEvent(ctx, p.ID(), eventType, inbound.PhaseCompletedPayload{
        PhaseID:            p.ID().String(),
        PhaseType:          string(p.Type()),
        EndedAt:            s.d.Clock.Now().UTC(),
        Confidence:         env.Confidence,
        EnvelopeStatus:     string(env.Status),
        EnvelopeConfidence: env.Confidence,
    })
}
```

**Notes**:
- `Deps` gains a single new field: `Init *initphase.Service` (or interface `InitRunner` if we want to fake-inject for phase.Service tests — recommended).
- `_ = in`: `RunPhaseInput` is currently unused by INIT (no `TaskDescription` needed; no `ContextOverrides` parsed). Reserved for future use (e.g. operator can pass `include_globs` overrides).
- The branch lives in `runAsync` (not `Run`) so the 202+SSE pattern is preserved.
- `persistArtifactsToMemory` is intentionally skipped for INIT — the persister already wrote the artifact via `MemoryClient.Ingest` inside `InitService.Run`. Calling it again would attempt a re-ingest with empty content from the synthetic envelope.

### 5. Pattern B graphify lifecycle (`internal/adapters/outbound/graphify/spawner.go`)

```go
type Spawner struct {
    Exec   ExecRunner    // interface wrapping exec.Command for testability
    Logger *slog.Logger
    Timeout time.Duration  // default 30s; override via SOPHIA_GRAPHIFY_TIMEOUT_MS
}

// ExecRunner abstracts exec.Command so unit tests inject a fake that returns
// canned stdout/exit-code without touching the OS.
type ExecRunner interface {
    Run(ctx context.Context, name string, args []string, opts ExecOpts) (stdout []byte, stderr []byte, exitCode int, err error)
}

type ExecOpts struct {
    Dir  string
    Env  []string
    StdinBytes []byte
}

func (s *Spawner) Build(ctx context.Context, repoRoot string) (*detector.GraphSummary, string, error) {
    // 1. Capture graphify version (for cache key + StructuralContext).
    verCtx, verCancel := context.WithTimeout(ctx, 5*time.Second)
    verStdout, _, exit, err := s.Exec.Run(verCtx, "graphify", []string{"--version"}, ExecOpts{Dir: repoRoot})
    verCancel()
    if err != nil || exit != 0 {
        return nil, "", fmt.Errorf("%w: graphify --version failed: %v", initphase.ErrGraphifyDegraded, err)
    }
    version := strings.TrimSpace(string(verStdout))

    // 2. Run `graphify update`.
    bldCtx, bldCancel := context.WithTimeout(ctx, s.Timeout)
    _, stderr, exit, err := s.Exec.Run(bldCtx, "graphify", []string{"update", repoRoot}, ExecOpts{Dir: repoRoot})
    bldCancel()
    if err != nil || exit != 0 {
        return nil, version, fmt.Errorf("%w: graphify update exit=%d: %s", initphase.ErrGraphifyDegraded, exit, string(stderr))
    }

    // 3. Read graphify-out/graph.json.
    graphPath := filepath.Join(repoRoot, "graphify-out", "graph.json")
    data, err := os.ReadFile(graphPath)
    if err != nil {
        return nil, version, fmt.Errorf("%w: read graph.json: %v", initphase.ErrGraphifyDegraded, err)
    }

    // 4. Parse into GraphSummary. The graph.json shape per graphify-audit
    //    has top-level `nodes`, `edges`, and possibly `communities`. We extract
    //    just what GraphSummary needs.
    var raw struct {
        Nodes []struct {
            ID      string `json:"id"`
            OutDeg  int    `json:"out_degree,omitempty"`
        } `json:"nodes"`
        Edges       []struct{} `json:"edges"`
        Communities []struct{} `json:"communities,omitempty"`
    }
    if err := json.Unmarshal(data, &raw); err != nil {
        return nil, version, fmt.Errorf("%w: parse graph.json: %v", initphase.ErrGraphifyDegraded, err)
    }

    // 5. Compute god_nodes: top-K out-degree (K=10 default).
    godNodes := topKByOutDegree(raw.Nodes, 10)

    return &detector.GraphSummary{
        TotalNodes:     len(raw.Nodes),
        TotalEdges:     len(raw.Edges),
        GodNodes:       godNodes,
        CommunityCount: len(raw.Communities),
    }, version, nil
}
```

**Notes**:
- `ExecRunner` interface keeps unit tests subprocess-free. The concrete impl wraps `exec.CommandContext`.
- All errors wrap `initphase.ErrGraphifyDegraded` so callers can `errors.Is` and downgrade to degraded mode.
- Timeout default = 30s; override via env. Per V4.1 §7-ter.8 the target is p95<30s.

### 6. Cache key computation (`internal/application/init/cache_key.go`)

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
    write := func(s string) {
        h.Write([]byte(s))
        h.Write([]byte{0}) // null separator prevents ambiguity
    }
    write(k.GraphifyVersion)
    write(k.RepoRoot)
    write(k.GitHead)
    write(k.DirtyTreeHash)
    globs := slices.Clone(k.IncludeGlobs)
    slices.Sort(globs)
    for _, g := range globs {
        write(g)
    }
    write(k.ConfigHash)
    return hex.EncodeToString(h.Sum(nil))
}

// Builder is the concrete CacheKeyBuilder. Reads git via injected GitRunner
// (interface, not exec.Command directly), reads .graphify.yaml via FileReader.
type Builder struct {
    Git    GitRunner   // interface for `git rev-parse HEAD` + `git status --porcelain`
    Files  FileReader  // interface for reading .graphify.yaml + .sophia.yaml
}

func (b *Builder) Build(ctx context.Context, repoRoot, graphifyVersion string) (string, error) {
    head, err := b.Git.RevParseHead(ctx, repoRoot)
    if err != nil { return "", fmt.Errorf("git rev-parse: %w", err) }

    porcelain, err := b.Git.StatusPorcelain(ctx, repoRoot)
    if err != nil { return "", fmt.Errorf("git status: %w", err) }
    dirty := sha256OfBytes(porcelain)

    configBytes, _ := b.Files.ReadIfExists(filepath.Join(repoRoot, ".graphify.yaml"))
    configHash := sha256OfBytes(configBytes)

    globs := readIncludeGlobs(b.Files, repoRoot) // defaults if .sophia.yaml absent

    return CacheKey{
        GraphifyVersion: graphifyVersion,
        RepoRoot:        repoRoot,
        GitHead:         head,
        DirtyTreeHash:   dirty,
        IncludeGlobs:    globs,
        ConfigHash:      configHash,
    }.Hash(), nil
}
```

**Notes**:
- `GitRunner` and `FileReader` are interfaces; tests inject fakes.
- `DirtyTreeHash` = sha256 of `git status --porcelain` raw bytes (newline-separated paths). Empty tree → sha256 of empty string (well-defined).
- `IncludeGlobs` default = `["**/*"]` if `.sophia.yaml` does not configure them.

### 7. Memory-engine persistence (`internal/application/init/persister.go`)

```go
type DualPersister struct {
    Memory outbound.MemoryClient  // EXISTING port; reuse the wired client
    Cache  CacheStore
    Logger *slog.Logger
    Tenant string  // from cfg.MemoryTenantID
    Env    string  // dev | staging | prod
}

func (p *DualPersister) Persist(ctx context.Context, sc detector.StructuralContext, cacheKey string) error {
    // 1. Marshal once.
    body, err := json.Marshal(sc)
    if err != nil {
        return fmt.Errorf("marshal structural_context: %w", err)
    }

    // 2. Memory-engine first (source of truth for cross-session reads).
    _, err = p.Memory.Ingest(ctx, outbound.IngestMemoryInput{
        Type:    "sdd_init",
        Content: string(body),
        Summary: fmt.Sprintf("INIT structural detection for %s", sc.ChangeName),
        Tags:    []string{"sdd", "init", "structural_context", "schema_v1"},
        TopicKey: fmt.Sprintf("sdd/%s/init", sc.ChangeName),
        Scope: outbound.MemoryScope{
            TenantID:    p.Tenant,
            ProjectID:   sc.ProjectID,
            SessionID:   sc.ChangeID,
            AgentID:     "sophia-orchestator",
            Environment: p.Env,
        },
        Provenance: outbound.MemoryProvenance{
            Source: "sophia-orchestator",
            Method: "sdd-phase-output",
        },
    })
    if err != nil {
        return fmt.Errorf("memory-engine ingest: %w", err)  // HARD fail
    }

    // 3. Local cache (fast path). Failure here is SOFT — log + continue.
    if cacheErr := p.Cache.Write(ctx, cacheKey, sc); cacheErr != nil {
        p.Logger.Warn("structural context cache write failed",
            "change_id", sc.ChangeID,
            "cache_key", cacheKey,
            "err", cacheErr)
        // intentionally swallowed: memory-engine is source of truth
    }

    return nil
}
```

**Notes**:
- Idempotency: memory-engine migration 004 partial unique index on `topic_key` makes re-ingestion safe. Re-running INIT on the same `change_name` overwrites the prior active row cleanly.
- Memory-engine failure is HARD — the phase fails. Local cache failure is SOFT — the phase succeeds (cross-session reads still work via memory-engine on the next run).
- `Tenant` and `Env` come from `cfg.MemoryTenantID` and `cfg.Environment` already-wired in `phase.ServiceConfig` (see `service.go:79-80`).

### 8. Local file cache (`internal/application/init/file_cache.go`)

```go
type FileCache struct {
    Dir    string         // <repo_root>/.sophia/cache/structural/
    Clock  shared.Clock
    Logger *slog.Logger
}

func (c *FileCache) Lookup(ctx context.Context, cacheKey string) (*detector.StructuralContext, bool, error) {
    path := filepath.Join(c.Dir, cacheKey+".json")
    data, err := os.ReadFile(path)
    if errors.Is(err, fs.ErrNotExist) {
        return nil, false, nil
    }
    if err != nil {
        return nil, false, fmt.Errorf("read cache: %w", err)
    }
    var sc detector.StructuralContext
    if err := json.Unmarshal(data, &sc); err != nil {
        // corrupt cache file is a soft miss; do not return error
        c.Logger.Warn("structural context cache corrupt", "path", path, "err", err)
        return nil, false, nil
    }
    return &sc, true, nil
}

func (c *FileCache) Write(ctx context.Context, cacheKey string, sc detector.StructuralContext) error {
    if err := os.MkdirAll(c.Dir, 0o755); err != nil {
        return fmt.Errorf("mkdir cache: %w", err)
    }
    data, err := json.MarshalIndent(sc, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal: %w", err)
    }

    // Atomic write: temp file + os.Rename. Prevents partial writes from being
    // read as valid hits on crash.
    tmp := filepath.Join(c.Dir, cacheKey+".json.tmp")
    if err := os.WriteFile(tmp, data, 0o644); err != nil {
        return fmt.Errorf("write tmp: %w", err)
    }
    final := filepath.Join(c.Dir, cacheKey+".json")
    if err := os.Rename(tmp, final); err != nil {
        _ = os.Remove(tmp)
        return fmt.Errorf("rename: %w", err)
    }
    return nil
}
```

**Notes**:
- TTL check is performed by `InitService.Run` AFTER `Lookup` (using `clock.Now().Sub(sc.DetectedAt) < TTL`), not inside the cache itself. Keeps `FileCache` pure (no clock dependency for `Lookup` — only `Clock` for hypothetical future write timestamps if needed).
- Corrupt cache files (bad JSON) are treated as misses, not errors — defensive against partial filesystem corruption or version drift.

### 9. CLI bootstrap (`sophia-cli`, PR1)

```go
// sophia-cli/internal/ports/outbound/graphify_prober.go
type GraphifyProber interface {
    Probe(ctx context.Context) (ProberResult, error)
    Bootstrap(ctx context.Context) error  // runs `uv tool install "graphifyy[mcp]==0.8.35"`
}

type ProberResult struct {
    Available    bool
    Version      string         // captured from `graphify --version`
    PythonOK     bool           // Python 3.10+ present
    UVOK         bool           // uv present
    MissingDeps  []string       // human-readable list
    DetectedAt   time.Time
}
```

**Wiring in `Initializer.Run()`**:
```go
// After .sophia.yaml write:
result, err := s.d.Prober.Probe(ctx)
if err != nil {
    s.d.Logger.Warn("graphify probe failed; INIT will run in degraded mode",
        "err", err)
} else if !result.Available {
    s.d.Logger.Warn("graphify not detected; INIT will run in degraded mode",
        "missing", result.MissingDeps)
    if s.d.AutoBootstrap {
        if bErr := s.d.Prober.Bootstrap(ctx); bErr != nil {
            s.d.Logger.Warn("graphify auto-bootstrap failed", "err", bErr)
        }
    }
} else {
    s.d.Logger.Info("graphify detected", "version", result.Version)
}
// Always continue. NEVER block init on probe result.
```

**Flag plumbing** (`internal/adapters/inbound/cli/init.go`):
```go
cmd.Flags().BoolVar(&autoBootstrap, "auto-bootstrap-graphify", false,
    "If graphify is not detected, attempt `uv tool install graphifyy[mcp]==0.8.35`. Default OFF.")
```

**Notes**:
- `ProberResult` is written to a local hint file (`<repo_root>/.sophia/graphify-probe.json`) so subsequent `sophia` invocations can short-circuit without re-probing. Orchestator does NOT read this file — it does its own version capture inside the spawner (D-INIT-7 cache key needs it).
- `--auto-bootstrap-graphify` is operator-opt-in only; absence means degraded-first per V4.1 §7-ter.7.

### 10. Bootstrap wiring (`internal/bootstrap/wire.go`)

```go
// New construction added near the existing phase.Service wiring:

// (a) Detector — pure Go, no deps beyond logger.
detector := detector.New(detector.Deps{Logger: logger})

// (b) Cache key builder — wraps git + file reads.
gitRunner := git.NewExecRunner()  // adapter wraps exec.Command
fileReader := files.NewOSReader()
keyBuilder := initphase.NewBuilder(gitRunner, fileReader)

// (c) File cache.
fileCache := initphase.NewFileCache(filepath.Join(repoRoot, ".sophia", "cache", "structural"), clock, logger)

// (d) Spawner — wraps exec.Command.
execRunner := exec.NewRealRunner()  // adapter
spawner := graphify.NewSpawner(execRunner, logger, cfg.Graphify.TimeoutMS)

// (e) Persister — reuses existing memoryClient.
persister := initphase.NewDualPersister(memoryClient, fileCache, logger, cfg.MemoryTenantID, cfg.Environment)

// (f) InitService.
initSvc := initphase.New(initphase.Deps{
    Detector:  detector,
    Spawner:   spawner,
    Persister: persister,
    Cache:     fileCache,
    CacheKey:  keyBuilder,
    Clock:     clock,
    IDGen:     idgen,
    Logger:    logger,
    CacheTTL:  24 * time.Hour,
})

// (g) Inject into phase.Service.Deps via new field.
phaseSvc := phase.New(phase.Deps{
    // ... existing fields
    Init: initSvc,
})
```

**Notes**:
- Single new ctor call per port (`detector`, `keyBuilder`, `fileCache`, `spawner`, `persister`, `initSvc`).
- All concrete adapters live in `internal/adapters/outbound/` (or new `internal/adapters/outbound/graphify/`, `internal/adapters/outbound/git/`, `internal/adapters/outbound/files/`) — bootstrap is the only place that imports them.
- `cfg.Graphify.TimeoutMS` is a new config field (default 30000); env var `SOPHIA_GRAPHIFY_TIMEOUT_MS`.

---

## Iron Law D1.2 enforcement (envelope-before-state)

| Step | Action | Ordering check |
|------|--------|----------------|
| 1 | `InitService.Run(ctx, c)` returns `(sc, env, nil)` | Pure compute; no state change |
| 2 | `Persister.Persist(ctx, sc, cacheKey)` writes to memory-engine + file | Artifact persisted FIRST |
| 3 | `Validator.Validate(envBytes, PhaseInit)` | Schema check; no state change |
| 4 | `p.Complete(env, clock.Now())` | In-memory state mutation |
| 5 | `s.d.PhaseRepo.Save(ctx, p)` | Phase row persisted |
| 6 | `advanceChange(ctx, c, PhaseInit)` | Change.CurrentPhase persisted |
| 7 | `publishEvent(... EventPhaseCompleted ...)` | SSE event emitted |

**Invariant**: artifact and envelope BOTH durable on the database side BEFORE the phase row is marked DONE and BEFORE the Change advances. A crash between (5) and (6) leaves an orphan-DONE phase but the next orchestrator boot will resume via recovery (existing machinery).

---

## Clock injection audit (Sophia CLAUDE.md D1.5)

Every `time.Now()` in `internal/application/init/*` is `s.d.Clock.Now()`:
- `InitService.Run` — `DetectedAt = s.d.Clock.Now().UTC()`
- TTL check — `s.d.Clock.Now().Sub(cached.DetectedAt) < s.d.CacheTTL`
- Envelope persistence — `p.Complete(env, s.d.Clock.Now())` (existing pattern)

Detectors, spawner, persister, cache: NONE call `time.Now()` directly. The only timestamp produced is in `InitService.Run` (the merge step) and it uses the injected clock.

---

## Test strategy

### Unit (no network, no subprocess, no real FS beyond `t.TempDir`)

| Component | Fake | Coverage targets |
|-----------|------|------------------|
| `detector.Detector` | NONE (pure FS reads on fixture dirs in `t.TempDir`) | Per-language: go.mod / package.json / pyproject.toml / Cargo.toml / pom.xml fixtures; framework fingerprint (Angular 17, NgRx, React, Next, Spring Boot 3, Django, FastAPI, Flask); arch heuristics (hex dirs `internal/{domain,application,ports,adapters}`); monorepo signals (nx.json, turbo.json) |
| `graphify.Spawner` | `FakeExecRunner` returning canned stdout / exit-codes | Happy path: version capture + update + parse graph.json with 100/500/2000 node fixtures. Degraded: `graphify --version` exit≠0, `graphify update` exit≠0, `graph.json` missing, `graph.json` malformed |
| `initphase.FileCache` | NONE (`t.TempDir`) | Miss (file absent), hit (file present + parseable), corrupt-as-miss, atomic-write under simulated crash (kill mid-write, verify final file either complete or absent) |
| `initphase.Builder` (cache key) | `FakeGitRunner`, `FakeFileReader` | Determinism: same inputs → same hash; sorted globs invariant; null-sep prevents ambiguity (`"foo"+"bar"` ≠ `"f"+"oobar"`); dirty-tree-hash changes with porcelain output |
| `initphase.DualPersister` | `FakeMemoryClient`, `FakeFileCache` | Memory-first ordering; memory failure is HARD; file failure is SOFT (logged, persist returns nil); idempotent re-persist (memory upsert via topic_key) |
| `initphase.Service` (InitService) | All deps faked | Cache hit short-circuits (no detector/spawner call); cache miss runs both; spawner failure populates `DegradedReason` but persist still happens; detector failure is HARD; envelope shape correct; `SchemaVersion=1`, `Status=ok`, `Confidence=1.0` |
| `phase.Service.runInitPhase` | `FakeInitService`, FakeValidator, FakePhaseRepo, FakeEvents, FakeAudit | INIT branch fires for `PhaseInit`; NO dispatcher call; envelope persisted before state change; `EventPhaseCompleted` emitted; `advanceChange` called only when `Status.AdvanceAllowed()` |
| `phase.Service.runAsync` (regression) | Existing fakes | NON-INIT phases still take the existing path (unchanged); `PhaseExplore` / `PhaseApply` etc. behavior is byte-identical to pre-change baseline |

**Critical assertion** (proposal §Risks #1): `TestRunInitPhase_DoesNotDispatchToLLM` — `FakeDispatcher.Dispatch` call counter is asserted == 0 for `PhaseInit`.

### Integration (real subprocess + real memory-engine via testcontainers)

```go
func haveGraphify() bool {
    _, err := exec.LookPath("graphify")
    return err == nil
}

func TestInitService_Integration_GoFixture(t *testing.T) {
    if !haveGraphify() { t.Skip("graphify not available") }
    // fixture: testdata/fixtures/go-hex-project/ (go.mod, internal/{domain,application,...})
    // assert: StructuralContext.Languages contains "go", ArchStyle contains "hexagonal",
    //         GraphAvailable=true, GraphSummary.TotalNodes>0
}

func TestInitService_Integration_AngularFixture(t *testing.T) {
    if !haveGraphify() { t.Skip() }
    // fixture: testdata/fixtures/angular17-project/ (package.json with @angular/core@17.x,
    //          app.config.ts, src/app/)
    // assert: Frameworks contains {Name: "angular", Version: "17.*"}
}

func TestInitService_Integration_PythonFixture(t *testing.T) {
    if !haveGraphify() { t.Skip() }
    // fixture: testdata/fixtures/fastapi-project/ (pyproject.toml, main.py with FastAPI())
    // assert: Languages contains "python", Frameworks contains {Name: "fastapi"}
}

func TestInitService_Integration_MemoryEnginePersist(t *testing.T) {
    // testcontainers-go boots memory-engine
    // assert: after Persist, GET /api/v1/memories/by-topic-key?topic_key=sdd/test-change/init
    //         returns the persisted StructuralContext
}
```

**Degraded-mode test** (no graphify available): runs without skip; asserts `GraphAvailable=false`, `DegradedReason != ""`, detector results still present, persist still succeeds.

### Phase-level integration (NO LLM dispatch assertion)

```go
func TestPhaseService_RunInit_NoLLMDispatch(t *testing.T) {
    // Wire phase.Service with FakeDispatcher that counts calls.
    // Call Run(ctx, RunPhaseInput{ChangeID: c.ID(), PhaseType: PhaseInit})
    // Wait for completion event.
    // Assert: FakeDispatcher.DispatchCalls == 0
    // Assert: FakeGovernance.EvaluatePhaseCalls == 0  (INIT bypasses governance)
    // Assert: FakeIronLaw.CheckCalls == 0             (INIT bypasses Iron Law)
    // Assert: FakePhaseRepo.Save called with status=DONE
    // Assert: EventPhaseCompleted emitted
}
```

---

## Risks revisited — concrete design mitigations

| Risk (proposal) | Design mitigation |
|------|------|
| `runPhase()` does NOT short-circuit for `PhaseInit` today | Explicit branch at TOP of `runAsync` (BEFORE governance/Iron-Law/dispatch). Regression test `TestRunInitPhase_DoesNotDispatchToLLM` asserts dispatcher counter == 0. Also asserts governance and Iron-Law counters == 0. |
| Python/graphify absent on dev machines / CI | `GraphifySpawner` interface; unit tests inject `FakeExecRunner`. Integration tests guarded with `if !haveGraphify() { t.Skip() }`. Runtime: degraded mode (`GraphAvailable=false`, INIT still succeeds). |
| Graphify version drift | Hard-pin `graphifyy[mcp]==0.8.35` in `--auto-bootstrap-graphify`. Capture version at spawn time and stamp it into `StructuralContext.GraphifyVersion` + cache key (so a version change invalidates the cache). |
| `StructuralContext` shape evolution | `SchemaVersion int` field on day 0 (constant `StructuralContextSchemaV1 = 1`). `InitService` checks `cached.SchemaVersion == StructuralContextSchemaV1` on cache read; mismatch → treat as miss + rebuild. Consumers (future M0.5/M1) MUST also check before deserialize. |
| AllowlistEnforcer remains unwired | Explicit non-goal. INIT-0 uses Pattern B (`exec.Command` only) — no MCP protocol involved, so no authorization layer needed. Archive warning #4 re-confirmed. |
| Pattern B cold-start cost | 24h TTL local cache keyed on 6-component sha256. Cache HIT path skips `Detector.Detect` AND `Spawner.Build`; target p95<5s. |
| go-sdk client mode unverified | OUT OF SCOPE for INIT-0; deferred risk. |
| Process leak risk if Pattern A adopted | Pattern A is OUT OF SCOPE; risk does not apply. |

---

## Out of scope (reaffirmed from proposal)

- Surface 2 (`AllowlistEnforcer` wiring + `ExternalMCPProxy` in sophia-agent-mcp).
- Pattern A (sidecar `graphify serve` + Go MCP stdio client).
- LLM-phase Graphify tool queries (`query_graph`, `god_nodes`, `get_community`).
- New event constants (no `wire_alignment_test` updates needed).
- `StructuralContext` consumption by `SkillMatcher` (M1's responsibility).
- `PriorContext` struct refactor (M0.5's responsibility).
- Auto-bootstrap ON by default (V4.1 §7-ter.7 mandates degraded-first).
- `affected_nodes` MCP tool / upstream Graphify PR.
- Docker sidecar for Graphify.
- Migration of `StructuralContext` schema beyond v1.

---

## Architectural risks / unresolved decisions

1. **Detector versioning**: `SophiaDetectorVer` is a manually-bumped const. RISK: forgetting to bump it on parser changes invalidates cache reuse logic. MITIGATION: add a linter rule or CI guard that fails if `detector/*.go` changes without bumping `Version`. (Defer to a separate ops task.)
2. **`include_globs` source**: cache key includes `IncludeGlobs` but the default if absent from `.sophia.yaml` is `["**/*"]`. RISK: silent cache invalidation if a user adds `.sophia.yaml` mid-project. ACCEPTABLE: cache will rebuild once on first run with new globs; cost is one cold-start.
3. **Memory-engine timeout**: existing `MemoryClient` timeout is 30s (assumed; needs verification in wire.go). RISK: a slow memory-engine could push INIT p95 past 30s budget. MITIGATION: use `cfg.Memory.TimeoutMS` if exposed; otherwise rely on parent ctx.
4. **`SophiaDetectorVer` not in cache key**: changing parser logic does NOT invalidate the cache unless we add it. CONSIDER adding `SophiaDetectorVer` as 7th cache key component in a follow-on. NOT done in v1 to keep the locked 6-component spec from V4.1 §7-ter.8.
5. **Concurrent INIT on same change_id**: existing phase machinery (`LockByChange`) prevents this at the phase level. RISK: NONE for INIT specifically.

---

## Strict TDD reminder

`strict_tdd: true` per `sdd-init/2026`. Every component above (detector, parsers, cache, spawner, persister, service, runInitPhase branch) must have a RED test FIRST. No production code lands without a prior failing test. `exec.Command` and `http.Client` calls live behind `ExecRunner` / `MemoryClient` interfaces so unit tests never spawn subprocesses or hit networks. Integration tests guarded with `if !haveGraphify() { t.Skip() }`.

`Clock` and `IDGenerator` injected per Sophia CLAUDE.md D1.5 + Iron Laws; no direct `time.Now()` or `ulid.Make()` anywhere under `internal/application/init/`.
