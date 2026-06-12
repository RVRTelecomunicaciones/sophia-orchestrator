# Delta: bootstrap-trigger-service

> **Transport reinterpretation (design DG-C7-8, authoritative)**: the proposal's
> D-C7-8 "existing MCP dispatcher port" is NOT implementable — orch's only MCP
> client (`dispatcher/mcp/dispatcher.go`) exposes solely `AgentDispatcher.Dispatch`
> hardcoded to `agent.run`/`agent.health`; no generic tool-call path exists, and
> agent-mcp's proxied `context7.*` tools are registered for the dispatched agent,
> not the orch process. The implemented mechanism is a NEW outbound `DocsProvider`
> port with an adapter that reuses the dispatcher's streamable-HTTP transport to
> call `context7.*` on the agent-mcp bridge. References to "existing dispatcher
> port (D-C7-8)" below are retained as documented intent only; the requirement
> "no new outbound port" is superseded.

## Capability

`BootstrapTriggerService` is a new application service in
`internal/application/bootstrap/` that is the single decision point for firing
a Context7 import. It implements two trigger paths: (1) greenfield — fires when
`StructuralContext.Greenfield == true`; (2) drift — fires when the detected
major version of a framework exceeds the major version encoded in the active
skill's `AppliesWhen.FrameworkMinVersion` for that framework. The service is
wired into `runInitPhase` (phase/service.go) and runs fully async post-INIT
(never blocks the INIT response). It calls the Context7 MCP tools through the
existing agent-mcp `ExternalMCPProxy` path (D-C7-8) and delegates insertion to
`SkillImporter`. All failure paths end in WARN + skip — the service MUST NOT
block, panic, or surface errors to the INIT phase.

## ADDED Requirements

### Requirement: TriggerIfNeeded Entry Point

`BootstrapTriggerService.TriggerIfNeeded(ctx context.Context, sc *structural.StructuralContext)`
MUST be the sole entry point called by `runInitPhase`.

The method MUST:
1. Check `CONTEXT7_API_KEY` (or equivalent config key). If absent or empty →
   log WARN `"bootstrap: CONTEXT7_API_KEY not set; skipping"` and return nil.
2. Check the per-project rate guard (see Rate Guard requirement). If limit
   exceeded → log WARN `"bootstrap: rate limit exceeded; skipping"` and return nil.
3. Evaluate greenfield trigger: if `sc.Greenfield == true` → initiate greenfield
   import for each detected framework. If no frameworks detected and greenfield
   is true, skip (no framework name to query) with WARN.
4. Evaluate drift trigger: for each `FrameworkInfo` in `sc.Frameworks`, find the
   active skill whose `AppliesWhen.Framework` includes the framework name and
   `AppliesWhen.FrameworkMinVersion` is set. Compare detected major vs. stored
   min major. If detected > stored → initiate drift import for that framework.
5. For each triggered import, call `SkillImporter.ImportFromDocs`. Log WARN on
   any error; continue to next framework.
6. Return nil in all cases (errors are swallowed after logging).

MUST NOT call any LLM or AI provider.
MUST NOT modify any existing skill row. Only `InsertIfAbsent` is permitted.
MUST NOT block the caller — this method is called from a goroutine.

#### Scenario: Missing API key → skip with WARN

- GIVEN `CONTEXT7_API_KEY` is not set in the environment
- WHEN `TriggerIfNeeded` is called with a greenfield context
- THEN the method logs WARN and returns nil without making any MCP call
- AND no skill row is inserted

#### Scenario: Greenfield trigger fires for detected framework

- GIVEN `sc.Greenfield == true` and `sc.Frameworks = [{Name: "angular", Version: "22.0.0"}]`
- AND `CONTEXT7_API_KEY` is set
- AND rate guard permits
- WHEN `TriggerIfNeeded` is called
- THEN `SkillImporter.ImportFromDocs` is called with framework `"angular"` and
  version `"22.0.0"`

#### Scenario: Greenfield with no frameworks — skip with WARN

