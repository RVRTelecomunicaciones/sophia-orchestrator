# Delta: init-graphify-spawn

## Capability

A `GraphifySpawner` interface and its concrete implementation at `sophia-orchestator/internal/application/init/graphify_spawn.go` that runs `graphify update <repo_root>` as a one-shot subprocess (Pattern B, no sidecar), reads `<repo_root>/graphify-out/graph.json` after the build completes, and caches the result keyed by a deterministic sha256 of six components with a 24-hour default TTL. Cache hits bypass the subprocess entirely. When graphify is absent or the build fails, the spawner returns a degraded result without propagating an error that would abort INIT.

---

## ADDED Requirements

### Requirement: One-Shot Graphify Build

The `GraphifySpawner.Spawn(ctx, repoRoot)` MUST execute `graphify update <repo_root>`, capture stdout and stderr, and parse `<repo_root>/graphify-out/graph.json` on success. It MUST NOT spawn a long-running sidecar process (Pattern B invariant per proposal §Scope and explore §8).

#### Scenario: cache miss with build

- GIVEN graphify is installed and no cached result exists for the current cache key
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN `graphify update <repo_root>` is executed as a subprocess
- AND `graphify-out/graph.json` is parsed on process exit with code 0
- AND the parsed result is written to the local cache at `<repo_root>/.sophia/cache/graphify/<cache_key>/`
- AND the result is returned with `graph_available=true`

#### Scenario: graphify absent returns degraded

- GIVEN graphify is not installed (`graphify` binary not on PATH)
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN no subprocess is attempted
- AND the spawner returns `graph_available=false`, `degraded_reason="graphify not installed"`
- AND no error is returned to the caller (INIT continues)

---

### Requirement: Cache Check Before Spawn

Before executing `graphify update`, the spawner MUST compute the cache key and check `<repo_root>/.sophia/cache/graphify/<cache_key>/` for an existing valid result. A valid cache hit MUST be used directly; `graphify update` MUST NOT be re-executed on a cache hit within TTL.

#### Scenario: cache hit reuses graph.json

- GIVEN a valid cached result exists for the current cache key and the cache entry is within the 24-hour TTL
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN `graphify update` is NOT executed
- AND the cached `graph.json` content is returned directly
- AND `graph_available=true` in the result

#### Scenario: dirty tree triggers cache miss

- GIVEN a cached result exists but the dirty tree hash has changed since the cache was written (uncommitted file modifications)
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN the cache entry is considered a miss
- AND `graphify update` is executed to produce a fresh result

---

### Requirement: Cache Key Computation

The cache key MUST be the hex-encoded sha256 of exactly six components concatenated in order: `graphify_version`, `repo_root` (absolute path), `git_head` (SHA), `dirty_tree_hash` (sha256 of `git status --porcelain` output), `include_globs` (sorted), `config_hash` (sha256 of `.graphify.yaml` contents, or empty string if absent). The key MUST be deterministic: same inputs always produce the same key.

#### Scenario: cache key determinism

- GIVEN the same six component values are provided on two separate calls
- WHEN `CacheKey.Hash()` is computed both times
- THEN both calls return an identical hex string

---

### Requirement: Malformed graph.json Handled as Degraded

If `graphify update` exits with code 0 but `graph.json` cannot be parsed (malformed JSON, missing required fields), the spawner MUST return `graph_available=false` and populate `degraded_reason` with a description of the parse error. It MUST NOT propagate a fatal error that aborts INIT.

#### Scenario: graph.json malformed returns degraded

- GIVEN `graphify update` exits with code 0 but `graphify-out/graph.json` contains invalid JSON
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN the spawner returns `graph_available=false`
- AND `degraded_reason` describes the JSON parse failure
- AND no error is returned to the caller

---

### Requirement: TTL Configurability

The cache TTL MUST default to 24 hours and MUST be configurable. The spawner MUST use an injected `Clock` interface (no direct `time.Now()` calls) to evaluate cache entry age against TTL, enabling deterministic unit-test control of time.

#### Scenario: expired cache entry treated as miss

- GIVEN a cached result exists but its write timestamp plus the configured TTL is in the past (as seen by the injected Clock)
- WHEN `GraphifySpawner.Spawn(ctx, repoRoot)` is called
- THEN the cache entry is considered expired
- AND `graphify update` is re-executed
