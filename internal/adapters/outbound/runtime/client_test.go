package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/runtime"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

func newClient(t *testing.T, srv *httptest.Server) *runtime.Client {
	t.Helper()
	cfg := runtime.DefaultConfig(srv.URL, "test-key")
	cfg.HTTPBase.MaxAttempts = 1
	cfg.Clock = shared.FixedClock(time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC))
	c, err := runtime.New(cfg)
	require.NoError(t, err)
	return c
}

// successReceipt builds a runtime-shaped ExecutionReceipt JSON for tests.
// stdout/stderr are raw bytes; the runtime serializes StreamRef.Data as
// base64 automatically (Go's default []byte JSON encoding).
func successReceipt(t *testing.T, stdout, stderr []byte, exit int) []byte {
	t.Helper()
	exitCode := exit
	doc := map[string]any{
		"receipt_id":     "01HZZZ0000000000000000RECT",
		"schema_version": "v1",
		"request": map[string]any{
			"correlation_id":     "01HZZZ0000000000000000CORL",
			"adapter_id":         "shell",
			"capability_name":    "exec",
			"capability_version": "v1",
			"payload":            json.RawMessage(`{"cmd":"echo hi"}`),
			"timeout_budget_ms":  5000,
			"submitted_at":       "2026-05-14T12:00:00.000Z",
		},
		"handle": map[string]any{
			"handle_id":      "01HZZZ0000000000000000HNDL",
			"correlation_id": "01HZZZ0000000000000000CORL",
			"adapter_id":     "shell",
			"capability":     "shell.exec@v1",
			"started_at":     "2026-05-14T12:00:00.000Z",
		},
		"result": map[string]any{
			"status":       "success",
			"retryable":    "non_retryable",
			"exit_code":    exitCode,
			"stdout_ref":   map[string]any{"mode": "inline", "data": stdout, "size_bytes": len(stdout)},
			"stderr_ref":   map[string]any{"mode": "inline", "data": stderr, "size_bytes": len(stderr)},
			"artifacts":    []any{},
			"adapter_meta": map[string]any{},
			"duration_ms":  123,
			"completed_at": "2026-05-14T12:00:01.000Z",
		},
		"provenance": map[string]any{
			"source":          "http",
			"source_version":  "v1",
			"host":            "test",
			"runtime_version": "v0",
		},
		"timings": map[string]any{
			"submitted_at": "2026-05-14T12:00:00.000Z",
			"started_at":   "2026-05-14T12:00:00.000Z",
			"completed_at": "2026-05-14T12:00:01.000Z",
		},
		"created_at": "2026-05-14T12:00:01.000Z",
	}
	b, err := json.Marshal(doc)
	require.NoError(t, err)
	return b
}

func TestNew_RejectsBadConfig(t *testing.T) {
	_, err := runtime.New(runtime.Config{})
	require.Error(t, err)
}

// TestExecute_WirePayloadShape verifies the POST body contains all the
// runtime-required snake_case fields, that capability is split into the
// 3 separated fields, that timeout_budget_ms replaces timeout_ms, and
// that the payload is RAW JSON (not base64-encoded).
func TestExecute_WirePayloadShape(t *testing.T) {
	var captured map[string]any
	var rawPayload json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/execute", r.URL.Path)
		// Decode into a struct that preserves payload as RawMessage to
		// inspect the on-wire shape (not the decoded form).
		var probe struct {
			CorrelationID     string          `json:"correlation_id"`
			AdapterID         string          `json:"adapter_id"`
			CapabilityName    string          `json:"capability_name"`
			CapabilityVersion string          `json:"capability_version"`
			Payload           json.RawMessage `json:"payload"`
			TimeoutBudgetMs   int64           `json:"timeout_budget_ms"`
			IdempotencyKey    string          `json:"idempotency_key"`
			SubmittedAt       string          `json:"submitted_at"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&probe))
		rawPayload = probe.Payload
		captured = map[string]any{
			"correlation_id":     probe.CorrelationID,
			"adapter_id":         probe.AdapterID,
			"capability_name":    probe.CapabilityName,
			"capability_version": probe.CapabilityVersion,
			"timeout_budget_ms":  probe.TimeoutBudgetMs,
			"idempotency_key":    probe.IdempotencyKey,
			"submitted_at":       probe.SubmittedAt,
		}
		_, _ = w.Write(successReceipt(t, []byte("hello"), []byte(""), 0))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability:     "shell.exec@v1",
		Payload:        []byte(`{"cmd":"echo hi"}`),
		TimeoutMS:      5000,
		IdempotencyKey: "key-001",
	})
	require.NoError(t, err)

	// Capability split into 3 separated fields.
	require.Equal(t, "shell", captured["adapter_id"])
	require.Equal(t, "exec", captured["capability_name"])
	require.Equal(t, "v1", captured["capability_version"])
	// New field names.
	require.EqualValues(t, 5000, captured["timeout_budget_ms"])
	require.Equal(t, "key-001", captured["idempotency_key"])
	require.NotEmpty(t, captured["submitted_at"])
	// CorrelationID is a 26-char Crockford ULID.
	cid, _ := captured["correlation_id"].(string)
	require.Len(t, cid, 26, "correlation_id must be a 26-char ULID")
	// Payload is RAW JSON on the wire — not base64. Should round-trip
	// to the exact bytes we sent.
	require.JSONEq(t, `{"cmd":"echo hi"}`, string(rawPayload))
	// Sanity: rawPayload starts with '{' (object), not a quoted string
	// (which would be base64).
	require.Equal(t, byte('{'), rawPayload[0], "payload must be raw JSON object, not a base64 string")
}

// TestExecute_CorrelationIDIsFreshULID verifies that two calls produce
// two distinct 26-char ULIDs.
func TestExecute_CorrelationIDIsFreshULID(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var probe struct {
			CorrelationID string `json:"correlation_id"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&probe))
		seen = append(seen, probe.CorrelationID)
		_, _ = w.Write(successReceipt(t, nil, nil, 0))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	for i := 0; i < 2; i++ {
		_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
			Capability: "shell.exec@v1",
			Payload:    []byte(`{}`),
			TimeoutMS:  1000,
		})
		require.NoError(t, err)
	}
	require.Len(t, seen, 2)
	require.Len(t, seen[0], 26)
	require.Len(t, seen[1], 26)
	require.NotEqual(t, seen[0], seen[1], "each call must mint a fresh ULID")
}

