// Package aider implements outbound.AgentDispatcher backed by the
// Aider CLI (https://aider.chat) — a coding agent that EDITS FILES
// DIRECTLY in the worktree rather than returning a JSON plan.
// Provider name in the V2 factory: "aider".
//
// Dispatched via runtime-adapters' shell.exec@v1 capability:
//
//	cmd: "aider"
//	args: ["--yes-always", "--no-auto-commits",
//	       "--model", "<model>",        // optional, when configured
//	       "--message", "<prompt>"]
//
// Differences vs the opencode + ollama adapters:
//
//   - Aider APPLIES EDITS in-place (does not return a JSON envelope).
//     `DispatchResult.EnvelopeRaw` is ALWAYS nil. The caller (apply
//     phase) must reconstruct a synthetic envelope from the worktree
//     state (git diff) or fall back to the memory-engine query path
//     (see DispatchRequest.EnvelopeOut = "memory-topic-key:KEY").
//
//   - Model flag is `--model <name>`, NOT `-m` (opencode) and NOT
//     positional (ollama). Examples: "claude-opus-4-7",
//     "openai/gpt-5.3-codex". Aider has its own model alias table.
//
//   - `--yes-always` skips all interactive confirmations; without it
//     aider blocks waiting for stdin which the runtime cannot supply.
//
//   - `--no-auto-commits` keeps the orchestrator in charge of git
//     commits — the apply phase commits after policy checks pass, not
//     mid-dispatch.
//
//   - Credentials come from environment variables (ANTHROPIC_API_KEY,
//     OPENAI_API_KEY, etc.) provisioned on the runtime-adapters image;
//     aider has no OAuth file format of its own.
//
//   - SuggestedMaxConcurrent defaults to 1: concurrent aider runs in
//     the same worktree will race on file edits; in different
//     worktrees they're safe but the operator should size up
//     explicitly via Config.Suggested rather than relying on a
//     parallel-friendly default.
//
// Intended use case: route the APPLY phase only — `SOPHIA_DISPATCHER_
// PROVIDER_APPLY=aider` while keeping spec/design/verify on opencode.
package aider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SuggestedMaxConcurrentDefault is the conservative default. Aider
// edits the worktree in-place; concurrent runs in the same worktree
// race. Worktree isolation is the orchestrator's job; here we keep
// the parallelism hint at 1 so the SpawnGovernor does not stack
// edits before the operator has opted in via Config.Suggested.
const SuggestedMaxConcurrentDefault = 1

// Config tunes Dispatcher.
type Config struct {
	// Cmd is the aider binary name. Default "aider".
	Cmd string
	// ExtraArgs are appended AFTER the standard flags and BEFORE
	// `--message <prompt>`. Reserve for operator overrides like
	// `--read <file>` to pre-load a doc into aider's context, or
	// `--map-tokens 0` to disable the repo map for very large repos.
	ExtraArgs []string
	// Suggested is the value returned by SuggestedMaxConcurrent.
	Suggested int
	// Model is the global default aider model name (e.g.
	// "claude-opus-4-7", "openai/gpt-5.3-codex"). Empty omits the
	// `--model` flag entirely and lets aider pick its default from
	// the operator's `~/.aider.conf.yml` or env vars.
	Model string
	// ModelByPhase is the per-phase model override (mirrors the
	// opencode + ollama adapter contract). Wired from
	// config.DispatcherConfig.ModelByPhase via env vars
	// SOPHIA_DISPATCHER_MODEL_<PHASE>.
	ModelByPhase map[string]string
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		Cmd:       "aider",
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
		panic("aider.Dispatcher: nil runtime client")
	}
	if cfg.Cmd == "" {
		cfg.Cmd = "aider"
	}
	if cfg.Suggested <= 0 {
		cfg.Suggested = SuggestedMaxConcurrentDefault
	}
	return &Dispatcher{cfg: cfg, runtime: runtime}
}

