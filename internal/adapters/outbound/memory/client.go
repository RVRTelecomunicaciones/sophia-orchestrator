// Package memory implements outbound.MemoryClient against sophia-memory-engine's
// HTTP API. Endpoints used (per ADR-0003, amended by ADR-0005 P0.1+P0.2):
//
//   POST /api/v1/memories                       → Ingest
//   GET  /api/v1/memories/{id}                  → Get (preserves content)
//   GET  /api/v1/memories/by-topic-key          → GetByTopicKey
//   POST /api/v1/memories/{id}/archive          → Archive
//   POST /api/v1/search                         → Search
//   POST /api/v1/search/context                 → BuildContext
//   POST /api/v1/decisions                      → RecordDecision
//   POST /api/v1/relations                      → RecordRelation
//
// 404 responses on Get / GetByTopicKey are translated to outbound.ErrNotFound
// so application-layer callers can rely on errors.Is.
package memory

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Config tunes Client.
type Config struct {
	HTTPBase http_base.Config
	APIKey   string
}

// DefaultConfig returns production defaults.
func DefaultConfig(baseURL, apiKey string) Config {
	hb := http_base.DefaultConfig("memory-engine", baseURL)
	if apiKey != "" {
		hb.DefaultHeaders = http.Header{"X-API-Key": {apiKey}}
	}
	hb.HTTPTimeout = 15 * time.Second // BuildContext can be slow
	return Config{HTTPBase: hb, APIKey: apiKey}
}

// Client implements outbound.MemoryClient.
type Client struct {
	cfg  Config
	http *http_base.Client
}

