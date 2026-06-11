# Delta: priorcontext-memory-layers

## Capability

`buildPriorContext` decomposes the single `RawMemoryBlob` into typed layers by mapping BuildContext sections: `recent_episodic` → `[]EpisodeRef`, `decisions`+`heuristics` → `[]RuleRef`, `semantic` IncludeTypes → `[]ChangeDigestRef`. `EpisodeRef`, `RuleRef`, `ChangeDigestRef` become real types with content and source metadata. `RawMemoryBlob` is removed from `PriorContext`.

## ADDED Requirements

### Requirement: EpisodeRef is a real type

`EpisodeRef` MUST be a concrete struct containing at minimum: `Content` (string), `TopicKey` (string, source reference), and `OccurredAt` or equivalent temporal metadata.

#### Scenario: EpisodeRef populated from recent_episodic section

- GIVEN BuildContext returns a `recent_episodic` section with one record
- WHEN `buildPriorContext` maps the section
- THEN `PriorContext.Episodes` contains one `EpisodeRef` with non-empty `Content` and `TopicKey`

### Requirement: RuleRef is a real type

`RuleRef` MUST be a concrete struct containing at minimum: `Content` (string), `TopicKey` (string), and `RuleType` (string — "decision" or "heuristic").

#### Scenario: RuleRef populated from decisions section

- GIVEN BuildContext returns a `decisions` section with one record
- WHEN `buildPriorContext` maps the section
- THEN `PriorContext.Rules` contains one `RuleRef` with `RuleType = "decision"`

#### Scenario: RuleRef populated from heuristics section

- GIVEN BuildContext returns a `heuristics` section with one record
- WHEN `buildPriorContext` maps the section
- THEN `PriorContext.Rules` contains one `RuleRef` with `RuleType = "heuristic"`

### Requirement: ChangeDigestRef is a real type

`ChangeDigestRef` MUST be a concrete struct containing at minimum: `Content` (string), `TopicKey` (string), and `ChangeID` (string).

#### Scenario: ChangeDigestRef populated via IncludeTypes semantic

- GIVEN BuildContext is called with `IncludeTypes: ["semantic"]` and returns records tagged as change digests
- WHEN `buildPriorContext` maps the result
- THEN `PriorContext.ChangeDigests` contains the digest records as `ChangeDigestRef`

### Requirement: RawMemoryBlob removed from PriorContext

The `RawMemoryBlob` field MUST NOT exist on `PriorContext` after this change. All callers that read `RawMemoryBlob` MUST be migrated to the typed layer fields.

#### Scenario: Zero references to RawMemoryBlob

- GIVEN the decomposition is complete
- WHEN the repo is searched for `RawMemoryBlob`
- THEN no occurrences are found

### Requirement: Empty sections render nothing

If a BuildContext section returns no records, the corresponding layer MUST remain an empty slice. `Render()` MUST skip empty layers without emitting empty headings or blank sections.

#### Scenario: No episodes — episodes section omitted from output

- GIVEN `PriorContext.Episodes` is empty
- WHEN `Render()` is called
- THEN the rendered output contains no episodes section heading

### Requirement: Digests via dedicated Search call (DG-1)

Change digests MUST be fetched using a dedicated `Memory.Search(SearchQuery{Query:"change digest", Scope:..., Types:["semantic"], Limit:3})` call inside `buildPriorContext`. The `SearchQuery` outbound port MUST NOT be extended with a `Tags` field in this change (the `Types` field is already consumed by ME `retrieval/search.go:67`). The workaround using `BuildContext` with `IncludeTypes` is inert on the ME side and is formally rejected in favor of this direct Search approach.

#### Scenario: Digests populated via dedicated Search with semantic type

- GIVEN `buildPriorContext` executes a `Memory.Search` with `Types:["semantic"]` and `Limit:3`
- WHEN the search returns semantic-typed memory records
- THEN `PriorContext.ChangeDigests` is populated with the results, each mapped to `ChangeDigestRef{ChangeID: record.ID, Content: record.Snippet}`
