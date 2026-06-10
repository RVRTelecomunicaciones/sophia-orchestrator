# Graphify Audit — Sophia Integration Decision

## Metadata

- Source repo: https://github.com/safishamsi/graphify
- Cloned at: 2026-06-07
- Commit SHA: 8a04560bf5d5eaeef8e466bce084270b7f68faae
- Version: 0.8.35 (PyPI package: `graphifyy`)
- License: MIT (Copyright 2026 Safi Shamsi)
- Audit scope: integration design for Sophia INIT phase (M-KNOW-INIT-0), no code copied

---

## Executive Summary

Graphify is a Python library (tree-sitter AST + NetworkX DiGraph) that exposes a 10-tool MCP stdio server over a pre-built `graph.json`. The integration model for Sophia is: Sophia CLI bootstraps Python 3.10+ and pins `graphifyy==0.8.35`, runs `graphify update <repo>` to build the AST graph, then spawns `graphify serve graphify-out/graph.json` as a per-change MCP sidecar registered in `sophia-agent-mcp`. Sophia's Go structural detector runs in parallel covering what Graphify omits (manifest parsing, framework fingerprinting, arch-style heuristics). The top risks are Python availability on target machines (mitigation: degraded mode), version drift on a project that ships multiple releases per day (mitigation: exact version pin), and `affected_nodes` not exposed as an MCP tool today (mitigation: shell out or upstream PR). The project is active (version 0.8.22 to 0.8.35 shipped in two weeks), so pinning is mandatory.

---

## Capability Map

| Capability | Where in Graphify | Adopt? | Sophia uses via | Notes |
|---|---|---|---|---|
| AST extraction (tree-sitter, 30+ langs) | `graphify/extract.py` | YES | `graphify update <repo>` CLI | Deterministic, no LLM |
| Per-language extractors | `extract_go`, `extract_python`, `extract_js`, `extract_rust`, `extract_java`, et al. | YES (implicitly, via build) | same | See Languages section |
| Graph build (NetworkX DiGraph) | `graphify/build.py` | YES | build step | Output: `graphify-out/graph.json` |
| Community detection (Leiden/modularity) | `graphify/cluster.py` | YES | via MCP `get_community`, `god_nodes` | Leiden requires `graspologic` optional extra |
| God nodes detection | `graphify/analyze.py` `god_nodes()` | YES | MCP tool `god_nodes` | Top-N by degree |
| MCP server (stdio) | `graphify/serve.py` | YES | `graphify serve graphify-out/graph.json` | See MCP contract section |
| MCP server (HTTP/SSE) | `graphify/serve.py` `serve_http()` | SKIP | — | Out of scope; per-developer transport is stdio |
| Watch daemon | `graphify/watch.py` | SKIP for dev-loop | Implicit via MCP hot-reload | MCP server hot-reloads on mtime+size change; no separate daemon needed |
| PR impact (`get_pr_impact`, `list_prs`, `triage_prs`) | `graphify/serve.py`, `graphify/prs.py` | CONDITIONAL | MCP tools, restricted in `tools_allowed` | Include `get_pr_impact` in allowed list; `list_prs`/`triage_prs` are optional |
| `affected_nodes` reverse traversal | `graphify/affected.py` | PARTIAL | CLI shell-out today | Not exposed as MCP tool; can shell out `graphify affected "<node>"` or contribute upstream |
| Export: HTML | `graphify/export.py` | SKIP | — | Developer tool, not INIT output |
| Export: SVG | `graphify/export.py` | SKIP | — | Requires matplotlib optional extra |
| Export: GraphML | `graphify/export.py` | SKIP | — | Out of scope |
| Export: Obsidian vault | `graphify/export.py` | SKIP | — | Explicitly excluded per D11/7-ter.8 |
| Export: Neo4j | `graphify/export.py` | SKIP | — | Explicitly excluded per D11/7-ter.8 |
| LLM `analyze()` community labeling | `graphify/analyze.py` | NO | — | LLM in INIT violates D11 |
| Semantic extraction (LLM via `extract --backend`) | `graphify/llm.py` | NO | — | LLM in INIT violates D11; AST-only is sufficient |
| Cache (SHA256 per-file, stat-indexed) | `graphify/cache.py` | YES (implicit) | Build step uses it | Cache keyed by file content hash, stored in `graphify-out/cache/` |
| Post-commit hooks | `graphify/hooks.py` | NO | — | Sophia has its own events; no post-commit coupling |
| Ingest (URLs, PDFs, office) | `graphify/ingest.py` | NO | — | Out of scope for INIT structural detection |
| SCIP ingest | `graphify/scip_ingest.py` | NO | — | Out of scope |
| PostgreSQL introspection | `graphify/pg_introspect.py` | NO | — | Out of scope for INIT |

