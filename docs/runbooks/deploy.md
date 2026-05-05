# Runbook — Deploying sophia-orchestator

## Prerequisites

- Postgres 16+ reachable from the orchestrator pod (sslmode=require in prod).
- agent-governance-core, sophia-memory-engine, and sophia-runtime-adapters
  reachable on the network.
- Bootstrap API key generated (`openssl rand -hex 32`) and stored in your
  secret manager.
- Container registry access for the orchestator image
  (`ghcr.io/rvrtelecomunicaciones/sophia-orchestator:<tag>`).

## Required env / Helm values

| Key | Helm path | Notes |
|---|---|---|
| `SOPHIA_DB_URL` | `secrets.dbUrl` | sslmode=require, dedicated DB role |
| `SOPHIA_GOVERNANCE_URL` | `secrets.governanceUrl` | https only in prod |
| `SOPHIA_MEMORY_URL` | `secrets.memoryUrl` | https only in prod |
| `SOPHIA_RUNTIME_URL` | `secrets.runtimeUrl` | https only in prod |
| `SOPHIA_HTTP_API_KEY` | `secrets.httpApiKey` | rotate quarterly |
| `SOPHIA_ENV` | `config.env` | `prod` / `staging` / `dev` |
| `SOPHIA_LOG_LEVEL` | `config.logLevel` | `info` (default) |
| `SOPHIA_SPAWN_MAX` | `config.spawn.max` | default 4; cap 6 (#53922) |
| `SOPHIA_DB_MIGRATE_ON_BOOT` | `migrateOnBoot` | `true` for first deploy, `false` after |
| `SOPHIA_METRICS_ENABLED` | (env) | default true |
| `SOPHIA_TRACES_ENABLED` | (env) | enable when collector is reachable |
| `SOPHIA_OTLP_ENDPOINT` | (env) | e.g. `otel-collector:4318` |

## Deploy with Helm

```bash
helm upgrade --install sophia ./helm/sophia-orchestator \
  --namespace sophia --create-namespace \
  --set image.tag=v0.1.0 \
  --set secrets.dbUrl="${DB_URL}" \
  --set secrets.governanceUrl="${GOV_URL}" \
  --set secrets.memoryUrl="${MEM_URL}" \
  --set secrets.runtimeUrl="${RT_URL}" \
  --set secrets.httpApiKey="${API_KEY}" \
  --set migrateOnBoot=true   # first deploy only
```

## Validate the rollout

1. `kubectl -n sophia rollout status deploy/sophia-sophia-orchestator` — must
   reach `successfully rolled out` within 60s.
2. Liveness probe: `kubectl exec deploy/... -- wget -qO- :8080/api/v1/health`.
3. Readiness probe: `wget -qO- :8080/api/v1/ready`. Returns 503 until
   Postgres is reachable; alert on >2 minutes of 503.
4. Smoke test:
   ```bash
   curl -s -X POST https://orchestator.example.com/api/v1/changes \
     -H "X-Sophia-API-Key: ${API_KEY}" \
     -H "Content-Type: application/json" \
     -d '{"name":"smoke","project":"smoke","artifact_store_mode":"memory-engine"}'
   # Expect 201 with change_id
   ```
5. Verify Prometheus scraping: `curl :8080/metrics | grep
   sophia_orchestator_phases_total` should return Help/Type lines.

## Idempotency / re-run

Migrations are idempotent (`golang-migrate` skips already-applied
versions). Re-running the Helm upgrade with `migrateOnBoot=true` is
safe, but PREFER to set it `false` in steady state and run migrations as
a separate Job for clearer audit trail.

## Rollback

See [`rollback.md`](rollback.md).

## Rotate the API key

1. Generate a new key: `openssl rand -hex 32 > new-key.txt`.
2. Helm upgrade with `--set secrets.httpApiKey="$(cat new-key.txt)"`.
3. Roll the deployment: `kubectl -n sophia rollout restart deploy/...`.
4. Distribute the new key to clients; revoke old key after grace period.

V2 will replace this static key with OIDC + per-project keys in the
`api_keys` table.
