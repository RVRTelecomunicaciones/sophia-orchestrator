# Hermes Agent Audit — Sophia Integration Decision

## Metadata

- Source repo: https://github.com/NousResearch/hermes-agent
- Cloned at: 2026-06-07
- Commit SHA: `1892e22acb8cece06ae68c792eace1f3c85834f2`
- License: MIT (confirmed — LICENSE file, copyright Nous Research 2025)
- Audit scope: pattern extraction only; no code copied
- Sophia strategy ref: V4 sections 3, 7-bis, 11-15
- Files read: `agent/background_review.py`, `agent/curator.py`, `agent/memory_manager.py`, `agent/system_prompt.py`, `agent/context_engine.py`, `tools/registry.py`, `tools/session_search_tool.py`, `tools/memory_tool.py`, `tools/threat_patterns.py`, `tools/skills_ast_audit.py`, `agent/agent_init.py`, `agent/prompt_builder.py`, `SECURITY.md`, `LICENSE`, `README.md`

---

## Executive Summary

Hermes is a single-tenant conversational agent built around a self-improvement loop: after every conversation turn it forks a daemon thread, replays the conversation in an isolated agent, and lets that agent write skills and memory updates. Skills are markdown files on disk; memory is a flat MEMORY.md/USER.md; session recall uses SQLite FTS5. Sophia adopts six patterns from Hermes: post-phase consolidation (mapped to the archive worker), skill lifecycle with automatic state transitions (mapped to the `skills` table extension), three-tier system prompt assembly (mapped to `PriorContext` builder), memory safety scanning (mapped to memory-engine ingest guard), FTS5 session search (already present in `MemoryTypeEpisodic`; extend metadata), and the single-provider memory rule (mapped to the 0-or-1 auxiliary provider constraint in D10). Sophia explicitly rejects Hermes's LLM-driven background review as a turn-level daemon, the 7-day inactivity curator cadence, AST-based tool auto-discovery, Honcho user modeling, and multi-provider memory. The primary risk is architectural mismatch: Hermes is conversational and user-centric; Sophia is a phase-bounded engineering platform. Every pattern must be reinterpreted in terms of phase boundaries and governance before implementation.

---

## Component-by-Component Table

