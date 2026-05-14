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
	// Model, if non-empty, is passed via opencode `-m <provider/model>`.
	// Empty = opencode picks its default model from its config.
	Model string
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
	// Flags aligned with current opencode CLI (v0.x):
	//   opencode run [options] <message>     ← message is POSITIONAL
	// No --prompt-stdin (legacy), no --output-json (now --format json).
	//
	// IMPORTANT: do NOT pass --dir. Opencode's sandboxed permission system
	// classifies any path outside the launching shell's cwd as
	// "external_directory" and auto-rejects file reads/writes — even if
	// --dir points there. The only way to grant the worktree access is to
	// spawn opencode with cwd=<worktree>, which we do by setting
	// working_dir on the runtime payload below (8th wire-alignment gap,
	// discovered during M-E0 Validation Gap #5).
	args := []string{"run"}
	if d.cfg.Model != "" {
		args = append(args, "-m", d.cfg.Model)
	}
	args = append(args, d.cfg.ExtraArgs...)
	args = append(args, req.Prompt) // positional message — full SDD prompt

	// Wire shape mirrors sophia-runtime-adapters shell adapter ExecPayload:
	//   command (not "cmd"), args, working_dir, env. No stdin — opencode
	//   reads message from positional argv. working_dir MUST be set: the
	//   runtime spawns the subprocess with cwd = working_dir, which is
	//   the only way opencode's permission system grants file access to
	//   the worktree. Outer timeout_budget_ms is on the ExecutionRequest.
	//   Runtime decoder is DisallowUnknownFields strict.
	//
	// env carries OPENCODE_CONFIG_CONTENT — an inline JSON config that
	// allowlists the worktree under opencode's permission system.
	// Without this, opencode auto-rejects every read/edit against the
	// worktree with "permission requested: external_directory; auto-
	// rejecting" because (a) external_directory defaults to "ask" and
	// (b) the subprocess has no TTY, so "ask" prompts auto-reject.
	// This is the 10th wire-alignment gap discovered during M-E0
	// Validation Gap #5 — see ADR-0006.
	execPayload := map[string]any{
		"command": d.cfg.Cmd,
		"args":    args,
	}
	if req.WorktreePath != "" && req.WorktreePath != "." {
		execPayload["working_dir"] = req.WorktreePath
		if cfg := opencodeWorktreeConfigJSON(req.WorktreePath); cfg != "" {
			execPayload["env"] = map[string]string{
				"OPENCODE_CONFIG_CONTENT": cfg,
			}
		}
	}
	payload, err := json.Marshal(execPayload)
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

// opencodeWorktreeConfigJSON returns a marshaled opencode config that
// allowlists the given worktree path under opencode's permission system.
// Returns "" if worktree is empty.
//
// opencode loads config (in order of precedence) from:
//  1. remote config
//  2. ~/.config/opencode/opencode.json (global)
//  3. OPENCODE_CONFIG env var (custom file path)
//  4. ./opencode.json walking up to nearest .git/
//  5. .opencode/ directories
//  6. OPENCODE_CONFIG_CONTENT env var (inline JSON, this is what we use)
//
// Inline content is preferred because it (a) avoids writing files to
// every worktree, (b) is scoped to the orch-spawned subprocess (does
// NOT bleed into the user's interactive opencode sessions), and (c) is
// auditable in code review — the rules live next to the dispatcher.
//
// Empirically verified against opencode v1.3.14: only the
// `external_directory` permission key accepts a dict-of-patterns map.
// Adding `read`/`edit`/`write`/`webfetch` with the same dict shape
// makes opencode exit with "Configuration is invalid at
// OPENCODE_CONFIG_CONTENT — Invalid input permission" at startup.
// Those tool-level permissions are inherited from the user's global
// ~/.config/opencode/opencode.json (which typically allows `*` for
// `read`/`bash`). Our job is only to allowlist the worktree itself —
// the actual filesystem boundary check.
//
// Without this allowlist, opencode logs `permission requested:
// external_directory; auto-rejecting` for every tool call against
// the worktree because (a) the worktree lives under /tmp (outside
// the user's home), (b) external_directory defaults to "ask", and
// (c) the subprocess has no TTY so "ask" prompts auto-reject.
func opencodeWorktreeConfigJSON(worktreePath string) string {
	if worktreePath == "" {
		return ""
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"permission": map[string]any{
			"external_directory": map[string]string{
				worktreePath:          "allow",
				worktreePath + "/**":  "allow",
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

// Compile-time interface check.
var _ outbound.AgentDispatcher = (*Dispatcher)(nil)
