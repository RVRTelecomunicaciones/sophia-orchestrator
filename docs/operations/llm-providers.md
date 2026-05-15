# LLM Provider Operations Guide

**Status**: living doc · last updated 2026-05-15 (V2.0 multi-LLM factory)

This is the operator-facing reference for wiring LLM providers into
Sophia. Combine it with the runtime-adapters operator doc
(`sophia-runtime-adapters/docs/architecture/llm-runtime-deployment.md`)
which covers the container-level setup.

## Quick reference — the two routing axes

Sophia has TWO independent per-phase routing knobs:

| Axis | Env var | What it selects |
|------|---------|-----------------|
| **Provider** (V2.0) | `SOPHIA_DISPATCHER_PROVIDER[_<PHASE>]` | Which adapter (`opencode`, future `aider`, `ollama`) executes the call |
| **Model** (V1.5)  | `SOPHIA_DISPATCHER_MODEL[_<PHASE>]`    | Which LLM model the chosen adapter invokes (e.g. `github-copilot/claude-opus-4.7`) |

Both axes default to the global value if no per-phase override matches.

## V2.0 baseline — providers shipped

| Provider name | Adapter status | CLI | Auth |
|---------------|----------------|-----|------|
| `opencode`    | ✅ shipped (default) | `opencode run -m <model>` | OAuth via `~/.local/share/opencode/auth.json` (bind-mount) |

V2.1+ roadmap: `aider`, `ollama`, `claude-code` (when Anthropic
unblocks third-party OAuth).

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

You only set the provider override when the V2.1+ adapters land. Today
("opencode" is the only registered provider) the variable has no
effect beyond echoing in healthcheck logs.

When V2.1 lands with e.g. an `aider` adapter, you would do:

```bash
SOPHIA_DISPATCHER_PROVIDER=opencode               # default
SOPHIA_DISPATCHER_PROVIDER_APPLY=aider            # apply runs aider
SOPHIA_DISPATCHER_MODEL_APPLY=anthropic/claude-opus-4-7  # aider's model namespace
```

The provider/model namespaces are independent — each adapter parses
its own model string format.

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
LLM → envelope → orch persists), see the validation script in
`docs/operations/cycle-validation.md` (TODO).

## Future work tracked in the multi-LLM factory roadmap

- V2.1: `aider` adapter (fork-friendly, supports git-aware prompts)
- V2.1: `ollama` adapter (local-first LLMs, zero cost, privacy)
- V2.2: `claude-code` adapter IF Anthropic unblocks third-party OAuth
- V2.2: per-call session provenance (record actual adapter, not default)
- V2.3: cost tracking + budget caps per provider
- V2.3: auto-failover (provider down → switch to backup mid-cycle)
