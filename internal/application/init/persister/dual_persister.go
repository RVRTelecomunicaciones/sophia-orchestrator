// Package persister provides DualPersister: writes StructuralContext to both
// sophia-memory-engine (HARD) and the local file cache (SOFT).
//
// Design D-INIT-2 + D-INIT-8: memory-engine is the primary durable store
// (cross-session); the file cache is the fast-path secondary store. If the
// memory-engine write fails the phase should surface the error. If the file
// cache write fails only a WARN is logged.
package persister

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// fileCacheWriter is a narrow interface for what DualPersister needs from the
// file cache. This avoids importing the full cache package.
type fileCacheWriter interface {
	Write(ctx context.Context, cacheKey string, sc detector.StructuralContext) error
}

// DualPersister satisfies the initphase.StructuralPersister port interface.
type DualPersister struct {
	memory outbound.MemoryClient
	cache  fileCacheWriter
	logger *slog.Logger

	// tenantID and env are injected from config for the MemoryScope.
	tenantID string
	env      string
}

// New constructs a DualPersister.
//
//   - memory:   the outbound.MemoryClient (reused from phase.Service wiring).
//   - cache:    the file cache writer (initphase.CacheStore or FileCache).
//   - logger:   optional slog logger (nil = discard).
//   - tenantID: SOPHIA_MEMORY_TENANT_ID config value (may be empty).
//   - env:      "dev" | "staging" | "prod" from config.
func New(memory outbound.MemoryClient, cache fileCacheWriter, logger *slog.Logger, tenantID, env string) *DualPersister {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(nilWriter{}, nil))
	}
	return &DualPersister{
		memory:   memory,
		cache:    cache,
		logger:   logger,
		tenantID: tenantID,
		env:      env,
	}
}

// Persist writes sc to memory-engine (HARD) then to file cache (SOFT).
// Memory-engine failure returns the error immediately (no file write attempted).
// File-cache failure logs a WARN and returns nil.
func (p *DualPersister) Persist(ctx context.Context, sc detector.StructuralContext, cacheKey string) error {
	body, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("persister: marshal: %w", err)
	}

	topicKey := "sdd/" + sc.ChangeName + "/init"

	// HARD path: memory-engine.
	_, err = p.memory.Ingest(ctx, outbound.IngestMemoryInput{
		Type:     "sdd_init",
		Content:  string(body),
		TopicKey: topicKey,
		Tags:     []string{"sdd", "init", "structural_context", "schema_v1"},
		Scope: outbound.MemoryScope{
			TenantID:    p.tenantID,
			ProjectID:   sc.ProjectID,
			SessionID:   sc.ChangeID,
			AgentID:     "sophia-orchestator",
			Environment: p.env,
		},
		Provenance: outbound.MemoryProvenance{
			Source: "sophia-orchestator",
			Method: "sdd-phase-output",
		},
	})
	if err != nil {
		return fmt.Errorf("persister: memory-engine ingest: %w", err)
	}

	// SOFT path: file cache.
	if cerr := p.cache.Write(ctx, cacheKey, sc); cerr != nil {
		p.logger.Warn("persister: file cache write failed (soft)",
			"cache_key", cacheKey,
			"error", cerr.Error(),
		)
	}

	return nil
}

// nilWriter discards log output.
type nilWriter struct{}

func (nilWriter) Write(p []byte) (n int, err error) { return len(p), nil }
