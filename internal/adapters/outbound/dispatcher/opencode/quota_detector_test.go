package opencode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/dispatcher/opencode"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// realQuota429Fixture returns the contents of testdata/quota_429_real.txt —
// the actual stdout captured during a live session when the Anthropic provider
// returned HTTP 429 quota_exceeded internally while opencode exited 0
// (receipt.Status = "success"). Pinned to validate the detector against the
// real observed log shape. See ADR-0010.
func realQuota429Fixture(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "quota_429_real.txt"))
	require.NoError(t, err, "testdata/quota_429_real.txt must exist")
	return string(data)
}

// ---------------------------------------------------------------------------
// detectProviderQuota — table-driven tests via the Dispatch boundary
// ---------------------------------------------------------------------------

// TestDetectProviderQuota covers all spec scenarios from
// spec.md §Quota Signal Detection.
func TestDetectProviderQuota(t *testing.T) {
	real429 := realQuota429Fixture(t)

	tests := []struct {
		name           string
		stdout         string
		stderr         string
		receiptStatus  outbound.ReceiptStatus
		wantQuota      bool
		wantRetryAfter int // 0 = absent / not asserted
	}{
		{
			// Spec §Scenario: Success receipt hiding internal 429 quota (stdout)
			name:           "success_receipt_real_429_in_stdout",
			stdout:         real429,
			stderr:         "",
			receiptStatus:  outbound.ReceiptSuccess,
			wantQuota:      true,
			wantRetryAfter: 86400,
		},
		{
			// Spec §Scenario: Success receipt hiding internal 429 quota (stderr)
			name:           "success_receipt_real_429_in_stderr",
			stdout:         "some normal output, no envelope",
			stderr:         real429,
			receiptStatus:  outbound.ReceiptSuccess,
			wantQuota:      true,
			wantRetryAfter: 86400,
		},
		{
			// Spec §Scenario: Failure receipt with quota signals
			name:           "failure_receipt_with_quota_tokens",
			stdout:         "",
			stderr:         "maxRetriesExceeded statusCode=429 x-ratelimit-exceeded quota_exceeded",
			receiptStatus:  outbound.ReceiptFailure,
			wantQuota:      true,
			wantRetryAfter: 0,
		},
		{
			// Spec §Scenario: Benign 429 substring without quota token
			name:          "isolated_429_no_quota_token_must_not_trigger",
			stdout:        "debug: HTTP 200 OK\ndebug: status code was 429 for unrelated endpoint",
			stderr:        "",
			receiptStatus: outbound.ReceiptSuccess,
			wantQuota:     false,
		},
		{
			// Transport token only — co-occurrence guard blocks
			name:          "AI_RetryError_only_no_quota_token",
			stdout:        "AI_RetryError: temporary server error 503",
			stderr:        "",
			receiptStatus: outbound.ReceiptSuccess,
			wantQuota:     false,
		},
		{
			// Quota token only — co-occurrence guard blocks
			name:          "quota_token_only_no_transport_token",
			stdout:        "note: account type is quota_exceeded_tier",
			stderr:        "",
			receiptStatus: outbound.ReceiptSuccess,
			wantQuota:     false,
		},
		{
			// maxRetriesExceeded (transport) + "quota exceeded" phrase (quota)
			name:           "maxRetriesExceeded_with_quota_exceeded_phrase",
			stdout:         `{"error":"maxRetriesExceeded","reason":"quota exceeded"}`,
			stderr:         "",
			receiptStatus:  outbound.ReceiptSuccess,
			wantQuota:      true,
			wantRetryAfter: 0,
		},
		{
			// "rate limit" (transport) + "x-ratelimit-exceeded" (quota)
			name:           "rate_limit_transport_with_x_ratelimit_exceeded",
			stdout:         "",
			stderr:         "error: rate limit hit, header x-ratelimit-exceeded:true",
			receiptStatus:  outbound.ReceiptFailure,
			wantQuota:      true,
			wantRetryAfter: 0,
		},
		{
			// Healthy dispatch — valid envelope, no quota signals
			name:          "healthy_dispatch_no_signals",
			stdout:        "```json\n{\"schema_version\":\"v1\",\"status\":\"DONE\"}\n```",
			stderr:        "",
			receiptStatus: outbound.ReceiptSuccess,
			wantQuota:     false,
		},
		{
			// Specific retry-after header wins over generic
			name:           "specific_retry_after_wins_over_generic",
			stdout:         "429 quota_exceeded x-ratelimit-quota-exceeded-retry-after: 3600 retry-after: 60",
			stderr:         "",
			receiptStatus:  outbound.ReceiptSuccess,
			wantQuota:      true,
			wantRetryAfter: 3600,
		},
		{
			// Generic retry-after used when specific header absent
			name:           "generic_retry_after_fallback",
			stdout:         "429 quota_exceeded retry-after: 120",
			stderr:         "",
			receiptStatus:  outbound.ReceiptSuccess,
			wantQuota:      true,
			wantRetryAfter: 120,
		},
		{
			// Timeout receipt with quota signals in stderr
			name:           "timeout_receipt_with_quota_signals",
			stdout:         "",
			stderr:         "process timeout after 180s; last error: 429 quota_exceeded",
			receiptStatus:  outbound.ReceiptTimeout,
			wantQuota:      true,
			wantRetryAfter: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
				Status: tt.receiptStatus,
				Stdout: []byte(tt.stdout),
				Stderr: []byte(tt.stderr),
			}}
			d := opencode.New(rt, opencode.DefaultConfig())

			_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
				Prompt: "implement something",
			})

			if tt.wantQuota {
				require.Error(t, err)
				require.True(t, errors.Is(err, outbound.ErrProviderQuotaExceeded),
					"expected ErrProviderQuotaExceeded, got: %v", err)

				var qe *outbound.ProviderQuotaError
				require.True(t, errors.As(err, &qe),
					"expected *ProviderQuotaError, got: %T", err)
				require.NotEmpty(t, qe.Evidence,
					"Evidence must not be empty on quota detection")
				require.Equal(t, "opencode", qe.Provider)

				if tt.wantRetryAfter != 0 {
					require.Equal(t, tt.wantRetryAfter, qe.RetryAfterSeconds,
						"RetryAfterSeconds mismatch")
				}
			} else if err != nil {
				// No quota: error may still be present (e.g. ErrDispatchFailed for
				// non-success receipts) but MUST NOT be ErrProviderQuotaExceeded.
				require.False(t, errors.Is(err, outbound.ErrProviderQuotaExceeded),
					"expected no quota error, got: %v", err)
			}
		})
	}
}