---

## Languages Confirmed

Languages verified by reading the actual extractor functions in `graphify/extract.py`, not by listing pyproject deps.

### Go

- **Extractor**: `extract_go()` at `graphify/extract.py:5724`
- **Tree-sitter dep**: `tree-sitter-go` (no version pin in pyproject; `tree-sitter>=0.23.0` required)
- **Node types captured**: file node, functions (`function_declaration`), methods (`method_declaration`), type declarations (`type_declaration`), struct fields, interface methods, imports (`import_declaration`)
- **Edge types**: `imports`, `calls` (INFERRED via call-graph second pass), `contains`, `inherits`, `implements`, `parameter_type`, `return_type`, `generic_arg` (EXTRACTED via type ref walk)
- **Confidence labels**: EXTRACTED for imports and type refs; INFERRED for call-graph second pass

### TypeScript / JavaScript

- **Extractor**: `extract_js()` at `graphify/extract.py:3621` dispatches to `_TS_CONFIG`, `_TSX_CONFIG`, or `_JS_CONFIG` — all share `_extract_generic()` with tree-sitter-typescript
- **Tree-sitter dep**: `tree-sitter-typescript`, `tree-sitter-javascript`
- **Node types captured**: classes, functions, arrow functions, `const`/`let`/`var` top-level declarations, interface declarations, type aliases, imports (`import_declaration`, dynamic `import()`)
- **Edge types**: `imports`, `imports_from`, `re_exports`, `calls` (INFERRED), `inherits`, `implements`, `contains`, `parameter_type`, `return_type`, `generic_arg`
- **Confidence labels**: EXTRACTED for static imports; INFERRED for call-graph pass
- **Extras**: tsconfig.json `paths` alias resolution, pnpm workspace package resolution, Svelte `.svelte` template regex fallback

### Python

- **Extractor**: `extract_python()` at `graphify/extract.py:3613` — delegates to `_extract_generic()` with `_PYTHON_CONFIG` + `_extract_python_rationale()`
- **Tree-sitter dep**: `tree-sitter-python`
- **Node types captured**: classes, functions, methods, top-level assignments
- **Edge types**: `imports`, `imports_from`, `calls` (INFERRED), `inherits`, `contains`, `parameter_type`, `return_type`, `attribute`
- **Confidence labels**: EXTRACTED for imports/inheritance; INFERRED for calls

### Rust

- **Extractor**: `extract_rust()` at `graphify/extract.py:6068`
- **Tree-sitter dep**: `tree-sitter-rust`
- **Node types captured**: functions, structs, enums, traits, impl blocks, impl methods, use declarations
- **Edge types**: `imports` (`use` declarations), `calls` (INFERRED), `inherits` (trait impls), `implements`, `parameter_type`, `return_type`, `generic_arg`, `field`
- **Confidence labels**: EXTRACTED for use/trait/struct; INFERRED for call-graph pass

### Java

- **Extractor**: `extract_java()` at `graphify/extract.py:3903` — delegates to `_extract_generic()` with `_JAVA_CONFIG`
- **Tree-sitter dep**: `tree-sitter-java`
- **Node types captured**: classes, interfaces, enums, methods, constructors, imports
- **Edge types**: `imports`, `calls` (INFERRED), `inherits`, `implements`, `contains`, `parameter_type`, `return_type`
- **Confidence labels**: EXTRACTED for imports/inheritance; INFERRED for calls

