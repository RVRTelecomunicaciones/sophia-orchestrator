# ADR-0010 — Provider Quota Resilience

- **Status**: accepted
- **Date**: 2026-06-06
- **Deciders**: Russell Vergara

## Context

During apply-phase execution the opencode dispatcher subprocess can exhaust
the LLM provider's rate-limit/quota mid-run. The observed failure mode is
subtle: `receipt.Status = "success"` (opencode process exited 0) while the
LLM backend replied with HTTP 429 internally, causing opencode to log an
`AI_RetryError / maxRetriesExceeded` message into stdout/stderr and produce
no usable envelope. The generic `ErrDispatchFailed` sentinel is insufficient
because it cannot be distinguished from a crash or missing binary — the apply
layer needs to know whether to burn an attempt, emit a specific SSE event,
try a fallback model, or trip the phase breaker.

The change is a dispatcher behavior change affecting `internal/ports/outbound`
and `internal/adapters/outbound/dispatcher/opencode`. Per AGENTS.md an ADR is
required.

Spec reference: `openspec/changes/apply-provider-resilience/specs/provider-resilience/spec.md` §Quota Signal Detection.

## Decision

1. **Sentinel `ErrProviderQuotaExceeded`** — a new package-level error variable
   in `internal/ports/outbound/dispatcher.go`. Callers use `errors.Is` to
   distinguish quota outcomes from generic dispatch failures.

2. **Typed `ProviderQuotaError`** — wraps the sentinel via `Unwrap()` and
   carries `RetryAfterSeconds int`, `Provider string`, `Model string`, and an
   `Evidence string` snippet (≤200 chars) taken from the matching output.
   The apply layer uses `errors.As` to extract retry-after when available.

3. **Co-occurrence guard** — the detector in the opencode adapter MUST require
   at least one transport token (`429`, `rate limit`, `AI_RetryError`,
   `maxRetriesExceeded`) AND at least one quota token (`quota_exceeded`,
   `quota exceeded`, `x-ratelimit-exceeded`) present in the COMBINED
   `stdout + stderr`. A lone `429` substring (e.g., from an HTTP debug log)
   MUST NOT trigger quota classification.

4. **Retry-after parsing** — the detector extracts the retry-after duration
   in seconds from `x-ratelimit-quota-exceeded-retry-after` (value in seconds)
   or the generic `retry-after` header reflection in the log, when present.

5. **Receipt-status agnosticism** — quota detection runs BEFORE the
   `receipt.Status` guard, and ALSO applies when `receipt.Status != success`.
   This covers both the observed success-hiding-429 case and explicit failure
   receipts.

6. **`DispatchRequest.ModelOverride`** — a new optional field on
   `DispatchRequest`. When non-empty it overrides any per-phase or global
   model config for that single request. Declared now (Slice 1); wired into
   the fallback dispatch path in Slice 4. Pre-existing callers that omit the
   field are unaffected.

7. **Validation policy** — Slice 1 is validated EXCLUSIVELY with mocked
   `ExecutionReceipt` values containing real 429 log text captured from
   production sessions. NO real LLM calls are made while provider quota is
   exhausted.

8. **Delivery** — this feature is split across five chained PRs using the
   `feature-branch-chain` strategy: each PR targets the tracker branch
   `feature/apply-provider-resilience`; the tracker is eventually merged to
   main. PR 1 (this slice) establishes the detection contract. PR 2 wires
   fail-fast and SSE emission. PR 3 adjusts timeout defaults. PR 4 wires the
   fallback model. PR 5 adds the phase circuit breaker.

## Consequences

### Positive

- Apply phase can distinguish quota exhaustion from crash/missing-binary,
  enabling targeted recovery (fail-fast, fallback, breaker) without polluting
  the `MaxAttempts` retry budget.
- The co-occurrence guard keeps false-positive rate low: benign log lines
  containing `"429"` or isolated quota tokens will not trigger.
- Typed error carries retry-after so the apply layer can surface it in SSE
  events and future scheduling decisions.
- `ModelOverride` on `DispatchRequest` is a pure additive field; all existing
  callers continue working unchanged.

### Negative

- The detector adds a string-scan over combined stdout+stderr on every
  dispatch. For long outputs this is O(n); acceptable because dispatch is
  already I/O-bound by the LLM round-trip.
- Quota classification relies on textual patterns from opencode's log format.
  If opencode changes its error formatting, the detector must be updated.
  Mitigated by the real-fixture test that pins the actual observed log shape.

### Neutral

- The `ProviderQuotaError.Evidence` snippet is truncated to 200 characters.
  This is enough for a human operator reading a log; full text is available
  from the receipt captured in the apply-phase SSE payload.
- Slices 2–5 build on this ADR; they do not require separate ADRs unless they
  introduce a new provider, persistence change, or SLO-affecting observability
  event beyond what is documented here.

## Alternatives considered

- **Detect in apply/teamlead from generic error text** — rejected. The adapter
  is the only layer that sees raw `stdout`/`stderr` for all receipt statuses
  including the observed success-hiding-429 case. Parsing at teamlead would
  require threading raw bytes up through the `DispatchResult`, coupling the
  port interface to a provider-specific log format.
- **Patch `ErrDispatchFailed` with a sub-code enum** — rejected. An enum on a
  single error variable cannot carry typed fields (retry-after, evidence) and
  cannot be extended without breaking the closed enum.
- **Auto-swap model inside the adapter** — rejected. Fallback is apply-specific
  business behavior. The adapter is a transport/parser boundary; embedding
  fallback logic there would hide policy decisions from the apply layer and
  make them impossible to test independently.
- **Provider-global circuit breaker** — rejected. The outage is
  phase-blocking operational state; a per-`Execute`-phase breaker is minimal
  and scoped correctly. A global breaker would incorrectly block other phases
  running concurrently.

## References

- Spec: `openspec/changes/apply-provider-resilience/specs/provider-resilience/spec.md`
- Design: `openspec/changes/apply-provider-resilience/design.md`
- Tasks: `openspec/changes/apply-provider-resilience/tasks.md`
- ADR-0002: Dispatcher abstraction
- ADR-0007: Multi-LLM dispatcher factory
- ADR-0009: Apply build verification (established `shell.exec@v1` working_dir contract)
