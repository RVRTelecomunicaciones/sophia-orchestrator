package cache_test

// key_test.go — D.1 + D.2 + T2.2 (Strict TDD: RED tests first)
//
// Tests that CacheKey.Hash() is deterministic and changes when SophiaDetectorVer changes.
// T2.2 adds: ManifestHash field coverage — Hash() differs when only ManifestHash differs;
// deterministic with 8th field set; serialized key includes manifest component.

import (
	"strings"
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

// T2.2a: Hash() differs when only ManifestHash differs.
func TestCacheKeyHash_DiffersOnManifestHashChange(t *testing.T) {
	base := cache.CacheKey{
		GraphifyVersion:   "0.8.35",
		RepoRoot:          "/repo",
		GitHead:           "abc123",
		DirtyTreeHash:     "deadbeef",
		IncludeGlobs:      []string{"**/*"},
		ConfigHash:        "cafebabe",
		SophiaDetectorVer: "v1.1.0",
		ManifestHash:      "aabbccdd11223344",
	}
	changed := base
	changed.ManifestHash = "1122334455667788"

	require.NotEqual(t, base.Hash(), changed.Hash(),
		"Hash must differ when only ManifestHash changes")
}

// T2.2b: Hash() is deterministic with ManifestHash set.
func TestCacheKeyHash_Deterministic_WithManifestHash(t *testing.T) {
	key := cache.CacheKey{
		GraphifyVersion:   "0.8.35",
		RepoRoot:          "/repo",
		GitHead:           "abc123",
		DirtyTreeHash:     "deadbeef",
		IncludeGlobs:      []string{"**/*"},
		ConfigHash:        "cafebabe",
		SophiaDetectorVer: "v1.1.0",
		ManifestHash:      "aabbccdd11223344",
	}

	h1 := key.Hash()
	h2 := key.Hash()
	require.NotEmpty(t, h1)
	require.Equal(t, h1, h2, "Hash must be deterministic with ManifestHash set")
}

// T2.2c: String representation includes all components including ManifestHash.
func TestCacheKeyHash_StringIncludesManifestHash(t *testing.T) {
	key := cache.CacheKey{
		GraphifyVersion:   "0.8.35",
		RepoRoot:          "/repo",
		GitHead:           "abc123",
		DirtyTreeHash:     "deadbeef",
		IncludeGlobs:      []string{"**/*"},
		ConfigHash:        "cafebabe",
		SophiaDetectorVer: "v1.1.0",
		ManifestHash:      "aabbccdd11223344",
	}
	s := key.String()
	require.True(t, strings.Contains(s, "aabbccdd11223344"),
		"String() must include the ManifestHash component; got %q", s)
}