### Other Languages Present (not in Sophia's primary target list)

The following are also fully implemented with tree-sitter: C/C++ (`extract_c`, `extract_cpp`), Ruby (`extract_ruby`), C# (`extract_csharp`), Kotlin (`extract_kotlin`), Scala (`extract_scala`), PHP (`extract_php`/`extract_blade`), Swift (`extract_swift`), Lua (`extract_lua`), Zig (`extract_zig`), PowerShell (`extract_powershell`), Elixir (`extract_elixir`), Objective-C (`extract_objc`), Julia (`extract_julia`), Verilog (`extract_verilog`), Fortran (`extract_fortran`), SQL (`extract_sql`), Dart (`extract_dart`), Bash (`extract_bash`), Groovy (`extract_groovy`, with Spock regex fallback), Pascal/Delphi/Lazarus, C#/.razor/SLN/csproj, JSON, DreamMaker, Terraform/HCL. Total: 38+ language extractor functions.

---

## MCP Server Contract

Read from `graphify/serve.py`.

### Transport

- **Default**: stdio (`serve()` at `serve.py:1033`). Confirmed: uses `mcp.server.stdio.stdio_server`. Spawned via `graphify serve graphify-out/graph.json` (or `python -m graphify.serve graphify-out/graph.json`).
- **Alternative**: Streamable HTTP (`serve_http()` at `serve.py:1190`), added in 0.8.34. Out of scope for Sophia INIT (per-developer stdio is the right model).
- **Blank-line filter**: `_filter_blank_stdin()` at `serve.py:475` installs a thread relay that strips blank stdin lines — avoids Pydantic ValidationError from some MCP clients.

### Hot-Reload

`_maybe_reload()` at `serve.py:535` checks `(mtime_ns, size)` of `graph.json` on every tool call. If changed, reloads the graph atomically under a threading lock. No daemon or separate watcher process needed on the server side. If Sophia rebuilds `graphify-out/graph.json` (via `graphify update`), the running MCP server auto-picks it up on next tool call.

### Tools Exposed (10 tools)

Registered in `_build_server()` at `serve.py:505`, dispatched via `_handlers` dict at `serve.py:934`.

| Tool name | Signature (required fields) | Description |
|---|---|---|
| `query_graph` | `question: str`, optional: `mode` (bfs/dfs), `depth` (int, default 3), `token_budget` (int, default 2000), `context_filter` (array of strings) | BFS/DFS traversal returning nodes+edges as text |
| `get_node` | `label: str` | Full details for a node by label or ID |
| `get_neighbors` | `label: str`, optional: `relation_filter: str` | Direct neighbors with edge details |
| `get_community` | `community_id: int` | All nodes in a community by integer ID |
| `god_nodes` | optional: `top_n: int` (default 10) | Top-N most connected nodes |
| `graph_stats` | (no required args) | Node count, edge count, communities, EXTRACTED/INFERRED/AMBIGUOUS percentages |
| `shortest_path` | `source: str`, `target: str`, optional: `max_hops: int` (default 8) | Shortest path between two concepts |
| `list_prs` | optional: `base: str`, `repo: str` | Open GitHub PRs with CI/review state and graph impact |
| `get_pr_impact` | `pr_number: int`, optional: `repo: str` | Graph impact for a specific PR |
| `triage_prs` | optional: `base: str`, `repo: str` | All actionable PRs with full graph impact ranked by blast radius |

### Resources Exposed (6 resources)

| URI | Content |
|---|---|
| `graphify://report` | Full `GRAPH_REPORT.md` |
| `graphify://stats` | Node/edge/community counts |
| `graphify://god-nodes` | Top 10 most-connected nodes |
| `graphify://surprises` | Cross-community surprising connections |
| `graphify://audit` | EXTRACTED/INFERRED/AMBIGUOUS edge breakdown |
| `graphify://questions` | Suggested questions for the codebase |

### Required Args to Launch (stdio)