// TestExecute_ParsesNestedReceipt verifies the orch decodes the runtime's
// nested ExecutionReceipt (status/retryable/exit_code/stdout_ref live under
// result, started_at under handle/timings) into the flat outbound.ExecutionReceipt.
func TestExecute_ParsesNestedReceipt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(successReceipt(t, []byte("hello world"), []byte("warn"), 0))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	receipt, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", Payload: []byte(`{"cmd":"echo hi"}`), TimeoutMS: 5000,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.ReceiptSuccess, receipt.Status)
	require.Equal(t, "hello world", string(receipt.Stdout))
	require.Equal(t, "warn", string(receipt.Stderr))
	require.Equal(t, 0, receipt.ExitCode)
	require.Equal(t, 123, receipt.DurationMS)
	require.Equal(t, "01HZZZ0000000000000000RECT", receipt.ReceiptID)
	require.Equal(t, outbound.RetryNonRetryable, receipt.RetryHint)
	require.False(t, receipt.StartedAt.IsZero())
	require.False(t, receipt.EndedAt.IsZero())
}

// TestExecute_FailureWithRetryHint exercises a nested failure receipt.
func TestExecute_FailureWithRetryHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		exit := 1
		doc := map[string]any{
			"receipt_id":     "01HZZZ0000000000000000RECF",
			"schema_version": "v1",
			"handle": map[string]any{
				"handle_id":      "01HZZZ0000000000000000HNDF",
				"correlation_id": "01HZZZ0000000000000000CORF",
				"adapter_id":     "shell",
				"capability":     "shell.exec@v1",
				"started_at":     "2026-05-14T12:00:00.000Z",
			},
			"result": map[string]any{
				"status":        "failure",
				"retryable":     "retryable",
				"error_class":   "transient",
				"error_message": "temporary failure",
				"exit_code":     exit,
				"stderr_ref":    map[string]any{"mode": "inline", "data": []byte("temporary failure"), "size_bytes": len("temporary failure")},
				"duration_ms":   42,
				"completed_at":  "2026-05-14T12:00:01.000Z",
			},
			"provenance": map[string]any{
				"source": "http", "source_version": "v1", "host": "test", "runtime_version": "v0",
			},
			"timings": map[string]any{
				"submitted_at": "2026-05-14T12:00:00.000Z",
				"started_at":   "2026-05-14T12:00:00.000Z",
				"completed_at": "2026-05-14T12:00:01.000Z",
			},
			"created_at": "2026-05-14T12:00:01.000Z",
		}
		b, err := json.Marshal(doc)
		require.NoError(t, err)
		_, _ = w.Write(b)
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
	require.Equal(t, 1, receipt.ExitCode)
}

// TestExecute_BadCapabilityFormat verifies the client rejects a malformed
// capability string before making any HTTP request.
func TestExecute_BadCapabilityFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server must not be called for bad capability")
	}))
	defer srv.Close()

	c := newClient(t, srv)
	cases := []string{
		"",
		"shell.exec",   // missing @version
		"shellexec@v1", // missing .name
		"shell.@v1",    // empty name
		".exec@v1",     // empty adapter
		"shell.exec@",  // empty version
	}
	for _, canonical := range cases {
		canonical := canonical
		t.Run(canonical, func(t *testing.T) {
			_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
				Capability: canonical, Payload: []byte(`{}`), TimeoutMS: 1000,
			})
			require.Error(t, err)
		})
	}
}

// TestExecute_HTTPError verifies non-2xx responses propagate as errors.
func TestExecute_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", Payload: []byte(`{}`), TimeoutMS: 1000,
	})
	require.Error(t, err)
}

// TestExecute_RejectsInvalidPayload verifies the client refuses to send
// non-JSON payloads, since the runtime requires a valid JSON object.
func TestExecute_RejectsInvalidPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server must not be called when payload is invalid")
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, err := c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", Payload: []byte(`not-json`), TimeoutMS: 1000,
	})
	require.Error(t, err)

	_, err = c.Execute(context.Background(), outbound.ExecutionRequest{
		Capability: "shell.exec@v1", Payload: nil, TimeoutMS: 1000,
	})
	require.Error(t, err)
}
