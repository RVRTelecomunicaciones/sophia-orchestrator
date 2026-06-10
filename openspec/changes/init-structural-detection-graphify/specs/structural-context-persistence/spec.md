# Delta: structural-context-persistence

## Capability

A `StructuralPersister` port and its concrete adapter at `sophia-orchestator/internal/application/init/persister.go` that writes a `StructuralContext` to two independent sinks: (1) the memory-engine via `POST /api/v1/memories` with `type=semantic` and `topic_key=init/<change_id>`, and (2) a local file at `<repo_root>/.sophia/cache/structural/<cache_key>.json`. Both writes MUST be idempotent. Failure of either sink MUST be logged but MUST NOT abort INIT or prevent the other sink from being attempted.

---

## ADDED Requirements

### Requirement: Memory-Engine Semantic Memory Write

The persister MUST POST to memory-engine `/api/v1/memories` with body fields `type="semantic"`, `topic_key="init/<change_id>"`, `content` as the JSON-serialized `StructuralContext`, `project_id`, and `tags=["init","structural_context"]`. Re-running the persister for the same `change_id` MUST update the existing memory-engine row (idempotent via migration 004 partial unique index on `topic_key` for active rows), not create a duplicate.

#### Scenario: both persistence paths succeed

- GIVEN the memory-engine is reachable and the local filesystem is writable
- WHEN `StructuralPersister.Persist(ctx, context, cacheKey)` is called
- THEN a `POST /api/v1/memories` request is made with `type=semantic` and `topic_key=init/<change_id>`
- AND the local file `<repo_root>/.sophia/cache/structural/<cache_key>.json` is written
- AND no error is returned to the caller

#### Scenario: idempotent re-persist

- GIVEN a `StructuralContext` was already persisted for `change_id="abc"`
- WHEN `StructuralPersister.Persist` is called again for the same `change_id`
- THEN no duplicate memory-engine row is created (upsert semantics via `topic_key`)
- AND the local file is overwritten with the new content

---

### Requirement: Local Cache File Write

The persister MUST write the JSON-serialized `StructuralContext` to `<repo_root>/.sophia/cache/structural/<cache_key>.json`. If the directory does not exist, the persister MUST create it. Overwriting an existing file at the same path MUST succeed silently.

#### Scenario: directory created on first write

- GIVEN `<repo_root>/.sophia/cache/structural/` does not exist
- WHEN `StructuralPersister.Persist` is called
- THEN the directory is created automatically
- AND the file is written successfully

---

### Requirement: Memory-Engine Unavailability Is Non-Fatal

When the memory-engine returns an error or is unreachable, the persister MUST still write the local cache file. The memory-engine failure MUST be logged at WARN level. The persister MUST NOT propagate a fatal error that would abort INIT.

#### Scenario: memory-engine down — local-only persistence

- GIVEN the memory-engine HTTP endpoint returns a 5xx error or connection refused
- WHEN `StructuralPersister.Persist` is called
- THEN the local cache file is written successfully
- AND the failure is logged at WARN level
- AND INIT continues to completion

---

### Requirement: Local Cache Write Failure Is Non-Fatal

When the local filesystem is read-only or the write otherwise fails, the persister MUST still complete the memory-engine write. The local write failure MUST be logged at WARN level. The persister MUST NOT propagate a fatal error that would abort INIT.

#### Scenario: local FS read-only — memory-engine-only persistence

- GIVEN the local filesystem path is not writable
- WHEN `StructuralPersister.Persist` is called
- THEN the memory-engine write completes successfully
- AND the local write failure is logged at WARN level
- AND INIT continues to completion

---

### Requirement: Both-Sink Failure Leaves INIT Running

When both the memory-engine and the local cache write fail simultaneously, INIT MUST still complete. The persister MUST log both failures at WARN level (or ERROR if both fail). INIT completion is blocked by logic errors, not by persistence errors.

#### Scenario: both sinks fail — INIT still completes

- GIVEN both the memory-engine is unreachable and the local filesystem write fails
- WHEN `StructuralPersister.Persist` is called
- THEN both failures are logged
- AND `StructuralPersister.Persist` returns without panicking
- AND the `InitService.Run` caller marks the phase DONE despite the persistence failures
