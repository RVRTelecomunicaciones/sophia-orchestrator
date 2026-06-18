# governance-integration-contract Specification

> Source of truth (synced from change `governance-advisory`, archived 2026-06-16).
> Cross-repo: `sophia-orchestator` ↔ `agent-governance-core` (GAP A, reframed).
> The `/governance/v1/*` facade ALREADY ships and matches the orch client
> byte-for-byte (govcore `router.go:142-144`, `decisions_handler.go`,
> `govdecisions/service.go`, `wire.go:207` == orch `client.go:72-75`). This
> capability VERIFIES and LOCKS that contract; it does NOT build it. The locked
> test host is `sophia-orchestator/test/integration/governance_contract_lock_test.go`
> (`//go:build contract`), driving the real govcore facade in-process via the
> public `govhttptest` seam; both repos share the `go.work` workspace.

## Purpose

The orch governance client and the govcore `/governance/v1/*` facade are each
tested only in ISOLATION (orch uses a mock; govcore has handler tests). Nothing
proves them wired together end-to-end, so the matching contract can silently
drift under independent CI + wire-checksum enforcement. This capability adds ONE
cross-repo end-to-end integration test that exercises the REAL orch governance
client against the REAL govcore facade and LOCKS the contract against drift. NO
production code changes in either service.

## Requirements

### Requirement: End-to-end real-client / real-facade integration test

An integration test MUST exercise the orchestrator's REAL governance client
against agent-governance-core's REAL `/governance/v1/*` facade — the real chi
handlers and real `govdecisions` service, NOT mocks or stubs on either side.
In-process wiring of the real handlers + real service is acceptable and
PREFERRED for hermeticity; a live HTTP roundtrip is an acceptable alternative.
No production code in either service may be modified.

#### Scenario: Real client reaches real facade in-process

- GIVEN the unmodified orch governance client and the unmodified govcore facade wired together in-process via the shared `go.work` workspace
- WHEN the test drives the client against the facade
- THEN no mock or stub stands in for either the client or the facade
- AND no production source file in either repo is changed by the test

### Requirement: Phase decision path works end-to-end

The test MUST send a phase decision request through the real client to the real
facade and confirm the client deserializes the response without error.

#### Scenario: Phase decision returns a deserializable decision payload

- GIVEN the real orch client and real facade are wired
- WHEN the client issues a `POST /governance/v1/decisions/phase` with `{change_id, phase_type, task_description, sensitive}`
- THEN the facade responds HTTP 200 (no 404)
- AND the client deserializes the body without error with a populated `decision` field (`allow` today)

### Requirement: Sensitive-action decision path works end-to-end

The test MUST exercise the sensitive-action decision path through the real
client and real facade.

#### Scenario: Sensitive decision returns a deserializable decision payload

- GIVEN the real orch client and real facade are wired
- WHEN the client issues a `POST /governance/v1/decisions/sensitive`
- THEN the facade responds HTTP 200 (no 404)
- AND the client deserializes the body without error with a populated `decision` field

### Requirement: Approval-status path works end-to-end

The test MUST exercise the approval-status GET path on the
`{change_id}/{phase_id}/status` route through the real client and real facade.

#### Scenario: Approval-status GET returns a deserializable status payload

- GIVEN the real orch client and real facade are wired
- WHEN the client issues `GET /governance/v1/approvals/{change_id}/{phase_id}/status`
- THEN the facade responds HTTP 200 (no 404)
- AND the client deserializes the body without error with a populated `status` field

### Requirement: Contract is locked against drift (regression guard)

The test MUST fail if either side's request or response JSON shape drifts out of
compatibility. This is the cross-repo regression guard the isolated tests cannot
provide.

#### Scenario: Request/response shape drift fails the test

- GIVEN the locked e2e test is green
- WHEN either the orch client request shape or the govcore facade request/response shape changes incompatibly
- THEN the e2e test fails (deserialization error, missing field, or 4xx/5xx)
- AND the failure surfaces the cross-repo incompatibility before it ships

## Non-Goals

- NO facade rebuild or modification — the `/governance/v1/*` facade already ships and matches; production code in BOTH services is untouched.
- NO `constrain` → `allow_with_constraints` enum mapping — no producer emits a non-`allow` decision today, so the mapping has no code path; recorded as follow-up only.
- The facade's current default-allow + audit behavior is taken AS-IS; this capability does not change governance semantics.

## Implementation note (as shipped)

The locked test resolves the cross-module dependency via `go.work`
(`use ./agent-governance-core`) plus the public `govhttptest` seam — NOT a
`go.mod require` — because govcore's module path
(`github.com/RVRTelecomunicaciones/agent-governance-core`) does not match its remote
(`RVRTelecomunicaciones`), so any `require` fails VCS lookup. CI runs the test in
a dedicated `contract` job that checks out both repos, runs `go work init`, and
builds with `-tags=contract`; the standard `integration` job does NOT compile the
contract test. The `contract` job is gated on the `ECOSYSTEM_REPO_TOKEN` secret
(configured) and on govcore main carrying the `govhttptest` seam (PR #10, merged).