```bash
graphify serve graphify-out/graph.json
# or equivalently:
python -m graphify.serve graphify-out/graph.json
# or with uv (isolated tool install):
uvx graphifyy serve graphify-out/graph.json
```

The `mcp` optional extra must be installed: `pip install "graphifyy[mcp]"` or `uv tool install "graphifyy[mcp]"`.

---

## Sophia INIT Integration Plan

Concrete steps for M-KNOW-INIT-0, per V4 strategy section 7-ter.

### 1. Bootstrap: Python Detection and Version Pin

Sophia CLI (`sophia-cli`) must detect Python 3.10+ before spawning Graphify.

```go
// In SophiaDetector bootstrap, pseudo-code:
cmd := exec.Command("python3", "--version")
// parse output, require >= 3.10
// If missing or too old → set graph_available = false, continue in degraded mode
```

Version to pin: **`graphifyy==0.8.35`** (commit `8a04560`, audited 2026-06-07).

Install command for bootstrap:
```bash
pip install "graphifyy[mcp]==0.8.35"
# preferred (isolated env, auto-PATH):
uv tool install "graphifyy[mcp]==0.8.35"
```

The `mcp` extra is mandatory for the MCP server. The `leiden` extra (`graspologic`) enables Leiden community detection but requires Python < 3.13; on Python 3.13+ fallback community detection is used. For INIT, community detection is still useful; recommend installing leiden where possible.

### 2. Build Step

Run once per change (or invalidated by cache miss):

```bash
graphify update <repo_root>
# AST-only, no LLM, no API key required
# Output: <repo_root>/graphify-out/graph.json, graphify-out/cache/
```

The `--update` flag (`graphify update`) does AST-only extraction with no LLM calls (confirmed: `graphify extract` without `--backend` on code-only repos runs offline per CHANGELOG 0.8.32). Cache is keyed by SHA256 of file contents; unchanged files are skipped on re-runs.

Cache directory: `graphify-out/cache/ast/`. Can be invalidated by deleting this directory or running with `--force`.

### 3. Serve Step: MCP Sidecar Lifecycle

```bash
graphify serve graphify-out/graph.json
```

**Lifecycle**: `spawned_per_change` (per V4 section 7-ter.6). Sophia INIT spawns the server, queries it for `graph_stats`, `god_nodes`, and optionally `get_community` for the top communities. After INIT completes, the server may remain live for EXPLORE/DESIGN/APPLY/VERIFY phases to re-query the graph. Hot-reload is automatic on mtime change so a background `graphify update` (e.g. after partial apply) does not require a server restart.

Sophia must handle the server startup delay (graph.json parse time). For a 500-file repo the parse should be sub-second. A configurable startup timeout (default 10s) with retry is recommended.

### 4. MCP Provider Registration Shape for `sophia-agent-mcp`

```yaml
mcp_providers:
  - id: graphify
    command: graphify serve graphify-out/graph.json
    transport: stdio
    tools_allowed:
      - query_graph
      - get_node
      - get_neighbors
      - get_community
      - god_nodes
      - graph_stats
      - get_pr_impact
    lifecycle: spawned_per_change
    startup_timeout_s: 10
    env:
      GRAPHIFY_QUERY_LOG_DISABLE: "1"
```

Notes:
- `shortest_path` omitted from `tools_allowed` — not needed in INIT; can be added for EXPLORE/DESIGN.
- `list_prs` and `triage_prs` omitted — require `gh` auth, not needed in INIT.
- `GRAPHIFY_QUERY_LOG_DISABLE=1` disables the `~/.cache/graphify-queries.log` to avoid noise in Sophia contexts.

### 5. `tools_allowed` Exact List (for INIT phase)

```
query_graph
get_node
get_neighbors
get_community
god_nodes
graph_stats
get_pr_impact
```

For subsequent phases (EXPLORE, APPLY, VERIFY) where PR impact is relevant, `get_pr_impact` stays. `shortest_path` can be added for EXPLORE/DESIGN.

### 6. What Sophia Structural Detector Must Add (Go module, ~200-400 LoC)

Graphify does NOT parse manifests or fingerprint frameworks. The Go detector must handle:

