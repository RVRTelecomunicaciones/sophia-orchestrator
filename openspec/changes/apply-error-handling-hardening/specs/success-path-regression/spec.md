# Delta: success-path-regression

## Capability

Negative/regression contract: the apply hot path, domain transition logic, and pg adapter scan paths MUST behave identically on the success path after this change lands. No new log entries, no new audit entries, no new errors, and no behavior differences are introduced by this change on any code path where the original calls returned nil.

## UNCHANGED Requirements

### Requirement: Apply hot path produces identical results on success

All outputs of the apply hot path (persisted state, emitted events, returned envelopes) MUST be identical to pre-change outputs when all domain-transition calls and repo-save calls return nil.

#### Scenario: Full apply run with no errors — behavior is unchanged

- GIVEN an apply run where every domain-transition call (`RecordOutcome`, `group.Fail`, `group.Complete`, `task.Complete`, `MarkRunning`, `task.Release`) returns nil
- AND every repo-save call (`BoardRepo.SaveGroup`, `BoardRepo.SaveTask`, `SessionRepo.Save`, `SpawnGov.Release`) returns nil
- WHEN the apply phase completes
- THEN the persisted board state, session state, and audit log are identical to pre-change behavior
- AND zero ERROR-level log entries are emitted by any Cluster 2 or Cluster 3 site

### Requirement: No new test regressions

`go test ./...` MUST pass green against the codebase after each Cluster's changes are applied. The test suite MUST NOT introduce new failures in packages outside the changed files.

#### Scenario: Full test suite passes after Cluster 2+3 changes

- GIVEN all Cluster 2 and Cluster 3 `_ =` sites have been updated with log + audit + continue
- WHEN `go test ./...` is run from the repo root
- THEN all previously-passing tests continue to pass
- AND the new unit tests covering log + audit behavior pass

#### Scenario: Full test suite passes after Cluster 4 changes

- GIVEN all Cluster 4 `_ =` sites in `board_repo.go`, `session_repo.go`, and `worktree_repo.go` have been updated
- WHEN `go test ./...` is run from the repo root
- THEN all previously-passing tests continue to pass
- AND the new unit tests covering zero-value ID prevention pass

### Requirement: Out-of-scope sites are not touched

The following sites MUST NOT be modified by this change:

- `phase/service.go:1291` (`fallbackToMemory` bug — separate change)
- Any Prometheus metrics instrumentation in `obs/metrics.go`
- Any skill-risk instrumentation for `rollback_count` or `deprecated_api_hits`
- Any public-facing HTTP API handler or domain port interface signature

#### Scenario: Out-of-scope file is unchanged

- GIVEN the set of files changed in this PR
- WHEN the diff is inspected
- THEN `obs/metrics.go` is NOT modified
- AND no public API interface signatures are modified
- AND `phase/service.go:1291` is NOT modified by this change