// TestDispatch_QuotaWinsOverDispatchFailed ensures that when quota signals are
// present AND receipt.Status is failure, ErrProviderQuotaExceeded takes
// priority over ErrDispatchFailed. Quota detection runs before the status gate.
func TestDispatch_QuotaWinsOverDispatchFailed(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptFailure,
		Stdout: []byte(""),
		Stderr: []byte("AI_RetryError maxRetriesExceeded statusCode: 429 x-ratelimit-exceeded:quota_exceeded"),
	}}
	d := opencode.New(rt, opencode.DefaultConfig())

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{Prompt: "x"})

	require.Error(t, err)
	require.True(t, errors.Is(err, outbound.ErrProviderQuotaExceeded),
		"quota must take priority over ErrDispatchFailed: got %v", err)
	require.False(t, errors.Is(err, outbound.ErrDispatchFailed),
		"ErrDispatchFailed must NOT be returned when quota is detected")
}

// TestDispatch_HealthyRunNotAffectedByQuotaDetection confirms backward compat:
// a healthy dispatch (valid envelope, no quota signals) behaves exactly as before.
func TestDispatch_HealthyRunNotAffectedByQuotaDetection(t *testing.T) {
	stdout := []byte("```json\n{\"schema_version\":\"v1\",\"status\":\"DONE\"}\n```")
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status:     outbound.ReceiptSuccess,
		Stdout:     stdout,
		Stderr:     []byte(""),
		DurationMS: 1000,
	}}
	d := opencode.New(rt, opencode.DefaultConfig())

	res, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt: "do something healthy",
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.EnvelopeRaw)
}

// TestDispatch_ModelOverride_WinsOverPhaseAndGlobal verifies ADR-0010 §Decision 6:
// a non-empty ModelOverride on DispatchRequest overrides both per-phase model
// config and the global Config.Model for that single request.
func TestDispatch_ModelOverride_WinsOverPhaseAndGlobal(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte("```json\n{\"schema_version\":\"v1\"}\n```"),
	}}
	d := opencode.New(rt, opencode.Config{
		Cmd:   "opencode",
		Model: "global-model/v1",
		ModelByPhase: map[string]string{
			"apply": "phase-model/v1",
		},
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:        "p",
		PhaseType:     "apply",
		ModelOverride: "fallback-model/v2",
	})
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := toStringSlice(t, payload["args"].([]any))

	var observedModel string
	for i, a := range args {
		if a == "-m" && i+1 < len(args) {
			observedModel = args[i+1]
		}
	}
	require.Equal(t, "fallback-model/v2", observedModel,
		"ModelOverride must win over PhaseType model and global model; args=%v", args)
}

// TestDispatch_ModelOverride_EmptyFallsThrough confirms that an empty
// ModelOverride does not interfere with normal PhaseType → global resolution.
func TestDispatch_ModelOverride_EmptyFallsThrough(t *testing.T) {
	rt := &fakeRuntime{returnRecp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte("```json\n{\"schema_version\":\"v1\"}\n```"),
	}}
	d := opencode.New(rt, opencode.Config{
		Cmd:   "opencode",
		Model: "global-model/v1",
	})
	_, _ = d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:        "p",
		ModelOverride: "", // explicitly empty — must use global model
	})

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	args := toStringSlice(t, payload["args"].([]any))

	var observedModel string
	for i, a := range args {
		if a == "-m" && i+1 < len(args) {
			observedModel = args[i+1]
		}
	}
	require.Equal(t, "global-model/v1", observedModel,
		"empty ModelOverride must fall through to global model; args=%v", args)
}

// toStringSlice converts []any to []string for payload inspection.
func toStringSlice(t *testing.T, in []any) []string {
	t.Helper()
	out := make([]string, len(in))
	for i, v := range in {
		s, ok := v.(string)
		require.True(t, ok, "args element %d is not a string: %T", i, v)
		out[i] = s
	}
	return out
}
