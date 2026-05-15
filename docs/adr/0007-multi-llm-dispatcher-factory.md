# ADR-0007 — Multi-LLM Dispatcher Factory (V2.0)

- **Status**: accepted
- **Date**: 2026-05-15
- **Deciders**: Russell Vergara
- **Context**:

  V1.5 of Sophia ships with a single dispatcher implementation
  (`opencode.Dispatcher`) injected directly into `phase.Service` and
  `apply.RunService`. PR #16 added per-phase MODEL routing
  (`SOPHIA_DISPATCHER_MODEL_<PHASE>`), which already enables a
  hybrid-model strategy via opencode's multi-provider support
  (Copilot/OpenAI/Google OAuth all routable through one CLI).

  However, opencode is not the only viable agent CLI. Future
  deployments may want to invoke `aider`, `ollama`, a future first-
  party Sophia agent, or even a hosted agent service via gRPC. ADR-0002
  ("dispatcher abstraction") anticipated this by defining
  `outbound.AgentDispatcher` as a provider-agnostic port — but the
  bootstrap layer wires exactly one implementation. Adding a second
  adapter today requires modifying every call site that touches
  `s.d.Dispatcher.Dispatch(...)`.

  V2.0 introduces a **factory pattern** so adapters can be added
  without touching `service.go` or `teamlead.go`, and so per-phase
  PROVIDER selection (not just model selection) becomes a runtime
  config choice.

- **Options considered**:
  - **Direct multi-injection**: pass a `map[string]AgentDispatcher` to
    every service and let the service do the lookup. Rejected — leaks
    routing concerns into business code.
  - **Conditional ifs in service.go**: branch on `req.PhaseType` and
    dispatch to the right adapter. Rejected — same routing-concern
    leak, plus rewriting every dispatch call site for every new
    adapter.
  - **Factory + WrappingDispatcher** *(chosen)*: the factory owns
    `name → adapter` registration; a single `WrappingDispatcher`
    implements `AgentDispatcher` and routes each call internally.
    `service.go` and `teamlead.go` keep talking to one dispatcher.

- **Decision**:

  Add `outbound.DispatcherFactory` interface with three methods:
  `Get(provider) → (AgentDispatcher, error)`, `Default()`,
  `Providers()`. Implement it in
  `internal/adapters/outbound/dispatcher/factory/`. Construct it in
  bootstrap with the existing opencode adapter as the only registered
  provider; wrap it in `factory.WrappingDispatcher` and inject the
  wrapper everywhere `AgentDispatcher` was injected before. The
  routing decision per Dispatch call lives entirely in the wrapper,
  driven by `cfg.Dispatcher.ProviderByPhase`.

- **Consequences**:
  - **Backward compatible 100%.** No env var means default behavior
    (uses opencode for every phase). Existing
    `SOPHIA_DISPATCHER_MODEL`/`SOPHIA_DISPATCHER_MODEL_<PHASE>`
    continue working unchanged.
  - **Two new env vars**: `SOPHIA_DISPATCHER_PROVIDER` (global default
    provider, defaults to "opencode") and
    `SOPHIA_DISPATCHER_PROVIDER_<PHASE>` (per-phase override).
  - **Adding adapters is `factory.Register(name, adapter)` in bootstrap +
    one new package under `internal/adapters/outbound/dispatcher/`**.
    `service.go` and `teamlead.go` are NOT touched. This is the V2.1+
    entry point — aider, ollama, hosted-agent, etc.
  - **Typo tolerance**: an unknown provider name in a per-phase
    override falls back to the default adapter at runtime (no error),
    instead of breaking the phase. Misconfig surfaces in operator-
    facing healthcheck logs at startup.
  - **Routing decision is HOT path**: `WrappingDispatcher.Dispatch`
    does one map lookup per call. Factory implementation uses
    `sync.RWMutex` (read-favoured) so the cost is in the noise vs the
    LLM call latency.
  - **Session provenance**: `session.Provider` recorded on each phase
    is the DEFAULT adapter's value, not the actual adapter that ran
    the call. V2.0 trades per-call provenance for routing simplicity;
    if V2.1 needs it, add a field to `DispatchResult`.

- **Spec references**: D1.6 (dispatcher pluggable; OpenCode is V1
  default, others V2). PR #16 (per-phase model mapper) is the prereq
  for the routing semantics this ADR formalizes.
