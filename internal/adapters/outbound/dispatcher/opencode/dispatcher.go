// Package opencode implements outbound.AgentDispatcher for OpenCode V1.
//
// Dispatches via runtime-adapters' shell.exec@v1 capability:
//
//	cmd: "opencode"
//	args: ["run", "--prompt-stdin", "--cwd", <worktree>, "--output-json"]
//	stdin: <prompt>
//
// The exact CLI flag set is an Open Question (spec § 13.2) — the contract
// here is provisional and will be tightened during the first real-OpenCode
// contract test. Envelope extraction (V1): last fenced ```json``` block in
// stdout. Fallback: caller queries memory-engine by topic_key.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SuggestedMaxConcurrentDefault is the conservative V1 default. Anthropic's
// Claude Code (issue #53922) hits a server-side burst-limiter at 4-6
// concurrent processes; OpenCode's behavior is not yet empirically verified.
// Spawn Governor uses this as a hint.
const SuggestedMaxConcurrentDefault = 4

// Config tunes Dispatcher.
type Config struct {
	// Cmd is the OpenCode binary name. Default "opencode".
	Cmd string
	// ExtraArgs are appended after the standard args, before stdin handoff.
	ExtraArgs []string
	// Suggested is the value returned by SuggestedMaxConcurrent.
	Suggested int
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		Cmd:       "opencode",
		Suggested: SuggestedMaxConcurrentDefault,
	}
}

// Dispatcher implements outbound.AgentDispatcher.
type Dispatcher struct {
	cfg     Config
	runtime outbound.RuntimeClient
}

// New constructs a Dispatcher backed by the given RuntimeClient.
func New(runtime outbound.RuntimeClient, cfg Config) *Dispatcher {
	if runtime == nil {
		panic("opencode.Dispatcher: nil runtime client")
	}
	if cfg.Cmd == "" {
		cfg.Cmd = "opencode"
	}
	if cfg.Suggested <= 0 {
		cfg.Suggested = SuggestedMaxConcurrentDefault
	}
	return &Dispatcher{cfg: cfg, runtime: runtime}
}

// Provider reports session.ProviderOpenCode.
func (d *Dispatcher) Provider() session.Provider { return session.ProviderOpenCode }

// SuggestedMaxConcurrent returns the per-provider rate-limit hint.
func (d *Dispatcher) SuggestedMaxConcurrent() int { return d.cfg.Suggested }

// HealthCheck runs `opencode --version` via runtime to verify the CLI is
// reachable. Returns nil on success.
func (d *Dispatcher) HealthCheck(ctx context.Context) error {
	payload, _ := json.Marshal(map[string]any{
		"command": d.cfg.Cmd,
		"args":    []string{"--version"},
	})
	receipt, err := d.runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  5000,
	})
	if err != nil {
		return fmt.Errorf("opencode HealthCheck: %w", err)
	}
	if receipt.Status != outbound.ReceiptSuccess {
		return fmt.Errorf("opencode HealthCheck: status=%s exit=%d", receipt.Status, receipt.ExitCode)
	}
	return nil
}

// Dispatch invokes OpenCode under WorktreePath with the given Prompt.
// Captures stdout, extracts the LAST fenced ```json``` block as
// EnvelopeRaw, and returns the structured result.
func (d *Dispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	args := []string{"run", "--prompt-stdin", "--output-json"}
	if req.WorktreePath != "" && req.WorktreePath != "." {
		args = append(args, "--cwd", req.WorktreePath)
	}
	args = append(args, d.cfg.ExtraArgs...)

	// Wire shape mirrors sophia-runtime-adapters shell adapter ExecPayload:
	//   command (not "cmd"), args, stdin ([]byte → JSON base64), env, working_dir.
	// Outer timeout_budget_ms is handled by the runtime client; do NOT send it here.
	// Runtime decoder is DisallowUnknownFields strict.
	payload, err := json.Marshal(map[string]any{
		"command": d.cfg.Cmd,
		"args":    args,
		"stdin":   []byte(req.Prompt), // encoding/json marshals []byte as base64
	})
	if err != nil {
		return nil, fmt.Errorf("opencode Dispatch: marshal payload: %w", err)
	}

	receipt, err := d.runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("opencode Dispatch: %w", err)
	}

	// M-E0 #3: check receipt.Status BEFORE attempting envelope extraction.
	// If the runtime did not succeed (e.g. the opencode binary is missing, the
	// process timed out, or was cancelled) the agent never ran — stdout is
	// empty or partial and must not be parsed as an envelope.
	//
	// Event semantics:
	//   runtime.dispatch_failed         — receipt.Status != "success"
	//                                     (shell.exec could not run the agent CLI)
	//   apply.envelope.validation_failed — agent ran (receipt.Status="success") but
	//                                      produced no fenced JSON or invalid envelope
	//   apply.dispatch.error            — transport-level failure (HTTP error, ctx cancel)
	if receipt.Status != outbound.ReceiptSuccess {
		return nil, fmt.Errorf("%w: status=%q stderr=%q",
			outbound.ErrDispatchFailed,
			receipt.Status,
			receipt.Stderr,
		)
	}

	envRaw := extractLastFencedJSON(receipt.Stdout)
	return &outbound.DispatchResult{
		ExitCode:    receipt.ExitCode,
		Stdout:      receipt.Stdout,
		Stderr:      receipt.Stderr,
		EnvelopeRaw: envRaw,
		DurationMS:  receipt.DurationMS,
	}, nil
}

// fencedJSONRE matches a fenced ```json ... ``` block. (?s) makes . match
// newlines; non-greedy to support multiple blocks (we want the LAST match).
var fencedJSONRE = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

// extractLastFencedJSON returns the JSON inside the LAST fenced block in
// stdout, or nil if none. Trailing whitespace is trimmed.
func extractLastFencedJSON(stdout []byte) []byte {
	matches := fencedJSONRE.FindAllSubmatch(stdout, -1)
	if len(matches) == 0 {
		return nil
	}
	return bytes.TrimSpace(matches[len(matches)-1][1])
}

// Compile-time interface check.
var _ outbound.AgentDispatcher = (*Dispatcher)(nil)
