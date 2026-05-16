# Cycle Validation — End-to-End SDD with a Real LLM

**Status**: living doc · last updated 2026-05-15

This doc covers how to validate the full Sophia stack end-to-end —
orchestator → governance → runtime → opencode → LLM → envelope →
orch persists — in under 10 minutes against your local docker daemon.
Pairs with `docs/operations/llm-providers.md` (which covers provider
auth and routing) and the runbook in
`sophia-runtime-adapters/docs/architecture/llm-runtime-deployment.md`.

The script is `scripts/e2e-sdd-cycle.sh`. This doc explains
prerequisites, what the script does, what to check in the output, and
what to do when it fails.

## Pre-requisites

### 1. Sibling repo layout

The compose file builds memory-engine, governance-core, and
runtime-adapters from sibling repos via relative paths. Layout MUST be:

```
<parent>/
  sophia-orchestator/        ← run the script from here
  sophia-memory-engine/
  agent-governance-core/
  sophia-runtime-adapters/
```

If the layout differs, `docker compose build` fails with
"file not found" on the build context. The script's pre-flight
catches this before bringing the stack up.

### 2. opencode authenticated

The validation runs against the `runtime-adapters-llm-opencode` image
target, which expects `auth.json` mounted at the path opencode reads
by default. On the host:

```bash
opencode auth login github-copilot
opencode auth login openai          # optional — for codex paths
opencode auth list                  # confirm credentials present
```

The auth file lives at `~/.local/share/opencode/auth.json` — the
script's pre-flight checks for it explicitly.

