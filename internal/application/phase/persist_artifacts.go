package phase

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// persistArtifactsToMemory iterates envelope.ArtifactsSaved and persists
// each declared artifact to memory-engine via MemoryClient.Ingest.
//
// Why this exists: the envelope contract has the LLM DECLARE which
// artifacts the phase produced (`artifacts_saved: [{topic_key, type}, ...]`),
// but until this method existed nobody actually persisted them — the
// declaration was a write-only field. opencode/ollama/aider don't run
// an MCP memory tool, so the orch must carry out the write itself for
// downstream phases to be able to read prior outputs via topic_key.
//
// Contract:
//   - Skips silently when len(env.ArtifactsSaved) == 0 (nothing to do).
//   - For each ArtifactRef, calls MemoryClient.Ingest with the FULL
//     envelope JSON as Content (so downstream phases get both the
//     declared data + the metadata the LLM emitted around it). Type is
//     namespaced as "sdd_<phase>" to align with the typed-memory
//     convention sophia-memory-engine uses for SDD outputs.
//   - On per-artifact failure: emits EventMemoryArtifactPersistFailed
//     with phase_id + topic_key + type + err and continues to the next
//     entry. The phase stays in its already-persisted state — Iron Law
//     #1 already guaranteed PhaseRepo.Save before this method ran.
//
// Idempotency: relies on memory-engine's partial unique index
// (migration 004 in sophia-memory-engine, topic_key uniqueness for
// active rows). A retry of the same envelope upserts in place rather
// than producing a duplicate — see UpsertByTopicKey on the memory
// side.
func (s *Service) persistArtifactsToMemory(ctx context.Context, c *change.Change, p *phase.Phase, env *envelope.Envelope) {
	if env == nil || len(env.ArtifactsSaved) == 0 {
		return
	}

	// One canonical content payload per envelope: the full JSON, so
	// readers can reconstruct both data + metadata. Marshalled once
	// and reused across all ArtifactsSaved entries (saves cycles when
	// the LLM declares multiple artifacts pointing at the same body).
	content, err := json.Marshal(env)
	if err != nil {
		// Marshal of a struct that just deserialized cleanly should
		// not fail in practice. If it does, emit one consolidated
		// failure event and return — the underlying envelope cannot
		// be reconstructed.
		for _, ref := range env.ArtifactsSaved {
			s.publishEvent(ctx, p.ID(), inbound.EventMemoryArtifactPersistFailed,
				inbound.MemoryArtifactPersistFailedPayload{
					PhaseID:  p.ID().String(),
					TopicKey: ref.TopicKey,
					Type:     ref.Type,
					Err:      fmt.Sprintf("marshal envelope: %v", err),
				})
		}
		return
	}

	scope := outbound.MemoryScope{
		ProjectID: c.Project(),
		// TenantID is wired via ServiceConfig.MemoryTenantID and
		// reflects the operator's auth-scope binding on the
		// memory-engine API key. Single-tenant deployments leave it
		// empty; multi-tenant deployments MUST set it to match the
		// API key's bound tenant or memory-engine returns 403.
		TenantID:  s.d.Config.MemoryTenantID,
		AgentID:   "sophia-orchestator",
		// Carry the change ID as session so a memory consumer can
		// trace back which orchestration cycle produced the artifact.
		SessionID: c.ID().String(),
	}
	// Method MUST be one of memory-engine's IngestMethod enum values:
	// direct | derived | imported | worker_generated (see
	// sophia-memory-engine/internal/domain/shared/enums.go). The orch's
	// SDD-phase output is "direct" — the agent (via the orch) created
	// the artifact in this turn rather than deriving it from a parent
	// memory record. Earlier drafts used a free-form string here; the
	// memory-engine API rejected that with HTTP 400 / validation_error
	// "invalid ingest method".
	prov := outbound.MemoryProvenance{
		Source:    "sophia-orchestator",
		Method:    "direct",
		SourceURI: fmt.Sprintf("orchestator://changes/%s/phases/%s", c.ID(), p.ID()),
	}

	for _, ref := range env.ArtifactsSaved {
		if ref.TopicKey == "" {
			// An ArtifactRef without a topic_key cannot be upserted
			// (memory-engine's idempotency relies on the key). Skip
			// rather than create a topic-less row.
			continue
		}
		in := outbound.IngestMemoryInput{
			// Type MUST match memory-engine's MemoryType enum: only
			// "episodic" (atomic events) or "semantic" (structured
			// knowledge derived from reasoning) are accepted. SDD
			// phase outputs are reasoning artifacts → semantic. The
			// per-phase distinction (explore vs spec vs apply) lives
			// on Tags so downstream queries can still filter by phase.
			Type:       "semantic",
			Tags:       []string{"sdd", string(p.Type()), ref.Type},
			Content:    string(content),
			Summary:    env.ExecutiveSummary,
			TopicKey:   ref.TopicKey,
			Scope:      scope,
			Provenance: prov,
		}
		_, ingestErr := s.d.Memory.Ingest(ctx, in)
		s.recordMemoryCall("ingest", ingestErr)
		if ingestErr != nil {
			s.publishEvent(ctx, p.ID(), inbound.EventMemoryArtifactPersistFailed,
				inbound.MemoryArtifactPersistFailedPayload{
					PhaseID:  p.ID().String(),
					TopicKey: ref.TopicKey,
					Type:     ref.Type,
					Err:      ingestErr.Error(),
				})
			continue
		}
	}
}

