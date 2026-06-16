# Delta: change-digest-deterministic

## Capability

The ME digest build drops only `Outcome="unknown"` skill entries (GetSkill failed) before semantic-memory persistence. Never-applied skills STAY, since availability is a matcher signal. The golden fixture is updated to reflect the filter.

## MODIFIED Requirements

### Requirement: Deterministic ChangeDigest structure

The `ChangeDigest` struct MUST contain: `change_id`, `project_id`, `duration_seconds`, `phases` (array sorted ascending by `phase_type` string), `skills_used` (array sorted ascending by `skill_id` string), and `errors_resolved` (array).

Each phase entry MUST include: `phase`, `status`, `attempts`. When `attempts > 1`, the entry MUST include `retry_reasons`.

Each skill entry MUST include: `skill_id`, `outcome`. Skill entries whose `outcome` is `"unknown"` (i.e. GetSkill failed) MUST be excluded from `skills_used` before persistence. Skills that were never applied MUST be retained — only `"unknown"` entries are dropped.
(Previously: every skill entry, including `outcome="unknown"`, was written into `skills_used`.)

YAML serialisation MUST be deterministic — given identical inputs, the output bytes MUST be identical across invocations and across Go processes.

#### Scenario: Unknown-outcome entries are dropped

- GIVEN a change whose skill set includes one entry with `outcome="unknown"` and others with known outcomes
- WHEN the digest generator runs
- THEN the `unknown` entry is absent from `skills_used`
- AND all known-outcome entries remain present

#### Scenario: Never-applied skills are retained

- GIVEN a skill that was available but never applied (a non-`unknown`, low-but-real-signal outcome)
- WHEN the digest is generated
- THEN that skill entry remains in `skills_used`

#### Scenario: Digest YAML matches updated golden fixture

- GIVEN a known change envelope including one `unknown`-outcome skill
- WHEN the digest generator runs
- THEN the produced YAML exactly matches the updated golden fixture byte-for-byte
- AND the updated fixture omits the `unknown` entry

#### Scenario: Phases sorted by phase_type ascending

- GIVEN a change with phases in insertion order `[apply, explore, spec]`
- WHEN the digest is generated
- THEN the YAML phases list is ordered `[apply, explore, spec]` (alphabetical)

#### Scenario: Skills sorted by skill_id ascending

- GIVEN a change using skills with IDs `[zzz-id, aaa-id, mmm-id]` (all non-`unknown`)
- WHEN the digest is generated
- THEN the YAML skills_used list is ordered `[aaa-id, mmm-id, zzz-id]`
