package cache_test

// file_cache_test.go — D.3, D.4, D.5 (Strict TDD: RED tests first)
//
// Tests for FileCache: hit + TTL + atomic write.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/cache"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

func makeSC() detector.StructuralContext {
	return detector.StructuralContext{
		SchemaVersion:     detector.StructuralContextSchemaV1,
		ProjectID:         "proj-1",
		ChangeName:        "my-change",
		GraphAvailable:    false,
		SophiaDetectorVer: detector.SophiaDetectorVer,
	}
}

// D.3: FileCache.Lookup returns hit + deserialized StructuralContext when TTL not expired.
func TestFileCache_Lookup_Hit(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock(now)
	ttl := 24 * time.Hour

	fc := cache.NewFileCache(dir, clock, ttl)
	ctx := context.Background()
	sc := makeSC()
	sc.DetectedAt = now.Add(-1 * time.Hour) // 1h ago, within TTL

	const key = "test-key-1"
	require.NoError(t, fc.Write(ctx, key, sc))

	got, ok, err := fc.Lookup(ctx, key)
	require.NoError(t, err)
	require.True(t, ok, "expected cache hit")
	require.NotNil(t, got)
	require.Equal(t, sc.SchemaVersion, got.SchemaVersion)
	require.Equal(t, sc.ChangeName, got.ChangeName)
}

// D.4: FileCache.Lookup returns ErrCacheMiss when TTL exceeded.
func TestFileCache_Lookup_TTLExpired(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock(now)
	ttl := 24 * time.Hour

	fc := cache.NewFileCache(dir, clock, ttl)
	ctx := context.Background()
	sc := makeSC()
	// DetectedAt is 25h ago — past the 24h TTL.
	sc.DetectedAt = now.Add(-25 * time.Hour)

	const key = "test-key-2"
	require.NoError(t, fc.Write(ctx, key, sc))

	_, ok, err := fc.Lookup(ctx, key)
	require.ErrorIs(t, err, initphase.ErrCacheMiss)
	require.False(t, ok)
}

// D.5: FileCache.Write writes atomically (temp file + os.Rename); final file present.
func TestFileCache_Write_Atomic(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	clock := shared.FixedClock(now)
	ttl := 24 * time.Hour

	fc := cache.NewFileCache(dir, clock, ttl)
	ctx := context.Background()
	sc := makeSC()
	sc.DetectedAt = now

	const key = "atomic-key"
	require.NoError(t, fc.Write(ctx, key, sc))

	// Final file must exist.
	expected := filepath.Join(dir, key+".json")
	require.FileExists(t, expected, "expected cache file at %s", expected)

	// No temp file should remain.
	entries, err := filepath.Glob(filepath.Join(dir, "*.tmp"))
	require.NoError(t, err)
	require.Empty(t, entries, "no temp files should remain after atomic write")
}