If you see "Anthropic OAuth blocked" or similar in the cycle output,
remember Anthropic blocks third-party Claude OAuth tokens at the
server layer (Feb 2026 ToS). Use `github-copilot/claude-*` model
strings instead (see `llm-providers.md` pitfall #1).

### 3. Tools

- Docker Desktop 4.x or Docker Engine 24+ with Compose v2 (`docker compose version`).
- `jq` (`brew install jq` on macOS).
- `curl`, `bash 4+` — already on every reasonable system.

### 4. Disk + RAM budget

The full stack runs:

- 4 Postgres instances (~50 MB each)
- orchestator (~50 MB)
- memory-engine (~50 MB)
- governance-core (~50 MB)
- runtime-adapters-llm-opencode (~520 MB — opencode bundled)
- 2 migration sidecars (~30 MB each, run once)

Budget ~2 GB RAM during the cycle; a full compose build downloads
~600 MB the first time (opencode tarball + apt deps in the LLM image).
Subsequent runs hit the buildx cache.

## Run

```bash
./scripts/e2e-sdd-cycle.sh                  # build + run + tear down
./scripts/e2e-sdd-cycle.sh --keep           # leave stack up after run
./scripts/e2e-sdd-cycle.sh --no-build       # skip build (use cached images)
./scripts/e2e-sdd-cycle.sh --change my-name --phase spec
```

Defaults:

| Flag         | Default                     |
|--------------|-----------------------------|
| change name  | `e2e-validation-<timestamp>`|
| project      | `e2e-local`                 |
| phase        | `explore` (cheapest phase)  |
| poll timeout | 600s (10 min)               |

The script tears down the stack on exit (success OR failure) unless
`--keep` is passed. Volumes are removed too — if you want the
postgres data to survive between runs, pass `--keep` and tear down
manually with `docker compose ... down` (without `-v`).

## What success looks like

Last lines of stdout when everything works:

```
[2026-05-15T22:00:43Z] cycle PASSED with status=DONE
```

The "final phase JSON" block printed just before that includes:

- `status`: `DONE` (cleanest), `DONE_WITH_CONCERNS` (LLM raised flags
  but completed), or `DONE_WITH_REJECTIONS` (governance overrode some
  decisions but the phase finished). Any of the three is a pass.
- `envelope`: the JSON the LLM emitted, validated against the
  envelope schema for the phase. For `explore` it should describe
  what the agent looked at and recommend next steps.
- `dispatcher`: provider used (likely `opencode`), model string
  (e.g. `github-copilot/claude-sonnet-4.6`), and duration.

The "last 80 lines of orchestator logs" block grep-filtered by the
phase id is for quick triage — if the run was clean it's noisy but
mostly INFO; if there were warnings or errors they show up here.

## What failure looks like — and how to triage

### `pre-flight check failed` (exit 1)

Fix the missing prerequisite and re-run. The error message names
exactly what failed (auth.json missing, sibling repo missing, etc.).

### `orchestator did not become healthy within 180s` (exit 2)

Almost always one of:

| Cause | How to confirm | Fix |
|---|---|---|
| Postgres not up yet on slow disk | `docker compose ... logs pg-orchestator` | bump `HEALTHY_TIMEOUT_S` at the top of the script |
| Migration sidecar failed | `docker compose ... logs migrate-memory-engine` (or governance-core) | check the migration's SQL — usually a schema drift between sibling repo's HEAD and the migrations the sidecar ran |
| Image build failure | the `up --build` step exited non-zero — scroll up | usually the runtime-adapters-llm-opencode build hitting an opencode tarball 404 or apt-get network glitch — re-run usually fixes |

### `cycle FAILED with status=FAILED` (exit 4)

The orchestator ran the full state machine but the phase ended in
FAILED. The "final phase JSON" prints the failure reason. Common
buckets:

| Symptom in the JSON | Cause | Fix |
|---|---|---|
| `error: agent produced no fenced JSON envelope` | The LLM didn't emit a valid envelope; opencode auth probably worked but the model rejected the prompt or hit a usage cap | check `opencode auth list` — try a different model via `SOPHIA_DISPATCHER_MODEL=github-copilot/claude-haiku-4.5` |
| Phase ends in `blocked` with `phase.failed: envelope validation: invalid json: unexpected end of JSON input` and the `execution_receipts` row shows `stdout_len=0, exit_code=0` | opencode ran but emitted no stdout — almost always because no model was configured. Without `-m <model>`, opencode has no implicit default and exits silently. Validated end-to-end on 2026-05-16 — this was the failure mode of the script's first run | set `SOPHIA_DISPATCHER_MODEL=github-copilot/claude-sonnet-4.6` (or another OAuth-backed model) on the orch service in `compose.e2e-llm.yaml`. The overlay shipped in this repo already does this — if you removed it, restore it |
| `error: ErrDispatchFailed: status="failure" stderr="exec: opencode: no such file..."` | The runtime image is the distroless target, not the LLM target | confirm `compose.e2e-llm.yaml` overlay is being applied — without it `runtime-adapters` falls back to the no-opencode default |
| `error: governance: routing_failed` | governance-core couldn't decide a route — usually because the project doesn't have a routing policy seeded | either seed the policy or use `artifact_store_mode: "engram"` (the default in the script) which bypasses router-required-policies |

### `phase did not terminate within 600s` (exit 4)

The LLM is slow OR opencode is hanging on a permission prompt. Two
things to try:

1. Re-run with `OPENCODE_DISABLE_TUI=1` confirmed in the runtime
   container env (the overlay sets it; verify with
   `docker compose ... exec runtime-adapters env | grep OPENCODE`).
2. Bump `PHASE_POLL_TIMEOUT_S` if you're on a slow connection or
   Anthropic's API is throttling.

## What the script does NOT cover (out of scope)

- `apply` phase end-to-end with real edits — the script defaults to
  `explore` because it's the cheapest phase and the one most
  representative of the full dispatch path. To exercise apply you'd
  need a worktree with non-trivial state for the LLM to edit; that's
  better suited to integration tests in CI than this smoke script.
- ollama or aider providers — the overlay only covers opencode, which
  is the default and the one validated end-to-end. To test ollama or
  aider you'd swap the overlay's `target` and add the appropriate
  env wiring (`SOPHIA_OLLAMA_CMD`, `SOPHIA_AIDER_CMD`,
  `SOPHIA_DISPATCHER_PROVIDER_<PHASE>=...`). See
  `llm-providers.md` for the routing matrix.
- Multi-cycle, multi-tenant, or load — this is a smoke validation
  script, not a load test. For load see `ops/load/` (when wired).

## Cleanup

If you used `--keep` and want to stop manually:

```bash
cd ops/local && docker compose \
  -f compose.full-stack.yaml \
  -f compose.e2e-llm.yaml \
  down -v
```

The `-v` removes the postgres volumes — drop it if you want the
schema state to survive.

## Pairs with

- `docs/operations/llm-providers.md` — provider auth + per-phase routing
- `ops/local/compose.full-stack.README.md` — the base compose stack
- `ops/local/compose.e2e-llm.yaml` — the LLM overlay this script applies
- `sophia-runtime-adapters/docs/architecture/llm-runtime-deployment.md` — image targets in detail
