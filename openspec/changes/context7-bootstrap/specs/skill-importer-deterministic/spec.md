# Delta: skill-importer-deterministic

## Capability

`SkillImporter` is a new application-layer component in
`internal/application/bootstrap/importer.go` that transforms raw Context7 doc
text into a Sophia skill row and inserts it via `SkillRepo.InsertIfAbsent`.
The transformation is deterministic template assembly: typed doc snippets
(best-practices, setup, control-flow sections) are slotted into fixed skill-body
sections. No LLM call is made at any point (D-C7-2). The output skill has
`status=candidate`, `activation_source=imported`, and name `stack/<framework>-<major>`
(D-C7-3). `InsertIfAbsent` makes the operation idempotent — a second call for
the same `(name, version)` pair is a no-op. The old active version row is NEVER
touched.

## ADDED Requirements

### Requirement: Deterministic Template Assembly (No LLM)

`SkillImporter.ImportFromDocs(ctx, framework, detectedVersion, docs string, sourceLibraryID string)`
MUST assemble the skill body by applying a fixed template:

```
# {framework} best practices (imported from Context7)
Source: {sourceLibraryID}
Detected version: {detectedVersion}

## Setup
{setup_section}

## Best Practices
{best_practices_section}

## Control Flow
{control_flow_section}
```

Where `{setup_section}`, `{best_practices_section}`, and `{control_flow_section}`
are extracted from `docs` using deterministic section-matching rules (e.g.,
searching for heading keywords). If a section cannot be found in `docs`, the
corresponding slot MUST use the full `docs` content (no section missing from the
output). The section extraction logic MUST be pure functions with no external calls.

MUST NOT call any LLM provider. MUST NOT call any MCP tool. The importer operates
purely on the `docs` string passed in.

#### Scenario: No LLM call — verified by isolation

- GIVEN `SkillImporter` is constructed with a fake `SkillRepo` and no LLM dependency
- WHEN `ImportFromDocs` is called with valid docs
- THEN the skill is inserted via `SkillRepo.InsertIfAbsent`
- AND the LLM dispatcher (if present in the DI graph) is never called
  (verified by a no-call spy or absence of the dependency in the constructor)

#### Scenario: Structured docs produce multi-section body

- GIVEN docs containing recognizable heading patterns for setup, best practices,
  and control flow
- WHEN `ImportFromDocs` is called
- THEN the assembled skill body contains all three sections with content from the docs

#### Scenario: Unstructured docs — full content in body

- GIVEN docs with no recognized section headings
- WHEN `ImportFromDocs` is called
- THEN the assembled skill body contains the full raw docs content
- AND no section is empty or omitted

### Requirement: Skill Name and Version — D-C7-3 Convention

The imported skill name MUST follow the pattern `stack/<framework>-<major>` where:
- `<framework>` is the framework name lowercased.
- `<major>` is the parsed major version from `detectedVersion` (e.g., `"22.0.0"` → `"22"`).

The skill `version` field MUST be set to `"v1"` for all imported skills (the
version column tracks the skill body version, not the framework version; the
framework version is encoded in the name).

The `stack/` prefix namespaces imported skills away from evidence-backed and seed
skills. MUST NOT use any other prefix for imported skills.

#### Scenario: Name derived correctly from framework and version

- GIVEN framework `"angular"` and detectedVersion `"22.0.0"`
- WHEN `ImportFromDocs` builds the skill name
- THEN `skill.Name == "stack/angular-22"`

#### Scenario: Name derived for go framework

- GIVEN framework `"go"` (from LanguageInfo) and detectedVersion `"1.26"`
- WHEN `ImportFromDocs` builds the skill name
- THEN `skill.Name == "stack/go-1"`

#### Scenario: Framework name lowercased

- GIVEN framework `"Angular"` (capitalized from detector)
- WHEN `ImportFromDocs` builds the skill name
- THEN `skill.Name == "stack/angular-22"` (lowercased)

#### Scenario: Drift import produces new name

- GIVEN an existing row `stack/angular-22` (active)
- AND `ImportFromDocs` called with framework `"angular"` version `"23.0.0"`
- WHEN `InsertIfAbsent` is called
- THEN a new row `stack/angular-23` is inserted
- AND the `stack/angular-22` row is unchanged

### Requirement: Lifecycle Fields on Import

The skill constructed by the importer MUST have:
- `status = StatusCandidate` (`"candidate"`)
- `activation_source = SourceImported` (`"imported"`)
- `risk_level = "medium"` (default; no override in V1)

These values MUST match the existing DB CHECK constraints from migration
`010_skills_lifecycle.up.sql:30` and the V4.1 §5.2 enum definitions.

MUST NOT set `status = "active"` on import.
MUST NOT set `activation_source` to any value other than `"imported"` for
Context7-sourced skills.

#### Scenario: Candidate lifecycle on insertion

- GIVEN `ImportFromDocs` is called with valid args
- WHEN the skill is persisted
- THEN the row has `status = 'candidate'` and `activation_source = 'imported'`
- AND it is not returned by `SkillMatcher` for `status = 'active'` queries

### Requirement: Idempotent InsertIfAbsent

`ImportFromDocs` MUST call `SkillRepo.InsertIfAbsent` (not `Upsert`). A second
call with the same `(name, version)` pair MUST be a no-op at the DB layer
(relies on `UNIQUE(name, version)` from migration `010_skills_lifecycle.up.sql:37`).

`InsertIfAbsent` returning a "row already exists" signal MUST NOT be treated as
an error by the importer. The importer MUST log DEBUG and return nil.

#### Scenario: Idempotent second import

- GIVEN `stack/angular-22` already exists as a candidate row
- WHEN `ImportFromDocs` is called a second time with the same framework and version
- THEN `InsertIfAbsent` is called and returns "already exists"
- AND the importer returns nil (no error)
- AND the existing row is unchanged

#### Scenario: Concurrent imports for same (name, version) — one succeeds

- GIVEN two goroutines calling `ImportFromDocs` concurrently for the same angular v22
- WHEN both call `InsertIfAbsent`
- THEN exactly one row is inserted (DB UNIQUE constraint)
- AND the losing goroutine receives "already exists" and returns nil
- AND no panic or deadlock occurs

### Requirement: Provenance Metadata in Skill Body

The assembled skill body MUST include a provenance header identifying:
- The source library ID used (version-specific or main entry — records which was
  actually used per D-C7-6 thin-entry fallback).
- The detected framework version at import time.
- A note if the main entry was used as fallback instead of a version-specific entry.

This provenance is embedded as plain text in the skill body. No separate metadata
column is added in V1.

#### Scenario: Fallback provenance recorded

- GIVEN the main entry was used as fallback (version-specific too thin)
- WHEN `ImportFromDocs` assembles the skill body
- THEN the body header includes text indicating the main entry ID was used as
  fallback and the original version-specific entry was below threshold
