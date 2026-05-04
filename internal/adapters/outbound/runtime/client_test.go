package runtime_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, srv *httptest.Server) *runtime.Client {
	t.Helper()
	cfg := runtime.DefaultConfig(srv.URL, "test-key")
	cfg.HTTPBase.MaxAttempts = 1
	c, err := runtime.New(cfg)
	require.NoError(t, err)
	return c
}

func TestNew_RejectsBadConfig(t *testing.T) {
	_, err := runtime.New(runtime.Config{})
	require.Error(t, err)
}

func TestExecute_Success(t *testing.T) {
	var capturedReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/executions", r.URL.Path)
		_ = json.NewDecoder(r.Body).Decode(&capturedReq)

		resp := map[string]any{
			"status":      "success",
			"stdout_b64":  base64.StdEncoding.EncodeToString([]byte("hello world")),
			"stderr_b64":  base64.StdEncoding.EncodeToString([]byte("")),
			"exit_code":   0,
			"duration_ms": 123,
			"receipt_id":  "rec_001",
			"retry_hint":  "non_retryable",
			"started_at":  "2026-05-03T12:00:00Z",
			"ended_at":    "2026-05-03T12:00:01Z",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	receipt, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability:     "shell.exec@v1",
		Payload:        []byte(`{"cmd":"echo hi"}`),
		TimeoutMS:      5000,
		IdempotencyKey: "key-001",
	})
	require.NoError(t, err)
	require.Equal(t, outbound.ReceiptSuccess, receipt.Status)
	require.Equal(t, "hello world", string(receipt.Stdout))
	require.Equal(t, 0, receipt.ExitCode)
	require.Equal(t, "rec_001", receipt.ReceiptID)
	require.Equal(t, outbound.RetryNonRetryable, receipt.RetryHint)
	require.Equal(t, "shell.exec@v1", capturedReq["capability"])
	require.Equal(t, "key-001", capturedReq["idempotency_key"])
}

func TestExecute_FailureWithRetryHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"status":     "failure",
			"exit_code":  1,
			"retry_hint": "retryable",
			"receipt_id": "rec_002",
			"stdout_b64": base64.StdEncoding.EncodeToString([]byte("")),
			"stderr_b64": base64.StdEncoding.EncodeToString([]byte("temporary failure")),
			"started_at": "2026-05-03T12:00:00Z",
			"ended_at":   "2026-05-03T12:00:01Z",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	receipt, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", Payload: []byte(`{}`), TimeoutMS: 1000,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.ReceiptFailure, receipt.Status)
	require.Equal(t, outbound.RetryRetryable, receipt.RetryHint)
	require.Equal(t, "temporary failure", string(receipt.Stderr))
}

func TestExecute_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"status":    "timeout",
			"exit_code": -1,
			"started_at": "2026-05-03T12:00:00Z",
			"ended_at":   "2026-05-03T12:00:30Z",
			"stdout_b64": "",
			"stderr_b64": "",
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	receipt, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", TimeoutMS: 1,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.ReceiptTimeout, receipt.Status)
}

func TestExecute_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{Capability: "x"})
	require.Error(t, err)
}

func TestExecute_BadBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"success","stdout_b64":"not-valid-base64-!@#","exit_code":0,"started_at":"2026-05-03T12:00:00Z","ended_at":"2026-05-03T12:00:01Z"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{Capability: "x"})
	require.Error(t, err)
}