| Component | File(s) | Adopt? | Sophia target | Risk | Notes |
|---|---|---|---|---|---|
| Background review — daemon fork | `agent/background_review.py` | Yes (concept) | `cmd/workers/main.go` — archive consolidation worker triggered by `phase.archived` event | Medium | Hermes forks on every turn; Sophia fires once per closed change. LLM critic is advisory-only (D8) |
| Skill lifecycle states | `agent/curator.py` (lines 56-59, 268-323) | Yes | `skills` table: candidate→validated→active→deprecated→blocked→archived (V4 section 5.1) | Low | Hermes uses active/stale/archived on inactivity; Sophia uses metrics-driven transitions and governance |
| Curator inactivity trigger | `agent/curator.py` (line 56: `DEFAULT_INTERVAL_HOURS = 24 * 7`) | No | N/A — trigger is `phase.archived` event, not elapsed time | Low | 7-day idle trigger is conversational UX pattern, not compatible with phase lifecycle |
| Memory manager single-provider rule | `agent/memory_manager.py` (lines 258-279) | Yes | D10: 0 or 1 auxiliary memory provider; enforce at boot | Low | Hermes allows 1 external + builtin; Sophia allows Engram or none; same constraint, different providers |
| Three-tier system prompt | `agent/system_prompt.py` (lines 62-350) | Yes | `PriorContext` builder: stable=phase identity+active skills, context=Sophia rules+StructuralContext, volatile=episodic context+routines | Medium | Hermes tiers are stable/context/volatile keyed to conversation session; Sophia tiers are keyed to phase boundary |
| Skills as markdown (SKILL.md) | `agent/prompt_builder.py` (line 1168), skills directories | Yes (concept) | `skills` table with `content` column (PR #76 pattern); inject via PriorContext attribution headers | Low | Hermes reads SKILL.md files from disk; Sophia stores skill content in Postgres and injects via `PriorContext` |
| Tools registry — AST auto-discovery | `tools/registry.py` (lines 42-74: `_module_registers_tools`, `discover_builtin_tools`) | No | N/A | None | Python-only; uses `ast.parse` to find `registry.register()` calls. Go runtime uses explicit registration. Wrong language entirely |
| Memory safety scanning | `tools/memory_tool.py` (lines 78-119, 185-211), `tools/threat_patterns.py` | Yes | memory-engine ingest guard (V4 section 15) | Low | Hermes scans at write time with `scan_for_threats()`. Sophia adopts the same scan points: prompt injection patterns, hardcoded secrets regex, invisible unicode detection |
| MEMORY.md / USER.md flat files | `agent/agent_init.py` (lines 1102-1121), `agent/system_prompt.py` (lines 309-327) | Yes (concept) | `MemoryTypeEpisodic` + semantic memory in memory-engine; no flat files | Low | Hermes persists to disk MEMORY.md. Sophia persists to Postgres. Pattern: structured memory block injected into volatile tier |
| SQLite FTS5 session search | `tools/session_search_tool.py` (lines 1-29) | Yes (concept) | `MemoryTypeEpisodic` + Postgres FTS (already exists); extend metadata for error_class + phase fields | Low | Hermes uses SQLite WAL + FTS5. Sophia already has Postgres FTS. Audit confirms: extend episodic metadata, not replace |
| Context compression / context engine | `agent/context_engine.py` (lines 1-100) | Partial | `change_digest` in memory-engine (D9) | Low | Hermes compresses active conversation context. Sophia compresses per-change history into deterministic digest |
| Honcho user modeling | referenced in `agent/memory_manager.py` (line 387 comment), `README.md` | No | N/A — explicitly out of scope (V4 section 17) | None | Dialectic user modeling for a conversational assistant; Sophia models engineering projects, not users |
| Multi-provider memory simultaneous | `agent/memory_manager.py` (lines 258-279) | No (specifically the rejection pattern is adopted) | D10 enforces 0-or-1; validation at boot | None | Hermes allows 1 external at a time; the rejection logic is what we adopt |
| Background review prompt engineering | `agent/background_review.py` (lines 34-232) | Partial | Archive worker pattern detection heuristics + LLM advisory prompt (D8, D10.1) | Medium | The prompt taxonomy (what to capture, what NOT to capture) is valuable signal for writing the advisory LLM prompt in the worker. Not copied verbatim |

---

## Pattern Extraction Details

### 1. Post-Phase Consolidation Worker

**What the pattern is.** After every conversation turn, Hermes calls `spawn_background_review_thread()` (`background_review.py:562-587`), which starts a daemon thread. That thread instantiates a full fork of `AIAgent` (`background_review.py:402-416`) with a restricted tool whitelist limited to `memory` and `skills` toolsets (`background_review.py:459-472`). The fork receives a snapshot of the conversation messages plus one of three review prompts (memory, skills, or combined). It writes to the shared `_memory_store` and skill files. The fork inherits the parent's cached system prompt byte-for-byte to hit the same prefix cache.

**Where Hermes implements it.** `agent/background_review.py` — entire file. Key fork: lines 327-553 (`_run_review_in_thread`). Thread spawn: lines 562-587 (`spawn_background_review_thread`). Trigger call site: `run_agent.py` (not read in full, but referenced by module docstring line 7).

**How it maps to Sophia.** The `cmd/workers/main.go` archive consolidation worker (V4 section 11) is the equivalent. Differences:
- Trigger: Sophia fires on `phase.archived` event, not after every turn. Phase boundaries are the unit of consolidation, not conversation turns.
- Executor: A Go worker process, not a Python thread fork.
- LLM role: Advisory only (D8). The worker generates change_digest deterministically first; LLM critique is opt-in per D9.
- Storage: Sophia writes to `skills` table and `memory-engine`, not to flat SKILL.md files and MEMORY.md.

**Concrete behavior to replicate.** On `phase.archived`, the worker reads the full phase envelope, apply/verify episodic records, and currently active skills used in the change. It runs deterministic pattern detection (repeated code structure, error_class resolution, retry patterns). If heuristics surface a repeatable pattern that cleared the threshold: create/upsert a candidate skill. Update metrics on skills that were injected and contributed to a successful outcome.

**What we explicitly leave behind.** The fork-an-entire-agent approach. The per-turn cadence. The in-memory Python-thread sharing of `_memory_store`. The conversation-replay mechanism (Sophia worker reads structured phase artifacts from Postgres, not a message snapshot).

---

### 2. Skill Lifecycle States and Automatic Transitions

**What the pattern is.** Hermes `curator.py` defines three states for agent-created skills: `STATE_ACTIVE`, `STATE_STALE`, `STATE_ARCHIVED` (`tools/skill_usage.py`, referenced at `curator.py:309-310`). `apply_automatic_transitions()` (`curator.py:268-323`) walks every curated skill and transitions based on `last_activity_at` against `stale_cutoff` (30 days default) and `archive_cutoff` (90 days default). The transitions are pure functions — no LLM. Pinned skills are never touched (`curator.py:294`). The curator also spawns a review agent for consolidation (archiving overlapping skills, merging narrow skills into class-level umbrellas).

**Where Hermes implements it.** `agent/curator.py:268-323` (automatic transitions), `curator.py:56-59` (constants), `curator.py:357-end` (curator review prompt for the LLM consolidation pass).

**How it maps to Sophia.** The V4 lifecycle has six states vs Hermes's three: `candidate→validated→active→deprecated→blocked→archived`. Hermes's stale is roughly Sophia's deprecated. Hermes's archive is Sophia's archived. The important difference: Sophia's transitions are driven by execution metrics (success_count, failure_count, rollback_count, deprecated_api_hits, avg_retry_reduction) not by elapsed time since last use. Hermes's time-based stale is a proxy for "no evidence of value"; Sophia's metric-based demotion is direct evidence of value or risk.

**Concrete behavior to replicate.** Write the transition evaluator in Go as a pure function over a `Skill` struct and a `Metrics` struct. No LLM, no I/O. The function returns the new status and a reason string. The archive worker calls this function per skill after updating metrics. Pinned-equivalent: skills with `risk_level='critical'` require governance decision before any demotion.

**What we explicitly leave behind.** Time-based inactivity as the primary signal. The 7/30/90-day cadence. The conversational curator review agent that rewrites SKILL.md content. Sophia's archive worker detects candidates from outcomes, it does not review and rewrite existing skill prose.

---

### 3. Memory Manager Single-Provider Rule

**What the pattern is.** `MemoryManager.add_provider()` (`memory_manager.py:258-279`) enforces that at most one non-builtin provider is registered. If a second external provider attempts registration, it is rejected with a warning that names the already-registered provider. The builtin provider (name `"builtin"`) is always accepted. The check is `if self._has_external: ... return`.

**Where Hermes implements it.** `agent/memory_manager.py` lines 258-279. The `_has_external` flag is the gate.

**How it maps to Sophia.** D10: 0 or 1 auxiliary memory provider (Engram or none). Sophia validates at boot that at most one auxiliary is configured. The rejection mechanism is identical in spirit: check before adding, log the conflict with the name of the already-registered provider, refuse to register the second.

**Concrete behavior to replicate.** In `sophia-agent-mcp` boot sequence: read auxiliary provider config; if more than one entry is `enabled=true`, log error with provider names and exit with non-zero. This is a configuration validation error, not a runtime guard, because Sophia's auxiliary providers are configured declaratively, not registered dynamically.

**What we explicitly leave behind.** The dynamic registration model (Sophia uses static YAML config). The runtime-registration API. The tool schema routing table in `MemoryManager` (Sophia routes tool calls through MCP protocol, not through Python dispatch).

---

### 4. Three-Tier System Prompt Assembly

**What the pattern is.** `build_system_prompt_parts()` (`system_prompt.py:62-350`) returns a dict with three keys: `stable`, `context`, `volatile`. Stable contains identity, tool guidance, skills prompt, environment hints — everything that should be byte-identical across all turns in a session to maximize prefix cache hits. Context contains cwd-dependent files (AGENTS.md, .cursorrules) and the caller-supplied system message. Volatile contains the MEMORY.md snapshot, USER.md profile, external memory provider block, and the timestamp line (date-only, not minute-precision, to preserve cache stability for a full day). The three parts are joined with `\n\n` by `build_system_prompt()` and cached on `agent._cached_system_prompt`. The cache is invalidated only after context compression events (`invalidate_system_prompt()`, `system_prompt.py:372-380`).

**Where Hermes implements it.** `agent/system_prompt.py` lines 62-413. Stable tier: lines 85-278. Context tier: lines 289-304. Volatile tier: lines 306-344.

**How it maps to Sophia.** The `PriorContext` builder already has a structure; the audit confirms this three-tier discipline should be formalized:
- `stable` → phase identity string (which phase, which SDD change, phase constraints) + active skills injected via SkillMatcher (content is stable within a phase run)
- `context` → Sophia rules + StructuralContext from INIT (project-scoped, stable per change)
- `volatile` → episodic context from memory-engine FTS (turn-level recall), change_digests, routine outputs (Graphify summary, Context7 docs, LSP diagnostics, git diff), Engram auxiliary block if configured

The key difference from Hermes: Sophia's tiers are keyed to the phase boundary, not the conversation session. Volatile content changes between phases, not between turns within a phase (unless a routine produces fresh output per turn).

**Concrete behavior to replicate.** In the `PriorContext` builder:
1. Build the stable block once per phase init; cache it.
2. Build the context block once per change (StructuralContext is computed in INIT and stable for the change duration).
3. Build the volatile block per turn within the phase (episodic recall, routine outputs).
4. Join with `\n\n` in stable→context→volatile order.
5. Each injected skill block gets a source attribution header (V4 section 12.3): `## Skill: <name> v<version> (active, source=<activation_source>, project=<project_id>)`.

**What we explicitly leave behind.** SOUL.md as identity source (Sophia's phase identity is deterministic, not a persona file). Environment probe lines (Python-specific). Platform hints (terminal platform hints for Telegram, Discord, etc. — not relevant to a phase engine). The Alibaba model workaround (provider-specific LLM quirk).

---

### 5. Memory Safety Scanning

**What the pattern is.** Hermes implements a two-layer memory guard:

Layer 1 — write-time content scan: `memory_tool.py:78-119` defines `_scan_memory_content()`, which calls `tools/threat_patterns.scan_for_threats()`. `scan_for_threats()` (`threat_patterns.py:187-213`) runs regex patterns in two scopes (`"all"` and `"strict"`): classic prompt injection patterns, HTML comment injection, exfiltration via curl/wget, hardcoded secrets (API keys, tokens), and invisible/bidirectional unicode characters (U+2062-U+2064, U+2066-U+2069). Content that matches is rejected at write time with an error string returned.

Layer 2 — snapshot-time scan: `MemoryStore.format_for_system_prompt()` (`memory_tool.py:135-211`) scans each entry again with `scope="strict"` when building the snapshot for injection into the system prompt. Entries that match at snapshot time are excluded from the prompt even if they passed the write-time check (in case patterns were updated after the write).

**Where Hermes implements it.** `tools/memory_tool.py:63-211` (scan infrastructure and MemoryStore). `tools/threat_patterns.py:1-250` (pattern library with 17 patterns + invisible unicode set).

**How it maps to Sophia.** Memory-engine ingest guard (V4 section 15). The pattern taxonomy maps directly:
- `prompt_injection` → scan every inbound memory record before persisting to Postgres
- `hardcoded_secret` → scan before accepting any text into episodic or semantic memory
- invisible unicode → scan before persisting; reject with `invisible_unicode_U+XXXX` error
- Provenance requirement: every ingest call must supply `source`, `agent_id`, `trust_level` (Sophia extension not present in Hermes)
- Quarantine: Hermes rejects silently (returns error, does not persist). Sophia adds optional `memory_quarantine` table to preserve evidence (V4 section 15 schema)

**Concrete behavior to replicate.** In memory-engine ingest path (before the INSERT into `episodic_memories` or `semantic_memories`): run prompt injection regex set, run secret entropy/regex check, scan for invisible unicode. On match: log with reason codes, write to `memory_quarantine` if table exists, return error to caller. Do NOT persist to main tables.

**What we explicitly leave behind.** The streaming context scrubber (`StreamingContextScrubber` class in `memory_manager.py:62-224`) — this is for preventing memory-context tags from leaking in streamed LLM output, a UI problem Sophia does not have (it does not stream to a terminal UI). The `<memory-context>` fence tags — Sophia uses structured JSONB in Postgres, not inline text tags.

---

### 6. SQLite FTS5 Session Search → Postgres FTS Extension

**What the pattern is.** `session_search_tool.py` implements three modes over a SQLite database with FTS5 index: DISCOVERY (FTS5 query → top-N sessions with snippet + ±5 message window + bookend messages), SCROLL (paginate by message_id), BROWSE (recent sessions). Key design decisions: zero LLM calls in any mode, deduplication by session lineage (walks `parent_session_id` chain to root), hidden session sources (`HERMES_SESSION_SOURCE=tool` sessions excluded from browsing).

**Where Hermes implements it.** `tools/session_search_tool.py:1-80+` (full file, only first 80 lines read). FTS5 index managed by `hermes_state.py` (referenced but not read in full).

**How it maps to Sophia.** `MemoryTypeEpisodic` in memory-engine already uses Postgres FTS. The audit confirms: do not replace, extend metadata. Specific additions needed for M3:
- Add `error_class` column to episodic memories (for worker to query "did we see this error before?")
- Add `phase` column to episodic memories (for FTS filter by phase type)
- Add `change_id` FK so episodic records are queryable per change
- Query pattern for PriorContext: `WHERE phase = $phase AND error_class = ANY($classes)` ordered by FTS rank, top-K results

**What we explicitly leave behind.** The BROWSE mode (session listing for conversational UX). The SCROLL mode (paginated message drill-down for terminal users). The bookend logic (first/last 3 messages per session — irrelevant to structured phase artifacts).

---

## Anti-Patterns to Avoid

### 1. LLM-driven skill creation in INIT

Hermes's background review can CREATE a new skill directly from a single conversation turn, with no execution evidence required. The `_SKILL_REVIEW_PROMPT` (`background_review.py:45-148`) instructs the review agent to "be ACTIVE — most sessions produce at least one skill update." This is the exact anti-pattern D11 and V4 section 7-bis.3 identify:

> LLM creates skill to avoid that the LLM hallucinates → but the skill was created with an LLM that could have hallucinated → garbage in, garbage out → the learning loop is born contaminated.

Sophia's rule: skills are born from repeated successful execution outcomes observed by the archive worker, not from a single-turn LLM judgment.

### 2. Background review forking the entire agent per turn

`_run_review_in_thread()` instantiates a full `AIAgent` after every conversation turn (`background_review.py:402`). This is a conversational agent pattern where cost-per-turn can be amortized across a long-running CLI session. Sophia's unit of work is a phase, which has an explicit start and end. Forking a full agent per phase-end is approximately what the archive worker does — but it is a Go process triggered by an event, not a Python daemon thread. The key difference: the worker does not have access to a live LLM for every invocation; LLM advisory is opt-in and budget-gated.

### 3. 7-day inactivity curator

`DEFAULT_INTERVAL_HOURS = 24 * 7` (`curator.py:56`). The curator fires when the agent has been idle for at least 2 hours (`DEFAULT_MIN_IDLE_HOURS = 2`) AND the last curator run was more than 7 days ago. This is a temporal heuristic designed for a personal assistant where "no recent use" correlates with "skills are stale." Sophia's phase lifecycle has explicit boundaries: a skill becomes deprecated when its API hits increment, or when retry_reduction drops below threshold, regardless of calendar time.

### 4. AST-based tool auto-discovery

`discover_builtin_tools()` (`tools/registry.py:57-74`) walks `tools/*.py`, parses each file with `ast.parse()`, and imports modules that have top-level `registry.register()` calls. This is elegant for a Python codebase where tools self-register. Sophia is written in Go. Tool registration is explicit and compile-time-checked. The pattern is not only the wrong language — the entire motivation (dynamic module discovery to avoid a central registration list) conflicts with Sophia's explicit allowlist governance model.

### 5. Honcho user modeling

`memory_manager.py` references Honcho as an external memory plugin (`on_delegation`, `on_session_switch`, and the shutdown path mention Honcho flush). Honcho models WHO THE USER IS across conversational sessions. Sophia models ENGINEERING CHANGES and their outcomes. Out of scope per V4 section 17 and D10 (auxiliary memory: Engram or none).

### 6. Multi-provider memory simultaneous

Hermes rejects a second external provider but allows 1 external + 1 builtin running simultaneously. The builtin writes to MEMORY.md on disk; the external provider (Honcho, mem0, supermemory, etc.) writes to its own backend. Both are queried on every prefetch. Sophia's rule is simpler: the memory-engine IS the canonical backend; no builtin-vs-external split. Engram, if configured, is a single auxiliary bridge. Multiple providers active simultaneously is a configuration error.

---

## Legal / License

MIT License confirmed. Full text in `/Users/russell/Documents/2026/_research/hermes-agent/LICENSE`. Copyright Nous Research 2025.

Per V4 section 4.3: if any literal code is ever copied or adapted, the following block is required in the file header:

```
## Copied/Adapted Component
Source repo:     https://github.com/NousResearch/hermes-agent
Source commit:   1892e22acb8cece06ae68c792eace1f3c85834f2
Source path:     <file>
Why:             <reason>
Changes:         <list>
License:         MIT
Tests:           <paths>
```

This audit copies no code. All current adoption is pattern-level.

---

## Next Actions

### Inputs for M-KNOW-INIT-0

The audit confirms that Hermes has no equivalent of structural detection (no Graphify, no manifest parsing, no framework fingerprinting). The `build_context_files_prompt()` path in Hermes reads AGENTS.md and .cursorrules from the working directory — this is the closest analog, and it is purely file-reading, not AST or manifest analysis. M-KNOW-INIT-0 starts from scratch; no Hermes pattern to adopt for structural detection.

### Inputs for M2 Worker Design

The audit confirms five concrete inputs:
1. The "what to capture" taxonomy from `_SKILL_REVIEW_PROMPT` (`background_review.py:45-148`) is the richest reference for writing the archive worker's pattern-detection heuristics. Read the full list of "signals to look for" and "do NOT capture" sections. These encode years of production experience on what makes a skill useful vs. noise.
2. The fork isolation pattern (tool whitelist, suppress status output, redirect stdout) maps to the worker's LLM advisory call: restrict the advisory LLM to read-only skill queries; it returns a structured recommendation, it never writes directly.
3. The `summarize_background_review_actions()` function (`background_review.py:237-297`) shows how to extract successful tool actions from a review session — useful for the worker's audit log.
4. The curator's `apply_automatic_transitions()` logic (`curator.py:268-323`) is the clearest reference for writing Sophia's metric evaluator. The structure (iterate skills, check state, apply transition, return counts) maps directly.
5. The `build_memory_write_metadata()` function (`background_review.py:300-324`) shows the provenance fields Hermes uses: `write_origin`, `execution_context`, `session_id`, `parent_session_id`, `platform`. Sophia's provenance schema adds `agent_id` and `trust_level` but the remaining fields are directly analogous.

### Inputs for M3 PriorContext Enrichment

The three-tier structure audit (section "Pattern Extraction: Three-Tier System Prompt") provides concrete content for each Sophia tier. The date-only timestamp optimization from Hermes (`system_prompt.py:331-337`) is directly applicable: avoid minute-precision timestamps in stable or context tiers to preserve cache stability.

### Questions for Operator

None surfaced that are not already captured in V4 section 18 (Q1-Q5). All blocking decisions for M1 and M2 are in the strategy doc.

---

## Open Questions

The following surfaced during the audit and are not addressed in V4. They are informational, not blocking M1.

**OQ-1: Skills Guard for ingest — scope of scan.**
Hermes scans memory writes with `scope="context"` (all patterns) and `scope="strict"` (adds hardcoded_secret patterns) at snapshot time. V4 section 15 mentions "secret scan + prompt injection scan" without specifying which scope to use at which point. Recommendation: use `scope="all"` at ingest time (write path) and re-scan with `scope="strict"` at PriorContext injection time. Closes before M6 (memory safety scan milestone).

**OQ-2: Skill content storage — text column vs markdown file.**
Hermes reads SKILL.md from disk at system prompt build time (`prompt_builder.py:1168`). Sophia stores skill content in the `skills` table (PR #76 `content` column). The audit confirms this is the right call, but: what is the max token budget per skill? Hermes injects skill descriptions (short) into the stable tier and full SKILL.md only on explicit `skill_view` load. Sophia's SkillMatcher should follow the same pattern: inject skill `name + description + applies_when` summary in stable tier; full `content` only if the phase explicitly requests it. Closes in M3 during PriorContext enrichment design.

**OQ-3: Skills Guard for worker-created candidates.**
Hermes protects bundled skills from the curator and background review. Sophia's governance layer is the equivalent — candidates created by the archive worker go through governance before becoming active. The question is: should the worker itself run a pre-submission scan on candidate skill content before persisting to the `skills` table? Recommendation: yes, run the same ingest guard as memory-engine. Closes before M2.

**OQ-4: Idempotency token for worker re-runs.**
Hermes has no equivalent of Sophia's worker idempotency requirement (V4 section 11.3). The audit finds no reference implementation to borrow from. This is a Sophia-native design decision: use `processed_changes` log or a `worker_runs` table with `(change_id, worker_version)` unique constraint. Not a Hermes gap — just confirming there is no reference to adapt.

**OQ-5: Background review prompt taxonomy — licensing note.**
The `_SKILL_REVIEW_PROMPT` and `_COMBINED_REVIEW_PROMPT` text in `background_review.py` is MIT-licensed. If Sophia's archive worker advisory LLM prompt draws heavily from the taxonomy of "signals to look for" and "do NOT capture," that constitutes adaptation of MIT-licensed text. A brief attribution comment in the worker prompt source file is sufficient — no separate block needed since this is prose instruction text, not functional code. Flag if legal review is required before M2.