// Provider reports session.ProviderOpenCode.
//
// V2.0 reuses the OpenCode session.Provider value for all dispatcher
// adapters because session.Provider is a closed enum that wasn't
// extended for V2 (ADR-0007 §Consequences). Per-call adapter
// provenance lands in V2.1; for now, audit logs identify the actual
// adapter from the dispatcher hint plus the receipt's command line.
func (d *Dispatcher) Provider() session.Provider { return session.ProviderOpenCode }

// SuggestedMaxConcurrent returns the per-provider rate-limit hint.
func (d *Dispatcher) SuggestedMaxConcurrent() int { return d.cfg.Suggested }

// HealthCheck runs `aider --version` via runtime to verify the CLI is
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
		return fmt.Errorf("aider HealthCheck: %w", err)
	}
	if receipt.Status != outbound.ReceiptSuccess {
		return fmt.Errorf("aider HealthCheck: status=%s exit=%d", receipt.Status, receipt.ExitCode)
	}
	return nil
}

// Dispatch invokes Aider with the given Prompt under WorktreePath.
//
// `EnvelopeRaw` in the returned DispatchResult is ALWAYS nil because
// aider does not produce a JSON envelope — the caller MUST either
// reconstruct one from the worktree's git state or use the
// memory-topic-key fallback path declared on DispatchRequest.
func (d *Dispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	args := []string{"--yes-always", "--no-auto-commits"}
	if model := d.modelFor(req.PhaseType); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, d.cfg.ExtraArgs...)
	// `--message <prompt>` is the last pair so the prompt always sits
	// at the end of argv (matches the opencode + ollama convention).
	args = append(args, "--message", req.Prompt)

	execPayload := map[string]any{
		"command": d.cfg.Cmd,
		"args":    args,
	}
	if req.WorktreePath != "" && req.WorktreePath != "." {
		// Aider operates on $PWD by default; pass working_dir so the
		// edits land in the orchestrator-managed worktree, not the
		// runtime-adapters' own cwd.
		execPayload["working_dir"] = req.WorktreePath
	}
	payload, err := json.Marshal(execPayload)
	if err != nil {
		return nil, fmt.Errorf("aider Dispatch: marshal payload: %w", err)
	}

	receipt, err := d.runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("aider Dispatch: %w", err)
	}

	// M-E0 #3 semantics: receipt status checked BEFORE caller sees a
	// result so a runtime-level failure (binary missing, timeout,
	// cancellation) does not get reported as a successful no-op edit.
	if receipt.Status != outbound.ReceiptSuccess {
		return nil, fmt.Errorf("%w: status=%q stderr=%q",
			outbound.ErrDispatchFailed,
			receipt.Status,
			receipt.Stderr,
		)
	}

	// EnvelopeRaw is intentionally nil. Aider's stdout is a narrative
	// transcript of what it changed, not a structured envelope. The
	// apply phase reconstructs a synthetic envelope from `git status
	// --porcelain` post-dispatch (it dispatches that reconstruction
	// based on AdapterID == "aider"; opencode/ollama do not set this
	// field so the existing nil-envelope fatal path keeps applying).
	return &outbound.DispatchResult{
		ExitCode:    receipt.ExitCode,
		Stdout:      receipt.Stdout,
		Stderr:      receipt.Stderr,
		EnvelopeRaw: nil,
		DurationMS:  receipt.DurationMS,
		AdapterID:   "aider",
	}, nil
}

// modelFor mirrors opencode.Dispatcher.modelFor + ollama's. Returns
// "" only when the global Model AND the per-phase entry are both
// empty — Dispatch then omits the `--model` flag entirely, deferring
// to aider's own default-resolution chain (`~/.aider.conf.yml` then
// provider env vars).
func (d *Dispatcher) modelFor(phaseType string) string {
	if phaseType != "" {
		if m, ok := d.cfg.ModelByPhase[phaseType]; ok && m != "" {
			return m
		}
	}
	return d.cfg.Model
}