**Files to read per language ecosystem:**

| Ecosystem | Files to read | What to extract |
|---|---|---|
| Go | `go.mod` | Module name, Go version, key deps (e.g. `github.com/gin-gonic/gin`) |
| Node/TypeScript | `package.json` | `name`, `version`, `dependencies`, `devDependencies` (Angular, React, Next, NgRx, NestJS, etc.) |
| Node/TypeScript | `tsconfig.json` | `target`, `module`, `paths` (alias hint) |
| Python | `pyproject.toml`, `setup.py`, `requirements.txt` | Framework presence (Django, FastAPI, Flask, etc.) |
| Rust | `Cargo.toml` | Edition, key deps (axum, actix-web, tokio, etc.) |
| JVM | `build.gradle`, `build.gradle.kts`, `pom.xml` | Spring Boot version, Kotlin presence, Micronaut, Quarkus |
| Generic | `Makefile`, `Dockerfile` | Secondary evidence for stack hints |

**Framework fingerprinting targets (Sophia V4 scope):**

- Angular: `@angular/core` version in package.json + presence of `app.module.ts` or `app.config.ts` (standalone, Angular 17+)
- NgRx: `@ngrx/store` in package.json
- React / Next.js: `react` + `next` in package.json
- Spring Boot: `org.springframework.boot:spring-boot-starter` in pom.xml/gradle + version
- Django / FastAPI / Flask: in pyproject.toml or requirements.txt
- Hexagonal arch heuristic: presence of `domain/`, `application/`, `infrastructure/` directories
- Monorepo heuristic: `pnpm-workspace.yaml`, multiple `go.mod` files, `nx.json`, `turbo.json`

**Sophia detector does NOT need to handle any of this via LLM.** It is pure file reads + regex/TOML/JSON parsing.

---

## What We Do NOT Adopt

- **`analyze()` LLM community labeling** (`graphify/analyze.py`, invoked by `graphify label` or `graphify cluster-only` without `--no-label`): INIT must be deterministic per D11. Community detection (Leiden/modularity) is adopted; the LLM naming step is not.
- **Obsidian and Neo4j exporters** (`graphify/export.py` `to_obsidian()`, Neo4j integration): explicitly out of scope per V4 section 7-ter.8.
- **Post-commit git hooks** (`graphify/hooks.py`): Sophia has its own change lifecycle events. Graphify hooks would create a parallel dependency chain.
- **Watch daemon for general dev workflow** (`graphify/watch.py`): The `graphify watch` command (which writes a flag file on FS changes, used by the hook-driven rebuild loop) is not adopted. MCP hot-reload via mtime detection in `serve.py` is sufficient for Sophia's use case.
- **Semantic extraction** (`graphify extract --backend <llm>`): LLM calls in INIT violate D11. AST-only (`graphify update`) is the correct build command.
- **SCIP ingest, PostgreSQL introspection, URL ingest, PDF/video/image ingestion**: all out of scope for structural code graph detection.

---

## Risks and Mitigations

### Risk 1: Python 3.10+ Availability on Target Machines

**Severity**: HIGH. Sophia is a Go binary; target machines may not have Python 3.10+ on PATH.

**Options**:
- **Option A — Degraded mode** (recommended for M-KNOW-INIT-0): Sophia detects Python at boot. If missing or < 3.10, sets `graph_available: false` in `StructuralContext`. PriorContext builder marks the gap explicitly. Skills that depend on graph context (`applies_when.graph_required: true`) are not injected. INIT continues with Sophia Go detector only.
- **Option B — Auto-bootstrap via `uv`**: If `uv` is available, Sophia CLI can run `uvx graphifyy==0.8.35 update <repo>` without a persistent install. Zero-configuration for uv users. Fails silently if uv is also absent.
- **Option C — Docker sidecar**: Package graphify as a Docker image (Dockerfile exists in repo). Sophia spawns it as a container. Adds Docker dependency; overkill for M-KNOW-INIT-0.

Operator must choose before implementing M-KNOW-INIT-0 (see Open Questions).

