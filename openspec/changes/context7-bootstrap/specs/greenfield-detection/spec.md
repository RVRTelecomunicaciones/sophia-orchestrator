# Delta: greenfield-detection

## Capability

`Detector.Detect` sets `StructuralContext.Greenfield = true` when the detected
structural context contains no recognized frameworks and no recognized languages.
The field is additive JSON (`omitempty`), so existing persisted INIT outputs
deserialize correctly without a schema version bump. Only `SophiaDetectorVer`
is bumped (`v1.0.0` → `v1.1.0`) to invalidate caches automatically on deploy.
The PROPOSE phase reads `Greenfield` via `renderStructural` and MUST emit
`needs_context` when the field is true and no stack signal is present —
no new server-side code is required for that escalation path (explore §6).

## ADDED Requirements

### Requirement: Greenfield Field on StructuralContext

`StructuralContext` in `internal/domain/structural/context.go` MUST gain a
`Greenfield bool` field serialized as `json:"greenfield,omitempty"`.

The field MUST NOT trigger a `SchemaVersion` bump. Only `SophiaDetectorVer` in
`detector/types.go` MUST be bumped to `"v1.1.0"`.

MUST NOT add `GreenfieldReason` or any other companion field in V1.

#### Scenario: Field present when no stack detected

- GIVEN a repository root that contains no recognized framework manifests and no
  recognized language sources
- WHEN `Detector.Detect` is called
- THEN `StructuralContext.Greenfield == true`
- AND `len(sc.Frameworks) == 0`
- AND `len(sc.Languages) == 0`

#### Scenario: Field absent when any framework detected

- GIVEN a repository root that contains a `package.json` referencing `@angular/core`
- WHEN `Detector.Detect` is called
- THEN `StructuralContext.Greenfield == false` (or field omitted in JSON output)
- AND `len(sc.Frameworks) >= 1`

#### Scenario: Field absent when only language detected (no framework)

- GIVEN a repository root that contains `go.mod` but no recognized framework dependency
- WHEN `Detector.Detect` is called
- THEN `StructuralContext.Greenfield == false`
- AND `len(sc.Languages) >= 1`

#### Scenario: JSON round-trip — false value is omitted

- GIVEN a `StructuralContext` with `Greenfield == false`
- WHEN marshaled to JSON
- THEN the `"greenfield"` key is absent from the output (omitempty)

#### Scenario: JSON round-trip — true value is present

- GIVEN a `StructuralContext` with `Greenfield == true`
- WHEN marshaled to JSON
- THEN the `"greenfield"` key is present with value `true`

#### Scenario: Backward-compatible deserialization

- GIVEN a persisted JSON INIT output that does not contain the `"greenfield"` key
  (i.e., written by a prior detector version)
- WHEN deserialized into `StructuralContext`
- THEN `Greenfield == false` (zero value)
- AND no parse error is returned

### Requirement: SophiaDetectorVer Bump

`SophiaDetectorVer` in `detector/types.go:17` MUST be set to `"v1.1.0"`.

The cache key component that includes `SophiaDetectorVer` (component 7 of the
7-component key at `cache/key.go:32-40`) MUST cause all existing INIT caches
produced by `v1.0.0` to be invalidated automatically on deploy. This is the
ONLY mechanism used for greenfield-related cache invalidation in PR2 (alongside
the manifest hash introduced by the `manifest-hash-cache-invalidation` spec).
This one-time global cache miss is accepted operational behavior.

#### Scenario: Version string updated

- GIVEN the compiled binary
- WHEN `detector.SophiaDetectorVer` is read
- THEN it equals `"v1.1.0"`

#### Scenario: Old cache entries are misses post-deploy

- GIVEN a cached INIT entry produced with `SophiaDetectorVer == "v1.0.0"`
- WHEN the new binary (v1.1.0) constructs the cache key for the same repo state
- THEN the resulting key differs from the stored key
- AND the cache lookup returns a miss
- AND INIT recomputes from scratch

### Requirement: Deterministic Greenfield Detection Rule

`Detector.Detect` MUST set `Greenfield` using the deterministic rule:

```
Greenfield = len(detected_frameworks) == 0 && len(detected_languages) == 0
```

This evaluation MUST occur AFTER all parsers have run. The result MUST be
stored directly on the returned `StructuralContext`; no post-matcher check
is required (the first definition from explore §2 item 2 applies).

MUST NOT query `SkillMatcher` to determine greenfield status. The detector
MUST remain independent of the matcher (single responsibility).

#### Scenario: Detection is purely structural

- GIVEN `Detector.Detect` is instrumented to count matcher calls
- WHEN called on a repository with no recognized stack
- THEN the matcher is NOT called during greenfield determination
- AND `Greenfield == true` is set before any matcher invocation

### Requirement: Async Bootstrap Fire Post-INIT

`runInitPhase` in `phase/service.go` MUST fire `BootstrapTriggerService.TriggerIfNeeded`
as an async goroutine AFTER `InitService.Run` returns — NOT in parallel with it.
The INIT phase response MUST be returned to the caller before the bootstrap
goroutine completes. A bootstrap failure (error, timeout, or insertion failure)
MUST NOT propagate to the INIT result and MUST NOT fail the INIT phase.

The async goroutine MUST use a detached background context (not the request
context) with a configurable timeout. Default timeout: 60s (> 15s Context7
latency). The timeout MUST be configurable via a `bootstrap.timeout` config key.

Bootstrap fires if and only if `sc.Greenfield == true`. When `sc.Greenfield == false`,
no goroutine is spawned and no bootstrap service call is made.

#### Scenario: Bootstrap fires after INIT returns

- GIVEN a greenfield repository
- WHEN `runInitPhase` completes
- THEN the INIT envelope is persisted and the phase response is sent
- AND THEN (after) the bootstrap goroutine starts `BootstrapTriggerService.TriggerIfNeeded`
- AND the phase response is not delayed by bootstrap execution

#### Scenario: Bootstrap failure is swallowed

- GIVEN a greenfield repository
- AND `BootstrapTriggerService.TriggerIfNeeded` is configured to return an error
- WHEN `runInitPhase` completes and the goroutine runs
- THEN the INIT phase result is SUCCESS (unaffected)
- AND the error is logged at WARN level
- AND no panic or goroutine leak occurs (verified by goroutine-leak check)

#### Scenario: Non-greenfield — no bootstrap goroutine

- GIVEN a repository with `sc.Greenfield == false`
- WHEN `runInitPhase` completes
- THEN no call to `BootstrapTriggerService.TriggerIfNeeded` is made
- AND no goroutine is spawned for bootstrap

#### Scenario: Bootstrap timeout does not orphan goroutine

- GIVEN a greenfield repository
- AND the bootstrap goroutine exceeds its configured timeout
- WHEN the timeout fires
- THEN the goroutine exits cleanly via context cancellation
- AND no OS subprocess is left orphaned
