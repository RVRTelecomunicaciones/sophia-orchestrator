// Package webhook implements the outbound HTTP transport that POSTs a
// phase.archived payload to the configured memory-engine URL. Since
// loop-hardening (D-LH-1) delivery is driven by the transactional outbox +
// relay poller, not a fire-and-forget goroutine: the relay calls Deliver
// synchronously and decides retry vs. mark-delivered from the returned error.
// The POST body and X-API-Key header stay byte-identical to the legacy
// contract so the ME receiver requires no change.
package webhook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrDisabled is returned by Deliver when the adapter has no URL configured.
// The relay treats this as a delivery failure and keeps the row pending rather
// than silently dropping the event.
var ErrDisabled = errors.New("webhook: adapter disabled (empty URL)")

// PhaseArchivedWebhookPayload mirrors inbound.PhaseArchivedPayload. It is the
// body shape posted to POST /api/v1/worker/phase-archived on the memory-engine.
// The relay delivers stored outbox bytes verbatim; this type documents the
// contract and is used by tests.
type PhaseArchivedWebhookPayload struct {
	ChangeID   string    `json:"change_id"`
	ChangeName string    `json:"change_name"`
	PhaseType  string    `json:"phase_type"`
	ArchivedAt time.Time `json:"archived_at"`
}

// Config holds the runtime parameters for the webhook adapter.
type Config struct {
	// URL is the full POST endpoint. Empty = adapter disabled (Deliver errors).
	URL string
	// APIKey is the value sent in the X-API-Key header.
	APIKey string
	// Timeout is the per-request HTTP client timeout. Defaults to 5s.
	Timeout time.Duration
}

// Adapter sends outbound webhook deliveries. It is safe for concurrent use.
type Adapter struct {
	cfg    Config
	client *http.Client
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

// Deliver synchronously POSTs the raw payload bytes to the memory-engine and
// returns nil on a 2xx response. Any disabled adapter, transport error,
// timeout, or non-2xx status yields a non-nil error so the relay reschedules
// the outbox row with backoff. The payload is written verbatim — the relay is
// responsible for storing byte-identical JSON.
func (a *Adapter) Deliver(ctx context.Context, payload []byte) error {
	if a.cfg.URL == "" {
		return ErrDisabled
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if a.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", a.cfg.APIKey)
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: deliver: %w", err)
	}
	defer func() {
		// Drain before close to enable connection reuse; ignore read error.
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: non-2xx response: status %d", resp.StatusCode)
	}
	return nil
}
