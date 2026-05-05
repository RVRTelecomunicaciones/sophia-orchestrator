# Runbook — Incident response

## Alert: SophiaPhaseAPILatencyHigh (T1, page)

Phase-API p99 above 500ms. Burning the latency error budget.

1. Check Grafana `sophia-overview` → "HTTP request duration p99 (ms)"
   panel. Identify which `route` is slow.
2. Check `sophia_orchestator_phases_running` gauge. If unusually high,
   the orchestrator may be backed up on async work; check goroutine
   counts.
3. Check Postgres connection pool saturation: `SELECT count(*) FROM
   pg_stat_activity WHERE state='active'` — if `=MaxConns`, raise
   `SOPHIA_DB_MAX_CONNS`.
4. Check downstream latency: `sophia_orchestator_governance_calls_total`,
   `_memory_calls_total`, `_runtime_calls_total` — if any target is slow
   the phase API will be slow.
5. **Mitigation**: scale replicas (V3 only — V1 is single-instance). For
   V1, page the on-call lead and consider `kubectl rollout restart` if
   the orchestrator process appears wedged.

## Alert: SophiaIronLawViolations (T1, page)

Iron Law violation rate spike. Iron Laws are non-rationalizable — this is
ALWAYS a code bug or data inconsistency. Do NOT silence the alert.

1. Check `audit_log` table:
   ```sql
   SELECT change_id, phase_id, event_type, payload->>'reason'
   FROM audit_log
   WHERE event_type = 'phase.failed'
     AND payload->>'reason' ILIKE '%iron law%'
   ORDER BY id DESC LIMIT 50;
   ```
2. Cross-reference with the Iron Law catalog in `internal/domain/ironlaw/laws.go`.
3. Page the engineering lead. **Root-cause this**, do not retry blindly.
4. If the violation is IL5 (escalation after 3 attempts), it indicates an
   architectural problem. Discuss the change's apply phase with the team
   before retrying.

## Alert: SophiaApplySuccessRateLow (T1, page)

Apply phase failing >10% over the last 30d window.

1. Query failing groups:
   ```sql
   SELECT change_id, phase_id, payload
   FROM audit_log
   WHERE event_type = 'apply.group.failed'
   ORDER BY id DESC LIMIT 50;
   ```
2. Common root causes:
   - Dispatcher provider down (`SophiaDispatcherAvailabilityLow` alert
     should fire concurrently).
   - Memory-engine missing the upstream tasks list. Check
     `sophia_orchestator_memory_calls_total{op="get",status="failure"}`.
   - Worktree creation failing on shared FS (V1 limitation: `git
     worktree add` runs via shell.exec; V2 uses runtime
     `git.worktree.create@v1` typed capability).
3. **Mitigation**: pause incoming apply runs (revoke the API key if
   needed) until root cause is identified.

## Alert: SophiaSpawnGovernorThrottling (T2, ticket)

Spawn-governor throttling above 10%. Either:
- Genuine load: raise `SOPHIA_SPAWN_MAX` (cap 6 per #53922).
- Anthropic burst rate-limiter: stagger settings need adjustment. Check
  `internal/application/discipline/spawn_governor.go` defaults.

## Alert: SophiaDispatcherAvailabilityLow (T1, page)

OpenCode (V1) failing > 0.5% per 30d.

1. Check container logs for the dispatcher subprocess errors.
2. Verify OpenCode CLI is installed in the runtime-adapters environment.
3. Check Anthropic API status (V1 dispatches Claude via OpenCode →
   Anthropic API — see #53922 for known burst limit).

## General triage flow

1. Open Grafana → `sophia-overview`. The 4 stat panels at the top tell
   you immediately where to look.
2. Tail orchestrator logs: `kubectl -n sophia logs -l
   app.kubernetes.io/name=sophia-orchestator --tail=500 -f`.
3. Query `audit_log` for the last 50 phase.failed events.
4. If the orchestrator process is wedged, `rollout restart`. Manual
   resume on running phases (V1) — see [`rollback.md`](rollback.md).