### Risk 2: Version Drift

**Severity**: MEDIUM. Graphify ships multiple versions per day (0.8.22 through 0.8.35 in approximately two weeks). The MCP tool signatures are stable across this range but edge-case behavior (community IDs, node deduplication, file exclusion logic) changed in several releases.

**Mitigation**: Pin exact version `graphifyy==0.8.35` in sophia-cli bootstrap. Add a `graphify-version: "0.8.35"` field to `StructuralContext` so the audit trail tracks which version produced each graph. Subscribe to the GitHub releases feed to evaluate upgrades. Do not auto-upgrade.

### Risk 3: `affected_nodes` Not Exposed as MCP Tool

**Severity**: LOW for INIT (INIT does not need reverse traversal), MEDIUM for APPLY/VERIFY.

`affected_nodes()` is implemented in `graphify/affected.py:74` and called from the CLI (`graphify affected "<node>"` at `__main__.py:2730`) but is **not registered as an MCP tool** in `serve.py`. The `_handlers` dict at `serve.py:934` has no `affected_nodes` entry.

**Mitigations**:
- **Shell out**: Sophia can call `graphify affected "<node>" --graph graphify-out/graph.json` as a subprocess. Output is plain text; parsing is trivial.
- **Upstream PR**: The implementation is a clean BFS over incoming edges (`affected.py:74-110`). Contributing it as an `affected_nodes` MCP tool would be a small (~30 LoC) PR. The project accepts external contributions.

For M-KNOW-INIT-0, shell-out is sufficient. The upstream PR can be deferred to M3 when `PriorContext` routines are wired.

### Risk 4: Active Development / Open Issues

**Severity**: LOW (for pinned version), MEDIUM (if not pinned).

The repo has 294 open issues and ships fixes and features daily. Breaking behavior is possible between minor versions (e.g., 0.8.27 changed file-level node ID format, which would silently break any code that assumes the old format). Exact version pin is non-negotiable.

### Risk 5: `mcp` Optional Extra

The MCP server requires the `mcp` package which is an optional extra (`graphifyy[mcp]`). Installing `graphifyy` without `[mcp]` produces an ImportError at `serve` startup. Bootstrap must use `graphifyy[mcp]==0.8.35`.

---

## Open Questions for Operator

These questions block the implementation of M-KNOW-INIT-0 and must be answered before the SDD spec phase.

**Q-G1: Python bootstrap strategy?**
- Option A: Degraded mode (warn + continue without Graphify) — simplest, no extra dependencies
- Option B: Auto-bootstrap via `uv` (`uvx graphifyy==0.8.35`) — requires `uv` on PATH
- Option C: Docker sidecar — most isolated, adds Docker dependency
- *Recommendation: Option A for M-KNOW-INIT-0, with Option B as an enhancement in M3.*

**Q-G2: Cache invalidation trigger?**
- Per `change_id` (always rebuild for each change, regardless of file changes)
- Per commit SHA (rebuild when HEAD changes; skip if same commit)
- Watch daemon integration (MCP hot-reload handles it; only rebuild when files actually change)
- *Recommendation: Use `graphify update` with its built-in mtime+SHA256 cache on every INIT call. The cache makes re-runs on unchanged repos near-instant. No additional invalidation logic needed.*

**Q-G3: Version pin — latest stable or specific release?**
- Pin `0.8.35` now and evaluate upgrades manually
- Track latest patch in a semver range (risky given release velocity)
- *Recommendation: Hard-pin `0.8.35`. Evaluate upgrades per ADR.*

**Q-G4: Contribute `affected_nodes` as MCP tool upstream?**
- Yes: open a PR to `safishamsi/graphify` adding `affected_nodes` tool to `serve.py`
- No: shell out via `graphify affected` CLI
- *Recommendation: Shell-out for M-KNOW-INIT-0; upstream PR as a follow-up (low complexity, high community value).*

