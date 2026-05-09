package memory_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// --- ADR-0005 P0.1: Get preserves Content -----------------------------------

func TestClient_Get_PreservesContent(t *testing.T) {
	body := `{"id":"01ARZ3NDEKTSV4RRFFQ69G5MEM","type":"sdd_tasks","status":"active","topic_key":"sdd/foo/tasks","content":"{\"groups\":[]}","created_at":"2026-05-03T12:00:00Z"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	rec, err := c.Get(context.Background(), "01ARZ3NDEKTSV4RRFFQ69G5MEM")
	require.NoError(t, err)
	require.Equal(t, `{"groups":[]}`, rec.Content,
		"Get must populate MemoryRecord.Content (P0.1 wire-shape fix)")
}

func TestClient_Get_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Get(context.Background(), "01ARZ3NDEKTSV4RRFFQ69G5MEM")
	require.Error(t, err)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

// --- ADR-0005 P0.2: GetByTopicKey -------------------------------------------

func TestClient_GetByTopicKey_HappyPath(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path + "?" + r.URL.RawQuery
		require.Equal(t, "/api/v1/memories/by-topic-key", r.URL.Path)
		_, _ = w.Write([]byte(`{
			"id":"01ARZ3NDEKTSV4RRFFQ69G5TKS","type":"sdd_tasks","status":"active",
			"topic_key":"sdd/feat-x/tasks","content":"{\"groups\":[{\"name\":\"g1\"}]}",
			"created_at":"2026-05-03T12:00:00Z"
		}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	rec, err := c.GetByTopicKey(context.Background(), outbound.MemoryScope{
		TenantID:    "t1",
		ProjectID:   "proj",
		RepoID:      "r1",
		AgentID:     "sophia-orchestator",
		SessionID:   "01ARZ3NDEKTSV4RRFFQ69G5C01",
		Environment: "dev",
	}, "sdd/feat-x/tasks")
	require.NoError(t, err)
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5TKS", rec.ID)
	require.Equal(t, "sdd_tasks", rec.Type)
	require.Equal(t, "active", rec.Status)
	require.Equal(t, "sdd/feat-x/tasks", rec.TopicKey)
	require.Equal(t, `{"groups":[{"name":"g1"}]}`, rec.Content)

	// All optional scope fields should be in the query string.
	require.Contains(t, capturedPath, "project_id=proj")
	require.Contains(t, capturedPath, "topic_key=sdd%2Ffeat-x%2Ftasks")
	require.Contains(t, capturedPath, "tenant_id=t1")
	require.Contains(t, capturedPath, "repo_id=r1")
	require.Contains(t, capturedPath, "agent_id=sophia-orchestator")
	require.Contains(t, capturedPath, "session_id=01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.Contains(t, capturedPath, "environment=dev")
}

func TestClient_GetByTopicKey_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.GetByTopicKey(context.Background(), outbound.MemoryScope{ProjectID: "proj"}, "sdd/missing/tasks")
	require.Error(t, err)
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestClient_GetByTopicKey_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.GetByTopicKey(context.Background(), outbound.MemoryScope{ProjectID: "proj"}, "sdd/feat-x/tasks")
	require.Error(t, err)
	require.False(t, errors.Is(err, outbound.ErrNotFound), "5xx must NOT be mapped to ErrNotFound")
}

func TestClient_GetByTopicKey_EmptyOptionalScope(t *testing.T) {
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path + "?" + r.URL.RawQuery
		_, _ = w.Write([]byte(`{"id":"x","type":"sdd_tasks","status":"active","topic_key":"k","content":"{}","created_at":"2026-05-03T12:00:00Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.GetByTopicKey(context.Background(), outbound.MemoryScope{ProjectID: "proj"}, "k")
	require.NoError(t, err)
	require.Contains(t, capturedPath, "project_id=proj")
	require.Contains(t, capturedPath, "topic_key=k")
	for _, opt := range []string{"tenant_id", "repo_id", "agent_id", "session_id", "environment"} {
		require.False(t, strings.Contains(capturedPath, opt+"="),
			"empty optional scope %q must not appear in the URL: %s", opt, capturedPath)
	}
}

func TestClient_GetByTopicKey_RequiredParams(t *testing.T) {
	// Server should never be hit; using a dead URL would also work but the
	// client-side validation must fail before the wire.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := newClient(t, srv)

	_, err := c.GetByTopicKey(context.Background(), outbound.MemoryScope{ProjectID: ""}, "k")
	require.Error(t, err)
	require.ErrorIs(t, err, outbound.ErrInvalidRequest)

	_, err = c.GetByTopicKey(context.Background(), outbound.MemoryScope{ProjectID: "p"}, "")
	require.Error(t, err)
	require.ErrorIs(t, err, outbound.ErrInvalidRequest)

	require.False(t, called, "validation must short-circuit before any HTTP call")
}
