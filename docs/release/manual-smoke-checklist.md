# Manual Smoke Checklist (sophia-orchestator)

This file accumulates per-tag smoke gates. Each release adds a new
edition section; older sections stay as audit history.

The orchestrator was not publicly tagged at v0.1.0 (per CLI v0.1.0
CHANGELOG `Known limitations`), so v0.2.0 is the first formal entry.

| Edition | Date | Status |
|---|---|---|
| [v0.2.0 edition](#v020-edition---day-7-promotion-gate) | 2026-05-15 (Day 7 target) | OPEN — gates required for `v0.2.0` final tag |

---

## v0.2.0 edition — Day 7 promotion gate

Mirrors `sophia-cli/docs/release/manual-smoke-checklist.md` v0.2.0
edition, scoped to orchestrator-side responsibilities. Gates the
`v0.2.0` final tag on this repo (D-M10-16 #3 release blocker).

The cli-side checklist is the canonical operator-smoke surface
(it exercises the wire from the consumer's perspective). The orch's
job here is to confirm the deployment artifact is sound and the
runtime wire shape matches the spec.

### A. Pre-requisites

- [ ] `sophia-orchestator` binary built from `m10/orchestrator-wire-v1`
      → `main` (post-merge commit) at `v0.2.0-rc.1` tag.
- [ ] PostgreSQL ≥ 16 reachable; migrations up to date
      (`make migrate-up` clean).
- [ ] Listener bind documented: loopback (`127.0.0.1`, `localhost`,
      `::1`) for anonymous mode OR non-loopback with API key
      configured.

### B. Wire-shape conformance (server-side)

These bullets validate the orchestrator implements the spec as
emitted, independent of any cli that connects.

- [ ] **B1. `GET /api/v1/health`** returns 200 with
      `{"status":"ok","version":"v1",...}` body. NO `/healthz`
      route (404).

- [ ] **B2. Auth gate matrix** matches D-M10-02:
      - Loopback bind + `AllowAnonLocalhost=true` + no key → 200.
      - Loopback bind + `AllowAnonLocalhost=false` + no key → 401.
      - Non-loopback bind + no key → 401 regardless of flag.
      - Non-loopback bind + valid `X-Sophia-API-Key` → 200.
      - Bootstrap log emits `WARN AllowAnonLocalhost requested but
        listener is not loopback-bound` when downgrading silently.

- [ ] **B3. Phase-scoped routes** all reachable
      (D-M10-13 Form A):
      - `GET /api/v1/phases/{id}` → `PhaseResponse` shape.
      - `POST /api/v1/phases/{id}/approve` with empty body → 400
        `approver_required`.
      - `POST /api/v1/phases/{id}/reject` same.
      - `GET /api/v1/phases/{id}/events` → `text/event-stream`,
        emits `open` first, then `heartbeat` periodically.

- [ ] **B4. SSE event payloads** match `sophia-wire-v1` §5.3:
      - `phase.started`: `phase_id, phase_type, change_id, started_at`
      - `phase.completed`: `phase_id, phase_type, ended_at, confidence`
      - `phase.failed`: `phase_id, phase_type, ended_at, error`
      - `approval.required`: `phase_id, gate_url, reason` (risk/policy
        Optional per Phase 1.5 amendment)
      - `approval.resolved`: `phase_id, decision, approver, decided_at`
      - Per-event `id:` field is a ULID (not RFC3339Nano).

- [ ] **B5. Error envelope** for non-2xx responses is
      `{code, error, details?}` (sophia-wire-v1 §9.1) with one of
      the 13 stable codes (§9.2). Triggered probes: send `limit=500`
      to `GET /api/v1/changes` → 400 `limit_too_large`; approve a
      non-gated phase → 409 `phase_not_gated`; approve a phase that
      already has `approval.resolved` → 409 `gate_already_decided`;
      attach SSE to a terminal phase → 410 `phase_terminal_no_events`.

- [ ] **B6. Migrations applied** without error on a fresh PG 16+
      database. Schema includes the audit_log table with the columns
      the new `HasEventForPhase` query uses.

### C. Cross-cutting

- [ ] **C1.** SHA256 of `docs/specs/sophia-wire-v1.md` matches
      sophia-cli at the to-be-tagged commit.

- [ ] **C2.** `go test ./...` (25 packages) + `GOWORK=off go test
      ./...` green on the to-be-tagged commit.

- [ ] **C3.** orch `ci.yaml` lint job remains the documented YELLOW
      pre-existing failure (golangci-lint v1↔v2 schema). No NEW
      lint failures introduced by Phase 3.x. Out-of-scope for v0.2.0;
      slated as follow-up post-v0.2.0.

- [ ] **C4.** Soak matrix Open RED entries: zero. Open YELLOW
      entries: only the documented C3 lint pre-existing.

### D. Sign-off

| Field | Value |
|-------|-------|
| Reviewer | __________ |
| Date | ____-__-__ |
| Deployment | __________ (staging URL or "in-process during contract suite") |
| Findings | (list 🟡/🔴 with disposition) |
| Tag at review | `v0.2.0-rc.1` (or rc.N) |
| Decision | promote / hold / re-cut rc.N+1 |
| Promotion command | `git tag -a v0.2.0` then `git push origin v0.2.0` (yes / no) |

> **Skip-policy.** If B-bullets B2/B3/B4/B5 cannot be exercised
> against a live deployment, they MAY be substituted by the orch's
> own `internal/adapters/inbound/http/router_test.go` + Phase 3.8
> tests (which assert the same shapes via `httptest.Server`). Document
> the substitution in Findings.
