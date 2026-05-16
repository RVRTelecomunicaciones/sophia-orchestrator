# Sophia Wire Protocol v1

```
spec id      : sophia-wire-v1
status       : draft
draft date   : 2026-05-07
target tag   : sophia-cli v0.2.0  +  sophia-orchestator v0.2.0
authority    : RVRTelecomunicaciones — single architectural owner
master copy  : sophia-cli/docs/specs/sophia-wire-v1.md
mirror copy  : sophia-orchestator/docs/specs/sophia-wire-v1.md
checksum     : docs/specs/sophia-wire-v1.sha256 (CI-enforced parity)
supersedes   : (none — first canonical version)
related      : sophia-cli/docs/adr/0003-cross-repo-wire-alignment.md
               sophia-cli/docs/superpowers/plans/2026-05-07-sophia-m10-wire-alignment-v0.2.0.md
```

> **What this document is.** The single source of truth for the HTTP +
> Server-Sent Events surface that connects `sophia-cli` (client) to
> `sophia-orchestator` (server). Both repositories implement this
> document verbatim; both repositories' CI gates a SHA256 checksum to
> detect drift.
>
> **What this document is not.** It does not describe internal
> orchestration logic, governance policy, memory subsystems, or
> agent-runtime adapters. Those are the concern of their respective
> repos.

---

## 1. Status, version, and lifecycle

- **`version`** field, present in every `info`/diagnostic response and
  in the `User-Agent` of HTTP clients: `v1`.
- **Backward-incompatible change policy:** any change that would cause
  a v1-conformant client to fail when talking to the new server (or
  vice versa) increments the major: `v2` lives in a separate spec
  file. Servers MAY support multiple versions concurrently via an
  `API-Version` request header (deferred to spec v2; v1 ignores the
  header).
- **Forward-compatible change policy:** clients MUST ignore unknown
  JSON object fields and MUST skip unknown SSE event types with a
  single warning to stderr. Adding new optional fields, new event
  types, or new endpoints is non-breaking.
- **Spec lifecycle:** `draft` → `accepted` (at v0.2.0 final tag) →
  `superseded` (when v2 ships).

## 2. Transport invariants

- **Protocol:** HTTP/1.1 and HTTP/2 are both acceptable; clients MUST
  not assume one over the other for non-streaming requests. SSE
  endpoints (Section 5) work over HTTP/1.1 with `Connection: keep-alive`
  and over HTTP/2 with stream multiplexing.
