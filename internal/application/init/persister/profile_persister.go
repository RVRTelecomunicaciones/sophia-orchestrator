// Package persister provides DualPersister and ProfileMemoryPersister.
// ProfileMemoryPersister implements the initphase.ProfilePersister port: it
// writes a ConventionProfile to sophia-memory-engine via MemoryClient.Ingest.
//
// Type: "convention_profile"
// Topic key: "convention/<projectID>/<framework>"
// Payload: JSON-serialised ConventionProfile (using domain getters).
//
// Errors returned by Ingest are surfaced to the caller, which treats them as
// non-fatal WARN (the INIT phase never hard-fails on profile persistence).
package persister

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Compile-time assertion: ProfileMemoryPersister must satisfy the
// initphase.ProfilePersister port.
var _ initphase.ProfilePersister = (*ProfileMemoryPersister)(nil)

// profilePayload is the JSON shape written to memory-engine for a
// convention_profile record. We use domain getters to avoid coupling on
// internal ConventionProfile struct layout.
type profilePayload struct {
	ProjectID  string                  `json:"project_id"`
	Framework  string                  `json:"framework"`
	Version    string                  `json:"version"`
	DetectedAt string                  `json:"detected_at"`
	Patterns   []convention.PatternEntry `json:"patterns"`
}

// ProfileMemoryPersister writes a ConventionProfile to sophia-memory-engine.
type ProfileMemoryPersister struct {
	memory   outbound.MemoryClient
	logger   *slog.Logger
	tenantID string
	env      string
}

// NewProfileMemoryPersister constructs a ProfileMemoryPersister.
//
//   - memory:   the outbound.MemoryClient (reused from phase.Service wiring).
//   - logger:   optional slog logger (nil = discard).
//   - tenantID: SOPHIA_MEMORY_TENANT_ID config value (may be empty).
//   - env:      "dev" | "staging" | "prod" from config.
func NewProfileMemoryPersister(memory outbound.MemoryClient, logger *slog.Logger, tenantID, env string) *ProfileMemoryPersister {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(nilWriter{}, nil))
	}
	return &ProfileMemoryPersister{
		memory:   memory,
		logger:   logger,
		tenantID: tenantID,
		env:      env,
	}
}

// PersistProfile serialises profile and calls MemoryClient.Ingest with
// Type="convention_profile" and TopicKey="convention/<projectID>/<framework>".
// Returns the Ingest error (if any) so the caller can log a WARN and continue.
func (p *ProfileMemoryPersister) PersistProfile(ctx context.Context, profile convention.ConventionProfile) error {
	payload := profilePayload{
		ProjectID:  profile.ProjectID(),
		Framework:  profile.Framework(),
		Version:    profile.Version(),
		DetectedAt: profile.DetectedAt().UTC().Format("2006-01-02T15:04:05Z"),
		Patterns:   profile.Patterns(),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("profile persister: marshal: %w", err)
	}

	topicKey := "convention/" + profile.ProjectID() + "/" + profile.Framework()

	_, err = p.memory.Ingest(ctx, outbound.IngestMemoryInput{
		Type:     "convention_profile",
		Content:  string(body),
		TopicKey: topicKey,
		Tags:     []string{"convention", "profile", profile.Framework(), "schema_v1"},
		Scope: outbound.MemoryScope{
			TenantID:    p.tenantID,
			ProjectID:   profile.ProjectID(),
			AgentID:     "sophia-orchestator",
			Environment: p.env,
		},
		Provenance: outbound.MemoryProvenance{
			Source: "sophia-orchestator",
			Method: "sdd-init-profile",
		},
	})
	if err != nil {
		return fmt.Errorf("profile persister: memory-engine ingest: %w", err)
	}

	p.logger.Info("profile persister: convention profile ingested",
		"topic_key", topicKey,
		"framework", profile.Framework(),
		"patterns", len(profile.Patterns()),
	)
	return nil
}
