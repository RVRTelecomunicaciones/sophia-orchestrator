# Delta: skill-matcher-structural

## Capability

`structuralMatches` in `pg/skill_matcher.go:237-259` gains an optional version
gate that is activated only when `AppliesWhen.FrameworkMinVersion` is set for a
given framework name. When the field is absent (nil or empty map), `structuralMatches`
behavior is identical to the pre-change implementation: name-only, case-insensitive
equality match. This is a MODIFIED capability spec because it changes existing
behavior for the version-gated path while requiring the name-only path to remain
fully backward-compatible.

The complete version gate semantics are defined in the `applies-when-version-semantics`
spec. This spec focuses on the matcher integration boundary: what changes in
`structuralMatches`, what must not change, and the contract with the rest of the
matching pipeline.

## MODIFIED Requirements

### Requirement: Backward Compatibility — Name-Only Path Unchanged

Skills that do NOT have `FrameworkMinVersion` set (all pre-existing seeds and
evidence-backed skills) MUST be matched exactly as before this change. The version
gate is strictly additive; it cannot retroactively filter skills that were
previously returned.

Any skill with `FrameworkMinVersion == nil` or `FrameworkMinVersion == map[]{}` MUST
match using the name-only path, regardless of the detected version in
`StructuralContext`.

MUST NOT change the signature of `structuralMatches`. MUST NOT change what fields
are read from `AppliesWhen.Framework` or `AppliesWhen.Language`.

#### Scenario: Existing seed skill unaffected post-change

- GIVEN the 9 legacy seed skills (activation_source = 'legacy_seed') with no
  `FrameworkMinVersion` set
- AND a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`
- WHEN `structuralMatches` is called for each seed
- THEN the same skills are returned as before this change
- AND no seed is filtered out by the version gate

#### Scenario: Empty FrameworkMinVersion map — name-only still applies

- GIVEN a skill with `AppliesWhen{Framework: ["angular"], FrameworkMinVersion: {}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "18.0.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned (empty map = gate inactive)

### Requirement: Version Gate Activated Only for Explicitly Set Entries

The version gate for framework `X` MUST be active if and only if
`FrameworkMinVersion[X]` exists in the map (after lowercasing the framework name).
If `FrameworkMinVersion["angular"]` is not present but `FrameworkMinVersion["react"]`
is, then angular name-matches in that skill MUST use the name-only path.

#### Scenario: Gate selective per framework

- GIVEN a skill with `AppliesWhen{Framework: ["angular", "react"], FrameworkMinVersion: {"react": "18.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "15.0.0"}, {Name: "react", Version: "18.0.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned (angular matches name-only; react passes version gate)

### Requirement: Failure Modes — Fail Open with WARN

All version parse failures in the matcher MUST result in fail-open behavior:
the skill is returned and a WARN is logged. This prevents a malformed version
string from silently removing a skill from results at match time.

MUST NOT return an error from `structuralMatches`. The function signature returns
a bool (or slice); it MUST continue to return a result, never an error.

#### Scenario: Unparseable detected version in context — fail open

- GIVEN a skill with `FrameworkMinVersion: {"angular": "22.0.0"}`
- AND `StructuralContext.Frameworks[0].Version = "edge"` (not parseable as semver)
- WHEN `structuralMatches` is called
- THEN the skill IS returned
- AND a WARN is logged at the slog level

#### Scenario: Unparseable FrameworkMinVersion value — fail open

- GIVEN a skill with `FrameworkMinVersion: {"angular": "not-a-version"}`
- AND `StructuralContext.Frameworks[0].Version = "22.0.0"`
- WHEN `structuralMatches` is called
- THEN the skill IS returned
- AND a WARN is logged

### Requirement: Hot-Path Performance Constraint

The version gate MUST NOT introduce any external I/O, DB query, or LLM call in
`structuralMatches`. The comparison MUST be in-memory only, operating on the
pre-loaded `AppliesWhen` struct and the `StructuralContext` passed to the matcher.
The gate MUST be O(F) where F = number of framework entries in `FrameworkMinVersion`.

#### Scenario: No I/O in matcher under test

- GIVEN `structuralMatches` is called with a mocked skill and context
- WHEN the test is run with a DB connection replaced by a fake
- THEN no DB call is made during the version comparison
- AND the function completes without any blocking I/O
