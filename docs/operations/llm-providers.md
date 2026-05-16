# LLM Provider Operations Guide

**Status**: living doc · last updated 2026-05-15 (V2.0 multi-LLM factory + ollama + aider)

This is the operator-facing reference for wiring LLM providers into
Sophia. Combine it with the runtime-adapters operator doc
(`sophia-runtime-adapters/docs/architecture/llm-runtime-deployment.md`)
which covers the container-level setup.

## Quick reference — the two routing axes

Sophia has TWO independent per-phase routing knobs:

| Axis | Env var | What it selects |
|------|---------|-----------------|
| **Provider** (V2.0) | `SOPHIA_DISPATCHER_PROVIDER[_<PHASE>]` | Which adapter (`opencode`, `ollama`, `aider`) executes the call |
| **Model** (V1.5)  | Provider-specific (see below)            | Which LLM model the chosen adapter invokes |

Both axes default to the global value if no per-phase override matches.

The model env var is **provider-scoped** because model strings are not
portable across adapters (opencode wants `anthropic/claude-opus-4-7`,
ollama wants `deepseek-r1:7b`, aider wants `claude-opus-4-7`). The
matrix:

| Provider | Global model env | Per-phase model env |
|----------|------------------|---------------------|
| `opencode` (default) | `SOPHIA_DISPATCHER_MODEL` | `SOPHIA_DISPATCHER_MODEL_<PHASE>` |
| `ollama`             | `SOPHIA_OLLAMA_MODEL`     | `SOPHIA_OLLAMA_MODEL_<PHASE>`     |
| `aider`              | `SOPHIA_AIDER_MODEL`      | `SOPHIA_AIDER_MODEL_<PHASE>`      |

## V2.0 baseline — providers shipped

| Provider name | Adapter status | Activation | CLI invocation | Auth |
|---------------|----------------|------------|----------------|------|
| `opencode`    | ✅ default (always registered) | always on | `opencode run -m <model> -- <prompt>` | OAuth via `~/.local/share/opencode/auth.json` (bind-mount) |
| `ollama`      | ✅ opt-in | set `SOPHIA_OLLAMA_CMD=ollama` | `ollama run <model> <prompt>` | none (local daemon) |
| `aider`       | ✅ opt-in (apply-only) | set `SOPHIA_AIDER_CMD=aider` | `aider --yes-always --no-auto-commits --model <model> --message <prompt>` | `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` env on runtime image |

**Why aider is apply-only**: aider edits files in-place rather than
returning a JSON envelope. Routing it to spec/design/verify breaks the
orchestrator's envelope contract — use it only on apply. The factory
does not enforce this; it's an operator contract.

V2.1+ roadmap: `claude-code` (when Anthropic unblocks third-party
OAuth), per-call session provenance, cost tracking.

## Recommended per-phase matrix (with `opencode` as the only V2.0 provider)

This is the strategy validated end-to-end in the LLM auth research
(2026-05-15). All entries use `opencode` as the dispatcher provider;
the routing happens via per-phase MODEL overrides.

```bash
# Provider stays at default ("opencode") for every phase.
# Operator does NOT set SOPHIA_DISPATCHER_PROVIDER_*.

# Per-phase models (validated end-to-end against the runtime-adapters-llm container)
SOPHIA_DISPATCHER_MODEL=github-copilot/claude-sonnet-4.6      # global fallback

SOPHIA_DISPATCHER_MODEL_EXPLORE=github-copilot/claude-haiku-4.5   # cheap+fast
SOPHIA_DISPATCHER_MODEL_PROPOSAL=github-copilot/claude-sonnet-4.6 # decent reasoning
SOPHIA_DISPATCHER_MODEL_SPEC=github-copilot/claude-opus-4.7       # high-quality architecture
SOPHIA_DISPATCHER_MODEL_DESIGN=github-copilot/claude-opus-4.7     # idem
SOPHIA_DISPATCHER_MODEL_TASKS=github-copilot/claude-sonnet-4.6    # mechanical
SOPHIA_DISPATCHER_MODEL_APPLY=openai/gpt-5.3-codex                # code-specialized
SOPHIA_DISPATCHER_MODEL_VERIFY=google/gemini-2.5-flash            # cheap+fast
SOPHIA_DISPATCHER_MODEL_ARCHIVE=google/gemini-2.5-flash           # idem
```

