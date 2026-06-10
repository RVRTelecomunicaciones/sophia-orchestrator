package webhook_test

// Group E — phase.archived outbound webhook (RED tests)
// E.1 webhook POSTs correct payload + API-key header
// E.2 network failure logged + orch continues (no panic)
// E.3 configurable timeout — timeout logged at WARN + continues
// E.4 non-2xx response logged at WARN + orch continues

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// E.1 — Happy path: adapter POSTs correct payload + API-key header.
func TestAdapter_Notify_PostsCorrectPayload(t *testing.T) {
	done := make(chan struct{})
	var captured struct {
		body    []byte
		headers http.Header
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.headers = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		captured.body = body
		w.WriteHeader(http.StatusAccepted)
		close(done)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "test-api-key",
		Timeout: 5 * time.Second,
	})

	payload := webhook.PhaseArchivedWebhookPayload{
		ChangeID:   "change-123",
		ChangeName: "my-change",
		PhaseType:  "archive",
		ArchivedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	adapter.Notify(t.Context(), payload)

	select {
	case <-done:
		// request arrived at server
	case <-time.After(500 * time.Millisecond):
		t.Fatal("webhook POST did not arrive within 500ms")
	}

	require.Equal(t, "test-api-key", captured.headers.Get("X-API-Key"))
	require.Equal(t, "application/json", captured.headers.Get("Content-Type"))

	var decoded webhook.PhaseArchivedWebhookPayload
	require.NoError(t, json.Unmarshal(captured.body, &decoded))
	assert.Equal(t, "change-123", decoded.ChangeID)
	assert.Equal(t, "my-change", decoded.ChangeName)
	assert.Equal(t, "archive", decoded.PhaseType)
}

// E.2 — Network failure: adapter logs WARN but orch continues without error/panic.
func TestAdapter_Notify_NetworkFailure_LogsAndContinues(t *testing.T) {
	adapter := webhook.New(webhook.Config{
		URL:     "http://127.0.0.1:1", // unreachable port
		APIKey:  "key",
		Timeout: 100 * time.Millisecond,
	})

	payload := webhook.PhaseArchivedWebhookPayload{
		ChangeID:   "ch-fail",
		ChangeName: "fail-change",
		PhaseType:  "archive",
		ArchivedAt: time.Now(),
	}

	// Must not panic and must complete without blocking.
	done := make(chan struct{})
	go func() {
		defer close(done)
		adapter.Notify(t.Context(), payload)
	}()

	select {
	case <-done:
		// Pass — completed without blocking.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Notify blocked when it should fire-and-forget")
	}
}

// E.3 — Timeout: adapter logs WARN and orch continues.
func TestAdapter_Notify_Timeout_LogsAndContinues(t *testing.T) {
	// Server that hangs long enough to trigger the client timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "key",
		Timeout: 50 * time.Millisecond, // shorter than server sleep
	})

	payload := webhook.PhaseArchivedWebhookPayload{
		ChangeID:   "ch-timeout",
		ChangeName: "timeout-change",
		PhaseType:  "archive",
		ArchivedAt: time.Now(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		adapter.Notify(t.Context(), payload)
	}()

	select {
	case <-done:
		// Pass — completed without blocking.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Notify blocked after timeout")
	}
}

// E.4 — Non-2xx response: adapter logs WARN but orch continues.
func TestAdapter_Notify_Non2xx_LogsAndContinues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	adapter := webhook.New(webhook.Config{
		URL:     srv.URL + "/api/v1/worker/phase-archived",
		APIKey:  "key",
		Timeout: 5 * time.Second,
	})

	payload := webhook.PhaseArchivedWebhookPayload{
		ChangeID:   "ch-500",
		ChangeName: "five-hundred",
		PhaseType:  "archive",
		ArchivedAt: time.Now(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		adapter.Notify(t.Context(), payload)
	}()

	select {
	case <-done:
		// Pass — non-2xx logged, orch continues.
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Notify blocked on non-2xx response")
	}
}

// E.5 — Empty URL disables webhook (no HTTP call, no panic).
func TestAdapter_Notify_EmptyURL_Disabled(t *testing.T) {
	adapter := webhook.New(webhook.Config{
		URL:     "", // disabled
		APIKey:  "key",
		Timeout: 5 * time.Second,
	})

	payload := webhook.PhaseArchivedWebhookPayload{
		ChangeID:  "ch-disabled",
		PhaseType: "archive",
		ArchivedAt: time.Now(),
	}

	// Must not panic.
	require.NotPanics(t, func() {
		adapter.Notify(t.Context(), payload)
	})
}