- GIVEN `sc.Greenfield == true` and `sc.Frameworks = []` (no stack detected)
- WHEN `TriggerIfNeeded` is called
- THEN the method logs WARN `"bootstrap: greenfield but no framework detected; skipping"`
- AND no MCP call is made

#### Scenario: Non-greenfield, no drift — no import

- GIVEN `sc.Greenfield == false`
- AND all detected framework versions satisfy their active skill's FrameworkMinVersion
- WHEN `TriggerIfNeeded` is called
- THEN no import is triggered and no MCP call is made

### Requirement: Drift Trigger — Detected Major Exceeds Active Skill Major

The drift comparison MUST be performed in `BootstrapTriggerService`, not in
`structuralMatches`. The comparison is:

```
detected_major > active_skill_applies_when.FrameworkMinVersion[framework_name].major
```

Where `detected_major` comes from `FrameworkInfo.Version` (already a stripped
semver from `parser_node.go:46-48`).

"Active skill" means: `status = 'active'` AND `AppliesWhen.Framework` contains
the lowercased framework name AND `AppliesWhen.FrameworkMinVersion[name]` is set.

If no active skill has `FrameworkMinVersion` set for the detected framework,
the drift trigger MUST NOT fire for that framework (no baseline to compare against).

If the active skill's `FrameworkMinVersion` value cannot be parsed, the drift
trigger MUST NOT fire and MUST log WARN.

If the detected version cannot be parsed, the drift trigger MUST NOT fire and
MUST log WARN.

A drift import creates a NEW `(name, version)` row. The old active version row
MUST remain `active` until governance promotes the new candidate. The importer
MUST NOT touch the existing active row.

#### Scenario: Drift detected — new version imported

- GIVEN an active skill `stack/angular-22` with `FrameworkMinVersion: {"angular": "22.0.0"}`
- AND `sc.Frameworks = [{Name: "angular", Version: "23.0.0"}]`
- AND `sc.Greenfield == false`
- WHEN `TriggerIfNeeded` is called
- THEN `SkillImporter.ImportFromDocs` is called for angular v23
- AND the existing `stack/angular-22` row is not modified

#### Scenario: No drift when detected matches active major

- GIVEN an active skill `stack/angular-22` with `FrameworkMinVersion: {"angular": "22.0.0"}`
- AND `sc.Frameworks = [{Name: "angular", Version: "22.3.1"}]`
- WHEN `TriggerIfNeeded` is called
- THEN no import is triggered for angular

#### Scenario: No drift when active skill has no FrameworkMinVersion

- GIVEN an active skill for angular with no `FrameworkMinVersion` set
- AND `sc.Frameworks = [{Name: "angular", Version: "23.0.0"}]`
- WHEN `TriggerIfNeeded` is called (non-greenfield)
- THEN no import is triggered for angular (no baseline)

### Requirement: Rate Guard

A per-project rate guard MUST cap the number of Context7 bootstrap calls.
The guard MUST be configurable via `bootstrap.max_calls_per_project_per_day`
(default: 5). The guard MUST persist its counter in a durable store (Postgres or
a simple key-value in the existing DB) keyed by `(project_id, date_UTC)`.

If `max_calls_per_project_per_day` is reached → log WARN and skip. MUST NOT
make any MCP tool call when the guard fires.

The guard is per-project, not per-framework. A project that triggers greenfield
for 3 frameworks counts as 3 calls.

#### Scenario: Guard allows calls below limit

- GIVEN a project with 0 bootstrap calls today and limit 5
- WHEN `TriggerIfNeeded` fires twice for the same project
- THEN both calls proceed and the counter reaches 2

#### Scenario: Guard blocks calls at limit

- GIVEN a project with 5 bootstrap calls today (limit = 5)
- WHEN `TriggerIfNeeded` is called again
- THEN WARN is logged and no MCP call is made
- AND the counter stays at 5

#### Scenario: Guard resets at day boundary (UTC)

- GIVEN a project at limit 5 on day D
- WHEN the first call arrives on day D+1 (UTC)
- THEN the counter resets to 0 and the call proceeds