- **Encoding:** JSON over UTF-8 for all request and response bodies
  except SSE streams (which use the SSE wire format defined by the
  HTML spec's "Server-Sent Events" section).
- **Content type:** `Content-Type: application/json; charset=utf-8`
  on JSON requests and responses. `Content-Type: text/event-stream`
  on SSE responses.
- **Identifiers:** all IDs (`change_id`, `phase_id`) are
  [ULID](https://github.com/ulid/spec) strings (Crockford base-32, 26
  chars). They are globally unique across the orchestrator's history
  for their respective entity type. Phase IDs do not collide with
  change IDs by construction.
- **Timestamps:** ISO-8601 in UTC with millisecond precision.
  Example: `2026-05-07T15:42:08.123Z`. Clients MUST tolerate
  zero-precision (`2026-05-07T15:42:08Z`) and fractional precision up
  to nanoseconds.
- **HTTP method semantics:** GET is idempotent and safe; POST is
  used for state-mutating operations even when CRUD-mapping suggests
  PUT/PATCH (chosen for symmetry across all operations).
- **Trailing slashes:** server MUST accept both forms; canonical paths
  in this spec are without trailing slash.

## 3. Auth scheme

### 3.1 Header

```
X-Sophia-API-Key: <opaque-string>
```

The header is the sole auth mechanism in v1. No bearer tokens, no
basic auth, no mTLS, no signed-request schemes. Future auth modes
ship in v2.

### 3.2 Server requirement matrix

The server MUST require the header on EVERY authenticated route
(every route under `/api/v1/changes`, `/api/v1/phases`,
`/api/v1/approvals`) UNLESS:

1. The server's HTTP listener is bound exclusively to a loopback
   address (`127.0.0.0/8`, `::1`, or the literal `localhost`), AND
2. The server's runtime config sets `HTTP.AllowAnonLocalhost = true`.

Both conditions must hold. If the listener binds any non-loopback
interface (including `0.0.0.0` or a specific routable IP), the header
is required regardless of `AllowAnonLocalhost`.

### 3.3 Client behavior

The client SHOULD send the header on every request. Specifically:

- If `SOPHIA_API_KEY` env var is set OR `--api-key` flag is provided,
  send the header on EVERY request (including those to localhost).
- If neither is set:
  - Parse `SOPHIA_ORCHESTRATOR_URL`. If host is loopback (per the
    same `127.0.0.0/8 / ::1 / localhost` set as section 3.2) AND no
    key is configured, send no header. The server will accept if its
    config allows anon-loopback; otherwise it will 401.
  - If host is non-loopback AND no key is configured, the client MUST
    fail with exit code 3 and a friendly message before any HTTP call:
    `auth required for remote orchestrator (set SOPHIA_API_KEY or --api-key)`.

### 3.4 Auth error envelope

Server responds to auth failures with HTTP 401 and:

```json
{"code":"unauthorized","error":"X-Sophia-API-Key required"}
```

Stable error code `unauthorized`. The `error` text is human-readable
and may vary between server versions; clients MUST switch on `code`,
not `error`.

### 3.5 Key rotation, multi-tenancy, scopes

Out of scope for v1. The server's key lookup function is treated as
a black box that accepts a string and returns `(authenticated bool,
project string, error error)`. v1 is single-tenant in practice.

## 4. Endpoint catalog

### 4.1 Health

#### `GET /api/v1/health`

- **Auth:** none.
- **Purpose:** process liveness probe (D-M10-14). MUST return 200 if
  the HTTP server is up, regardless of downstream dependency state.
- **Response 200:**
  ```json
  {
    "status": "ok",
    "version": "v1",
    "uptime_seconds": 12345,
    "checked_at": "2026-05-07T15:42:08.123Z"
  }
  ```
- **Response 5xx:** never expected from a running server. If the HTTP
  layer is broken enough to return 5xx, treat the host as down.
- **Client behavior:** `sophia doctor` HARD-gates on this. Non-200 →
  `doctor` fails with exit 3.

#### `GET /api/v1/ready`

- **Auth:** none.
- **Purpose:** dependency-readiness probe (D-M10-14). Returns 200
  when DB + downstream services (governance, memory, runtime) are
  reachable; 503 with details if any is degraded.
- **Response 200:**
  ```json
  {
    "status": "ready",
    "checks": {
      "database": "ok",
      "governance": "ok",
      "memory": "ok",
      "runtime": "ok"
    }
  }
  ```
- **Response 503:**
  ```json
  {
    "status": "not_ready",
    "checks": {
      "database": "ok",
      "governance": "ok",
      "memory": "unreachable",
      "runtime": "ok"
    },
    "error": "memory: dial tcp 10.0.0.5:9002: i/o timeout"
  }
  ```
- **Client behavior:** `sophia doctor` SOFT-gates: 200 → green;
  503 → yellow check with "(orchestrator dependencies degraded)"
  annotation but doctor stays exit 0; absent endpoint → "ready
  endpoint not implemented; skipping". Doctor MUST NOT exit non-zero
  on `/ready` failure alone.

### 4.2 Changes

#### `POST /api/v1/changes`

- **Auth:** required.
- **Request:**
  ```json
  {
    "name": "implement /healthz endpoint",
    "project": "ms-x",
    "base_ref": "main",
    "artifact_store_mode": "memory-engine"
  }
  ```
  Required: `name`, `project`. Optional: `base_ref` (default `"main"`),
  `artifact_store_mode` (default `"memory-engine"`).
- **Response 201:** `ChangeResponse` (Section 6.1) with `status` set
  to `"running"` (or `"pending"` if the orchestrator queues briefly
  before picking up).
- **Errors:**
  - 400 `validation_failed` — bad input (missing `name`, etc).
  - 401 `unauthorized` — missing/invalid key.
  - 409 `change_already_exists` — orchestrator policy disallows
    duplicate `(project, name)` within a window (rare; documented in
    governance docs).

#### `GET /api/v1/changes`

- **Auth:** required.
- **Query params** (Section 7):
  - `project` (string, optional) — filter to one project. Empty or
    omitted → all projects.
  - `status` (string, optional) — filter to one status (`pending`,
    `running`, `done`, `blocked`, `failed`).
  - `limit` (int, default 10, max 100) — page size.
  - `offset` (int, default 0) — page start.
- **Response 200:**
  ```json
  {
    "items": [ChangeResponse, ...],
    "total": 42,
    "limit": 10,
    "offset": 0
  }
  ```
- **Errors:** 400 on invalid query (`limit > 100`, etc).

#### `GET /api/v1/changes/{change_id}`

- **Auth:** required.
- **Response 200:** `ChangeResponse`.
- **Errors:**
  - 404 `change_not_found` — unknown ID.

#### `POST /api/v1/changes/{change_id}/abort`

- **Auth:** required.
- **Request:**
  ```json
  { "reason": "user requested" }
  ```
  `reason` optional.
- **Response 200:**
  ```json
  { "status": "aborted", "change_id": "01HX...", "aborted_at": "2026-05-07T..." }
  ```
- **Errors:**
  - 404 `change_not_found`.
  - 409 `change_already_terminal` — already done/failed/blocked.

### 4.3 Phases

Phase IDs are globally unique (Section 2 ULID rule). Endpoints are
phase-scoped (no redundant change-id in the URL) per D-M10-13 Form A.
The exception is `POST /changes/{change_id}/phases/{phase_type}/run`
which creates a phase that does not yet have an ID — the change-id
resolves the parent.

#### `POST /api/v1/changes/{change_id}/phases/{phase_type}/run`

- **Auth:** required.
- **Status:** **intentionally unsupported by `sophia-cli` v1.** The
  orchestrator drives phase boundaries autonomously per its own
  governance rules; the CLI does not control phase advancement.
  Listed here for completeness because the orchestrator MUST expose
  it for governance / admin tooling.
- **Request:** orchestrator-internal; not part of the CLI contract.
- **Response 202** with phase snapshot. Documented in the orchestrator
  repo, not here.

#### `GET /api/v1/phases/{phase_id}`

- **Auth:** required.
- **Response 200:** `PhaseResponse` (Section 6.2).
- **Errors:** 404 `phase_not_found`.

#### `POST /api/v1/phases/{phase_id}/resume`

- **Auth:** required.
- **Status:** **optional in v1.** CLI surfaces `sophia resume <phase_id>`
  only in `--no-tui --json` mode; TUI flows do not expose it
  (resumption is handled by the orchestrator's own retry budget).
- **Request:** none.
- **Response 202:** `PhaseResponse` with `status` set to `"running"`.
- **Errors:**
  - 404 `phase_not_found`.
  - 409 `phase_not_resumable` — already running, or terminal.

#### `POST /api/v1/phases/{phase_id}/approve`

- **Auth:** required.
- **D-M10-13 Form A.** Phase-scoped; no change-id in URL.
- **Request:**
  ```json
  { "approver": "russell", "reason": "looks good" }
  ```
  `approver` required, `reason` optional.
- **Response 200:**
  ```json
  { "status": "approved", "phase_id": "01HX...", "decided_at": "2026-05-07T...", "approver": "russell" }
  ```
- **Idempotency** (D-M10-03): if the gate has already been approved
  (regardless of channel: in-band POST, browser-flow, etc), return
  409 `gate_already_decided` with the original decision payload.
- **Errors:**
  - 404 `phase_not_found`.
  - 409 `gate_already_decided` — second decision after first.
  - 409 `phase_not_gated` — phase is not currently awaiting approval.
  - 422 `approver_required` — empty `approver`.

#### `POST /api/v1/phases/{phase_id}/reject`

Symmetric to `/approve`. Response status is `"rejected"`.

#### `GET /api/v1/phases/{phase_id}/board`

- **Auth:** required.
- **Status:** **optional in v1.** CLI's TUI ApplyBoard view (M7)
  uses this endpoint as a refresh source; if it returns 404 or is
  absent, the TUI falls back to SSE-derived state (the M7 default).
- **Response 200:** `BoardResponse` (Section 6.3).
- **Errors:** 404 `phase_not_found` or `phase_has_no_board`.

#### `GET /api/v1/phases/{phase_id}/events`

- **Auth:** required (see also Section 5.2).
- **Response 200** with `Content-Type: text/event-stream`. SSE stream
  (Section 5) of events scoped to a single phase. The stream closes
  cleanly when the phase reaches a terminal status (server emits a
  final `phase.completed` / `phase.failed` event then closes).
  Server emits a `heartbeat` event every 15 seconds to keep
  middleware connections alive.
- **`Last-Event-ID` request header** is honored within the lifetime
  of a single phase: a reconnecting client resumes from the event
  immediately after the cited ID. Across phases, `Last-Event-ID` is
  ignored (the multiplexer in Section 5.4 explicitly resets on phase
  switch).
- **Errors:**
  - 404 `phase_not_found`.
  - 410 `phase_terminal_no_events` — phase is already terminal and
    the event log was retained but not streamable; the client should
    GET the phase snapshot instead.

### 4.4 Approvals (Form B — reserved for v0.3.0+)

D-M10-13 Form B (`POST /api/v1/approvals/{gate_id}/approve|reject`)
is RESERVED but NOT IMPLEMENTED in v1. v1 uses Form A exclusively.
v2/v0.3.0 may introduce gate IDs distinct from phase IDs; if so, the
v1 phase-scoped endpoints stay valid (multiple gates per phase
addressed by deciding on the most-recent open gate).

### 4.5 Out-of-scope endpoints

The orchestrator MAY expose the following; the CLI does NOT call them
in v1 and treats them as "intentionally unsupported":

- `GET /metrics` — Prometheus scrape target.
- `POST /api/v1/changes/{change_id}/phases/{phase_type}/run` (already
  noted in 4.3).
- Admin / debug endpoints under `/api/v1/admin/*` (orchestrator's
  decision; not a v1-spec concern).

## 5. SSE event stream

### 5.1 Wire format

Standard SSE per HTML living spec, "Server-Sent Events" section.
Each event has the form:

```
event: <type>
id: <ULID>
data: <JSON object>

```

Two newlines terminate an event. Comments (`: ...`) MAY appear and
clients MUST skip them.

### 5.2 Auth on SSE

Same `X-Sophia-API-Key` header as REST (Section 3.1). The CLI MUST
send it on the initial GET; SSE reconnects (driven by the server
closing the stream) MUST resend the header on each new GET.

### 5.3 Event taxonomy

The following event types are part of v1. Each carries a
`payload` JSON object whose schema is documented per type. Clients
MUST skip unknown event types (forward-compat).

| Type | Required? | Payload schema | Emitted by |
|------|-----------|----------------|------------|
| `heartbeat` | required | `{ "ts": "..." }` | server every 15s of idle |
| `phase.started` | required | `{ "phase_id", "phase_type", "started_at" }` | orchestrator on phase boot |
| `phase.completed` | required | `{ "phase_id", "phase_type", "ended_at", "confidence" }` | orchestrator on phase ok-terminal |
| `phase.failed` | required | `{ "phase_id", "phase_type", "ended_at", "error" }` | orchestrator on phase fail-terminal |
| `task.created` | optional | `{ "task_id", "phase_id", "name", "agent" }` | apply-phase task spawn |
| `task.started` | optional | `{ "task_id", "started_at" }` | apply-phase task start |
| `task.completed` | optional | `{ "task_id", "ended_at", "output_summary?" }` | apply-phase task ok-terminal |
| `task.failed` | optional | `{ "task_id", "ended_at", "error" }` | apply-phase task fail-terminal |
| `agent.dispatched` | optional | `{ "agent_id", "task_id", "model" }` | apply-phase agent dispatch |
| `agent.completed` | optional | `{ "agent_id", "ended_at" }` | apply-phase agent done |
| `approval.required` | required | `{ "phase_id", "gate_url", "reason", "risk?", "policy?" }` | governance gate trigger |
| `approval.resolved` | required | `{ "phase_id", "decision", "approver", "reason?", "decided_at" }` | gate decision (any channel) |
| `open` | optional | `{ "phase_id" }` | server emits at SSE stream open as a connection-live signal; clients MAY use it for fast reconnect detection. Skipping is safe. |
| `phase.completed_with_concerns` | optional (orch-internal extension) | `{ "phase_id", "phase_type", "ended_at", "confidence", "concerns" }` | orchestrator emits when a phase finishes successfully but Iron Law / governance flagged advisory concerns. Clients MAY surface as a yellow timeline marker; skipping is safe. |
| `phase.needs_context` | optional (orch-internal extension) | `{ "phase_id", "phase_type", "missing": [string] }` | orchestrator emits when a phase requires additional context to proceed. Clients MAY surface a hint; skipping is safe. |
| `agent.envelope.received` | optional (orch-internal extension) | `{ "agent_id", "phase_id", "envelope_hash" }` | orchestrator emits when a dispatched agent returns its envelope. Diagnostic signal; clients MAY ignore. |
| `apply.board.created` / `apply.group.completed` / `apply.group.failed` / `apply.board.save_failed` / `apply.worktree.error` | optional (apply-phase diagnostic events) | `{ phase-specific fields }` | apply-phase coordination diagnostics. Clients MAY surface as additional ApplyBoard signal but MUST tolerate absence + must NOT exit on these alone. The CLI's M7 ApplyBoard view does NOT depend on these. |

`phase_id` in the payload of every phase-bound event MUST equal the
`phase_id` in the URL of the GET that opened the stream. Cross-phase
events MUST NOT appear on a per-phase stream (the multiplexer in 5.4
relies on this invariant).

**Note on `risk` and `policy` in `approval.required`** (Phase 1.5
amendment, 2026-05-07): these were originally specified as required
fields. The audit (`docs/specs/contract-readiness-audit.md` §2.2)
revealed that the orchestrator's governance contract does NOT
currently surface these fields, and forcing them into the wire
required either (a) a governance contract change (out of M10 scope)
or (b) downgrading them to Optional. v1 takes path (b). Servers MAY
emit them when the underlying governance decision carries the data;
clients MUST tolerate their absence. v0.3.0 may promote them back to
required once the governance contract evolves.

**Note on Optional / orch-internal extension events** (Phase 1.5
amendment, 2026-05-07): the events tagged "orch-internal extension"
above (`phase.completed_with_concerns`, `phase.needs_context`,
`agent.envelope.received`) and the `apply.*` diagnostic family exist
in the orchestrator's emission surface today. They are documented
here so that:
1. CLI implementers can decide whether to consume them (Optional
   per the compatibility matrix) without "unknown event type" log
   noise.
2. The wire spec is honest about the orchestrator's full output —
   clients of the spec see what they may receive.
3. Future spec versions can promote them to required without breaking
   v1 clients (forward-compat per §10).

The CLI v0.2.0 marks all of them Optional in its compatibility
matrix; absence MUST NOT cause exit non-zero, and presence MUST NOT
cause unrecognized-event log noise.

### 5.4 Phase-stream multiplexer protocol (D-M10-05)

The CLI subscribes to per-phase streams (`/api/v1/phases/{phase_id}/events`),
not per-change. To follow a change end-to-end, the CLI must transition
between phase streams as the change advances:

1. The CLI fetches `GET /api/v1/changes/{change_id}` to learn the
   current phase: `current_phase_id`.
2. The CLI subscribes to `/api/v1/phases/{current_phase_id}/events`.
3. The CLI consumes events until the stream closes.
4. After close, the CLI fetches `GET /api/v1/changes/{change_id}`
   again. If the change is terminal (status ∈ `done`, `blocked`,
   `failed`), the CLI exits per Section 9 exit-code mapping.
5. If the change is not terminal AND `current_phase_id` changed,
   GOTO step 2 with the new phase ID.
6. If the change is not terminal AND `current_phase_id` is unchanged,
   the close was a network blip; retry step 2 with bounded backoff
   (server SHOULD allow `Last-Event-ID` resume per Section 4.3).
7. Approval gates do NOT close the phase stream; they emit
   `approval.required` and the stream stays open. The CLI handles
   the gate via Section 8.

The server-side invariant: a phase stream closes ONLY when the phase
itself reaches a terminal status. Server MUST NOT silently disconnect
a healthy stream for load-balancer reasons without sending a final
event; if it must, the client treats it as a transient error and
resumes (step 6).

## 6. Response shapes

### 6.1 `ChangeResponse`

```json
{
  "change_id": "01HX...",
  "name": "implement /healthz endpoint",
  "project": "ms-x",
  "base_ref": "main",
  "artifact_store_mode": "memory-engine",
  "status": "running",
  "current_phase_id": "01HY...",
  "phases": [PhaseDTO, ...],
  "created_at": "2026-05-07T...",
  "updated_at": "2026-05-07T..."
}
```

`status` ∈ `{ "pending", "running", "done", "blocked", "failed", "aborted" }`.

`PhaseDTO` (embedded in `phases`):

```json
{
  "phase_id": "01HY...",
  "phase_type": "implement",
  "status": "running",
  "confidence": 0.92,
  "started_at": "2026-05-07T...",
  "ended_at": "2026-05-07T..."
}
```

`phase_type` ∈ `{ "init", "explore", "proposal", "spec", "design",
"tasks", "apply", "verify", "archive" }` (the orchestrator's 9
canonical SDD phases).

`phase status` ∈ `{ "pending", "running", "blocked", "done", "failed" }`.
`blocked` indicates the phase is waiting on an approval gate.

### 6.2 `PhaseResponse`

Standalone phase fetch:

```json
{
  "phase_id": "01HY...",
  "change_id": "01HX...",
  "phase_type": "implement",
  "status": "blocked",
  "confidence": 0.84,
  "attempts": 1,
  "retry_budget": 3,
  "started_at": "2026-05-07T...",
  "ended_at": null,
  "blocked_reason": "approval required: high-risk diff"
}
```

### 6.3 `BoardResponse`

```json
{
  "board_id": "01HZ...",
  "phase_id": "01HY...",
  "status": "running",
  "groups": [
    {
      "group_id": "g1",
      "name": "domain",
      "status": "running",
      "tasks": [
        {
          "task_id": "t1",
          "name": "extract Runner.Observe",
          "status": "running",
          "agents": [
            { "agent_id": "a1", "model": "opus-4-7", "status": "dispatched" }
          ]
        }
      ]
    }
  ]
}
```

## 7. Pagination

List endpoints (currently only `GET /api/v1/changes` in v1) accept:

- `limit` — 1..100. Default 10.
- `offset` — 0..N. Default 0.

Servers MUST clamp `limit` to 100 and reject `limit > 100` with 400
`limit_too_large`. Total count is included in the response so clients
can implement page navigation. Cursor-based pagination is reserved
for v2.

## 8. Approval flow (D-M10-03)

A phase reaches an approval gate when its `status` becomes `blocked`.
The orchestrator emits an `approval.required` event on the phase's
SSE stream with `{ phase_id, gate_url, reason, risk, policy }`.

### 8.1 Decision channels (equivalent + idempotent)

- **In-band POST:** `POST /api/v1/phases/{phase_id}/approve` or
  `/reject` per Section 4.3. CLI ships `sophia approve` and
  `sophia reject` commands.
- **Out-of-band browser:** the user opens `gate_url` in a browser.
  The orchestrator's web UI / governance UI surfaces the same
  decision endpoints behind a session-cookie auth path. CLI's M7
  TUI keybinding `[O]` opens this URL.

Both channels resolve the gate. The orchestrator MUST treat the
first decision as authoritative and respond 409 `gate_already_decided`
to any subsequent attempt (regardless of channel).

### 8.2 Decision propagation

Upon a successful decision, the orchestrator emits an
`approval.resolved` event on the phase's SSE stream with
`{ phase_id, decision: "approved" | "rejected", approver, reason?, decided_at }`.
Clients MUST:

- Update local UI state to clear the gate banner.
- Cancel any approval-timeout timer (`approvalTimeoutSink` per M7).
- Continue consuming the phase stream until the phase itself reaches
  a terminal status — `approval.resolved` does NOT close the phase
  stream.

### 8.3 Approval timeout

CLI's `--approval-timeout` flag (M7 / spec §5.8) starts a timer when
the CLI sees the FIRST `approval.required` event for a given gate
(eager-arm if `attach` connects to a phase already in `blocked` per
D-M8-13 / cambio 3). Timer expiry yields exit code 5. Timer cancels
on `approval.resolved`.

The orchestrator does NOT enforce a timeout; it has no concept of
client-side patience. Timeouts are purely a CLI UX decision.

## 9. Errors

### 9.1 Error envelope

All error responses (4xx, 5xx) carry:

```json
{
  "code": "<machine_code>",
  "error": "<human_readable_message>",
  "details": { ... }
}
```

`code` is stable across server versions; `error` is human-readable
and may vary; `details` is an open object whose schema depends on
`code`.

### 9.2 Stable error codes (v1)

| Code | HTTP status | Meaning |
|------|-------------|---------|
| `unauthorized` | 401 | Missing or invalid `X-Sophia-API-Key`. |
| `validation_failed` | 400 | Request body or query failed schema validation. `details` carries `{field: reason}` map. |
| `change_not_found` | 404 | `change_id` unknown. |
| `change_already_exists` | 409 | Project policy disallowed the create. |
| `change_already_terminal` | 409 | Abort against a done/failed/blocked change. |
| `phase_not_found` | 404 | `phase_id` unknown. |
| `phase_not_resumable` | 409 | Resume against a non-resumable phase. |
| `phase_not_gated` | 409 | Approve/reject against a phase that isn't currently blocked. |
| `gate_already_decided` | 409 | Second decision after first. |
| `phase_terminal_no_events` | 410 | SSE GET against a terminal phase whose stream is gone. |
| `approver_required` | 422 | `approver` field missing on approve/reject. |
| `limit_too_large` | 400 | Pagination clamp violation. |
| `internal_error` | 500 | Unexpected server-side failure. `details.trace_id` for log correlation. |

### 9.3 Client exit-code mapping (`*application.ExitError`)

Mirrors sophia-cli's M5-M8 mapping:

| Server response | Client interpretation | CLI exit code |
|-----------------|------------------------|---------------|
| 200 + change `done` | success | 0 |
| 200 + change `blocked` / `failed` | terminal-not-success | 1 |
| 401 `unauthorized` (any) | config error | 3 |
| 404 `change_not_found` / `phase_not_found` | resource missing | 3 |
| 5xx / network | orchestrator unreachable | 3 |
| ctx cancel / fetch deadline | transient | 4 |
| approval timeout (CLI-side) | timeout | 5 |

## 10. Forward compatibility

### 10.1 Unknown JSON fields

Clients MUST decode JSON with "ignore unknown fields" semantics
(default behavior of Go's `encoding/json`). Adding fields to a
response is non-breaking.

### 10.2 Unknown SSE event types

Clients MUST log a single-line warning to stderr (`sophia: unknown
SSE event type 'foo' (skipped)`) and continue. Adding event types
is non-breaking.

### 10.3 Removed fields and types

Removing a field or event type IS breaking — must wait for v2.

### 10.4 Default-value migrations

Changing the default of an optional request field IS NOT breaking
provided the new default round-trips through old clients without
behavior change. Deprecation cycle: 2 minor versions of warning
before removal.

### 10.5 New endpoints

Adding endpoints is non-breaking. Clients targeting v1 MUST NOT
fail when they observe the orchestrator advertising v2-only
endpoints they don't recognize.

## 11. Open governance considerations

These items are reserved for the v0.3.0 / v2 spec and explicitly NOT
part of v1:

- **Form B approvals** with distinct gate IDs separable from phase
  IDs (D-M10-13 alternative form).
- **API versioning header** `API-Version` for multi-version servers.
- **Multi-tenant API keys** with scopes and rotation policy.
- **mTLS / federated auth** modes.
- **Cursor-based pagination** for `GET /api/v1/changes`.
- **Server-Sent Events over WebSocket** as a transport alternative.
- **Per-phase permission grants** (today every authenticated key can
  approve any phase; future versions might constrain).

## Appendix A — endpoint summary table

| # | Method | Path | Auth | Class | CLI command(s) |
|---|--------|------|------|-------|----------------|
| 1 | GET | `/api/v1/health` | none | Required | `sophia doctor` |
| 2 | GET | `/api/v1/ready` | none | Optional | `sophia doctor` (degraded) |
| 3 | POST | `/api/v1/changes` | required | Required | `sophia run` |
| 4 | GET | `/api/v1/changes` | required | Required | `sophia changes` |
| 5 | GET | `/api/v1/changes/{change_id}` | required | Required | `sophia run`, `sophia attach`, `sophia status` |
| 6 | POST | `/api/v1/changes/{change_id}/abort` | required | Required | `sophia abort` |
| 7 | POST | `/api/v1/changes/{change_id}/phases/{phase_type}/run` | required | Intentionally unsupported (CLI) | (orchestrator-internal) |
| 8 | GET | `/api/v1/phases/{phase_id}` | required | Required | (multiplexer) |
| 9 | POST | `/api/v1/phases/{phase_id}/resume` | required | Optional | `sophia resume` (`--no-tui --json` only) |
| 10 | POST | `/api/v1/phases/{phase_id}/approve` | required | Required | `sophia approve` |
| 11 | POST | `/api/v1/phases/{phase_id}/reject` | required | Required | `sophia reject` |
| 12 | GET | `/api/v1/phases/{phase_id}/board` | required | Optional | TUI (M7) refresh |
| 13 | GET | `/api/v1/phases/{phase_id}/events` | required | Required | SSE multiplexer |

## Appendix B — change log

| Version | Date | Author | Note |
|---------|------|--------|------|
| 0.1 (draft) | 2026-05-07 | repository architect | First canonical draft. Authored from M9 inventory (`docs/superpowers/research/m10-wire-inventory.md`) + ADR-0003 + D-M10-01..17. Awaiting Phase 1 Task 1.2 (mirror) and Task 1.3 (ADR promotion). |
| 0.1.1 (cross-reviewed) | 2026-05-07 | repository architect | Phase 1 Task 1.1 Step 2 complete: every endpoint in `m10-wire-inventory.md` is either covered, explicitly migrated, or explicitly excluded. See Appendix C. |
| 0.1.2 (Phase 1.5 amendments) | 2026-05-07 | repository architect | Five amendments from the contract readiness audit (`docs/specs/contract-readiness-audit.md`): (a) `approval.required.risk` and `.policy` downgraded to Optional. (b) `open` event documented as Optional (server emits at stream open, payload `{phase_id}`). (c) `phase.completed_with_concerns`, `phase.needs_context`, `agent.envelope.received` documented as Optional orch-internal extensions. (d) `apply.*` diagnostic event family documented as Optional. (e) clarifying paragraph in §5.3 on the v1 status of these fields/events and forward-compat to v2. Spec checksum bumps; both repos must re-mirror in the same commit pair. |

## Appendix C — Inventory cross-review (Phase 1 Task 1.1 Step 2)

Full mapping from `docs/superpowers/research/m10-wire-inventory.md` to
this spec. Two columns: inventory entry → spec disposition.

### Inventory: sophia-cli outbound calls (6 entries)

| # | Inventory entry | Spec disposition |
|---|-----------------|------------------|
| 1 | CLI calls `GET /api/v1/healthz` | **MIGRATED** — Section 4.1 canonical is `/api/v1/health`. CLI changes per D-M10-06 / Phase 4 Task 4.1. |
| 2 | CLI calls `POST /api/v1/changes` (no auth) | **COVERED** — Section 4.2; spec adds required `X-Sophia-API-Key` per D-M10-02 (CLI changes per Phase 4 Task 4.2). |
| 3 | CLI calls `GET /api/v1/changes/{id}` (no auth) | **COVERED** — Section 4.2; auth header added. |
| 4 | CLI calls `GET /api/v1/changes?...` (no auth) | **COVERED** — Section 4.2 + Section 7 pagination; auth header added. |
| 5 | CLI calls `GET /api/v1/changes/{id}/events` (no auth) | **MIGRATED** — Section 4.3 + 5.4 phase-stream multiplexer per D-M10-05. New URL `/api/v1/phases/{phase_id}/events`. CLI replaces per Phase 4 Task 4.3. |
| 6 | CLI calls `GET /api/v1/events` (sseprobe) | **EXPLICITLY EXCLUDED** — D-M10-07. CLI removes `sseprobe` per Phase 4 Task 4.7. |

### Inventory: sophia-orchestator inbound routes (14 entries)

| # | Inventory entry | Spec disposition |
|---|-----------------|------------------|
| 1 | `HealthHandler.Check` GET `/api/v1/health` | **COVERED** — Section 4.1, no change. |
| 2 | `HealthHandler.Ready` GET `/api/v1/ready` | **COVERED** — Section 4.1, no change. |
| 3 | metrics GET `/metrics` | **EXPLICITLY EXCLUDED FROM CLI CONTRACT** — Section 4.5; orchestrator MAY expose, CLI doesn't call. |
| 4 | `ChangesHandler.Create` POST `/api/v1/changes` | **COVERED** — Section 4.2, no path change. |
| 5 | `ChangesHandler.List` GET `/api/v1/changes` | **COVERED** — Section 4.2 + Section 7. |
| 6 | `ChangesHandler.Get` GET `/api/v1/changes/{change_id}` | **COVERED** — Section 4.2. |
| 7 | `ChangesHandler.Abort` POST `/api/v1/changes/{change_id}/abort` | **COVERED** — Section 4.2. |
| 8 | `PhasesHandler.Run` POST `/api/v1/changes/{change_id}/phases/{phase_type}/run` | **COVERED** — Section 4.3, intentionally unsupported by CLI. Path retained (change-id needed because phase doesn't yet exist). |
| 9 | `PhasesHandler.Get` GET `/api/v1/changes/{change_id}/phases/{phase_id}` | **MIGRATED** — Section 4.3 phase-scoped form `/api/v1/phases/{phase_id}` per D-M10-13. Orchestrator changes per Phase 3 Task 3.1. |
| 10 | `PhasesHandler.Resume` POST `/api/v1/changes/{change_id}/phases/{phase_id}/resume` | **MIGRATED** — `/api/v1/phases/{phase_id}/resume`. |
| 11 | `PhasesHandler.Approve` POST `/api/v1/changes/{change_id}/phases/{phase_id}/approve` | **MIGRATED** — `/api/v1/phases/{phase_id}/approve`. |
| 12 | `PhasesHandler.Reject` POST `/api/v1/changes/{change_id}/phases/{phase_id}/reject` | **MIGRATED** — `/api/v1/phases/{phase_id}/reject`. |
| 13 | `ApplyHandler.GetBoard` GET `/api/v1/changes/{change_id}/phases/{phase_id}/board` | **MIGRATED** — `/api/v1/phases/{phase_id}/board`. |
| 14 | `SSEHandler.Stream` GET `/api/v1/changes/{change_id}/phases/{phase_id}/events` | **MIGRATED** — `/api/v1/phases/{phase_id}/events`. |

### Net delta summary

- **CLI breaking changes:** 1 path rename (`/healthz` → `/health`),
  1 SSE model rewrite (per-Change → per-Phase multiplexer), 1 endpoint
  removed (`sseprobe`/`/api/v1/events`), auth header added globally.
- **Orchestrator breaking changes:** 5 path migrations from
  change-scoped to phase-scoped (`get`, `resume`, `approve`, `reject`,
  `board`, `events`). The change-scoped path `POST /changes/{cid}/phases/{type}/run`
  retained (change-id is the parent identifier when the phase is
  being created).
- **Both:** auth header is mandatory on the new authenticated
  endpoints; loopback-anonymous path is the only escape hatch
  (Section 3.2).

No inventoried endpoint is left undocumented. No design-level concern
from the inventory's "Mismatches" table (M-marked or D-marked) is
unaddressed in the spec. Phase 1 Task 1.1 Step 2 is therefore
complete; Step 3 (owner sign-off) is the next gate.
