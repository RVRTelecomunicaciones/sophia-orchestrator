// Package webhook implements the outbound best-effort HTTP adapter that POSTs
// a PhaseArchivedWebhookPayload to the configured memory-engine URL after
// phase.archived is published. Per D-M2-1 and D-M2-14: fire-and-forget,
// no retry in M2, failures logged at WARN level, orch never blocks or errors.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// PhaseArchivedWebhookPayload mirrors inbound.PhaseArchivedPayload and is the
// body posted to POST /api/v1/worker/phase-archived on the memory-engine.
type PhaseArchivedWebhookPayload struct {
	ChangeID   string    `json:"change_id"`
	ChangeName string    `json:"change_name"`
	PhaseType  string    `json:"phase_type"`
	ArchivedAt time.Time `json:"archived_at"`
}

// Config holds the runtime parameters for the webhook adapter.
// URL is the full endpoint (e.g. "http://memory-engine:8080/api/v1/worker/phase-archived").
// Empty URL disables the adapter entirely with a one-time debug log.
type Config struct {
	// URL is the full POST endpoint. Empty = adapter disabled.
	URL string
	// APIKey is the bearer/API-key value sent in the X-API-Key header.
	APIKey string
	// Timeout is the per-request HTTP client timeout. Defaults to 5s.
	Timeout time.Duration
}

// Adapter sends outbound webhook notifications. All methods are safe for
// concurrent use.
type Adapter struct {
	cfg    Config
	client *http.Client
	once   sync.Once // for the "disabled" one-time log
}

// New constructs an Adapter. A zero Timeout falls back to 5 seconds.
func New(cfg Config) *Adapter {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Adapter{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// Notify sends the webhook payload asynchronously. It is fire-and-forget: any
// error (network, timeout, non-2xx) is logged at WARN with the change_id and
// never propagated to the caller. A disabled adapter (empty URL) logs once at
// DEBUG and returns immediately.
//
// Notify spawns a goroutine to perform the HTTP POST so the caller is never
// blocked. The provided context is NOT forwarded to the HTTP request to prevent
// the orch's request context cancellation from aborting the delivery.
func (a *Adapter) Notify(_ context.Context, payload PhaseArchivedWebhookPayload) {
	if a.cfg.URL == "" {
		a.once.Do(func() {
			slog.Debug("webhook adapter disabled: SOPHIA_MEMORY_WEBHOOK_URL is empty")
		})
		return
	}

	// Copy payload to avoid data races in the goroutine closure.
	p := payload
	go a.post(p)
}

// post performs the synchronous HTTP POST. Called inside a goroutine by Notify.
func (a *Adapter) post(payload PhaseArchivedWebhookPayload) {
	body, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("webhook: marshal payload failed",
			slog.String("change_id", payload.ChangeID),
			slog.String("error", err.Error()),
		)
		return
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, a.cfg.URL, bytes.NewReader(body))
	if err != nil {
		slog.Warn("webhook: build request failed",
			slog.String("change_id", payload.ChangeID),
			slog.String("error", err.Error()),
		)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", a.cfg.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		slog.Warn("webhook: delivery failed",
			slog.String("change_id", payload.ChangeID),
			slog.String("error", err.Error()),
			slog.String("webhook.delivery_status", "failed"),
		)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.Warn("webhook: non-2xx response",
			slog.String("change_id", payload.ChangeID),
			slog.Int("status_code", resp.StatusCode),
			slog.String("webhook.delivery_status", "failed"),
		)
		return
	}

	slog.Debug("webhook: delivered",
		slog.String("change_id", payload.ChangeID),
		slog.Int("status_code", resp.StatusCode),
		slog.String("webhook.delivery_status", fmt.Sprintf("%d", resp.StatusCode)),
	)
}
