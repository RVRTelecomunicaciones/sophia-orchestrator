// Package runtime implements outbound.RuntimeClient against
// sophia-runtime-adapters' HTTP API. The runtime exposes a single endpoint
// that accepts a capability + payload and returns an ExecutionReceipt:
//
//	POST /api/v1/execute
//
// Wire shape (request, snake_case per runtime D5.14):
//
//	correlation_id      string  // 26-char Crockford ULID, REQUIRED
//	adapter_id          string  // e.g. "shell"
//	capability_name     string  // e.g. "exec"
//	capability_version  string  // e.g. "v1"
//	payload             json    // RAW JSON object (NOT base64)
//	timeout_budget_ms   int64
//	idempotency_key     string  // optional
//	actor_id / task_id / workflow_run_id  string  // optional
//	retry_attempt       int     // optional
//	submitted_at        string  // RFC3339, runtime treats as informational
//
// Wire shape (response, ExecutionReceipt — nested):
//
//	receipt_id, schema_version, request{...}, handle{...},
//	result { status, retryable, exit_code, duration_ms,
//	         stdout_ref{ mode, data, size_bytes }, stderr_ref{...},
//	         completed_at, ... },
//	provenance{...}, timings { submitted_at, started_at, completed_at },
//	created_at, persisted_at (optional)
//
// Receipt status enum (R15 in runtime spec): success | failure | timeout
// | cancelled | partial. RetryHint enum: retryable | non_retryable | unknown.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Config tunes Client. Clock and Entropy are injectable for tests (R12);
// nil values fall back to shared.SystemClock and crypto/rand.Reader.
type Config struct {
	HTTPBase http_base.Config
	APIKey   string
	// Clock supplies submitted_at on every request. Injectable for tests.
	Clock shared.Clock
	// Entropy is the source of ULID randomness. Defaults to crypto/rand.
	Entropy io.Reader
}

// DefaultConfig returns production defaults.
func DefaultConfig(baseURL, apiKey string) Config {
	hb := http_base.DefaultConfig("runtime", baseURL)
	if apiKey != "" {
		hb.DefaultHeaders = http.Header{"X-API-Key": {apiKey}}
	}
	// HTTP client timeout MUST exceed the maximum inner timeout_budget_ms we
	// will ever send (apply phase default = 1_800_000ms = 30min). The HTTP
	// transport cancels the request when this elapses, so a tight setting
	// here makes runtime return timeout receipts even when the inner budget
	// would allow the subprocess to keep going (e.g. opencode + LLM call).
	// 35min gives a 5min buffer above the apply default; phase service uses
	// 600s so this also covers explore/proposal/spec/design/tasks.
	hb.HTTPTimeout = 35 * time.Minute
	hb.MaxAttempts = 1 // runtime is replay-everything; we don't retry on top
	return Config{HTTPBase: hb, APIKey: apiKey}
}

// Client implements outbound.RuntimeClient.
type Client struct {
	cfg     Config
	http    *http_base.Client
	clock   shared.Clock
	entropy io.Reader
}

// New constructs a Client. Defaults Clock to shared.SystemClock and
// Entropy to crypto/rand.Reader when not set.
func New(cfg Config) (*Client, error) {
	hc, err := http_base.New(cfg.HTTPBase)
	if err != nil {
		return nil, fmt.Errorf("runtime client: %w", err)
	}
	clk := cfg.Clock
	if clk == nil {
		clk = shared.SystemClock{}
	}
	ent := cfg.Entropy
	if ent == nil {
		ent = rand.Reader
	}
	return &Client{cfg: cfg, http: hc, clock: clk, entropy: ent}, nil
}

// --- request wire shape (matches runtime ExecutionRequest.MarshalJSON) ---

type executionRequest struct {
	CorrelationID     string          `json:"correlation_id"`
	AdapterID         string          `json:"adapter_id"`
	CapabilityName    string          `json:"capability_name"`
	CapabilityVersion string          `json:"capability_version"`
	Payload           json.RawMessage `json:"payload"`
	TimeoutBudgetMs   int64           `json:"timeout_budget_ms"`
	IdempotencyKey    string          `json:"idempotency_key,omitempty"`
	ActorID           string          `json:"actor_id,omitempty"`
	TaskID            string          `json:"task_id,omitempty"`
	WorkflowRunID     string          `json:"workflow_run_id,omitempty"`
	RetryAttempt      int             `json:"retry_attempt,omitempty"`
	SubmittedAt       string          `json:"submitted_at"`
}

// --- response wire shapes (mirror runtime entities.ExecutionReceipt) ---

type streamRefWire struct {
	Mode            string `json:"mode,omitempty"`
	Data            []byte `json:"data,omitempty"` // base64 on wire, native []byte in Go
	SizeBytes       int64  `json:"size_bytes,omitempty"`
	TruncatedAtByte int64  `json:"truncated_at_byte,omitempty"`
}

