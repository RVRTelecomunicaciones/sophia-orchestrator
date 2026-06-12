package webhook_test

// adapter_test.go — synchronous Deliver transport (loop-hardening D-LH-1).
//
// The relay poller calls Deliver(ctx, payload) and decides retry vs. mark
// based on the returned error. The fire-and-forget Notify goroutine path is
// gone; the POST body and API-key header stay byte-identical to the legacy
// contract so the ME receiver is untouched.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deliver POSTs the raw payload bytes verbatim with the API-key + JSON headers
// and returns nil on 2xx.
func TestAdapter_Deliver_PostsPayloadAndHeaders(t *testing.T) {
	var captured struct {
		body    []byte
		headers http.Header
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "test-api-key",
		Timeout: 5 * time.Second,
	})

	// Byte-identical payload: the relay delivers stored outbox bytes verbatim.
	payload := []byte(`{"change_id":"change-123","change_name":"my-change","phase_type":"archive","archived_at":"2026-01-01T00:00:00Z"}`)

	err := adapter.Deliver(context.Background(), payload)
	require.NoError(t, err)

	assert.Equal(t, "test-api-key", captured.headers.Get("X-API-Key"))
	assert.Equal(t, "application/json", captured.headers.Get("Content-Type"))
	assert.Equal(t, payload, captured.body, "POST body must be byte-identical to the stored payload")
}

// Network failure → non-nil error (relay reschedules).
func TestAdapter_Deliver_NetworkFailure_ReturnsError(t *testing.T) {
	adapter := webhook.New(webhook.Config{
		URL:     "http://127.0.0.1:1", // unreachable
		APIKey:  "key",
		Timeout: 100 * time.Millisecond,
	})
	err := adapter.Deliver(context.Background(), []byte(`{}`))
	require.Error(t, err)
}

// Timeout → non-nil error.
func TestAdapter_Deliver_Timeout_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "key",
		Timeout: 50 * time.Millisecond,
	})
	err := adapter.Deliver(context.Background(), []byte(`{}`))
	require.Error(t, err)
}

// Non-2xx → non-nil error (relay reschedules, row stays pending).
func TestAdapter_Deliver_Non2xx_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "key",
		Timeout: 5 * time.Second,
	})
	err := adapter.Deliver(context.Background(), []byte(`{}`))
	require.Error(t, err)
}

// Empty URL (disabled adapter) → error so the relay keeps the row pending
// rather than silently dropping it.
func TestAdapter_Deliver_EmptyURL_ReturnsError(t *testing.T) {
	adapter := webhook.New(webhook.Config{URL: "", APIKey: "key", Timeout: 5 * time.Second})
	err := adapter.Deliver(context.Background(), []byte(`{}`))
	require.Error(t, err)
}
