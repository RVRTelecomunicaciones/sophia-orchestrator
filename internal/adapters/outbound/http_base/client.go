// Package http_base provides a hardened HTTP client used by every outbound
// HTTP adapter (governance, memory, runtime, opencode dispatcher). It bundles:
//
//   - per-target circuit breaker (sony/gobreaker/v2) with separate consecutive-
//     failure threshold (3) and consecutive-timeout threshold (5)
//   - exponential backoff with ±30% jitter (default 100ms initial, 5s max,
//     up to 3 attempts)
//   - JSON helper methods (PostJSON, GetJSON) that fully read and close the
//     response body before returning so retries can replay safely
//   - structured Error type carrying status code + response body for callers
//
// One Client per target — never share a Client between governance and
// memory because the circuit-breaker state is target-specific.
package http_base

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sony/gobreaker/v2"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
)

// Sentinel errors raised by Client.
var (
	ErrCircuitOpen     = errors.New("http_base: circuit breaker open")
	ErrAttemptsExhausted = errors.New("http_base: max attempts exhausted")
)

// Result is the captured HTTP response. Body is fully read; the underlying
// response body is already closed by the time Result is returned.
type Result struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

// Error is returned when the server responded with status >= 400 after
// exhausting retries (or on a 4xx, which is non-retryable).
type Error struct {
	StatusCode int
	Body       []byte
	URL        string
}

// Error formats the HTTP error including a truncated body preview.
func (e *Error) Error() string {
	preview := string(e.Body)
	if len(preview) > 256 {
		preview = preview[:256] + "..."
	}
	return fmt.Sprintf("http_base: %s -> %d: %s", e.URL, e.StatusCode, preview)
}

// IsClient returns true for 4xx errors (non-retryable on the caller side).
func (e *Error) IsClient() bool { return e.StatusCode >= 400 && e.StatusCode < 500 }

// IsServer returns true for 5xx errors (retryable).
func (e *Error) IsServer() bool { return e.StatusCode >= 500 }

// Config tunes the Client.
type Config struct {
	BaseURL              string
	Name                 string // for circuit-breaker telemetry
	HTTPTimeout          time.Duration
	MaxAttempts          int
	InitialBackoff       time.Duration
	MaxBackoff           time.Duration
	BackoffMultiplier    float64
	JitterFraction       float64 // 0.3 = ±30%
	CBFailureThreshold   uint32  // consecutive hard-failure trip threshold
	CBTimeoutThreshold   uint32  // consecutive timeout trip threshold
	CBOpenDuration       time.Duration
	HalfOpenMaxRequests  uint32
	DefaultHeaders       http.Header
	// PropagateTrace controls W3C traceparent propagation (ADR-0005 P2.2a).
	// When true (the default for Sophia ecosystem clients), every outbound
	// request carries a Traceparent header whose trace_id matches the inbound
	// request and whose span_id is a fresh child span. Set to false only for
	// non-Sophia consumers of http_base that do not participate in the trace chain.
	PropagateTrace bool
}

// DefaultConfig returns sensible production defaults.
// PropagateTrace is true by default — all Sophia ecosystem outbound clients
// participate in W3C traceparent propagation out of the box.
func DefaultConfig(name, baseURL string) Config {
	return Config{
		BaseURL:             strings.TrimRight(baseURL, "/"),
		Name:                name,
		HTTPTimeout:         10 * time.Second,
		MaxAttempts:         3,
		InitialBackoff:      100 * time.Millisecond,
		MaxBackoff:          5 * time.Second,
		BackoffMultiplier:   2.0,
		JitterFraction:      0.3,
		CBFailureThreshold:  3,
		CBTimeoutThreshold:  5,
		CBOpenDuration:      30 * time.Second,
		HalfOpenMaxRequests: 1,
		PropagateTrace:      true,
	}
}

// Validate ensures the config is sane.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("http_base: BaseURL required")
	}
	if c.Name == "" {
		return errors.New("http_base: Name required")
	}
	if c.MaxAttempts <= 0 {
		return errors.New("http_base: MaxAttempts must be > 0")
	}
	if _, err := url.Parse(c.BaseURL); err != nil {
		return fmt.Errorf("http_base: invalid BaseURL: %w", err)
	}
	return nil
}

// Client is the hardened HTTP wrapper.
type Client struct {
	cfg        Config
	httpClient *http.Client
	cb         *gobreaker.CircuitBreaker[*Result]
	timeoutCnt uint32 // consecutive timeout count (separate from CB failures)
}

// New constructs a Client. Returns an error on invalid config.
func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c := &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.HTTPTimeout,
		},
	}
	c.cb = gobreaker.NewCircuitBreaker[*Result](gobreaker.Settings{
		Name:        cfg.Name,
		MaxRequests: cfg.HalfOpenMaxRequests,
		Timeout:     cfg.CBOpenDuration,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= cfg.CBFailureThreshold
		},
	})
	return c, nil
}

// PostJSON sends body as JSON and decodes the response into out. out may be
// nil if the response is ignored. Returns *Error on HTTP >= 400 (after
// retries exhausted on 5xx).
func (c *Client) PostJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, body, out)
}

// GetJSON issues a GET and decodes the response into out.
func (c *Client) GetJSON(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

// PutJSON sends a PUT with JSON body.
func (c *Client) PutJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPut, path, body, out)
}

