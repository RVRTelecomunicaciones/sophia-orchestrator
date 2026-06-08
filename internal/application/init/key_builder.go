package initphase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
)

// KeyBuilder implements CacheKeyBuilder using a GitRunner and FileReader.
type KeyBuilder struct {
	git    GitRunner
	reader FileReader
}

// NewKeyBuilder constructs a KeyBuilder.
func NewKeyBuilder(git GitRunner, reader FileReader) *KeyBuilder {
	return &KeyBuilder{git: git, reader: reader}
}

// Build computes the 7-component cache key for repoRoot.
// Components: graphify_version + repo_root + git_head + dirty_tree_hash +
// sorted(include_globs) + config_hash + SophiaDetectorVer.
//
// include_globs default = ["**/*"] when .sophia.yaml absent (one-time rebuild
// cost is acceptable per design D-INIT-7).
func (b *KeyBuilder) Build(ctx context.Context, repoRoot, graphifyVersion string) (string, error) {
	// git HEAD.
	gitHead, err := b.git.RevParseHead(ctx, repoRoot)
	if err != nil {
		// Non-fatal: use empty string so the key is still unique but will
		// never match a cached entry on a subsequent call with a real HEAD.
		gitHead = ""
	}

	// Dirty tree: sha256 of `git status --porcelain` output.
	porcelain, _ := b.git.StatusPorcelain(ctx, repoRoot)
	dirtySum := sha256.Sum256(porcelain)
	dirtyHash := hex.EncodeToString(dirtySum[:8]) // 16 hex chars is sufficient

	// Config hash: sha256 of .sophia.yaml if present.
	configPath := filepath.Join(repoRoot, ".sophia.yaml")
	configBytes, _ := b.reader.ReadIfExists(configPath)
	configSum := sha256.Sum256(configBytes) // empty bytes if missing → stable hash
	configHash := hex.EncodeToString(configSum[:8])

	// Include globs: ["**/*"] when .sophia.yaml absent.
	// TODO: parse include_globs from .sophia.yaml when present.
	includeGlobs := []string{"**/*"}

	return computeCacheKeyHash(
		graphifyVersion,
		repoRoot,
		gitHead,
		dirtyHash,
		includeGlobs,
		configHash,
		detector.SophiaDetectorVer,
	), nil
}

// computeCacheKeyHash computes a sha256 over the 7 cache key components.
// Null byte separator prevents component boundary ambiguity. IncludeGlobs are
// sorted before joining so glob order does not affect the result.
func computeCacheKeyHash(graphifyVersion, repoRoot, gitHead, dirtyTreeHash string, includeGlobs []string, configHash, sophiaDetectorVer string) string {
	globs := make([]string, len(includeGlobs))
	copy(globs, includeGlobs)
	sort.Strings(globs)
	globsJoined := strings.Join(globs, ",")

	sep := string([]byte{0})
	components := strings.Join([]string{
		graphifyVersion,
		repoRoot,
		gitHead,
		dirtyTreeHash,
		globsJoined,
		configHash,
		sophiaDetectorVer,
	}, sep)

	sum := sha256.Sum256([]byte(components))
	return hex.EncodeToString(sum[:])
}

// OSFileReader implements FileReader using os.ReadFile.
type OSFileReader struct{}

// NewOSFileReader constructs an OSFileReader.
func NewOSFileReader() *OSFileReader { return &OSFileReader{} }

// ReadIfExists returns file contents or nil if the file does not exist.
func (r *OSFileReader) ReadIfExists(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filereader: %w", err)
	}
	return b, nil
}

// Ensure types satisfy interfaces.
var (
	_ CacheKeyBuilder = &KeyBuilder{}
	_ FileReader      = &OSFileReader{}
)
