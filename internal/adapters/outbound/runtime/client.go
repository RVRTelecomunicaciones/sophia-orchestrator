// Package runtime implements outbound.RuntimeClient against
// sophia-runtime-adapters' HTTP API. The runtime exposes a single endpoint
// that accepts a capability + payload and returns an ExecutionReceipt:
//
//   POST /api/v1/execute
//
// Receipt status enum (R15 in runtime spec): success | failure | timeout
// | cancelled | partial. RetryHint enum: retryable | non_retryable | unknown.
package runtime

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Config tunes Client.
type Config struct {
	HTTPBase http_base.Config
	APIKey   string
}

// DefaultConfig returns production defaults.
func DefaultConfig(baseURL, apiKey string) Config {
	hb := http_base.DefaultConfig("runtime", baseURL)
	if apiKey != "" {
		hb.DefaultHeaders = http.Header{"X-API-Key": {apiKey}}
	}
	hb.HTTPTimeout = 30 * time.Second // shell.exec subprocess can be long
	hb.MaxAttempts = 1                // runtime is replay-everything; we don't retry on top
	return Config{HTTPBase: hb, APIKey: apiKey}
}

// Client implements outbound.RuntimeClient.
type Client struct {
	cfg  Config
	http *http_base.Client
}

// New constructs a Client.
func New(cfg Config) (*Client, error) {
	hc, err := http_base.New(cfg.HTTPBase)
	if err != nil {
		return nil, fmt.Errorf("runtime client: %w", err)
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// --- wire shapes ---

type executionRequest struct {
	Capability     string `json:"capability"`
	PayloadBase64  string `json:"payload_b64"`
	TimeoutMS      int    `json:"timeout_ms"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type executionReceiptResponse struct {
	Status        string    `json:"status"`
	StdoutBase64  string    `json:"stdout_b64,omitempty"`
	StderrBase64  string    `json:"stderr_b64,omitempty"`
	ExitCode      int       `json:"exit_code"`
	DurationMS    int       `json:"duration_ms"`
	ReceiptID     string    `json:"receipt_id"`
	RetryHint     string    `json:"retry_hint,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
}

// Execute POSTs /api/v1/execute and returns the typed receipt.
func (c *Client) Execute(ctx context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	wireReq := executionRequest{
		Capability:     req.Capability,
		PayloadBase64:  base64.StdEncoding.EncodeToString(req.Payload),
		TimeoutMS:      req.TimeoutMS,
		IdempotencyKey: req.IdempotencyKey,
	}
	var resp executionReceiptResponse
	if err := c.http.PostJSON(ctx, "/api/v1/execute", wireReq, &resp); err != nil {
		return nil, fmt.Errorf("runtime Execute: %w", err)
	}
	stdout, err := base64.StdEncoding.DecodeString(resp.StdoutBase64)
	if err != nil {
		return nil, fmt.Errorf("runtime decode stdout: %w", err)
	}
	stderr, err := base64.StdEncoding.DecodeString(resp.StderrBase64)
	if err != nil {
		return nil, fmt.Errorf("runtime decode stderr: %w", err)
	}
	return &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptStatus(resp.Status),
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   resp.ExitCode,
		DurationMS: resp.DurationMS,
		ReceiptID:  resp.ReceiptID,
		RetryHint:  outbound.RetryHint(resp.RetryHint),
		StartedAt:  resp.StartedAt,
		EndedAt:    resp.EndedAt,
	}, nil
}

// Compile-time interface check.
var _ outbound.RuntimeClient = (*Client)(nil)
