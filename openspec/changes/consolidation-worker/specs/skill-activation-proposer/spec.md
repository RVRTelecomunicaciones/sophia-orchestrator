# Delta: skill-activation-proposer

## Capability

After metrics are updated for each processed change, checks whether any `validated` skill has reached `usage_count ≥ 5` and emits a `SkillActivationProposal` to memory-engine pending governance storage.

## ADDED Requirements

### Requirement: Proposal threshold check

After each metrics update, the proposer MUST check every skill in `skills_used` whose `status = validated`. If the skill's `usage_count ≥ 5` (Q1 operator-locked value), the proposer MUST emit a `SkillActivationProposal`.

The proposer MUST NOT emit a proposal for skills with `usage_count < 5`.

The proposer MUST NOT emit proposals for skills with status other than `validated`.

#### Scenario: Validated skill at usage_count 5 emits proposal

- GIVEN a skill with `status = validated` and `usage_count = 5`
- WHEN the proposer evaluates the skill after a metrics update
- THEN a `SkillActivationProposal` is emitted and stored in memory-engine

#### Scenario: Validated skill at usage_count 4 does not emit

- GIVEN a skill with `status = validated` and `usage_count = 4`
- WHEN the proposer evaluates the skill
- THEN no proposal is emitted

#### Scenario: Active skill skipped by proposer

- GIVEN a skill with `status = active` and `usage_count = 10`
- WHEN the proposer evaluates the skill
- THEN no proposal is emitted

### Requirement: SkillActivationProposal content

The emitted `SkillActivationProposal` MUST include: `skill_id`, `skill_name`, `version`, `scope`, `applies_when`, `risk_level`, `metrics` (snapshot at emission time), `proposed_by = "archive_worker"`, and `evidence_changes` (array of change_ids that contributed).

#### Scenario: Proposal contains correct proposed_by

- GIVEN a validated skill eligible for proposal
- WHEN the proposal is constructed
- THEN the `proposed_by` field is `"archive_worker"`
- AND the emitting change_id appears in `evidence_changes`

### Requirement: Proposal storage in memory-engine

The proposal MUST be stored in memory-engine at `topic_key = governance/skill-proposal/{skill_id}`, type `semantic`, tags `["governance", "skill_proposal", "pending"]`.

Storage MUST be asynchronous — the worker pipeline MUST NOT block waiting for confirmation.

#### Scenario: Proposal stored at correct topic_key

- GIVEN the proposer has constructed a valid proposal for skill_id `abc-123`
- WHEN the proposal is persisted
- THEN memory-engine contains a record at `topic_key = governance/skill-proposal/abc-123`
- AND the record type is `semantic`
- AND the record tags include `"governance"`, `"skill_proposal"`, `"pending"`

### Requirement: Idempotent re-emission appends evidence

When a proposal for the same skill_id already exists at `governance/skill-proposal/{skill_id}`, the proposer MUST append the new change_id to the existing `evidence_changes` list rather than creating a duplicate record. The `metrics` snapshot MUST be updated to the latest values.

#### Scenario: Repeated emission appends evidence_changes

- GIVEN a proposal already exists at `governance/skill-proposal/{skill_id}` with `evidence_changes = ["change-A"]`
- WHEN the proposer emits for the same skill_id with `change_id = "change-B"`
- THEN the record at `governance/skill-proposal/{skill_id}` has `evidence_changes = ["change-A", "change-B"]`
- AND no duplicate record is created
