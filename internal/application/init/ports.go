// Package initphase implements the INIT phase orchestration: structural
// detection, graphify spawn, cache, and persistence. The INIT branch fires
// at the top of phase.Service.runAsync BEFORE governance/IronLaw/dispatch
// (design D-INIT-3). All subprocess and HTTP dependencies are behind the
// interfaces defined in this file.
package initphase

import (
	"context"
	"errors"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
)

// --- Sentinel errors ---

// ErrGraphifyDegraded is returned (or wrapped) by GraphifySpawner when graphify
// is absent or returns a non-zero exit code. Callers use errors.Is to detect
// degraded mode and continue with GraphAvailable=false.
var ErrGraphifyDegraded = errors.New("initphase: graphify degraded")

// ErrCacheMiss is returned by CacheStore.Lookup when no valid cache entry is
// found (absent, expired, or corrupt).
var ErrCacheMiss = errors.New("initphase: cache miss")

// ErrSchemaVersionMismatch is returned when a cached StructuralContext has a
// SchemaVersion that does not match StructuralContextSchemaV1.
var ErrSchemaVersionMismatch = errors.New("initphase: schema version mismatch")

// --- Result types ---

// DetectorResult is the output of a SophiaDetector.Detect call. It carries
// the partially-built StructuralContext from the pure-Go FS analysis (no
// graph data — that comes from GraphifySpawner).
type DetectorResult struct {
	Languages       []detector.LanguageInfo
	Frameworks      []detector.FrameworkInfo
	PackageManagers []string
	ArchStyle       []string
	ConventionHints []string
}

// --- Port interfaces ---

// SophiaDetector performs pure-Go FS analysis to produce language, framework,
// and architecture-style detection results. No subprocesses, no HTTP.
type SophiaDetector interface {
	// Detect analyses repoRoot and returns a DetectorResult.
	// Hard error: FS read failures that prevent meaningful detection.
	Detect(ctx context.Context, repoRoot string) (DetectorResult, error)
}

// GraphifySpawner runs graphify as a subprocess (Pattern B: CLI per-query)
// and returns the graph summary with the version string.
// All errors wrap ErrGraphifyDegraded so callers can errors.Is-check.
type GraphifySpawner interface {
	// Build runs `graphify update <repoRoot>` and parses
	// <repoRoot>/graphify-out/graph.json. Returns nil summary on degraded.
	Build(ctx context.Context, repoRoot string) (*detector.GraphSummary, string, error)
}

// StructuralPersister writes a StructuralContext to both memory-engine
// (HARD path) and local file cache (SOFT path).
type StructuralPersister interface {
	// Persist writes sc to persistent storage. Memory-engine failure is HARD
	// (returns error). File-cache failure is SOFT (logs WARN, returns nil).
	Persist(ctx context.Context, sc detector.StructuralContext, cacheKey string) error
}

// CacheStore is the local file cache for StructuralContext.
type CacheStore interface {
	// Lookup returns (sc, true, nil) on a valid, fresh cache hit.
	// Returns (nil, false, ErrCacheMiss) on miss, expiry, or corrupt data.
	Lookup(ctx context.Context, cacheKey string) (*detector.StructuralContext, bool, error)

	// Write persists sc under cacheKey atomically (temp file + os.Rename).
	Write(ctx context.Context, cacheKey string, sc detector.StructuralContext) error
}

// CacheKeyBuilder constructs the deterministic 7-component cache key for a
// given repo root and graphify version.
type CacheKeyBuilder interface {
	// Build computes and returns the cache key string.
	// Components: graphify_version + repo_root + git_head + dirty_tree_hash +
	// sorted(include_globs) + config_hash + SophiaDetectorVer.
	Build(ctx context.Context, repoRoot, graphifyVersion string) (string, error)
}

// --- Adapter-level interfaces ---

// ExecRunner abstracts os/exec.Command so spawner and prober adapters can be
// unit-tested without real subprocesses.
type ExecRunner interface {
	// Run executes name with args. Returns stdout, stderr, exit code, and
	// any execution error (e.g. binary not found). Exit code is -1 when the
	// process did not start. opts carries optional settings (working dir, env).
	Run(ctx context.Context, name string, args []string, opts ExecOpts) (stdout, stderr []byte, exitCode int, err error)
}

// ExecOpts carries optional settings for ExecRunner.Run.
type ExecOpts struct {
	// Dir is the working directory for the subprocess (empty = inherit).
	Dir string
	// Env is the environment for the subprocess (nil = inherit).
	Env []string
	// TimeoutMS overrides the context deadline (0 = use ctx deadline as-is).
	TimeoutMS int
}

// GitRunner provides the git operations needed by CacheKeyBuilder.
type GitRunner interface {
	// RevParseHead returns the HEAD commit hash for the repo at repoRoot.
	RevParseHead(ctx context.Context, repoRoot string) (string, error)

	// StatusPorcelain returns the `git status --porcelain` output for repoRoot.
	StatusPorcelain(ctx context.Context, repoRoot string) ([]byte, error)
}

// FileReader reads arbitrary files from the filesystem.
type FileReader interface {
	// ReadIfExists returns the file contents if the file exists, or nil if not.
	// Returns an error only on unexpected FS failures.
	ReadIfExists(path string) ([]byte, error)
}

// ProfileExtractor extracts an evidence-based ConventionProfile from a target
// repository's source tree. It is an optional dependency in InitService.Deps —
// when the field is nil the INIT phase continues normally without extracting a
// profile (graceful degradation).
//
// The extractor MUST NOT re-detect framework or language independently of the
// StructuralContext it receives (D11-analogue: detector is the single source of
// framework truth).
type ProfileExtractor interface {
	// Extract walks repoRoot and produces a ConventionProfile. sc is the
	// StructuralContext already produced by SophiaDetector; the extractor uses
	// sc.Frameworks to pick the right detection strategy and MUST NOT re-detect
	// framework or language.
	//
	// Returns a degraded profile (empty Patterns) on non-fatal detection errors.
	// Returns a hard error only on genuine FS failures that prevent any detection.
	Extract(ctx context.Context, repoRoot string, sc detector.StructuralContext) (*convention.ConventionProfile, error)
}
