# Delta: proposal-schema-reconcile

## Capability

The memory-engine `SkillActivationProposal` emitter MUST produce the full V4.1 §9 shape. M2 shipped only 6 fields (skill_id, version, metrics, proposed_by, evidence_changes, threshold_snapshot); §9 adds `skill_name`, `scope`, `applies_when`, and `risk_level`. Closes M2 verify WARNING 2.

## ADDED Requirements

### Requirement: Full V4.1 §9 proposal shape

The memory-engine proposer MUST emit a `SkillActivationProposal` containing all fields mandated by V4.1 §9:

| Field | Source |
|---|---|
| `skill_id` | existing |
| `skill_name` | fetched from orch GetSkill response or skill record |
| `version` | existing |
| `scope` | fetched from GetSkill response |
| `applies_when` | fetched from GetSkill response |
| `risk_level` | fetched from GetSkill response |
| `metrics` | existing (snapshot at emission) |
| `proposed_by` | existing (`"archive_worker"`) |
| `evidence_changes` | existing |

No field from the above list MAY be omitted in any emitted proposal.

#### Scenario: Proposal contains all nine required §9 fields

- GIVEN a validated skill with `usage_count ≥ 5` is processed by the proposer
- WHEN the proposal is constructed and stored
- THEN the persisted record contains `skill_id`, `skill_name`, `version`, `scope`, `applies_when`, `risk_level`, `metrics`, `proposed_by`, and `evidence_changes`
- AND no field is nil or zero-value when the source data is available

#### Scenario: Proposal shape survives re-emit (appends evidence)

- GIVEN a proposal already exists at `governance/skill-proposal/{skill_id}`
- WHEN the proposer processes the same skill again with a new change_id
- THEN the stored record still contains all nine §9 fields
- AND `evidence_changes` contains both the original and new change_id

### Requirement: Proposal storage unchanged

The topic_key, type, and tags for persisting proposals MUST remain unchanged from the existing `skill-activation-proposer` spec: `topic_key = governance/skill-proposal/{skill_id}`, type `semantic`, tags `["governance", "skill_proposal", "pending"]`.

#### Scenario: Storage metadata preserved after reconcile

- GIVEN the proposer emits a full §9 proposal
- WHEN the proposal is stored in memory-engine
- THEN the memory record's topic_key is `governance/skill-proposal/{skill_id}`
- AND tags include `"governance"`, `"skill_proposal"`, and `"pending"`
