//go:build wirecontract

package wirecontract_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/memory"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// memoryConfig builds a default Config aimed at the supplied test server.
func memoryConfig(baseURL string) memory.Config {
	cfg := memory.DefaultConfig(baseURL, "test-key")
	return cfg
}

// scope returns a minimal valid MemoryScope used across all memory tests.
func scope() outbound.MemoryScope {
	return outbound.MemoryScope{
		ProjectID:   "wire-test",
		AgentID:     "sophia-orchestator",
		SessionID:   "01ARZ3NDEKTSV4RRFFQ69G5C01",
		Environment: "dev",
	}
}

// Matrix row #5: POST /api/v1/memories
func TestMemory_Ingest_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"id":"m1","type":"sdd_spec","status":"active","topic_key":"k","created_at":"2026-05-15T00:00:00Z"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.Ingest(context.Background(), outbound.IngestMemoryInput{
		Type:     "sdd_spec",
		Content:  "wire-test content",
		TopicKey: "wire-test/key",
		Scope:    scope(),
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/memories", 5)
}

// Matrix row #6: GET /api/v1/memories/by-topic-key
func TestMemory_GetByTopicKey_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"id":"m1","type":"sdd_spec","status":"active","topic_key":"wire-test/key","content":"x","created_at":"2026-05-15T00:00:00Z"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.GetByTopicKey(context.Background(), scope(), "wire-test/key")
	require.NoError(t, err)

	assertRoute(t, capt, "GET", "/api/v1/memories/by-topic-key", 6)
}

// Matrix row #7: GET /api/v1/memories/{id}
func TestMemory_Get_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"id":"m-fixed-id","type":"sdd_spec","status":"active","topic_key":"k","content":"x","created_at":"2026-05-15T00:00:00Z"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.Get(context.Background(), "m-fixed-id")
	require.NoError(t, err)

	assertRoutePrefix(t, capt, "GET", "/api/v1/memories/", 7)
}

// Matrix row #8: POST /api/v1/memories/{id}/archive
func TestMemory_Archive_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	err = client.Archive(context.Background(), "m-fixed-id", "wire-test", "tester")
	require.NoError(t, err)

	// Path is /api/v1/memories/{id}/archive — assert it ends with /archive.
	method, path, _ := capt.snapshot()
	require.Equal(t, "POST", method, "wire-contract drift on row #8")
	require.Equal(t, "/api/v1/memories/m-fixed-id/archive", path,
		"wire-contract drift on row #8: see docs/architecture/wire-contracts.md")
}

// Matrix row #9: POST /api/v1/search
func TestMemory_Search_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"results":[],"total_count":0,"query":"q"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.Search(context.Background(), outbound.SearchQuery{
		Query: "wire-test",
		Scope: scope(),
		Limit: 10,
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/search", 9)
}

// Matrix row #10: POST /api/v1/search/context
func TestMemory_BuildContext_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"sections":[],"total_tokens":0,"truncated":false,"generated_at":"2026-05-15T00:00:00Z"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.BuildContext(context.Background(), outbound.ContextRequest{
		Scope:     scope(),
		Query:     "wire-test",
		MaxTokens: 1000,
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/search/context", 10)
}

// Matrix row #11: POST /api/v1/decisions
func TestMemory_RecordDecision_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"id":"d1","type":"decision","status":"active","topic_key":"k","created_at":"2026-05-15T00:00:00Z"}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	_, err = client.RecordDecision(context.Background(), outbound.RecordDecisionInput{
		Title:      "wire-test decision",
		Decision:   "allow",
		Rationale:  "test",
		Scope:      scope(),
		Confidence: 0.9,
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/decisions", 11)
}

// Matrix row #12: POST /api/v1/relations
func TestMemory_RecordRelation_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{}`)

	client, err := memory.New(memoryConfig(srv.URL))
	require.NoError(t, err)

	err = client.RecordRelation(context.Background(), outbound.RecordRelationInput{
		FromID:       "m1",
		ToID:         "m2",
		RelationType: "supersedes",
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/relations", 12)
}
