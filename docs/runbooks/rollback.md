# Runbook — Rollback / disaster recovery

## Helm rollback (fast)

```bash
helm history sophia -n sophia
helm rollback sophia <REVISION> -n sophia
kubectl -n sophia rollout status deploy/sophia-sophia-orchestator
```

The helm rollback preserves Postgres state. If the previous revision had
`migrateOnBoot=true`, reverting does NOT roll back schema migrations —
those must be undone manually if incompatible.

## Schema rollback (db migrations)

`golang-migrate` reverses migrations one at a time. **Only roll back
schema if the rollback target version cannot read the current schema.**
For idempotent additive migrations (the V1 default), do NOT roll back
schema; the older binary will simply ignore new columns.

```bash
docker run --rm -v "$(pwd)/migrations/postgres:/m" \
  migrate/migrate -path /m -database "$DATABASE_URL" down 1
```

Verify the change:
```sql
SELECT version, dirty FROM schema_migrations;
```

If `dirty=true`, the previous attempt failed mid-flight — you MUST clear
the dirty flag manually after fixing:
```sql
UPDATE schema_migrations SET dirty=false WHERE version=<n>;
```

## Resuming interrupted phases (V1)

V1 has manual resume. After a crash mid-phase, the Phase row is left in
`status=running` (or `interrupted` after the next startup scan).

1. Find interrupted phases:
   ```sql
   SELECT change_id, id, phase_type, attempts, started_at
   FROM phases
   WHERE status IN ('running', 'interrupted')
   ORDER BY started_at;
   ```
2. For each, decide: resume or reject.
   - Resume: `POST /api/v1/changes/{cid}/phases/{pid}/resume` — re-runs
     the goroutine. Iron Law #5 still applies (max 3 attempts before
     escalation).
   - Reject: `POST /api/v1/changes/{cid}/phases/{pid}/reject` — marks
     the phase BLOCKED. Use when the change context is no longer valid.
3. If many are interrupted, script the resume:
   ```bash
   psql -t -A -c "SELECT change_id || '|' || id FROM phases
                  WHERE status='interrupted'" \
     | while IFS=\| read CID PID; do
         curl -s -X POST -H "X-Sophia-API-Key: $KEY" \
           "https://orchestator.example.com/api/v1/changes/$CID/phases/$PID/resume"
       done
   ```

V2 introduces auto-resume on startup (idempotent side-effect handling,
worktree reconciliation, stale-branch cleanup). Until then, resume is a
manual operator decision.

## Worktree cleanup

Apply-phase worktrees live under `/var/sophia/worktrees/{change_id}/...`
on the host (or the orchestrator's mounted volume in k8s).

After a crashed apply phase, cleanup is manual in V1:
```bash
# In the orchestrator container, or wherever runtime-adapters runs:
git worktree list
# Look for sophia/<change>/<group> worktrees
git worktree remove /var/sophia/worktrees/<change_id>/<group_name>
git branch -D sophia/<change_name>/<group_name>
```

V1.5 (when runtime-adapters Phase 2 ships `git.worktree.cleanup@v1`)
will make this automatic.

## Database disaster recovery

Restore Postgres from the latest base backup + WAL stream:

```bash
pg_basebackup --pgdata=$RESTORE_DIR --format=tar --gzip ...
# point-in-time recovery to T-5min before incident
recovery_target_time = '2026-05-04 12:30:00'
```

After restore: orchestrator's idempotency keys
`(change_id, phase_type, attempts)` ensure replays produce the same
envelope, so re-running affected phases is safe.

## Contact tree

- **Engineering lead**: rfactperu@gmail.com
- **On-call rotation**: see PagerDuty service `sophia-orchestator`.
- **Memory-engine team**: separate runbook in that repo.
- **Runtime-adapters team**: separate runbook in that repo.
