# Domain Invariants (I1..I20)

These invariants hold across every state of the system. Each must be enforced
either by domain types (constructor/method validation), database constraints,
or application-layer guards. Tests verify them at every layer.

| ID | Invariant |
|---|---|
| **I1** | A Change has a unique `(project, name)`. |
| **I2** | Status transitions are explicit; no hidden state mutations. |
| **I3** | `CurrentPhase` advances only when prior `Phase.Status == DONE` and `confidence ≥ threshold`. |
| **I4** | A Phase belongs to exactly one Change. |
| **I5** | `Phase.Envelope` is set iff `Status ∈ {done, done_with_concerns, blocked, needs_context}`. |
| **I6** | `Confidence ∈ [0.0, 1.0]`; below threshold → status forced to `BLOCKED` or `DONE_WITH_CONCERNS`. |
| **I7** | An Apply Board exists iff `Phase.Type == apply` and `Status != pending`. |
| **I8** | A Group's `depends_on` is a subset of the same Board's Group IDs (no cross-board deps). |
| **I9** | A Task's `claimed_by` is set iff `Status ∈ {claimed, running, done, failed, blocked}`. |
| **I10** | `AgentSession.Provider` is a closed set (V1: `opencode`; V2: + `claude-code`, `cursor`, `gemini`). |
| **I11** | `AgentSession.AgentRole ∈ {sdd-init, sdd-explore, sdd-proposal, sdd-spec, sdd-design, sdd-tasks, sdd-verify, sdd-archive, team-lead, implement}`. |
| **I12** | `Worktree.Path` is unique while `Status != cleaned`. |
| **I13** | **Iron Law #1**: persisted-before-return. Envelope must be persisted before any caller-visible state change. |
| **I14** | **Iron Law #2**: apply requires tasks Phase `DONE` with confidence ≥ 0.8 AND approved bit. |
| **I15** | **Iron Law #3**: archive requires verify Phase `DONE` with confidence ≥ 0.9. |
| **I16** | **Iron Law #4**: every transition goes through governance `ClassifyTask + EvaluatePolicy + RouteDecision`. |
| **I17** | **Iron Law #5**: at attempts == 3 on a Task, escalate; do not attempt fix #4. |
| **I18** | `SpawnGovernor` counter ≥ 0 always; saturated callers block or 429 — never overprovision. |
| **I19** | Audit log entries are insert-only, in monotonic-time order per `(change_id)`. |
| **I20** | Idempotency: re-POST with same `(change_id, phase_type, attempts)` returns the cached envelope. |
