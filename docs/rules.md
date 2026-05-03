# Rules (R1..R12)

These rules are operational forms of the spec. Violations are bugs.

| ID | Rule |
|---|---|
| **R1** | Orchestrator does not decide policy. Phase transitions and sensitive runtime calls go through governance. |
| **R2** | Envelope before transition. Persisted before caller-visible state change (Iron Law #1 enforced). |
| **R3** | Status enum closed: `DONE` \| `DONE_WITH_CONCERNS` \| `BLOCKED` \| `NEEDS_CONTEXT`. |
| **R4** | Phase types closed: `init` \| `explore` \| `proposal` \| `spec` \| `design` \| `tasks` \| `apply` \| `verify` \| `archive`. |
| **R5** | No `time.Now()` in domain/application — inject `shared.Clock`. |
| **R6** | No `ulid.Make()` in domain/application — inject `shared.IDGenerator`. |
| **R7** | Adapters are wired only in `internal/bootstrap/wire.go`. Domain/application never import adapters. |
| **R8** | Long-running phases respond `202 Accepted` + SSE. No request-thread > 30s on user-facing endpoints. |
| **R9** | `SpawnGovernor` MUST gate every dispatcher invocation. |
| **R10** | Idempotency replay-everything: `(change_id, phase_type, attempts)` UNIQUE; re-runs replay last envelope. |
| **R11** | Audit log is append-only. No updates. No deletes. |
| **R12** | Conventional commits, no AI attribution. Scope from `{domain, application, bootstrap, change, phase, apply, session, pg, http, governance, memory, runtime, dispatcher, discipline, audit, ci, docs, test}`. |

## Enforcement

- R5 / R6 — `golangci-lint` `forbidigo` rule.
- R7 — `golangci-lint` `forbidigo` against importing `internal/adapters/*` from domain/application.
- R10 — Postgres `UNIQUE (change_id, phase_type, attempts)` constraint.
- R11 — `audit_log` table has no `UPDATE` or `DELETE` grants for the application user.
- R3 / R4 — Domain enum methods (`IsValid`); validation at port boundaries.
- R12 — git pre-commit hook (V1.1; manual review V1).
