package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// FileCache is the local file-based cache for StructuralContext. It satisfies
// the initphase.CacheStore interface.
//
// Cache file path: <cacheDir>/<cacheKey>.json
//
// Atomic writes: writes go to a temp file in cacheDir, then os.Rename to the
// final path. This prevents partial reads on crash.
//
// TTL check: based on StructuralContext.DetectedAt. Expired or corrupt entries
// are treated as soft misses (ErrCacheMiss, no error).
type FileCache struct {
	dir   string
	clock shared.Clock
	ttl   time.Duration
}

// NewFileCache constructs a FileCache.
//
//   - dir: directory for cache files (auto-created on first Write).
//   - clock: injectable clock (use shared.FixedClock in tests).
//   - ttl: maximum age of a cache entry (default 24h).
func NewFileCache(dir string, clock shared.Clock, ttl time.Duration) *FileCache {
	return &FileCache{dir: dir, clock: clock, ttl: ttl}
}

// Lookup returns a cached StructuralContext if one exists, is valid JSON, and
// is within TTL. Returns (nil, false, ErrCacheMiss) on any miss condition.
func (c *FileCache) Lookup(_ context.Context, cacheKey string) (*detector.StructuralContext, bool, error) {
	path := c.filePath(cacheKey)
	data, err := os.ReadFile(path) // #nosec G304 -- path is under the configured cache dir
	if os.IsNotExist(err) {
		return nil, false, initphase.ErrCacheMiss
	}
	if err != nil {
		return nil, false, initphase.ErrCacheMiss // soft miss on FS error
	}

	var sc detector.StructuralContext
	if err := json.Unmarshal(data, &sc); err != nil {
		// Corrupt cache file → soft miss.
		return nil, false, initphase.ErrCacheMiss
	}

	// Schema version check.
	if sc.SchemaVersion != detector.StructuralContextSchemaV1 {
		return nil, false, fmt.Errorf("%w: got %d want %d",
			initphase.ErrSchemaVersionMismatch, sc.SchemaVersion, detector.StructuralContextSchemaV1)
	}

	// TTL check: DetectedAt must be within ttl from now.
	age := c.clock.Now().Sub(sc.DetectedAt)
	if age > c.ttl {
		return nil, false, initphase.ErrCacheMiss
	}

	return &sc, true, nil
}

// Write persists sc to the cache atomically. The directory is auto-created.
// Overwrites existing entries — callers do not need to delete first.
func (c *FileCache) Write(_ context.Context, cacheKey string, sc detector.StructuralContext) error {
	if err := os.MkdirAll(c.dir, 0o750); err != nil {
		return fmt.Errorf("filecache: mkdir: %w", err)
	}

	data, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("filecache: marshal: %w", err)
	}

	// Atomic write: temp file + os.Rename.
	tmp, err := os.CreateTemp(c.dir, "*.tmp")
	if err != nil {
		return fmt.Errorf("filecache: create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("filecache: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("filecache: close temp: %w", err)
	}

	final := c.filePath(cacheKey)
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("filecache: rename: %w", err)
	}
	return nil
}

func (c *FileCache) filePath(cacheKey string) string {
	return filepath.Join(c.dir, cacheKey+".json")
}
