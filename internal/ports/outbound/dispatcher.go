package outbound

import (
	"context"
	"errors"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
)

// ErrDispatchFailed is returned by AgentDispatcher.Dispatch when the underlying
// runtime execution did NOT succeed (e.g., the agent CLI was not found in the
// runtime's AllowedCommandsPath, shell.exec returned status="failure"/"timeout").
// In this case, no envelope extraction is attempted; the caller must NOT treat
// this as a "bad envelope from the agent" — the agent never ran.
var ErrDispatchFailed = errors.New("dispatcher: runtime execution did not succeed")

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
}

// DispatchResult is the output of AgentDispatcher.Dispatch.
type DispatchResult struct {
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
	EnvelopeRaw []byte // JSON; empty if extraction failed
	DurationMS  int
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
