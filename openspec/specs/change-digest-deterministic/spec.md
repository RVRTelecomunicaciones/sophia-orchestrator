# Delta: change-digest-deterministic

## Capability

Generates a deterministic YAML `ChangeDigest` document per V4.1 §13.1 from the change envelope and skill_usage rows, then persists it to memory-engine. No LLM is involved.

## ADDED Requirements

### Requirement: Deterministic ChangeDigest structure

The `ChangeDigest` struct MUST contain: `change_id`, `project_id`, `duration_seconds`, `phases` (array sorted ascending by `phase_type` string), `skills_used` (array sorted ascending by `skill_id` string), and `errors_resolved` (array).

Each phase entry MUST include: `phase`, `status`, `attempts`. When `attempts > 1`, the entry MUST include `retry_reasons`.

Each skill entry MUST include: `skill_id`, `outcome`.

YAML serialisation MUST be deterministic — given identical inputs, the output bytes MUST be identical across invocations and across Go processes.

#### Scenario: Digest YAML matches golden fixture

- GIVEN a known change envelope with two phases and one skill
- WHEN the digest generator runs
- THEN the produced YAML exactly matches the pre-approved golden fixture byte-for-byte

#### Scenario: Phases sorted by phase_type ascending

- GIVEN a change with phases in insertion order `[apply, explore, spec]`
- WHEN the digest is generated
- THEN the YAML phases list is ordered `[apply, explore, spec]` (alphabetical)

#### Scenario: Skills sorted by skill_id ascending

- GIVEN a change using skills with IDs `[zzz-id, aaa-id, mmm-id]`
- WHEN the digest is generated
- THEN the YAML skills_used list is ordered `[aaa-id, mmm-id, zzz-id]`

### Requirement: LLM-assisted digest MUST NOT be implemented

The M2 digest generator MUST NOT invoke any LLM client or make any external AI API call. Any code path that would trigger an LLM call within the digest generation is forbidden in M2. The LLM-assisted variant (V4.1 §13.2) is deferred to M3.

#### Scenario: Digest generated without LLM call

- GIVEN a complete change envelope
- WHEN the digest generator runs
- THEN no HTTP call to an LLM provider is made
- AND the digest is produced using only deterministic, in-process computation

### Requirement: Digest persistence

The generated YAML MUST be stored in memory-engine at `topic_key = digest/{change_id}`, with type `semantic` and tags `["change_digest"]`.

#### Scenario: Digest persisted to memory-engine

- GIVEN the digest generator has produced a valid YAML document
- WHEN the persistence step executes
- THEN a memory-engine record exists at `topic_key = digest/{change_id}`
- AND the record type is `semantic`
- AND the record tags include `"change_digest"`
