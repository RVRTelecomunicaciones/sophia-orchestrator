package memory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/memory"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, srv *httptest.Server) *memory.Client {
	t.Helper()
	cfg := memory.DefaultConfig(srv.URL, "test-key")
	cfg.HTTPBase.MaxAttempts = 1
	c, err := memory.New(cfg)
	require.NoError(t, err)
	return c
}

func TestNew_RejectsBadConfig(t *testing.T) {
	_, err := memory.New(memory.Config{})
	require.Error(t, err)
}

func TestIngest_Success(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/memories", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5MEM","created_at":"2026-05-03T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	rec, err := c.Ingest(context.Background(), outbound.IngestMemoryInput{
		Type:     "sdd_spec",
		Content:  "spec body",
		TopicKey: "sdd/feat-x/spec",
		Scope: outbound.MemoryScope{
			ProjectID: "proj", AgentID: "sophia-orchestator", SessionID: "01ARZ3NDEKTSV4RRFFQ69G5C01",
		},
		Provenance: outbound.MemoryProvenance{
			Source: "sophia-orchestator", Method: "sdd-phase-output",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5MEM", rec.ID)
	require.Equal(t, "sdd_spec", rec.Type)
	require.Equal(t, "sdd/feat-x/spec", rec.TopicKey)
	require.Equal(t, "sdd_spec", captured["type"])
	require.Equal(t, "sdd/feat-x/spec", captured["topic_key"])
}

func TestIngest_WithTemporal(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_, _ = w.Write([]byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5MEM","created_at":"2026-05-03T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	from := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	until := from.Add(24 * time.Hour)
	_, err := c.Ingest(context.Background(), outbound.IngestMemoryInput{
		Type: "x", Content: "y", ValidFrom: &from, ValidUntil: &until,
		Scope:      outbound.MemoryScope{ProjectID: "p"},
		Provenance: outbound.MemoryProvenance{Source: "x", Method: "m"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, captured["valid_from"])
	require.NotEmpty(t, captured["valid_until"])
}

func TestGet_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/memories/01ARZ3NDEKTSV4RRFFQ69G5MEM", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5MEM","type":"sdd_spec","status":"active","topic_key":"sdd/feat-x/spec","created_at":"2026-05-03T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	rec, err := c.Get(context.Background(), "01ARZ3NDEKTSV4RRFFQ69G5MEM")
	require.NoError(t, err)
	require.Equal(t, "sdd_spec", rec.Type)
	require.Equal(t, "active", rec.Status)
}

func TestArchive(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/memories/01ARZ3NDEKTSV4RRFFQ69G5MEM/archive", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"archived"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	require.NoError(t, c.Archive(context.Background(), "01ARZ3NDEKTSV4RRFFQ69G5MEM", "obsolete", "alice"))
	require.Equal(t, "obsolete", captured["reason"])
	require.Equal(t, "alice", captured["requested_by"])
}

func TestSearch_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/search", r.URL.Path)
		_, _ = w.Write([]byte(`{
			"results": [{"id":"1","record_type":"sdd_spec","title":"Spec","snippet":"...","score":0.9,"freshness":"fresh","created_at":"2026-05-03T12:00:00Z"}],
			"total_count": 1, "query": "spec"
		}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	res, err := c.Search(context.Background(), outbound.SearchQuery{
		Query: "spec", Scope: outbound.MemoryScope{ProjectID: "p"}, Limit: 10, Offset: 0,
	})
	require.NoError(t, err)
	require.Len(t, res.Results, 1)
	require.Equal(t, "Spec", res.Results[0].Title)
	require.Equal(t, 1, res.TotalCount)
}

func TestBuildContext_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{
			"sections": [{"type":"decisions","records":[{"id":"d1","type":"decision","content":"use bcrypt","score":0.9}],"token_count":50}],
			"total_tokens": 50, "truncated": false, "generated_at": "2026-05-03T12:00:00Z"
		}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	bundle, err := c.BuildContext(context.Background(), outbound.ContextRequest{
		Scope: outbound.MemoryScope{ProjectID: "p"}, MaxTokens: 4000, ExpandGraph: true,
	})
	require.NoError(t, err)
	require.Len(t, bundle.Sections, 1)
	require.Equal(t, 50, bundle.TotalTokens)
}

func TestRecordDecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/decisions", r.URL.Path)
		_, _ = w.Write([]byte(`{"id":"01ARZ3NDEKTSV4RRFFQ69G5DEC","created_at":"2026-05-03T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	rec, err := c.RecordDecision(context.Background(), outbound.RecordDecisionInput{
		Title: "use bcrypt", Decision: "approved", Rationale: "industry standard",
		Scope: outbound.MemoryScope{ProjectID: "p"}, Confidence: 0.9,
		Provenance: outbound.MemoryProvenance{Source: "sophia-orchestator", Method: "sdd-decision"},
	})
	require.NoError(t, err)
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5DEC", rec.ID)
	require.Equal(t, "decision", rec.Type)
}

func TestRecordRelation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/relations", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	require.NoError(t, c.RecordRelation(context.Background(), outbound.RecordRelationInput{
		FromID: "1", ToID: "2", RelationType: "supersedes",
	}))
}
