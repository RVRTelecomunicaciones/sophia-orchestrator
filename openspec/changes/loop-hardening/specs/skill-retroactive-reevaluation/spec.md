# skill-retroactive-reevaluation Specification

## Purpose

Provide an operator-gated admin command that recomputes skill promotion/demotion using real `apply_attempts` (sourced from `tasks.attempts`) instead of the always-`0.333` proxy. It MUST first run a dry-run that reports which skills would change status — with per-skill metric deltas — and MUST mutate skill status only on explicit operator confirmation in a separate invocation. There is no automatic mass demotion.

## Requirements

### Requirement: Dry-run reports status changes without mutation

The command MUST support a dry-run mode (default) that, for each tracked skill, recomputes `avg_retry_reduction` from historical `tasks.attempts` for the change(s), re-evaluates the promoter (`>= 0.20`) and demoter (`< 0.05`) gates, and reports which skills WOULD change status. Each reported skill MUST include its current status, projected status, and the per-skill metric deltas (old vs. new `avg_retry_reduction` and the `apply_attempts` basis). Dry-run MUST NOT write any skill status change.

#### Scenario: Dry-run lists projected status changes with deltas

- GIVEN skills promoted under the always-`0.333` proxy with real `tasks.attempts` data available
- WHEN the operator runs the command in dry-run mode
- THEN it lists each skill whose status would change, with current status, projected status, and old/new `avg_retry_reduction`
- AND no skill status is mutated in the database

#### Scenario: Dry-run on no-op / empty state reports zero changes

- GIVEN no skill's recomputed metric crosses a promote/demote gate (or there are no tracked skills)
- WHEN the operator runs the dry-run
- THEN it reports zero projected status changes and exits successfully
- AND makes no mutation

### Requirement: Apply mutates status only on explicit confirmation

Status mutation MUST require an explicit apply/confirm invocation distinct from dry-run (separate flag or separate command). When applied, only the skills identified by the recomputed gates MUST have their status changed (promote or demote); all other skills MUST remain untouched. The same command MUST be able to reverse a confirmed change (rollback path) so operators can undo a mass shift.

#### Scenario: Apply mutates only gated skills after confirmation

- GIVEN a dry-run identified a set of skills that would change status
- WHEN the operator runs the explicit apply invocation
- THEN exactly those skills have their status updated per the recomputed gates
- AND no skill outside that set is modified

#### Scenario: Default invocation never mutates

- GIVEN the operator runs the command without the explicit apply flag
- WHEN the command executes
- THEN it behaves as a dry-run and performs no status mutation

### Requirement: Metric recomputed from historical tasks.attempts

The recomputed `avg_retry_reduction` MUST derive `apply_attempts` from `tasks.attempts` (per-change), not from the hardcoded `0`. The recomputation MUST use the same `avg_retry_reduction = (1.5 - apply_attempts) / 1.5` proxy formula already in use, so dry-run and live evaluation agree.

#### Scenario: Recomputed metric matches live GetUsage basis

- GIVEN a change with known `tasks.attempts` values
- WHEN the re-evaluation recomputes `avg_retry_reduction`
- THEN the `apply_attempts` basis equals what enriched `GET /usage` now returns for that change
- AND the resulting metric reflects real data rather than the constant `0.333`