type executionResultWire struct {
	Status       string         `json:"status"`
	Retryable    string         `json:"retryable"`
	ErrorClass   string         `json:"error_class,omitempty"`
	ErrorMessage string         `json:"error_message,omitempty"`
	StdoutRef    *streamRefWire `json:"stdout_ref,omitempty"`
	StderrRef    *streamRefWire `json:"stderr_ref,omitempty"`
	ExitCode     *int           `json:"exit_code,omitempty"`
	DurationMs   int64          `json:"duration_ms"`
	CompletedAt  time.Time      `json:"completed_at"`
}

type executionHandleWire struct {
	HandleID      string    `json:"handle_id"`
	CorrelationID string    `json:"correlation_id"`
	AdapterID     string    `json:"adapter_id"`
	Capability    string    `json:"capability"`
	StartedAt     time.Time `json:"started_at"`
}

type executionReceiptResponse struct {
	ReceiptID     string              `json:"receipt_id"`
	SchemaVersion string              `json:"schema_version"`
	Handle        executionHandleWire `json:"handle"`
	Result        executionResultWire `json:"result"`
	// Request, Provenance, Timings are present but we only need a subset.
	Timings struct {
		SubmittedAt time.Time  `json:"submitted_at"`
		StartedAt   time.Time  `json:"started_at"`
		CompletedAt time.Time  `json:"completed_at"`
		PersistedAt *time.Time `json:"persisted_at,omitempty"`
	} `json:"timings"`
}

// parseCapability splits "<adapter>.<name>@<version>" into its parts.
// Returns an error if the format is invalid.
func parseCapability(canonical string) (adapter, name, version string, err error) {
	atIdx := strings.LastIndex(canonical, "@")
	if atIdx < 0 {
		return "", "", "", fmt.Errorf("capability missing @version: %q", canonical)
	}
	version = canonical[atIdx+1:]
	left := canonical[:atIdx]
	var found bool
	adapter, name, found = strings.Cut(left, ".")
	if !found {
		return "", "", "", fmt.Errorf("capability missing adapter.name: %q", canonical)
	}
	if adapter == "" || name == "" || version == "" {
		return "", "", "", fmt.Errorf("capability has empty part(s): %q", canonical)
	}
	return adapter, name, version, nil
}

// newCorrelationID mints a fresh 26-char Crockford-base32 ULID using the
// injected entropy reader and the clock's current time (millisecond
// precision per ulid.Timestamp).
func (c *Client) newCorrelationID() (string, error) {
	ms := ulid.Timestamp(c.clock.Now())
	id, err := ulid.New(ms, c.entropy)
	if err != nil {
		return "", fmt.Errorf("ulid: %w", err)
	}
	return id.String(), nil
}

// Execute POSTs /api/v1/execute and returns the typed receipt.
func (c *Client) Execute(ctx context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	adapter, name, version, err := parseCapability(req.Capability)
	if err != nil {
		return nil, fmt.Errorf("runtime Execute: %w", err)
	}
	cid, err := c.newCorrelationID()
	if err != nil {
		return nil, fmt.Errorf("runtime Execute: %w", err)
	}
	// Validate that req.Payload is valid JSON before sending; the runtime
	// will reject a malformed payload with 400.
	if len(req.Payload) == 0 || !json.Valid(req.Payload) {
		return nil, fmt.Errorf("runtime Execute: payload must be non-empty valid JSON")
	}
	wireReq := executionRequest{
		CorrelationID:     cid,
		AdapterID:         adapter,
		CapabilityName:    name,
		CapabilityVersion: version,
		Payload:           json.RawMessage(req.Payload),
		TimeoutBudgetMs:   int64(req.TimeoutMS),
		IdempotencyKey:    req.IdempotencyKey,
		SubmittedAt:       c.clock.Now().UTC().Format(time.RFC3339),
	}
	var resp executionReceiptResponse
	if err := c.http.PostJSON(ctx, "/api/v1/execute", wireReq, &resp); err != nil {
		return nil, fmt.Errorf("runtime Execute: %w", err)
	}
	// Map nested receipt → flat orch outbound.ExecutionReceipt.
	var stdout, stderr []byte
	if resp.Result.StdoutRef != nil {
		stdout = resp.Result.StdoutRef.Data
	}
	if resp.Result.StderrRef != nil {
		stderr = resp.Result.StderrRef.Data
	}
	exit := 0
	if resp.Result.ExitCode != nil {
		exit = *resp.Result.ExitCode
	}
	startedAt := resp.Handle.StartedAt
	if startedAt.IsZero() {
		startedAt = resp.Timings.StartedAt
	}
	endedAt := resp.Result.CompletedAt
	if endedAt.IsZero() {
		endedAt = resp.Timings.CompletedAt
	}
	return &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptStatus(resp.Result.Status),
		Stdout:     stdout,
		Stderr:     stderr,
		ExitCode:   exit,
		DurationMS: int(resp.Result.DurationMs),
		ReceiptID:  resp.ReceiptID,
		RetryHint:  outbound.RetryHint(resp.Result.Retryable),
		StartedAt:  startedAt,
		EndedAt:    endedAt,
	}, nil
}

// Compile-time interface check.
var _ outbound.RuntimeClient = (*Client)(nil)
