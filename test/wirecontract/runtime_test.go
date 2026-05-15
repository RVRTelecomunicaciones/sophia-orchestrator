//go:build wirecontract

package wirecontract_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// Matrix row #4: POST /api/v1/execute
func TestRuntime_Execute_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{
		"receipt_id": "01ARZ3NDEKTSV4RRFFQ69G5RC1",
		"schema_version": "v1",
		"handle": {
			"handle_id": "h1",
			"correlation_id": "c1",
			"adapter_id": "shell",
			"capability": "exec@v1",
			"started_at": "2026-05-15T00:00:00Z"
		},
		"result": {
			"status": "success",
			"retryable": "no",
			"duration_ms": 0,
			"completed_at": "2026-05-15T00:00:00Z"
		},
		"timings": {
			"submitted_at": "2026-05-15T00:00:00Z",
			"started_at": "2026-05-15T00:00:00Z",
			"completed_at": "2026-05-15T00:00:00Z"
		}
	}`)

	cfg := runtime.DefaultConfig(srv.URL, "test-key")
	client, err := runtime.New(cfg)
	require.NoError(t, err)

	_, err = client.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    []byte(`{"command":"echo","args":["hi"]}`),
		TimeoutMS:  1000,
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/api/v1/execute", 4)
}
