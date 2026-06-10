# Delta: priorcontext-struct

## Capability

Introduce a canonical `PriorContext` struct in the `internal/application/discipline` package with a `Render(opts RenderOpts) string` method and 8 forward-compatibility stub types. The struct provides the single authoritative assembly point for prior-context content; `RenderOpts` zero-value MUST be a strict no-op so no hook activates silently. All 8 stub types are declared for M3 enrichment but carry no behaviour in M0.5.

References: proposal §Scope / §Capabilities.New `priorcontext-struct`; explore.md §§1,3,4,5.

---

## ADDED Requirements

### Requirement: PriorContext struct declaration

The system MUST declare `PriorContext` in `internal/application/discipline/prior_context.go` with the following exported fields in declaration order:

| Field | Type | Notes |
|---|---|---|
| `PhaseIdentity` | `string` | Phase or change name |
| `Skills` | `[]RenderedSkill` | Forward-compat stub; empty in M0.5 |
| `StructuralCtx` | `*StructuralContextRef` | Option D: nil-only opaque marker in M0.5 |
| `Episodes` | `[]EpisodeRef` | Forward-compat stub; empty in M0.5 |
| `ChangeDigests` | `[]ChangeDigestRef` | Forward-compat stub; empty in M0.5 |
| `BusinessRules` | `[]RuleRef` | Forward-compat stub; empty in M0.5 |
| `Routines` | `[]RoutineOutput` | Forward-compat stub; empty in M0.5 |
| `AuxiliaryMemory` | `*AuxiliaryBlock` | Forward-compat stub; nil in M0.5 |
| `RawMemoryBlob` | `string` | M0.5-interim — M3 decomposes into Episodes/ChangeDigests/BusinessRules |

The `RawMemoryBlob` field godoc MUST explicitly state: _"M0.5-interim — M3 decomposes into Episodes/ChangeDigests/BusinessRules"_.

#### Scenario: Struct is importable from sibling packages

- GIVEN the `discipline` package is compiled
- WHEN a sibling package imports `discipline`
- THEN all 9 fields and all 8 stub types resolve without compilation error

#### Scenario: All stub types are exported and zero-value constructible

- GIVEN the `discipline` package is compiled
- WHEN caller code assigns `discipline.RenderedSkill{}`, `discipline.StructuralContextRef{}`, `discipline.EpisodeRef{}`, `discipline.ChangeDigestRef{}`, `discipline.RuleRef{}`, `discipline.RoutineOutput{}`, `discipline.AuxiliaryBlock{}`, `discipline.RenderOpts{}`
- THEN each assignment compiles without error and produces a valid zero value

---

### Requirement: Render method with no-op zero-value guarantee

The system MUST define `func (pc PriorContext) Render(opts RenderOpts) string` on `PriorContext`.

`Render` MUST return a string whose content is controlled exclusively by the populated fields of `pc`. When called with `RenderOpts{}` (zero value), the method MUST NOT activate any token-budget hook, source-attribution hook, or any other opt-in transformation — the output MUST equal what a direct inline concatenation of the same raw field content would produce.

`PriorContext`, `Render`, and `RenderOpts` MUST each carry a godoc comment.

#### Scenario: Empty struct renders empty string

- GIVEN a `PriorContext` with all fields at zero value
- WHEN `Render(RenderOpts{})` is called
- THEN the return value is `""`

#### Scenario: RawMemoryBlob-only renders the blob unchanged

- GIVEN a `PriorContext` where only `RawMemoryBlob` is set to a non-empty string S
- WHEN `Render(RenderOpts{})` is called
- THEN the return value equals S byte-for-byte

#### Scenario: RenderOpts zero-value is a strict no-op

- GIVEN a control string C produced by direct string concatenation of the same raw field values
- WHEN `Render(RenderOpts{})` is called on a `PriorContext` populated with those same values
- THEN the return value equals C byte-for-byte
- AND no additional headers, tokens counts, or source lines appear in the output

#### Scenario: StructuralCtx nil-only in M0.5 renders nothing

- GIVEN a `PriorContext` where `StructuralCtx` is `nil`
- WHEN `Render(RenderOpts{})` is called
- THEN no output segment is emitted for `StructuralCtx`

#### Scenario: Skills empty slice in M0.5 renders nothing

- GIVEN a `PriorContext` where `Skills` is an empty slice
- WHEN `Render(RenderOpts{})` is called
- THEN no output segment is emitted for `Skills`

---

### Requirement: Godoc coverage

The system MUST include godoc comments on `PriorContext`, `Render`, and `RenderOpts`. The `RawMemoryBlob` field comment MUST include the M0.5-interim annotation and the M3 decomposition path.

#### Scenario: Godoc is present and linter-clean

- GIVEN the `discipline` package is linted with `golangci-lint run`
- WHEN the linter checks documentation requirements
- THEN no missing-doc warnings are emitted for `PriorContext`, `Render`, or `RenderOpts`

---

### Requirement: TDD — failing test first

The system MUST follow strict TDD: the test file `prior_context_test.go` MUST exist and reference `PriorContext` and `Render` BEFORE the production file `prior_context.go` is created, causing a compilation failure (red). The production file is introduced to make tests pass (green).

#### Scenario: Red phase — tests reference undefined symbol

- GIVEN `prior_context_test.go` exists and calls `discipline.PriorContext{}.Render(discipline.RenderOpts{})`
- WHEN `go build ./internal/application/discipline/...` is run without `prior_context.go`
- THEN the build fails with an undefined symbol error

#### Scenario: Green phase — production code satisfies tests

- GIVEN `prior_context.go` is created with the struct, stub types, and `Render` method
- WHEN `go test ./internal/application/discipline/...` is run
- THEN all tests pass
