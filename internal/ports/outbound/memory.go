package outbound

import (
	"context"
	"time"
)

// MemoryClient is the outbound port for sophia-memory-engine.
// Operations are a curated subset of the memory-engine HTTP API matched to
// what the orchestrator needs (per ADR-0003, amended by ADR-0005 P0.1+P0.2):
//
//   POST /api/v1/memories                       → Ingest
//   GET  /api/v1/memories/{id}                  → Get (returns full record incl. content)
//   GET  /api/v1/memories/by-topic-key          → GetByTopicKey (latest active record)
//   POST /api/v1/memories/{id}/archive          → Archive
//   POST /api/v1/search                         → Search
//   POST /api/v1/search/context                 → BuildContext
//   POST /api/v1/decisions                      → RecordDecision
//   POST /api/v1/relations                      → RecordRelation
//
// Heuristics, feedback, profile, and purge endpoints are intentionally not
// exposed to the orchestrator — those are used by other ecosystem clients.
//
// Both Get and GetByTopicKey populate MemoryRecord.Content with the full
// record body (string; may carry JSON, markdown, or any text). Callers that
// need bytes can convert via []byte(rec.Content).
type MemoryClient interface {
	Ingest(ctx context.Context, in IngestMemoryInput) (*MemoryRecord, error)
	Get(ctx context.Context, id string) (*MemoryRecord, error)
	GetByTopicKey(ctx context.Context, scope MemoryScope, topicKey string) (*MemoryRecord, error)
	Archive(ctx context.Context, id, reason, requestedBy string) error
	Search(ctx context.Context, q SearchQuery) (*SearchResults, error)
	BuildContext(ctx context.Context, in ContextRequest) (*ContextBundle, error)
	RecordDecision(ctx context.Context, in RecordDecisionInput) (*MemoryRecord, error)
	RecordRelation(ctx context.Context, in RecordRelationInput) error
}

// MemoryScope mirrors sophia-memory-engine's multi-scope record model.
// At minimum project_id is required; tenant is filled from config.
type MemoryScope struct {
	TenantID    string
	ProjectID   string
	RepoID      string
	AgentID     string // typically "sophia-orchestator"
	SessionID   string // typically the change_id
	Environment string // dev | staging | prod
}

// MemoryProvenance carries sophia-memory-engine's provenance record.
type MemoryProvenance struct {
	Source    string // "sophia-orchestator"
	SourceURI string
	Method    string // "sdd-phase-output" | "sdd-audit" | "sdd-decision" | ...
	ParentID  string
}

// IngestMemoryInput is the wire shape for POST /api/v1/memories.
type IngestMemoryInput struct {
	Type        string // "sdd_proposal" | "sdd_spec" | ... | "sdd_audit"
	Content     string
	Summary     string
	Tags        []string
	TopicKey    string // "sdd/{change_name}/{phase_type}" — upsert key
	FTSLanguage string // optional, defaults to "english"
	Scope       MemoryScope
	Provenance  MemoryProvenance
	ValidFrom   *time.Time
	ValidUntil  *time.Time
}

// MemoryRecord is a returned record. Content carries the full record body
// (markdown, JSON, or any text) and is populated by Get and GetByTopicKey.
// Ingest returns a record with Content empty (the caller already has it).
// For the full record shape returned by sophia-memory-engine, see its docs;
// only fields actually consumed by the orchestrator are echoed here.
type MemoryRecord struct {
	ID        string
	Type      string
	Status    string
	TopicKey  string
	Content   string
	CreatedAt time.Time
}

// SearchQuery is the wire shape for POST /api/v1/search.
type SearchQuery struct {
	Query  string
	Scope  MemoryScope
	Types  []string
	Limit  int
	Offset int
}

// SearchResults is the response shape (subset).
type SearchResults struct {
	Results    []SearchResult
	TotalCount int
	Query      string
}

// SearchResult is one result in SearchResults.
type SearchResult struct {
	ID         string
	RecordType string
	Title      string
	Snippet    string
	Score      float64
	Freshness  string
	CreatedAt  time.Time
}

// ContextRequest is the wire shape for POST /api/v1/search/context.
type ContextRequest struct {
	Scope        MemoryScope
	Query        string
	MaxTokens    int
	IncludeTypes []string
	ExpandGraph  bool
}

// ContextBundle is the response shape.
type ContextBundle struct {
	Sections    []ContextSection
	TotalTokens int
	Truncated   bool
	GeneratedAt time.Time
}

// ContextSection is one logical section (e.g., "decisions", "heuristics").
type ContextSection struct {
	Type       string
	Records    []ContextRecord
	TokenCount int
}

// ContextRecord is one record in a ContextSection.
type ContextRecord struct {
	ID       string
	Type     string
	Content  string
	Score    float64
	Relation string
}

// RecordDecisionInput is the wire shape for POST /api/v1/decisions.
type RecordDecisionInput struct {
	Title      string
	Decision   string
	Rationale  string
	Scope      MemoryScope
	Confidence float64
	Provenance MemoryProvenance
}

// RecordRelationInput is the wire shape for POST /api/v1/relations.
type RecordRelationInput struct {
	FromID       string
	ToID         string
	RelationType string // "supersedes" | "contradicts" | "derived_from" | ...
}