**Cost** (with operator's existing subscriptions): $0 incremental.
GitHub Copilot OAuth + ChatGPT Plus/Pro OAuth + Google AI OAuth (free
tier) cover everything above.

## When to set `SOPHIA_DISPATCHER_PROVIDER_*`

You set the per-phase provider override when you want a phase to run
through a non-default adapter. The default is always `opencode`. The
target adapter MUST be activated first (otherwise the factory falls
back to opencode and logs a warning).

### Example 1 — Local verify on ollama, rest on opencode (cost-zero verify loop)

```bash
SOPHIA_OLLAMA_CMD=ollama                          # activate ollama
SOPHIA_OLLAMA_MODEL_VERIFY=qwen3:14b              # local model for verify
SOPHIA_DISPATCHER_PROVIDER_VERIFY=ollama          # route verify → ollama
# All other phases stay on opencode + the SOPHIA_DISPATCHER_MODEL_* matrix.
```

Use case: heavy verify loops that don't need a frontier model;
privacy-sensitive verify on a self-hosted GPU.

### Example 2 — Aider for apply, opencode for everything else

```bash
SOPHIA_AIDER_CMD=aider                            # activate aider
SOPHIA_AIDER_MODEL_APPLY=claude-opus-4-7          # aider's model namespace
SOPHIA_DISPATCHER_PROVIDER_APPLY=aider            # route apply → aider
# Runtime image MUST expose ANTHROPIC_API_KEY in aider's env.
```

Use case: prefer aider's repo-map + diff-aware editing over opencode's
permission-sandboxed file ops for the apply phase. The apply executor
must reconstruct a synthetic envelope from `git status --porcelain`
post-dispatch (or use the `memory-topic-key:KEY` fallback path) since
aider returns `EnvelopeRaw = nil`.

### Example 3 — Three providers active simultaneously

```bash
SOPHIA_OLLAMA_CMD=ollama
SOPHIA_AIDER_CMD=aider

# Per-phase routing
SOPHIA_DISPATCHER_PROVIDER_VERIFY=ollama          # local cheap verify
SOPHIA_DISPATCHER_PROVIDER_APPLY=aider            # diff-aware apply
# explore/proposal/spec/design/tasks/archive default to opencode

# Per-provider models
SOPHIA_OLLAMA_MODEL_VERIFY=qwen3:14b
SOPHIA_AIDER_MODEL_APPLY=claude-opus-4-7
SOPHIA_DISPATCHER_MODEL_SPEC=github-copilot/claude-opus-4.7
SOPHIA_DISPATCHER_MODEL_DESIGN=github-copilot/claude-opus-4.7
# (rest fall back to SOPHIA_DISPATCHER_MODEL)
```

The provider and model axes are independent and **provider-scoped** —
each adapter parses its own model-string format.

## Per-provider tuning knobs

Each opt-in provider has a small set of env vars beyond the activation
flag:

| Env var | Default | Effect |
|---------|---------|--------|
| `SOPHIA_OLLAMA_CONCURRENT` | `2` | SpawnGovernor hint. Single-GPU hosts queue concurrent ollama runs internally; the hint just keeps the orchestrator from stacking work the daemon will serialize anyway. |
| `SOPHIA_AIDER_CONCURRENT`  | `1` | Concurrent in-place edits against the same worktree race. Size up only when worktrees are isolated per spawn (which the orchestrator already does — sizing to 2-4 is usually safe). |

## Auth setup by environment

This is the **5-environment matrix** from the 2026-05-15 LLM auth
research. Apply the row that matches your deployment.

### 1. Dev local (single dev's machine)

```bash
opencode auth login github-copilot
opencode auth login openai
opencode auth login google
opencode auth list   # confirm all three OAuth credentials present
```

In the orch container:

```bash
docker run -v ~/.local/share/opencode/auth.json:/home/nonroot/.local/share/opencode/auth.json:ro ...
```

**Pros**: zero incremental cost, works today. **Cons**: tied to
operator's personal subscriptions; not multi-user.

### 2. CI/CD (GitHub Actions)

```yaml
- name: Run E2E SDD cycle
  env:
    ANTHROPIC_API_KEY: ${{ secrets.ANTHROPIC_API_KEY }}
    OPENAI_API_KEY:    ${{ secrets.OPENAI_API_KEY }}
    GOOGLE_API_KEY:    ${{ secrets.GOOGLE_API_KEY }}
    SOPHIA_DISPATCHER_MODEL: anthropic/claude-sonnet-4-6
  run: docker compose -f compose.full-stack.yaml up -d
```

opencode auto-detects API keys from these env vars (no `auth.json`
needed). For Codex paths use `OPENAI_API_KEY` directly — opencode
proxies it.

**Cost** for 50 cycles/month at ~30K tokens each:
- Anthropic Sonnet: ~$6/month
- OpenAI Codex: ~$4/month
- Total: well under the round-up threshold of any usage tier.

### 3. Staging single-tenant (Vault Agent injector)

```yaml
metadata:
  annotations:
    vault.hashicorp.com/agent-inject: "true"
    vault.hashicorp.com/role: "sophia"
    vault.hashicorp.com/agent-inject-secret-llm: "secret/data/sophia/staging/llm"
    vault.hashicorp.com/agent-inject-template-llm: |
      {{- with secret "secret/data/sophia/staging/llm" -}}
      export ANTHROPIC_API_KEY="{{ .Data.data.anthropic_key }}"
      export OPENAI_API_KEY="{{ .Data.data.openai_key }}"
      {{- end }}
```

opencode reads `{env:ANTHROPIC_API_KEY}` from its config — no rotation
plumbing needed.

### 4. Production single-tenant (WIF or pinned API key)

When opencode adds Anthropic WIF support: use it (zero static secrets).
Until then: API key in Vault/AWS Secrets Manager + External Secrets
Operator + per-pod env injection. NEVER set `ANTHROPIC_API_KEY` AND
attempt WIF — the API key shadows WIF silently and you bill twice.

### 5. Production multi-tenant (BYOK)

Each tenant brings their own API key on onboarding. Sophia encrypts
with KMS, stores in `tenant_credentials`, and injects the right key per
cycle. The SaaS provider key is reserved for freemium with tight
rate-limit gateway in front.

## Common pitfalls (all observed in practice)

1. **`anthropic/*` via OAuth in opencode = "out of usage" 24h/7**.
   Anthropic blocks third-party Claude Code OAuth tokens at the
   server layer (Feb 2026 ToS update). Use `github-copilot/claude-*`
   instead — Microsoft's deal with Anthropic explicitly permits it.
2. **API key wins over OAuth** if both are present. Set ONE, not both.
3. **Subscription ≠ unlimited**. Pro/Plus tiers throttle hard at
   ~50-100 high-end model calls per day. Heavy use needs API tier.
4. **`OPENCODE_DISABLE_TUI=1`** is required in any container
   ENTRYPOINT — opencode tries to render a TUI by default and that
   blocks subprocess invocation. Set in the Dockerfile or compose.
5. **opencode `--yolo` flag** auto-approves all permissions. Required
   for headless cycles (without it, every tool-use prompts and hangs
   on the missing TTY). The runtime-adapters-llm Dockerfile sets
   `OPENCODE_DISABLE_TUI=1` and operators inject `opencode-config.json`
   with `permissions.bash=allow` etc to achieve the same effect.

## Validating a provider end-to-end

Smoke test directly against the dispatcher subprocess (faster than
running a full SDD cycle):

```bash
# Inside the runtime-adapters-llm container
opencode run -m openai/gpt-5.3-codex \
  "return literally: SMOKE-TEST-OK"
```

Expected: ~5-10 second wall-clock, output is exactly `SMOKE-TEST-OK`
(no Markdown wrapping). If the output is "out of usage" or auth error,
fix auth before touching Sophia config.

For full cycle validation (Sophia → governance → runtime → opencode →
LLM → envelope → orch persists), see
[`docs/operations/cycle-validation.md`](cycle-validation.md) and run
`./scripts/e2e-sdd-cycle.sh` from the repo root. The script brings up
the full local stack with the `runtime-adapters-llm-opencode` image
target, drives a real `explore` phase against your authenticated
opencode credentials, and prints the final envelope + orchestator
logs.

## Future work tracked in the multi-LLM factory roadmap

- V2.1: `claude-code` adapter IF Anthropic unblocks third-party OAuth
- V2.1: apply-phase synthetic envelope from `git status --porcelain`
  (needed to make `aider` end-to-end useful — currently the apply
  executor sees `EnvelopeRaw = nil` and falls back to the
  memory-topic-key path declared on the DispatchRequest)
- V2.2: per-call session provenance (record actual adapter, not
  `session.ProviderOpenCode` reused as a V1 enum placeholder)
- V2.3: cost tracking + budget caps per provider
- V2.3: auto-failover (provider down → switch to backup mid-cycle)

## Done in this version

- ✅ V2.0 factory (PR #18)
- ✅ `ollama` adapter as opt-in (PR #19)
- ✅ `aider` adapter as opt-in apply-only (PR #20)