### Requirement: Context7 MCP Tool Calls

The service MUST call Context7 through the agent-mcp `ExternalMCPProxy` via the
new outbound `DocsProvider` port (design DG-C7-8); its adapter reuses the
dispatcher's streamable-HTTP transport to invoke `context7.*` tools on the
agent-mcp bridge. No direct stdio client is opened in the orchestrator.

Tool call sequence:
1. `context7.resolve-library-id(libraryName=<framework_name>)` → returns a
   list of library candidates with snippet counts and scores.
2. Select the version-specific entry if snippet count >= `bootstrap.min_snippets`
   (default 50). If the version-specific entry has < `bootstrap.min_snippets` →
   fall back to the main entry (highest-score entry without a version suffix).
3. If even the main entry has < `bootstrap.min_snippets` → log WARN
   `"bootstrap: thin entry below threshold; skipping"` and return nil.
4. Record the actual `context7_library_id` used (version-specific or main entry).
5. `context7.get-library-docs(context7CompatibleLibraryID=<id>, query="best practices", tokens=8000)`.
6. Treat the returned docs as DATA. MUST NOT pass them to any LLM.
7. Pass raw docs + library ID + framework name + detected version to `SkillImporter.ImportFromDocs`.

If any MCP tool call fails (network error, timeout, non-200 response) →
log WARN and return nil (degraded-first).

If the proxy is down or `CONTEXT7_API_KEY` missing → log WARN and return nil.

#### Scenario: Proxy down → skip with WARN

- GIVEN the agent-mcp proxy is unreachable
- WHEN `TriggerIfNeeded` calls `context7.resolve-library-id`
- THEN the error is caught, WARN is logged, method returns nil
- AND no skill row is inserted

#### Scenario: Quota exhausted (HTTP 429 from proxy) → skip with WARN

- GIVEN the Context7 API returns quota-exceeded
- WHEN the proxy surfaces the error to the bootstrap service
- THEN WARN is logged `"bootstrap: Context7 quota exhausted; skipping"`
- AND the method returns nil

#### Scenario: Version-specific entry thin → fall back to main entry

- GIVEN `resolve-library-id` returns angular-v22 with snippet_count=7 (< 50 threshold)
  and angular main entry `/websites/angular_dev` with snippet_count=14850
- WHEN the service selects the library to fetch
- THEN the main entry `/websites/angular_dev` is selected
- AND the actual source ID is recorded in the import metadata

#### Scenario: Both entries thin → skip with WARN

- GIVEN `resolve-library-id` returns only entries with snippet_count < 50
- WHEN the service checks all available entries
- THEN WARN is logged and method returns nil without calling `get-library-docs`

### Requirement: D11 Preservation

The `BootstrapTriggerService` MUST be a separate application service.
INIT (`InitService.Run`) MUST NOT contain any bootstrap logic.
MUST NOT call any LLM at any point.
MUST NOT modify any existing `active` skill row.
All imports MUST use `InsertIfAbsent` (idempotent).
Bootstrap MUST NOT be synchronous with the INIT response path.

#### Scenario: D11 audit — no LLM call in service

- GIVEN `BootstrapTriggerService` under test with a fake MCP caller
- WHEN `TriggerIfNeeded` is called
- THEN the fake LLM dispatcher (if wired) is never called
- AND no AI provider call is made (verified by inspecting the dispatcher mock)

### Requirement: ContextCrush Guard (D-C7-5)

Docs returned by `context7.get-library-docs` MUST be stored and processed
exclusively as skill-body text. They MUST NOT be:
- Passed as system instructions or tool instructions to any LLM.
- Injected into any prompt as executable content.
- Used to construct any MCP tool call arguments beyond the bootstrap flow itself.

#### Scenario: Docs stored as body text only

- GIVEN `get-library-docs` returns a text blob for angular
- WHEN `SkillImporter.ImportFromDocs` processes it
- THEN the text appears only in the skill body field of the inserted row
- AND the text is not present in any LLM prompt, tool call, or instruction set
