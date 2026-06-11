# Delta: routines-layer-concrete

## Capability

`RoutineOutput` gains concrete fields `Source` and `Content` (previously an empty struct). `buildPriorContext` populates exactly 2 routines from `StructuralContext.GraphSummary`: `graphify.graph_stats` (all phases) and `graphify.god_nodes` (EXPLORE + APPLY only). `Render()` emits each routine with a source-attribution header. A nil `GraphSummary` produces empty routines (degraded-INIT safe). Zero subprocess calls in this path. The marshal assertion at `prior_context_test.go:148` MUST be updated in the same commit.

## ADDED Requirements

### Requirement: RoutineOutput Concrete Fields

`RoutineOutput` MUST have two exported string fields: `Source` and `Content`. Both fields MUST be serializable to JSON with the keys `"source"` and `"content"`. An empty `RoutineOutput{}` value MUST marshal to `{"source":"","content":""}`.

#### Scenario: RoutineOutput marshals with fields

- GIVEN an empty `RoutineOutput{}`
- WHEN it is marshalled to JSON
- THEN the result is `{"source":"","content":""}`
- AND `prior_context_test.go:148` assertion reflects this new shape (updated in the same commit)

#### Scenario: RoutineOutput with values marshals correctly

- GIVEN `RoutineOutput{Source: "graphify.graph_stats", Content: "Graph: 42 nodes, 100 edges, 5 communities"}`
- WHEN it is marshalled to JSON
- THEN the result contains `"source":"graphify.graph_stats"` and `"content":"Graph: 42 nodes, 100 edges, 5 communities"`

### Requirement: buildPriorContext Populates 2 Routines

`buildPriorContext` MUST populate `PriorContext.Routines` with exactly 2 `RoutineOutput` entries when `StructuralContext.GraphSummary` is non-nil: (1) `graphify.graph_stats` for all phases; (2) `graphify.god_nodes` for EXPLORE and APPLY phases only. No subprocess MUST be spawned to produce these values.

#### Scenario: graph_stats routine emitted on all phases

- GIVEN a non-nil `GraphSummary` with `TotalNodes = 50`, `TotalEdges = 120`, `CommunityCount = 6`
- WHEN `buildPriorContext` is called for any phase (INIT, EXPLORE, DESIGN, APPLY, VERIFY)
- THEN `Routines` contains an entry with `Source = "graphify.graph_stats"` and `Content = "Graph: 50 nodes, 120 edges, 6 communities"`

#### Scenario: god_nodes routine emitted on EXPLORE and APPLY only

- GIVEN a non-nil `GraphSummary` with `GodNodes = ["pkg/core", "pkg/domain"]`
- WHEN `buildPriorContext` is called for EXPLORE phase
- THEN `Routines` contains an entry with `Source = "graphify.god_nodes"` and `Content = "Top blast-radius nodes: pkg/core, pkg/domain"`

#### Scenario: god_nodes routine absent on non-EXPLORE/APPLY phases

- GIVEN a non-nil `GraphSummary` with non-empty `GodNodes`
- WHEN `buildPriorContext` is called for INIT, DESIGN, or VERIFY phase
- THEN `Routines` does NOT contain a `graphify.god_nodes` entry
- AND `Routines` contains exactly 1 entry (`graphify.graph_stats`)

#### Scenario: No subprocess spawned

- GIVEN a non-nil `GraphSummary` already populated in memory
- WHEN `buildPriorContext` is called
- THEN no OS subprocess is spawned and no external process is invoked

### Requirement: Degraded-INIT Safe — Nil GraphSummary

When `StructuralContext.GraphSummary` is nil, `buildPriorContext` MUST produce an empty `Routines` slice (or nil). No panic, no error, no subprocess interaction MUST occur.

#### Scenario: Nil GraphSummary yields empty routines

- GIVEN a `StructuralContext` with a nil `GraphSummary`
- WHEN `buildPriorContext` is called for any phase
- THEN `PriorContext.Routines` is empty (len 0 or nil)
- AND no error is returned and no panic occurs

### Requirement: Render Emits Routines With Attribution

`Render()` MUST emit each `RoutineOutput` in `PriorContext.Routines` with a source-attribution header derived from `Source`. An empty or nil `Routines` slice MUST produce no routine output in `Render()`.

#### Scenario: Routines rendered with attribution header

- GIVEN `Routines` contains 2 populated `RoutineOutput` entries
- WHEN `Render()` is called
- THEN the output includes each routine's `Content` preceded by a header that identifies the `Source`
- AND both entries appear in the rendered output

#### Scenario: Empty routines produce no output

- GIVEN `Routines` is empty (nil GraphSummary path)
- WHEN `Render()` is called
- THEN no routine section appears in the rendered output
- AND no panic occurs
