# Delta: applies-when-version-semantics

## Capability

`AppliesWhen` in `internal/domain/skill/lifecycle.go:117-130` gains an optional
`FrameworkMinVersion map[string]string` field mapping lowercased framework names
to semver minimum version strings (e.g., `{"angular": "22.0.0"}`). This field
is additive: skills that do not set it continue to match by framework name only
(backward compatible). When set, the matcher applies a major-version gate in
addition to the existing name gate. The drift detection comparison (detected
major vs. active skill major) is performed in `BootstrapTriggerService`, not in
the matcher, to preserve D11 and keep the hot-path cheap (D-C7-4). The
`BootstrapTriggerService` spec covers the drift trigger; this spec covers the
`AppliesWhen` shape and the matcher gate.

## MODIFIED Requirements

### Requirement: AppliesWhen FrameworkMinVersion Field

`AppliesWhen` struct in `internal/domain/skill/lifecycle.go` MUST gain:

```go
FrameworkMinVersion map[string]string `json:"framework_min_version,omitempty"`
```

Key: lowercased framework name (e.g., `"angular"`, `"go"`, `"django"`).
Value: semver string with optional leading `v` prefix (e.g., `"22.0.0"`, `"v22.0.0"`).
The matcher MUST strip a leading `v` prefix before comparison.

The field MUST be serialized as `json:"framework_min_version,omitempty"`.
If the map is nil or empty, the field MUST be absent from JSON output.

MUST NOT rename or remove `Framework []string` or `Language []string` fields.
Those remain the primary name-match gates and are ALWAYS evaluated before the
version gate.

#### Scenario: Field absent in existing skill — no JSON change

- GIVEN a skill persisted before this change (no `framework_min_version` key)
- WHEN deserialized into `AppliesWhen`
- THEN `FrameworkMinVersion` is nil
- AND no parse error occurs

#### Scenario: Field present — correct map deserialized

- GIVEN a skill with `applies_when = {"framework": ["angular"], "framework_min_version": {"angular": "22.0.0"}}`
- WHEN deserialized into `AppliesWhen`
- THEN `FrameworkMinVersion["angular"] == "22.0.0"`
- AND `Framework == ["angular"]`

### Requirement: Matcher Version Gate (Optional, Additive)

`structuralMatches` in `pg/skill_matcher.go:237-259` MUST apply the
`FrameworkMinVersion` gate as an additional filter ONLY when
`aw.FrameworkMinVersion` is non-nil and non-empty.

When `FrameworkMinVersion` is nil or empty, `structuralMatches` behavior MUST
be identical to the pre-change name-only behavior. Specifically:
- A skill with `Framework: ["angular"]` and no `FrameworkMinVersion` MUST match
  a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`.
- A skill with `Framework: ["angular"]` and no `FrameworkMinVersion` MUST match
  a context with `Frameworks: [{Name: "angular", Version: ""}]` (version not captured).

When `FrameworkMinVersion` IS set for a framework name, the matcher MUST:
1. Parse the detected `FrameworkInfo.Version` as a semver (major.minor.patch).
2. Parse the `FrameworkMinVersion[name]` value as a semver minimum.
3. Pass the version gate if and only if `detected_major >= min_major`.
4. If `detected_major < min_major`, the skill MUST NOT be returned in results.
5. If the detected version string cannot be parsed as semver, the gate MUST
   FAIL OPEN: the skill IS returned (treat missing version as satisfying the gate)
   and a WARN is logged.
6. If the `FrameworkMinVersion` value cannot be parsed, the gate MUST FAIL OPEN
   and a WARN is logged.

The comparison is MAJOR version only: `detected_major >= min_major`.

#### Scenario: Name-only match unchanged when FrameworkMinVersion absent

- GIVEN a skill with `AppliesWhen{Framework: ["angular"]}` (no FrameworkMinVersion)
- AND a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned

#### Scenario: Name-only match unchanged when version empty

- GIVEN a skill with `AppliesWhen{Framework: ["angular"]}` (no FrameworkMinVersion)
- AND a context with `Frameworks: [{Name: "angular", Version: ""}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned

#### Scenario: Version gate passes — detected major equals min major

- GIVEN a skill with `AppliesWhen{Framework: ["angular"], FrameworkMinVersion: {"angular": "22.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned

#### Scenario: Version gate passes — detected major exceeds min major

- GIVEN a skill with `AppliesWhen{Framework: ["angular"], FrameworkMinVersion: {"angular": "22.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "23.1.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned

#### Scenario: Version gate fails — detected major below min major

- GIVEN a skill with `AppliesWhen{Framework: ["angular"], FrameworkMinVersion: {"angular": "22.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "18.2.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill is NOT returned

#### Scenario: Name mismatch — version gate not evaluated

- GIVEN a skill with `AppliesWhen{Framework: ["react"], FrameworkMinVersion: {"react": "18.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`
- WHEN `structuralMatches` is called
- THEN the skill is NOT returned (name gate fails before version gate)

#### Scenario: Unparseable detected version — fail open

- GIVEN a skill with `AppliesWhen{Framework: ["angular"], FrameworkMinVersion: {"angular": "22.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "edge"}]`
- WHEN `structuralMatches` is called
- THEN the skill IS returned (fail open)
- AND a WARN is logged containing the unparseable version string

#### Scenario: FrameworkMinVersion key not present for matched framework

- GIVEN a skill with `AppliesWhen{Framework: ["angular", "react"], FrameworkMinVersion: {"react": "18.0.0"}}`
- AND a context with `Frameworks: [{Name: "angular", Version: "22.0.0"}]`
- WHEN `structuralMatches` is called for the angular name match
- THEN the angular name match passes without a version gate (no entry in the map for "angular")
- AND the skill IS returned

### Requirement: Semver Comparison — Major Only

The version comparison in the matcher MUST operate on the major component only.
Minor and patch components MUST be ignored. A `"v"` prefix on either the detected
version or the configured minimum MUST be stripped before comparison.

#### Scenario: v-prefix stripped correctly

- GIVEN `FrameworkMinVersion: {"angular": "v22.0.0"}`
- AND detected version `"v22.3.1"`
- WHEN the gate is evaluated
- THEN both values are stripped to `22` and the gate passes
