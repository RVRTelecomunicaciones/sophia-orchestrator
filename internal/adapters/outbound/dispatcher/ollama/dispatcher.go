// Package ollama implements outbound.AgentDispatcher backed by the
// Ollama CLI for LOCAL LLM inference (Llama, Qwen, DeepSeek, etc.).
// Provider name in the V2 factory: "ollama".
//
// Dispatched via runtime-adapters' shell.exec@v1 capability:
//
//	cmd: "ollama"
//	args: ["run", "<model>", "<prompt>"]
//
// Differences vs the opencode adapter:
//   - Model is a POSITIONAL argument (not `-m flag`).
//   - No OAuth credentials needed (Ollama runs local, no provider account).
//   - No worktree config injection (Ollama doesn't sandbox by directory).
//   - Output is plain text; envelope extraction uses the same
//     last-fenced-json regex as opencode (so SDD prompts that ask for
//     ```json blocks work uniformly).
//
// Use case: cost-zero phases (verify, archive) or privacy-sensitive
// phases that should not leave the deployment.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SuggestedMaxConcurrentDefault is the conservative default for local
// inference. Ollama serializes requests internally per model file; on a
// single-GPU host even 2 concurrent ollama runs against the same model
// will queue. We keep the hint at 2 so the SpawnGovernor allows
// pipelining a CPU-bound phase with a GPU-bound one but doesn't try to
// fan out beyond what local hardware sustains.
const SuggestedMaxConcurrentDefault = 2

// Config tunes Dispatcher.
type Config struct {
	// Cmd is the Ollama binary name. Default "ollama".
	Cmd string
	// ExtraArgs are appended after the standard args, before the
	// positional model + prompt. Use sparingly; most knobs (--format
	// json, --think, --hidethinking) belong to the calling system
	// prompt, not the dispatcher.
	ExtraArgs []string
	// Suggested is the value returned by SuggestedMaxConcurrent.
	Suggested int
	// Model is the global default Ollama model name (e.g.
	// "deepseek-r1:7b", "qwen3:14b", "llama3.3:70b"). Empty means the
	// dispatcher MUST receive a per-phase model via ModelByPhase or it
	// will fail at Dispatch — Ollama has no implicit default.
	Model string
	// ModelByPhase is the per-phase model override (mirrors the
	// opencode adapter contract). Wired from
	// config.DispatcherConfig.ModelByPhase via env vars
	// SOPHIA_DISPATCHER_MODEL_<PHASE>.
	ModelByPhase map[string]string
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		Cmd:       "ollama",
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
		panic("ollama.Dispatcher: nil runtime client")
	}
	if cfg.Cmd == "" {
		cfg.Cmd = "ollama"
	}
	if cfg.Suggested <= 0 {
		cfg.Suggested = SuggestedMaxConcurrentDefault
	}
	return &Dispatcher{cfg: cfg, runtime: runtime}
}

// Provider reports session.ProviderOllama.
//
// V2.0 originally reused ProviderOpenCode here because the session
// enum hadn't been extended yet. As of V2.1 (2026-05-16, PR #28)
// session.Provider knows about ollama natively, so this adapter
// reports its real identity — audit logs now distinguish ollama
// sessions from opencode/aider ones without needing the receipt's
// command line as a workaround.
func (d *Dispatcher) Provider() session.Provider { return session.ProviderOllama }

// SuggestedMaxConcurrent returns the per-provider rate-limit hint.
func (d *Dispatcher) SuggestedMaxConcurrent() int { return d.cfg.Suggested }

// HealthCheck runs `ollama --version` via runtime to verify the CLI is
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
		return fmt.Errorf("ollama HealthCheck: %w", err)
	}
	if receipt.Status != outbound.ReceiptSuccess {
		return fmt.Errorf("ollama HealthCheck: status=%s exit=%d", receipt.Status, receipt.ExitCode)
	}
	return nil
}

// Dispatch invokes Ollama with the given Prompt. Captures stdout,
// extracts the LAST fenced ```json``` block as EnvelopeRaw.
func (d *Dispatcher) Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	model := d.modelFor(req.PhaseType)
	if model == "" {
		return nil, fmt.Errorf("ollama Dispatch: model is required (set Config.Model or ModelByPhase[%q])", req.PhaseType)
	}

	args := []string{"run", model}
	args = append(args, d.cfg.ExtraArgs...)
	args = append(args, req.Prompt) // positional prompt — the entire SDD prompt

	execPayload := map[string]any{
		"command": d.cfg.Cmd,
		"args":    args,
	}
	if req.WorktreePath != "" && req.WorktreePath != "." {
		// Ollama doesn't sandbox by working dir, but we set it so any
		// relative path the model emits in its envelope is interpreted
		// against the worktree (consistent with opencode).
		execPayload["working_dir"] = req.WorktreePath
	}
	payload, err := json.Marshal(execPayload)
	if err != nil {
		return nil, fmt.Errorf("ollama Dispatch: marshal payload: %w", err)
	}

	receipt, err := d.runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  req.TimeoutMS,
	})
	if err != nil {
		return nil, fmt.Errorf("ollama Dispatch: %w", err)
	}

	// Same M-E0 #3 semantics as opencode: receipt status checked BEFORE
	// envelope extraction so a runtime-level failure (binary missing,
	// timeout, cancellation) does not get reported as a bad envelope.
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

// modelFor mirrors opencode.Dispatcher.modelFor for the per-phase
// override lookup. Returns "" only when the global Model AND the
// per-phase entry are both empty — Dispatch then fails fast with a
// helpful error (Ollama has no implicit default).
func (d *Dispatcher) modelFor(phaseType string) string {
	if phaseType != "" {
		if m, ok := d.cfg.ModelByPhase[phaseType]; ok && m != "" {
			return m
		}
	}
	return d.cfg.Model
}

// fencedJSONRe matches the LAST fenced ```json``` block in stdout. The
// regex is identical to opencode.fencedJSONRe so the envelope contract
// stays consistent across adapters (SDD prompts produce the same
// markdown shape regardless of which CLI ran them).
var fencedJSONRe = regexp.MustCompile("(?s)```json\\s*\\n(.*?)\\n```")

// extractLastFencedJSON returns the contents of the LAST fenced ```json
// block in stdout, or nil if none. Adapters share this implementation.
func extractLastFencedJSON(stdout []byte) []byte {
	matches := fencedJSONRe.FindAllSubmatch(stdout, -1)
	if len(matches) == 0 {
		return nil
	}
	last := matches[len(matches)-1]
	if len(last) < 2 {
		return nil
	}
	return bytes.TrimSpace(last[1])
}
