# Delta: render-budget-attribution

## Capability

`RenderOpts.TokenBudget` enforces per-layer token budgets per V4.1 §12.2. `RenderOpts.EnableAttribution` emits structured attribution headers per V4.1 §12.3. Both fields have been zero-value no-ops since M0.5; M3 activates them. Zero-value `RenderOpts` behavior is unchanged.

## ADDED Requirements

### Requirement: Per-layer token budget enforcement

When `RenderOpts.TokenBudget > 0`, `Render()` MUST apply the following V4.1 §12.2 cut rules:

| Layer | Cut rule |
|---|---|
| Skills | Sort by relevance, cut at budget allocation |
| Episodes | Top-K by recency |
| ChangeDigests | Top-3 maximum |
| Rules | Cut at budget allocation |

When a layer is truncated, `Render()` MUST append an explicit truncation marker (e.g., `[truncated: N items omitted]`) at the end of that layer's section.

#### Scenario: Budget cuts skills at limit with marker

- GIVEN `RenderOpts.TokenBudget` is set to a value that allows only 1 of 3 skills
- WHEN `Render()` is called
- THEN the output contains exactly 1 skill and a truncation marker

#### Scenario: Budget limits ChangeDigests to top-3

- GIVEN `PriorContext.ChangeDigests` contains 5 digests
- WHEN `Render()` is called with any non-zero `TokenBudget`
- THEN at most 3 digest entries appear in the output
- AND a truncation marker is present if any were omitted

#### Scenario: Zero TokenBudget — no cuts applied

- GIVEN `RenderOpts.TokenBudget = 0` (zero-value)
- WHEN `Render()` is called with 5 skills
- THEN all 5 skills appear in the output (no truncation)

### Requirement: Source attribution headers

When `RenderOpts.EnableAttribution = true`, `Render()` MUST prefix each rendered item with a structured attribution header using the following formats per V4.1 §12.3:

| Layer | Header format |
|---|---|
| Skill | `## Skill: <name> v<version> (<status>, source=<activation_source>)` |
| Episode | `## Episode: <topic_key>` |
| ChangeDigest | `## Digest: <change_id> (<topic_key>)` |
| Rule | `## Rule: <topic_key> (<rule_type>)` |

#### Scenario: Attribution headers present when enabled

- GIVEN `RenderOpts.EnableAttribution = true` and `PriorContext.Skills` has one active skill
- WHEN `Render()` is called
- THEN the output contains a line matching `## Skill: <name> v<version> (active, source=<source>)`

#### Scenario: Attribution headers absent when disabled

- GIVEN `RenderOpts.EnableAttribution = false` (zero-value)
- WHEN `Render()` is called
- THEN no `## Skill:` attribution header appears in the output

### Requirement: Zero-value RenderOpts preserves M0.5 no-op contract

When `RenderOpts` is zero-value (`TokenBudget = 0`, `EnableAttribution = false`), `Render()` MUST behave identically to the M0.5 contract: no budget cuts, no attribution headers.

#### Scenario: Zero-value RenderOpts — output unchanged vs M0.5

- GIVEN `RenderOpts{}` (zero-value)
- WHEN `Render()` is called on a `PriorContext` with skills and episodes
- THEN all items are included and no attribution headers are present