// PatchJSON sends a PATCH with JSON body.
func (c *Client) PatchJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPatch, path, body, out)
}

// Delete issues a DELETE.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// do is the inner request executor wrapped by gobreaker.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("http_base: marshal body: %w", err)
		}
	}

	fullURL := c.cfg.BaseURL + path

	// Resolve a child-span context once per logical call (not per retry) so
	// all retry attempts share the same child span_id, which keeps log
	// correlation clean. If ctx has no parent Trace, propagation is silently
	// skipped when PropagateTrace is false; when true, a fresh top-level Trace
	// is generated so the outbound call is still traceable.
	outCtx := ctx
	if c.cfg.PropagateTrace {
		childCtx, _, _ := trace.ChildSpan(ctx, rand.Reader)
		outCtx = childCtx
	}

	result, err := c.cb.Execute(func() (*Result, error) {
		res, retryErr := c.executeWithRetry(outCtx, method, fullURL, payload)
		if retryErr != nil {
			return nil, retryErr
		}
		// 5xx after retries counts as a circuit-breaker failure. 4xx is a
		// client error and does NOT trip the breaker (the upstream is healthy).
		if res.StatusCode >= 500 {
			return res, fmt.Errorf("http_base %s: upstream %d", c.cfg.Name, res.StatusCode)
		}
		return res, nil
	})
	if err != nil {
		if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
			return fmt.Errorf("%w: %s", ErrCircuitOpen, c.cfg.Name)
		}
		// 5xx with body — return structured Error.
		if result != nil {
			return &Error{StatusCode: result.StatusCode, Body: result.Body, URL: fullURL}
		}
		return err
	}

	if result.StatusCode >= 400 {
		return &Error{StatusCode: result.StatusCode, Body: result.Body, URL: fullURL}
	}
	if out != nil && len(result.Body) > 0 {
		if err := json.Unmarshal(result.Body, out); err != nil {
			return fmt.Errorf("http_base: decode response: %w", err)
		}
	}
	return nil
}

// executeWithRetry implements the inner exponential-backoff retry loop.
// 4xx responses are NOT retried (client error). 5xx and network errors are.
func (c *Client) executeWithRetry(ctx context.Context, method, fullURL string, payload []byte) (*Result, error) {
	var lastErr error
	for attempt := 0; attempt < c.cfg.MaxAttempts; attempt++ {
		req, err := buildRequest(ctx, method, fullURL, payload, c.cfg.DefaultHeaders, c.cfg.PropagateTrace)
		if err != nil {
			return nil, err
		}
		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			lastErr = doErr
			if !c.shouldRetry(doErr, 0, attempt) {
				return nil, fmt.Errorf("http_base %s: %w", c.cfg.Name, doErr)
			}
			c.sleepBackoff(ctx, attempt)
			continue
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			c.sleepBackoff(ctx, attempt)
			continue
		}
		result := &Result{StatusCode: resp.StatusCode, Headers: resp.Header, Body: body}
		if resp.StatusCode >= 500 && attempt < c.cfg.MaxAttempts-1 {
			lastErr = fmt.Errorf("http_base: server returned %d", resp.StatusCode)
			c.sleepBackoff(ctx, attempt)
			continue
		}
		return result, nil
	}
	if lastErr != nil {
		return nil, fmt.Errorf("%w: %v", ErrAttemptsExhausted, lastErr)
	}
	return nil, ErrAttemptsExhausted
}

// shouldRetry returns true for retryable network errors.
func (c *Client) shouldRetry(err error, _ int, attempt int) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return attempt < c.cfg.MaxAttempts-1
}

// sleepBackoff sleeps for the computed exponential backoff with jitter,
// honoring ctx cancellation.
func (c *Client) sleepBackoff(ctx context.Context, attempt int) {
	d := c.computeBackoff(attempt)
	if d <= 0 {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func (c *Client) computeBackoff(attempt int) time.Duration {
	d := c.cfg.InitialBackoff
	for i := 0; i < attempt; i++ {
		d = time.Duration(float64(d) * c.cfg.BackoffMultiplier)
	}
	if d > c.cfg.MaxBackoff {
		d = c.cfg.MaxBackoff
	}
	if c.cfg.JitterFraction > 0 {
		// Apply ±jitter as fraction of d.
		jitter := float64(d) * c.cfg.JitterFraction
		offset := (mathrand.Float64()*2 - 1) * jitter //nolint:gosec // backoff jitter, not crypto
		d = time.Duration(float64(d) + offset)
		if d < 0 {
			d = 0
		}
	}
	return d
}

func buildRequest(ctx context.Context, method, fullURL string, payload []byte, defaultHeaders http.Header, propagateTrace bool) (*http.Request, error) {
	var bodyReader io.Reader
	if len(payload) > 0 {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("http_base: build request: %w", err)
	}
	for k, vs := range defaultHeaders {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if len(payload) > 0 && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// Inject W3C traceparent from the context only when propagation is enabled.
	// The child span was already generated by do() before the retry loop so we
	// read it directly from ctx here — no new random I/O on each retry.
	if propagateTrace {
		if t, ok := trace.FromContext(ctx); ok {
			req.Header.Set("Traceparent", t.String())
		}
	}
	return req, nil
}

// State returns the circuit-breaker state (closed/open/half-open). Useful
// for observability (export as Prometheus gauge).
func (c *Client) State() string {
	return c.cb.State().String()
}
