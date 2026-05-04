# sophia-orchestator helm chart

Minimal Helm chart for deploying the Sophia SDD workflow coordinator.

## Install

```bash
helm install sophia ./helm/sophia-orchestator \
  --set secrets.dbUrl="postgres://user:pass@host:5432/db?sslmode=require" \
  --set secrets.governanceUrl="https://governance.example.com" \
  --set secrets.memoryUrl="https://memory.example.com" \
  --set secrets.runtimeUrl="https://runtime.example.com" \
  --set secrets.httpApiKey="$(openssl rand -hex 32)" \
  --set image.tag=0.1.0
```

## Required values

| Key | Description |
|---|---|
| `secrets.dbUrl` | Full Postgres URL with sslmode |
| `secrets.governanceUrl` | agent-governance-core base URL |
| `secrets.memoryUrl` | sophia-memory-engine base URL |
| `secrets.runtimeUrl` | sophia-runtime-adapters base URL |

## Production guidance

- Use **External Secrets Operator** to inject `secrets.*` from your secret
  store (AWS Secrets Manager / Vault / etc.) rather than committing values.
- Set `replicaCount: 1` for V1 — the orchestrator is not yet multi-instance
  safe (advisory locks coordinate per-process; V3 adds leader election).
- Pin `image.tag` to a SHA, never `latest`.
- Run with `migrateOnBoot: false` once your DB is on a stable version, and
  apply migrations via your normal CI/CD release process instead.

## Liveness vs readiness

- `/api/v1/health` (liveness): returns 200 if the process is up. Use to
  detect process hangs.
- `/api/v1/ready` (readiness): returns 200 only when Postgres is reachable.
  Use to gate traffic during boot or DB outages.
