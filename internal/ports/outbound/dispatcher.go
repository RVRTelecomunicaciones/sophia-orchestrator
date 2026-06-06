package outbound

import (
	"context"
	"errors"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
)

// ErrDispatchFailed is returned by AgentDispatcher.Dispatch when the underlying
// runtime execution did NOT succeed (e.g., the agent CLI was not found in the
// runtime's AllowedCommandsPath, shell.exec returned status="failure"/"timeout").
// In this case, no envelope extraction is attempted; the caller must NOT treat
// this as a "bad envelope from the agent" — the agent never ran.
var ErrDispatchFailed = errors.New("dispatcher: runtime execution did not succeed")

// ErrProviderQuotaExceeded is returned by AgentDispatcher.Dispatch when the
// combined stdout+stderr from the underlying runtime execution contains quota-
// exhaustion signals from the LLM provider (e.g., HTTP 429 with quota tokens).
// It is distinct from ErrDispatchFailed: the transport may have succeeded
// (receipt.Status="success") while the LLM backend rejected the request
// internally. Callers use errors.Is to distinguish quota outcomes from generic
// dispatch failures and errors.As to extract typed fields such as RetryAfterSeconds.
// See ADR-0010.
var ErrProviderQuotaExceeded = errors.New("dispatcher: provider quota exceeded")

// ProviderQuotaError is a typed error wrapping ErrProviderQuotaExceeded. It
// carries the parsed retry-after hint (when the provider includes it) and a
// short evidence snippet for observability. Use errors.As to retrieve it.
type ProviderQuotaError struct {
	// RetryAfterSeconds is the provider's retry-after hint in seconds.
	// Zero means the provider did not supply a value.
	RetryAfterSeconds int

	// Provider is the dispatcher provider name (e.g. "opencode"). May be
	// empty for adapters that do not opt-in to self-identification.
	Provider string

	// Model is the model string that triggered the quota error, when known.
	// May be empty.
	Model string

	// Evidence is a short snippet (≤200 chars) from the combined
	// stdout+stderr that matched the quota signal. Useful for logging and
	// SSE payloads without transmitting the full output.
	Evidence string
}

// Error implements the error interface.
func (e *ProviderQuotaError) Error() string {
	if e.RetryAfterSeconds > 0 {
		return fmt.Sprintf("dispatcher: provider quota exceeded (retry-after %ds): %s", e.RetryAfterSeconds, e.Evidence)
	}
	return fmt.Sprintf("dispatcher: provider quota exceeded: %s", e.Evidence)
}

// Unwrap returns ErrProviderQuotaExceeded, making errors.Is work transparently.
func (e *ProviderQuotaError) Unwrap() error { return ErrProviderQuotaExceeded }

// AgentDispatcher launches an AI CLI subprocess (V1: OpenCode) inside the
// orchestrator-managed worktree, captures stdout, and extracts the JSON
// envelope. The interface is provider-agnostic; per-provider quirks
// (rate-limiter ceiling, prompt format, envelope extraction) live inside
// the adapter implementation. See ADR-0002.
type AgentDispatcher interface {
	// Provider returns the dispatcher's session.Provider value.
	Provider() session.Provider

	// SuggestedMaxConcurrent reports the empirically-safe concurrent process
	// count for this provider. Spawn Governor uses this as a hint.
	SuggestedMaxConcurrent() int

	// HealthCheck verifies the provider CLI is installed and responsive.
	HealthCheck(ctx context.Context) error

	// Dispatch invokes the agent with the given prompt under WorktreePath.
	// On return, EnvelopeRaw contains the extracted JSON envelope (or nil if
	// extraction failed; caller falls back to memory-engine query).
	Dispatch(ctx context.Context, req DispatchRequest) (*DispatchResult, error)
}

// DispatchRequest is the input to AgentDispatcher.Dispatch.
type DispatchRequest struct {
	Prompt       string
	WorktreePath string
	TimeoutMS    int
	// EnvelopeOut hints how to extract the envelope:
	//   "stdout-fenced-json" — last fenced ```json block in stdout (V1 default)
	//   "memory-topic-key:KEY" — fall back to MemoryClient.Get with the topic_key
	EnvelopeOut string
	// PhaseType is the lowercase phase string (e.g. "explore", "spec",
	// "apply") used by the dispatcher to look up a per-phase model
	// override (Config.ModelByPhase). Empty falls back to the global
	// Config.Model. Pre-existing callers that omit this still work
	// unchanged — they get the global default.
	PhaseType string

	// ModelOverride, when non-empty, overrides any per-phase or global
	// model config for this single request. Intended for the apply-phase
	// fallback dispatch path (Slice 4): the apply layer sets this to the
	// configured fallback model after a primary-model quota failure.
	// Pre-existing callers that do not set this field are unaffected —
	// the dispatcher falls back to PhaseType → Config.Model resolution as
	// before. See ADR-0010.
	ModelOverride string
}

// DispatchResult is the output of AgentDispatcher.Dispatch.
type DispatchResult struct {
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
	EnvelopeRaw []byte // JSON; empty if extraction failed
	DurationMS  int
	// AdapterID is the V2 factory provider name of the adapter that
	// produced this result ("opencode", "ollama", "aider", ...). Empty
	// for adapters that do not opt-in to identifying themselves
	// (backward-compat with V1 callers that didn't read this field).
	//
	// The apply executor reads this to decide whether an EnvelopeRaw==nil
	// result is fatal (most adapters: yes) or expected (aider: yes —
	// reconstruct synthetically from the worktree's git state).
	//
	// V2.1 will replace this string with a richer per-call provenance
	// record once session.Provider's closed enum is split (ADR-0007
	// §Consequences); until then this is the single carrier the apply
	// executor uses to disambiguate adapters at the call site.
	AdapterID string
}

// ErrUnknownDispatcherProvider is returned by DispatcherFactory.Get when the
// provider name is not registered. The caller should fall back to the
// configured default provider rather than failing the phase outright.
var ErrUnknownDispatcherProvider = errors.New("dispatcher: unknown provider")

// DispatcherFactory resolves a provider name (e.g. "opencode", "aider",
// "claude-code") to an AgentDispatcher implementation. V2.0 ships with
// "opencode" as the only registered provider; future versions add more
// adapters without forcing changes to phase.Service or apply.RunService —
// they continue talking to a single AgentDispatcher (a wrapping facade that
// internally consults the factory per request).
//
// Implementations MUST be safe for concurrent use. Get is called on the
// hot path of every Dispatch.
type DispatcherFactory interface {
	// Get returns the AgentDispatcher registered under the given provider
	// name. Returns ErrUnknownDispatcherProvider if no adapter matches.
	Get(provider string) (AgentDispatcher, error)

	// Default returns the dispatcher used when no per-phase provider
	// override applies (config.DispatcherConfig.Provider). Always
	// non-nil in a properly-wired factory.
	Default() AgentDispatcher

	// Providers returns the list of registered provider names. Order
	// is not significant; callers use it for diagnostics + healthcheck
	// fan-out.
	Providers() []string
}
