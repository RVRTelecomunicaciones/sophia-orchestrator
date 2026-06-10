# Delta: consolidation-worker-skeleton

## Capability

Runnable worker process entry point at `sophia-memory-engine/cmd/workers/main.go`. Replaces the 5-line TODO stub with a skeleton that starts with graceful context-cancellation lifecycle, registers a stub `phase.archived` handler that logs receipt, and exposes a publisher interface so M2 can wire a real transport without rewriting the handler. No consolidation logic is executed in PRE-0. No transport is chosen.

**Source refs:** proposal §Scope item 3; explore §Item 3 (Worker skeleton); explore §Worker subscription approaches considered.

---

## ADDED Requirements

### Requirement: Worker Process Lifecycle

The system MUST start the worker process with a `context.Context` that is cancelled on SIGINT or SIGTERM, and MUST exit cleanly after all in-flight handlers complete.

#### Scenario: Worker starts without error

- GIVEN a correctly compiled `cmd/workers/main.go` binary
- WHEN the binary is started
- THEN it initializes without panicking
- AND it logs a startup message via `slog`
- AND it enters its event-listening loop

#### Scenario: Worker shuts down cleanly on SIGINT

- GIVEN the worker binary is running
- WHEN SIGINT is sent to the process
- THEN the root context is cancelled
- AND the worker exits with code 0 after in-flight work drains

---

### Requirement: Stub Handler for phase.archived

The system MUST register a handler for the `"phase.archived"` event string. In PRE-0 the handler MUST log receipt of the payload and MUST NOT execute any consolidation logic.

#### Scenario: Handler logs receipt of a fake payload

- GIVEN the worker is initialized with a fake/in-memory publisher
- WHEN the fake publisher delivers a payload with `change_id = "test-change-001"` and `phase_type = "archive"`
- THEN the handler logs a message containing the received `change_id`
- AND no consolidation logic is invoked

#### Scenario: Handler does not execute consolidation in PRE-0

- GIVEN the worker receives a `phase.archived` event
- WHEN the stub handler runs
- THEN no external calls to memory-engine, Graphify, or any consolidation service are made

---

### Requirement: Publisher Interface for M2 Extensibility

The system MUST expose a `Publisher` interface (or equivalent abstraction) that the stub handler accepts as a dependency, so M2 can inject a real transport implementation without modifying the handler.

#### Scenario: Fake publisher satisfies the interface in tests

- GIVEN a test-only fake implementation of the `Publisher` interface
- WHEN the fake is passed to the worker initialization
- THEN the worker compiles and the handler can be exercised in unit tests
- AND no real network transport is required

#### Scenario: Real publisher slot is marked deferred

- GIVEN the `cmd/workers/main.go` source
- WHEN the production publisher wiring point is inspected
- THEN a `// TODO(M2): wire real transport` comment or typed nil is present
- AND the code compiles and starts without a real publisher configured

---

### Requirement: Worker Transport Deferred to M2

The system MUST NOT commit to any real event transport (SSE client, webhook receiver, or message bus) in PRE-0. The skeleton MUST compile and satisfy acceptance criteria using only a fake in-memory publisher.

#### Scenario: Binary compiles without a transport dependency

- GIVEN `sophia-memory-engine/cmd/workers/main.go` at PRE-0
- WHEN `go build ./cmd/workers/...` runs
- THEN the binary is produced without importing any SSE, webhook, or message-bus library beyond what already exists in memory-engine