**Q-G5: Leiden extra (`graspologic`)?**
- Install `graphifyy[mcp,leiden]==0.8.35` for better community detection (requires Python < 3.13)
- Install `graphifyy[mcp]==0.8.35` and accept fallback community detection on Python 3.13+
- *Recommendation: Install with leiden where Python < 3.13. On Python 3.13+ fallback is acceptable for INIT.*

---

## Legal / License

- **License**: MIT. Confirmed in `LICENSE` file: "Copyright (c) 2026 Safi Shamsi. Permission is hereby granted, free of charge, to any person obtaining a copy..."
- **Integration model**: Sophia shells out to `graphify update` (subprocess exec) and queries via MCP stdio protocol. No Graphify source code is copied into Sophia.
- **Attribution requirement**: MIT requires attribution only when distributing copies of the Software. Shell-out + MCP integration is not code distribution. **No attribution block is required** in Sophia source unless Sophia copies literal Graphify code.
- **If code is ever copied**: Follow the V4 section 4.3 attribution block format (Source repo, commit, path, why, changes, license, tests).

---

## Next Actions

1. **Operator approves this audit** and resolves Q-G1 through Q-G5.
2. **Initiate M-KNOW-INIT-0 SDD**: `/sdd-new "structural-detection-graphify"` — covers Sophia Go structural detector + Graphify MCP bootstrap + `StructuralContext` struct + INIT phase spawn-and-merge flow + degraded mode.
3. **Do not start M1** (skills schema/lifecycle/matcher) until M-KNOW-INIT-0 acceptance criteria are met.
4. **Defer upstream PR** for `affected_nodes` MCP tool to post-M-KNOW-INIT-0.
5. **Defer version upgrade** evaluation until M3.

---

## Appendix: Module Inventory

Top-level modules in `graphify/` package (verified by `ls`):

| Module | Role |
|---|---|
| `__main__.py` | CLI entry point (`graphify <cmd>`) |
| `__init__.py` | Package init |
| `detect.py` | File discovery, classification, manifest handling, gitignore |
| `extract.py` | Per-language AST extractors (tree-sitter) + dispatch |
| `build.py` | Assemble extractions into NetworkX DiGraph |
| `cluster.py` | Community detection (Leiden or modularity fallback) |
| `analyze.py` | God nodes, surprising connections, suggested questions, import cycles |
| `report.py` | Render `GRAPH_REPORT.md` |
| `export.py` | Export to HTML, SVG, GraphML, Obsidian, `graph.json` |
| `serve.py` | MCP stdio/HTTP server (10 tools, 6 resources) |
| `affected.py` | Reverse BFS to find nodes affected by a change |
| `cache.py` | SHA256 per-file cache (stat-indexed) |
| `watch.py` | FS watcher (writes flag file, consumed by hook rebuild) |
| `callflow_html.py` | Mermaid call-flow HTML export |
| `ingest.py` | URL/document ingestion |
| `llm.py` | LLM backends (claude, openai, gemini, kimi, bedrock, ollama) |
| `prs.py` | GitHub PR fetching and graph impact computation |
| `security.py` | URL/path/label validation helpers |
| `validate.py` | Extraction schema validator |
| `dedup.py` | Node deduplication passes |
| `querylog.py` | Query logging to `~/.cache/graphify-queries.log` |
| `manifest.py` | Manifest read/write for incremental detection |
| `mcp_ingest.py` | MCP config file parsing |
| `scip_ingest.py` | SCIP protocol ingest |
| `pg_introspect.py` | PostgreSQL schema introspection |
| `google_workspace.py` | Google Workspace file conversion |
| `global_graph.py` | Multi-repo global graph merging |
| `multigraph_compat.py` | MultiDiGraph compatibility helpers |
| `symbol_resolution.py` | Cross-file symbol resolution |
| `semantic_cleanup.py` | Post-LLM semantic graph cleanup |
| `benchmark.py` | Token comparison benchmark |
| `diagnostics.py` | Multigraph diagnostics |
| `wiki.py` | Wiki-format export |
| `tree_html.py` | D3 collapsible-tree HTML export |
| `transcribe.py` | Audio/video transcription |
| `hooks.py` | Git post-commit/post-checkout hook management |