// New constructs a Client.
func New(cfg Config) (*Client, error) {
	hc, err := http_base.New(cfg.HTTPBase)
	if err != nil {
		return nil, fmt.Errorf("memory client: %w", err)
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// --- wire shapes (mirroring sophia-memory-engine HTTP DTOs) ---

type scopeWire struct {
	TenantID    string `json:"tenant_id,omitempty"`
	ProjectID   string `json:"project_id"`
	RepoID      string `json:"repo_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	Environment string `json:"environment,omitempty"`
}

type provenanceWire struct {
	Source    string `json:"source"`
	SourceURI string `json:"source_uri,omitempty"`
	Method    string `json:"method"`
	ParentID  string `json:"parent_id,omitempty"`
}

type ingestRequest struct {
	Type        string         `json:"type"`
	Content     string         `json:"content"`
	Summary     string         `json:"summary,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	TopicKey    string         `json:"topic_key,omitempty"`
	FTSLanguage string         `json:"fts_language,omitempty"`
	Scope       scopeWire      `json:"scope"`
	Provenance  provenanceWire `json:"provenance"`
	ValidFrom   string         `json:"valid_from,omitempty"`
	ValidUntil  string         `json:"valid_until,omitempty"`
}

type ingestResponse struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

type memoryResponse struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	TopicKey  string    `json:"topic_key,omitempty"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type archiveRequest struct {
	Reason      string `json:"reason"`
	RequestedBy string `json:"requested_by"`
}

type searchRequest struct {
	Query  string    `json:"query"`
	Scope  scopeWire `json:"scope"`
	Types  []string  `json:"types,omitempty"`
	Limit  *int      `json:"limit,omitempty"`
	Offset *int      `json:"offset,omitempty"`
}

type searchResponse struct {
	Results []struct {
		ID         string    `json:"id"`
		RecordType string    `json:"record_type"`
		Title      string    `json:"title"`
		Snippet    string    `json:"snippet"`
		Score      float64   `json:"score"`
		Freshness  string    `json:"freshness"`
		CreatedAt  time.Time `json:"created_at"`
	} `json:"results"`
	TotalCount int    `json:"total_count"`
	Query      string `json:"query"`
}

type contextRequest struct {
	Scope        scopeWire `json:"scope"`
	Query        string    `json:"query,omitempty"`
	MaxTokens    *int      `json:"max_tokens,omitempty"`
	IncludeTypes []string  `json:"include_types,omitempty"`
	ExpandGraph  *bool     `json:"expand_graph,omitempty"`
}

type contextResponse struct {
	Sections []struct {
		Type    string `json:"type"`
		Records []struct {
			ID       string  `json:"id"`
			Type     string  `json:"type"`
			Content  string  `json:"content"`
			Score    float64 `json:"score"`
			Relation *string `json:"relation,omitempty"`
		} `json:"records"`
		TokenCount int `json:"token_count"`
	} `json:"sections"`
	TotalTokens int       `json:"total_tokens"`
	Truncated   bool      `json:"truncated"`
	GeneratedAt time.Time `json:"generated_at"`
}

type decisionRequest struct {
	Title      string         `json:"title"`
	Decision   string         `json:"decision"`
	Rationale  string         `json:"rationale"`
	Scope      scopeWire      `json:"scope"`
	Confidence float64        `json:"confidence"`
	Provenance provenanceWire `json:"provenance"`
}

type relationRequest struct {
	FromID       string `json:"from_id"`
	ToID         string `json:"to_id"`
	RelationType string `json:"relation_type"`
}

// --- methods ---

// Ingest POSTs /api/v1/memories.
func (c *Client) Ingest(ctx context.Context, in outbound.IngestMemoryInput) (*outbound.MemoryRecord, error) {
	req := ingestRequest{
		Type:        in.Type,
		Content:     in.Content,
		Summary:     in.Summary,
		Tags:        in.Tags,
		TopicKey:    in.TopicKey,
		FTSLanguage: in.FTSLanguage,
		Scope:       toScopeWire(in.Scope),
		Provenance:  toProvWire(in.Provenance),
	}
	if in.ValidFrom != nil {
		req.ValidFrom = in.ValidFrom.Format(time.RFC3339)
	}
	if in.ValidUntil != nil {
		req.ValidUntil = in.ValidUntil.Format(time.RFC3339)
	}
	var resp ingestResponse
	if err := c.http.PostJSON(ctx, "/api/v1/memories", req, &resp); err != nil {
		return nil, fmt.Errorf("memory Ingest: %w", err)
	}
	return &outbound.MemoryRecord{
		ID:        resp.ID,
		Type:      in.Type,
		TopicKey:  in.TopicKey,
		CreatedAt: resp.CreatedAt,
	}, nil
}

// Get GETs /api/v1/memories/{id}. Returns outbound.ErrNotFound on 404 so
// callers can branch on errors.Is(err, outbound.ErrNotFound).
func (c *Client) Get(ctx context.Context, id string) (*outbound.MemoryRecord, error) {
	var resp memoryResponse
	if err := c.http.GetJSON(ctx, "/api/v1/memories/"+id, &resp); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("memory Get: %w", outbound.ErrNotFound)
		}
		return nil, fmt.Errorf("memory Get: %w", err)
	}
	return &outbound.MemoryRecord{
		ID:        resp.ID,
		Type:      resp.Type,
		Status:    resp.Status,
		TopicKey:  resp.TopicKey,
		Content:   resp.Content,
		CreatedAt: resp.CreatedAt,
	}, nil
}

// GetByTopicKey GETs /api/v1/memories/by-topic-key. project_id and topicKey
// are required; remaining scope fields are forwarded as optional filters
// (server policy: latest active record wins). Returns outbound.ErrNotFound
// when the server reports 404. Validation of required fields happens
// client-side so we never hit the wire with an obviously-invalid request.
func (c *Client) GetByTopicKey(ctx context.Context, scope outbound.MemoryScope, topicKey string) (*outbound.MemoryRecord, error) {
	if scope.ProjectID == "" {
		return nil, fmt.Errorf("memory GetByTopicKey: %w: project_id required", outbound.ErrInvalidRequest)
	}
	if topicKey == "" {
		return nil, fmt.Errorf("memory GetByTopicKey: %w: topic_key required", outbound.ErrInvalidRequest)
	}

	q := url.Values{}
	q.Set("project_id", scope.ProjectID)
	q.Set("topic_key", topicKey)
	if scope.TenantID != "" {
		q.Set("tenant_id", scope.TenantID)
	}
	if scope.RepoID != "" {
		q.Set("repo_id", scope.RepoID)
	}
	if scope.AgentID != "" {
		q.Set("agent_id", scope.AgentID)
	}
	if scope.SessionID != "" {
		q.Set("session_id", scope.SessionID)
	}
	if scope.Environment != "" {
		q.Set("environment", scope.Environment)
	}

	path := "/api/v1/memories/by-topic-key?" + q.Encode()
	var resp memoryResponse
	if err := c.http.GetJSON(ctx, path, &resp); err != nil {
		if isNotFound(err) {
			return nil, fmt.Errorf("memory GetByTopicKey: %w", outbound.ErrNotFound)
		}
		return nil, fmt.Errorf("memory GetByTopicKey: %w", err)
	}
	return &outbound.MemoryRecord{
		ID:        resp.ID,
		Type:      resp.Type,
		Status:    resp.Status,
		TopicKey:  resp.TopicKey,
		Content:   resp.Content,
		CreatedAt: resp.CreatedAt,
	}, nil
}

// isNotFound returns true if err is a 404 response from http_base.
func isNotFound(err error) bool {
	var hbe *http_base.Error
	return errors.As(err, &hbe) && hbe.StatusCode == http.StatusNotFound
}

// Archive POSTs /api/v1/memories/{id}/archive.
func (c *Client) Archive(ctx context.Context, id, reason, requestedBy string) error {
	req := archiveRequest{Reason: reason, RequestedBy: requestedBy}
	if err := c.http.PostJSON(ctx, "/api/v1/memories/"+id+"/archive", req, nil); err != nil {
		return fmt.Errorf("memory Archive: %w", err)
	}
	return nil
}

// Search POSTs /api/v1/search.
func (c *Client) Search(ctx context.Context, q outbound.SearchQuery) (*outbound.SearchResults, error) {
	req := searchRequest{
		Query: q.Query,
		Scope: toScopeWire(q.Scope),
		Types: q.Types,
	}
	if q.Limit > 0 {
		req.Limit = &q.Limit
	}
	if q.Offset > 0 {
		req.Offset = &q.Offset
	}
	var resp searchResponse
	if err := c.http.PostJSON(ctx, "/api/v1/search", req, &resp); err != nil {
		return nil, fmt.Errorf("memory Search: %w", err)
	}
	out := &outbound.SearchResults{
		TotalCount: resp.TotalCount,
		Query:      resp.Query,
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, outbound.SearchResult{
			ID: r.ID, RecordType: r.RecordType, Title: r.Title,
			Snippet: r.Snippet, Score: r.Score,
			Freshness: r.Freshness, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

// BuildContext POSTs /api/v1/search/context.
func (c *Client) BuildContext(ctx context.Context, in outbound.ContextRequest) (*outbound.ContextBundle, error) {
	req := contextRequest{
		Scope:        toScopeWire(in.Scope),
		Query:        in.Query,
		IncludeTypes: in.IncludeTypes,
	}
	if in.MaxTokens > 0 {
		req.MaxTokens = &in.MaxTokens
	}
	if in.ExpandGraph {
		v := true
		req.ExpandGraph = &v
	}
	var resp contextResponse
	if err := c.http.PostJSON(ctx, "/api/v1/search/context", req, &resp); err != nil {
		return nil, fmt.Errorf("memory BuildContext: %w", err)
	}
	out := &outbound.ContextBundle{
		TotalTokens: resp.TotalTokens,
		Truncated:   resp.Truncated,
		GeneratedAt: resp.GeneratedAt,
	}
	for _, s := range resp.Sections {
		section := outbound.ContextSection{Type: s.Type, TokenCount: s.TokenCount}
		for _, r := range s.Records {
			rel := ""
			if r.Relation != nil {
				rel = *r.Relation
			}
			section.Records = append(section.Records, outbound.ContextRecord{
				ID: r.ID, Type: r.Type, Content: r.Content, Score: r.Score, Relation: rel,
			})
		}
		out.Sections = append(out.Sections, section)
	}
	return out, nil
}

// RecordDecision POSTs /api/v1/decisions.
func (c *Client) RecordDecision(ctx context.Context, in outbound.RecordDecisionInput) (*outbound.MemoryRecord, error) {
	req := decisionRequest{
		Title:      in.Title,
		Decision:   in.Decision,
		Rationale:  in.Rationale,
		Scope:      toScopeWire(in.Scope),
		Confidence: in.Confidence,
		Provenance: toProvWire(in.Provenance),
	}
	var resp ingestResponse
	if err := c.http.PostJSON(ctx, "/api/v1/decisions", req, &resp); err != nil {
		return nil, fmt.Errorf("memory RecordDecision: %w", err)
	}
	return &outbound.MemoryRecord{
		ID:        resp.ID,
		Type:      "decision",
		CreatedAt: resp.CreatedAt,
	}, nil
}

// RecordRelation POSTs /api/v1/relations.
func (c *Client) RecordRelation(ctx context.Context, in outbound.RecordRelationInput) error {
	req := relationRequest{
		FromID:       in.FromID,
		ToID:         in.ToID,
		RelationType: in.RelationType,
	}
	if err := c.http.PostJSON(ctx, "/api/v1/relations", req, nil); err != nil {
		return fmt.Errorf("memory RecordRelation: %w", err)
	}
	return nil
}

func toScopeWire(s outbound.MemoryScope) scopeWire {
	return scopeWire{
		TenantID: s.TenantID, ProjectID: s.ProjectID, RepoID: s.RepoID,
		AgentID: s.AgentID, SessionID: s.SessionID, Environment: s.Environment,
	}
}

func toProvWire(p outbound.MemoryProvenance) provenanceWire {
	return provenanceWire{
		Source: p.Source, SourceURI: p.SourceURI,
		Method: p.Method, ParentID: p.ParentID,
	}
}

// Compile-time interface check.
var _ outbound.MemoryClient = (*Client)(nil)
