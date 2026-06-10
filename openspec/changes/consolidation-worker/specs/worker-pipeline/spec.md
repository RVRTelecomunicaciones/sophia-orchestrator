# Delta: worker-pipeline

## Capability

Replaces the PRE-0 stub `Handler.Handle` with the real end-to-end consolidation pipeline: idempotency check → fetch skill_usage rows → compute metrics deltas → patch orch metrics → run promoter → run demoter → emit proposals → generate and persist digest.

## ADDED Requirements

### Requirement: Pipeline step ordering

`Handler.Handle` MUST execute steps in the following strict order:
1. Idempotency check (see `worker-idempotency` spec — abort if digest already exists)
2. Fetch `skill_usage` rows for the change_id from orch `GET /api/v1/skills/usage?change_id={id}`
3. Compute per-skill metrics deltas from the fetched rows
4. For each skill: call `SkillsClient.PatchMetrics` with the computed delta
5. Run promoter against all skills in `skills_used`
6. Run demoter against all skills in `skills_used`
7. Run proposer against all skills in `skills_used`
8. Generate deterministic `ChangeDigest` YAML
9. Persist digest at `digest/{change_id}` in memory-engine

The pipeline MUST NOT call any LLM API in M2. Any LLM client import in any PR2 consolidation code path is forbidden.

#### Scenario: Full pipeline happy path — 1 skill, 1 change

- GIVEN memory-engine receives a valid `PhaseArchivedReceived` for change_id `C1` with one skill `S1`
- AND no digest exists for `C1`
- WHEN `Handler.Handle` runs
- THEN `GET /api/v1/skills/usage?change_id=C1` is called
- AND `PatchMetrics` is called for skill `S1`
- AND promoter and demoter evaluate `S1`
- AND proposer evaluates `S1`
- AND a `ChangeDigest` is persisted at `digest/C1`

### Requirement: Per-skill error isolation

A `PatchMetrics` failure for one skill MUST NOT abort processing for subsequent skills. The pipeline MUST log the error with the failing skill_id and continue to the next skill.

Similarly, a promoter, demoter, or proposer error for one skill MUST NOT prevent other skills or the digest step from completing.

#### Scenario: PatchMetrics fails for skill A, pipeline continues for skill B

- GIVEN a change has two skills: `A` and `B`
- AND orch returns HTTP 500 for `PatchMetrics` on skill `A`
- WHEN `Handler.Handle` processes the change
- THEN the error for skill `A` is logged
- AND `PatchMetrics` is still called for skill `B`
- AND the pipeline continues to the digest step

### Requirement: No LLM calls in any pipeline code path

No file under `internal/application/consolidation/` in sophia-memory-engine MUST import or invoke an LLM client package in M2. This prohibition covers direct imports, injected clients, and any wrapper that ultimately calls an LLM HTTP API.

#### Scenario: Static analysis confirms no LLM imports in consolidation

- GIVEN the PR2 codebase at merge-ready state
- WHEN a static dependency scan is run on `internal/application/consolidation/`
- THEN no LLM provider SDK or HTTP client targeting an LLM endpoint is found

### Requirement: Pipeline exception safety

The pipeline MUST recover from panics within individual step goroutines or closures using `recover()`. A panic in one skill's processing step MUST be logged and treated as a per-skill error without crashing the worker process.

#### Scenario: Panic in one skill step is recovered

- GIVEN a bug in promoter logic causes a panic for skill `X`
- WHEN the pipeline is running
- THEN the panic is recovered, an error is logged for skill `X`
- AND the pipeline continues for remaining skills and the digest step
- AND the worker process does not exit
