# ADR 0002: Pluggable dispatcher abstraction (OpenCode V1)

- **Status:** accepted
- **Date:** 2026-05-03
- **Deciders:** rfactperu

## Context

V1 of sophia-orchestator must launch AI CLI subprocesses to execute SDD
phases. The user-facing target for V1 is **OpenCode**, but the orchestrator
must remain AI-provider-agnostic so V2 can add Claude Code, Cursor, Gemini,
or other providers without touching domain or application layers.

Spec § 12.3 explicitly rejects depending on Claude Code-specific features
(e.g., the `-w` worktree flag introduced in v2.1.50): worktrees are managed
by the orchestrator via runtime-adapters, not by the dispatcher.

Industry verification (May 2026) found that Anthropic's Claude Code has an
undocumented bulk-spawn rate limiter at 4-6 concurrent processes (issue
[#53922](https://github.com/anthropics/claude-code/issues/53922)). OpenCode's
behavior is **not yet verified empirically**; the V1 default assumes a similar
ceiling.

## Decision

1. **Outbound port** `internal/ports/outbound/dispatcher.go` defines the
   `AgentDispatcher` interface with methods:
   - `Provider() session.Provider`
   - `SuggestedMaxConcurrent() int`
   - `HealthCheck(ctx)`
   - `Dispatch(ctx, DispatchRequest) -> DispatchResult`

2. **V1 implementation** lives at
   `internal/adapters/outbound/dispatcher/opencode/dispatcher.go`. It calls
   `runtime-adapters` `shell.exec@v1` capability with the OpenCode CLI.

3. **V1 default `SuggestedMaxConcurrent` = 4.** Hard cap = 6. The Spawn
   Governor enforces these caps across all providers.

4. **V2 implementations** (Claude Code, Cursor, Gemini) add new adapter
   directories. Each requires its own ADR documenting:
   - Rate limiter ceiling (empirically verified)
   - Prompt format and stdin/stdout contract
   - Envelope extraction strategy

5. **Worktree management stays in the orchestrator**, via runtime-adapters'
   `git.worktree.*` capabilities (Phase 2). Dispatchers receive a
   `WorktreePath` argument and never manage worktrees themselves.

## Consequences

### Positive

- Adding a new provider in V2 is a contained change: one adapter + one ADR.
- Worktree semantics are uniform across providers.
- Spawn Governor caps apply globally; no per-provider escape.

### Negative

- The exact OpenCode CLI flags and envelope-extraction details are deferred
  to first contact (Open Question § 13.2 in the spec). The V1 dispatcher
  ships with a placeholder implementation that will be tightened during the
  first contract test against real OpenCode.
- `SuggestedMaxConcurrent = 4` is conservative; we may relax after empirical
  rate-limiter testing of OpenCode.

### Neutral

- The interface is small (4 methods); no risk of leaky abstraction yet.

## Alternatives considered

- **Direct embedding of LLM SDKs (e.g., `anthropic-go`)**: rejected — the
  orchestrator's job is to dispatch agents, not to talk to LLMs. LLM
  integration lives inside the OpenCode subprocess.
- **MCP-only dispatch**: rejected — would force the orchestrator to be both
  HTTP server (for CLI) and MCP server (for dispatch). MCP exposure is on
  the V2 roadmap as a separate addition, not a replacement.
- **Single hard-coded OpenCode call without abstraction**: rejected — locks
  out V2 multi-provider support; no real complexity savings.
