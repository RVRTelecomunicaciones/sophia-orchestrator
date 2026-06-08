package cache_test

// key_test.go — D.1 + D.2 (Strict TDD: RED tests first)
//
// Tests that CacheKey.Hash() is deterministic and changes when SophiaDetectorVer changes.

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/cache"
	"github.com/stretchr/testify/require"
)

// D.1: CacheKey.Hash() is deterministic — same inputs produce identical sha256 hex.
func TestCacheKeyHash_Deterministic(t *testing.T) {
	key := cache.CacheKey{
		GraphifyVersion:   "0.8.35",
		RepoRoot:          "/home/user/myrepo",
		GitHead:           "abc123def456",
		DirtyTreeHash:     "deadbeef",
		IncludeGlobs:      []string{"**/*", "src/**"},
		ConfigHash:        "cafebabe",
		SophiaDetectorVer: "v1.0.0",
	}

	h1 := key.Hash()
	h2 := key.Hash()

	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2, "Hash must be deterministic")
}

// D.2: CacheKey.Hash() differs when SophiaDetectorVer changes.
func TestCacheKeyHash_DiffersOnDetectorVerChange(t *testing.T) {
	base := cache.CacheKey{
		GraphifyVersion:   "0.8.35",
		RepoRoot:          "/home/user/myrepo",
		GitHead:           "abc123def456",
		DirtyTreeHash:     "deadbeef",
		IncludeGlobs:      []string{"**/*"},
		ConfigHash:        "cafebabe",
		SophiaDetectorVer: "v1.0.0",
	}
	changed := base
	changed.SophiaDetectorVer = "v2.0.0"

	require.NotEqual(t, base.Hash(), changed.Hash(),
		"Hash must differ when SophiaDetectorVer changes")
}
