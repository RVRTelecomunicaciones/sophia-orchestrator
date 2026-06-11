# Design: priorcontext-enrichment (M3)

**Strategy ref:** V4.1 §12 (enrichment layers + token budget + attribution), §16 milestone M3 — FINAL milestone of the learning-loop arc.
**Proposal:** `openspec/changes/priorcontext-enrichment/proposal.md` + engram `sdd/priorcontext-enrichment/proposal`.
**Exploration:** `openspec/changes/priorcontext-enrichment/explore.md`.
**Predecessor design:** `openspec/changes/priorcontext-struct-refactor/design.md` (M0.5 — stub types, Render-at-boundary, byte-exact goldens).
**Artifact store:** hybrid. **Strict TDD:** true. **No code changes in this phase — design only.**

---

## 0. Verification corrections (read FIRST — code anchors checked against main)

Designing against the proposal surfaced THREE discrepancies between the proposal text and the verified code. The design resolves each explicitly so `sdd-tasks` does not inherit a false premise.

### C-1 — There are only TWO `*StructuralContextRef` nil-markers, not three

Proposal §PR2 and explore §2 item 2 say "3 consumers / replace the 3 `*StructuralContextRef` nil-markers". Verified on main:

- `internal/application/discipline/prior_context.go:25` — `StructuralCtx *StructuralContextRef` ✅ (nil-marker)
- `internal/application/discipline/skill_matcher.go:50` — `SkillQuery.StructuralContext *StructuralContextRef` ✅ (nil-marker)
- worker `last_stack_version` is **`skill.Metrics.LastStackVersion *string`** (`internal/domain/skill/lifecycle.go:134`) — it is ALREADY a `*string`, NOT a `*StructuralContextRef`. It has its own JSON tag, round-trip test, and PG integration test.

**Decision (D-M3-3):** PR2 replaces exactly the TWO `*StructuralContextRef` markers. `Metrics.LastStackVersion` stays `*string` and is OUT of scope for the structural move — it is a captured version string, not a structural-context reference. The proposal's "3rd nil-marker" is a miscount; the third "consumer" is conceptual (the worker reads a stack version string), not a `StructuralContextRef` field. This shrinks PR2 blast radius and removes a phantom edit.

### C-2 — `AppliesWhen` has NO Framework/Language fields yet

Proposal §PR2 and acceptance criterion 1 assume the matcher can read `applies_when.framework` / `applies_when.language`. Verified: `skill.AppliesWhen` (`lifecycle.go:117-121`) carries only `FeatureType` / `TouchedPaths` / `ExcludePaths`. Its godoc literally says *"M3 adds Framework + StateModel."* Those fields **do not exist on main.**

**Decision (D-M3-4):** PR2 MUST first ADD `Framework []string` and `Language []string` to `skill.AppliesWhen` (domain change + JSONB persistence round-trip), THEN activate the matcher filter against them. This is a prerequisite sub-task, not an afterthought. See D-M3-4 for the field shape and migration note.

### C-3 — The `IncludeTypes: ["semantic"]` digest workaround does NOT work as proposed

This is the most consequential finding. The proposal (§Approach, §Risks row 1) claims change digests ride into PriorContext via `BuildContext` with `IncludeTypes: ["semantic"]`. Verified end-to-end:

1. orch client DOES send `include_types` over the wire (`internal/adapters/outbound/memory/client.go:136,325`).
2. ME inbound `ContextRequest` DOES declare `IncludeTypes []string` (`retrieval_service.go:60`).
3. **BUT** ME `ContextBuilder.BuildContext` (`context_builder.go:58-177`) NEVER reads `req.IncludeTypes`. The field is plumbed and inert on the ME side.
4. The bundle has exactly four section types — `decisions`, `heuristics`, `recent_episodic`, `related` (`context_builder.go:231,268,307,407`). There is no `semantic` section.
5. `recent_episodic` is produced ONLY by `fetchMemories` (FTS), which runs **only when `req.Query` is non-empty** (`context_builder.go:91-93`). Change digests are `Type: "semantic"`, `Tags: ["change_digest"]` (`consolidation/handler.go:251-253`) — they live in the memories table and surface only through FTS.
6. The current orch `buildPriorContext` (`phase/service.go:1015-1048`) passes **NO Query** — so even episodics never surface today; the bundle is decisions + heuristics + (graph-related) only.

**Decision (D-M3-6, digest path):** The `IncludeTypes` workaround is **rejected as written** — it is a no-op against the live ME. Two honest options:

- **Option DG-1 (chosen): single dedicated `Search` call for digests.** Add ONE `Search(SearchQuery{Query:"change digest", Scope:..., Types:["semantic"], Limit:3})` call inside `buildPriorContext`, filter results whose record carries the `change_digest` signal, map to `[]ChangeDigestRef`. `SearchQuery.Types` IS consumed by ME search (`retrieval/search.go:67`). No ME change, no outbound-port change. Honours the proposal's "no ME enrichment API change" intent.
- **Option DG-2 (rejected): make BuildContext honour IncludeTypes on the ME side.** Requires an ME code change (read `req.IncludeTypes`, add a `semantic`/`change_digest` section). Rejected: pushes scope into ME beyond PR1's proposer reconcile; widens the cross-repo surface the proposal explicitly minimised.

DG-1 keeps digests reachable with one extra orch-side round-trip and zero ME-retrieval changes. The p95 budget (D-M3-7) absorbs it (1 BuildContext + 1 GetByTopicKey for StructuralContext + 1 Search for digests ≈ 3 round-trips < 200ms).

**Decision (D-M3-6, episodic path):** Episodes map from the bundle's `recent_episodic` section, which today is empty because `buildPriorContext` sends no Query. PR3 MUST set `ContextRequest.Query` to a non-empty retrieval seed (the change name + phase, e.g. `c.Name()`), so `recent_episodic` actually populates. Without a Query the Episodes layer is permanently empty and acceptance criterion (episodes top-K) is untestable against real data. This is a one-line addition to the existing BuildContext call, not a new round-trip.

---

## 1. Architecture approach

**Pattern:** Hexagonal, preserved. All three PRs respect the existing port boundaries:

- **PR1** adds an inbound HTTP adapter method (GetSkill handler) + an inbound port method (`SkillService.GetSkill`) backed by the existing `SkillRepo.FindByID` outbound adapter. ME side adds fields to an application-layer DTO only.
- **PR2** introduces a new pure domain package `internal/domain/structural` and rewires two application-layer fields plus the matcher adapter. No new ports.
- **PR3** enriches the `discipline.PriorContext` value object and its `Render` method (pure, deterministic, no I/O), and changes two application-layer callsites (`phase/service.go`, `apply/run.go` + `teamlead.go`) to hydrate the typed layers. The deprecated `pg.SkillProvider` adapter and its `discipline.SkillProvider` port are deleted.

**Layering invariant honoured (CLAUDE.md):** `Render` stays deterministic — no `time.Now()`, no `ulid.Make()`, no env. All inputs arrive in the struct. The matcher stays in the adapter (`pg`), the value object stays in `discipline`, the structural type moves to `domain`.

**Boundaries:** orch ↔ ME crosses only HTTP. PR1 adds `GET /skills/{id}` (orch serves, ME consumes via existing `SkillsClient.GetSkill`). PR3 adds one orch→ME `Search` round-trip (DG-1) and one Query param on the existing BuildContext call. No new ME endpoints.

**Data-flow direction (PR3 enrichment):**

```
phase/service.go:buildPriorContext
   ├─ Memory.BuildContext(Query=c.Name(), Scope) ─→ sections{decisions,heuristics,recent_episodic,related}
   ├─ Memory.GetByTopicKey(structural topic)      ─→ *structural.StructuralContext   (D-M3-3)
   ├─ Memory.Search(Types=[semantic], q=digest)   ─→ []ChangeDigestRef               (DG-1)
   └─ SkillMatcher.SkillsForContext(SkillQuery{   ─→ []*skill.Skill ─→ map ─→ []RenderedSkill
        Phase, ProjectID, StructuralContext})
                    │
                    ▼
   PriorContext{Skills, StructuralCtx, Episodes, ChangeDigests, BusinessRules, RawMemoryBlob=""}
                    │  .Render(RenderOpts{TokenBudget: N, EnableAttribution: true})
                    ▼
   PromptInput.PriorContext (string)  ──→ Build (NO sibling # Skill section) ──→ dispatcher
```

---

## 2. Architecture Decisions (ADR-style)

### D-M3-1 — GetSkill endpoint (PR1)

**Choice.** Add `GET /api/v1/skills/{skill_id}` serving a JSON body byte-compatible with the ME worker's `SkillSnapshot` (`sophia-memory-engine/internal/ports/outbound/skills_client.go:36-42`).

**Handler.** New method on the existing `SkillsHandler` (`handlers/skills.go`), following the `GetUsage` pattern exactly:

```go
// getSkillResp is the JSON shape for GET /api/v1/skills/{skill_id}.
// Field names + nesting mirror the ME worker's SkillSnapshot VERBATIM.
type getSkillResp struct {
    SkillID   string          `json:"skill_id"`
    Status    string          `json:"status"`
    RiskLevel string          `json:"risk_level"`
    Version   string          `json:"version"`
    Metrics   getSkillMetrics `json:"metrics"`
    // Additive richer fields (see D-M3-2) — unknown to the worker's
    // narrow SkillSnapshot, ignored by Go's json default. NOT consumed
    // by the worker; consumed by the ME proposer reconcile.
    Name        string              `json:"skill_name,omitempty"`
    Scope       getSkillScope       `json:"scope,omitempty"`
    AppliesWhen getSkillAppliesWhen `json:"applies_when,omitempty"`
}

type getSkillMetrics struct {
    UsageCount        int     `json:"usage_count"`
    SuccessCount      int     `json:"success_count"`
    FailureCount      int     `json:"failure_count"`
    TestsPassedCount  int     `json:"tests_passed_count"`
    DeprecatedAPIHits int     `json:"deprecated_api_hits"`
    RollbackCount     int     `json:"rollback_count"`
    AvgRetryReduction float64 `json:"avg_retry_reduction"`
}

func (h *SkillsHandler) GetSkill(w http.ResponseWriter, r *http.Request) {
    skillID := chi.URLParam(r, "skill_id")
    sk, err := h.svc.GetSkill(r.Context(), skillID)
    if err != nil {
        if errors.Is(err, outbound.ErrNotFound) {
            h.writeJSON(w, http.StatusNotFound, map[string]string{
                "error": "skill not found", "code": "skill_not_found"})
            return
        }
        h.writeErr(w, err)
        return
    }
    h.writeJSON(w, http.StatusOK, toGetSkillResp(sk))
}
```

`toGetSkillResp(*skill.Skill) getSkillResp` maps domain getters (`Status()`, `RiskLevel()`, `Version()`, `Metrics()`, `Name()`, `Scope()`, `AppliesWhen()`) into the wire shape. `risk_level` / `status` are stringified domain enums.

**Service.** Add to the `SkillService` interface (`ports/inbound/skill.go`):

```go
GetSkill(ctx context.Context, skillID string) (*skill.Skill, error)
```

Impl in `internal/application/skill/service.go` parses `skillID` via `ids.ParseSkillID`, delegates to `SkillRepo.FindByID` (already exists, `skill_repo.go:268-286`), returns `outbound.ErrNotFound` verbatim on miss / parse failure.

**Route.** Add inside the existing `r.Route("/{skill_id}", …)` block (`router.go:143-146`):

```go
r.Get("/", skillH.GetSkill)
```

**Rationale.** Closes M2 verify WARNING 1 — the live promote/demote path calls this exact endpoint. `SkillRepo.FindByID` exists; only handler+service+route are new (~50 LoC). Registered only when `d.Skills != nil` (skills feature flag), same gate as the sibling routes.

**Rejected.** Serving a fresh struct instead of reusing `SkillsHandler` — rejected: the writeErr/writeJSON closures are already injected into `SkillsHandler`; a new handler duplicates wiring.

---

### D-M3-2 — Proposal reconcile + GetSkill richness source (PR1)

**Problem.** M2 WARNING 2: ME `SkillActivationProposal` (`consolidation/proposer.go:16-23`) emits 6 fields (`skill_id, version, proposed_by, proposed_at, evidence_changes, metrics_snapshot`); V4.1 §9 requires `skill_name`, `scope`, `applies_when`, `risk_level` too. The proposer's only orch data source is `SkillSnapshot`, which is narrow (no name/scope/applies_when).

**Where does the proposer get the new fields?** `SkillSnapshot` already carries `risk_level`. It does NOT carry name/scope/applies_when. Two routes evaluated:

- **PR-A (chosen): extend `GET /skills/{id}` response with additive `skill_name`/`scope`/`applies_when` fields** (D-M3-1 already shows them). The ME `SkillSnapshot` struct stays unchanged — Go's `encoding/json` ignores unknown fields by default, so the worker's existing `GetSkill` deserialization is unaffected (verified: `SkillSnapshot` has no `DisallowUnknownFields`; standard decoder). The proposer then needs to read the richer fields, which means the ME proposer must unmarshal into a WIDER local struct than `SkillSnapshot`.
- **PR-B (rejected): proposer fetches scope/applies_when from a second orch source.** Rejected: no such endpoint exists; would require a new orch surface beyond GetSkill.

**ME reconcile shape.** The proposer gains a richer snapshot type for its own use (NOT replacing the narrow `SkillSnapshot` that the worker's status-transition path uses):

```go
// proposerSkillView is the WIDER read used only by the proposer. It is the
// SkillSnapshot fields PLUS the additive richness GET /skills/{id} now serves.
type proposerSkillView struct {
    outbound.SkillSnapshot                    // embeds skill_id/status/risk_level/version/metrics
    SkillName   string              `json:"skill_name"`
    Scope       map[string]any      `json:"scope"`
    AppliesWhen map[string]any      `json:"applies_when"`
}

type SkillActivationProposal struct {
    SkillID         string                `yaml:"skill_id"`
    SkillName       string                `yaml:"skill_name"`   // NEW (V4.1 §9)
    Scope           map[string]any        `yaml:"scope"`        // NEW
    AppliesWhen     map[string]any        `yaml:"applies_when"` // NEW
    RiskLevel       string                `yaml:"risk_level"`   // NEW
    Version         string                `yaml:"version"`
    ProposedBy      string                `yaml:"proposed_by"`
    ProposedAt      time.Time             `yaml:"proposed_at"`
    EvidenceChanges []string              `yaml:"evidence_changes"`
    Metrics         outbound.SkillMetrics `yaml:"metrics_snapshot"`
}
```

`RiskLevel` comes from the existing `SkillSnapshot.RiskLevel`; the other three from the additive GetSkill fields. **Whether the ME `SkillsClient.GetSkill` is widened to fetch the richer view, or the proposer issues its own read, is a PR1 tasks decision** — design recommendation: widen the ME proposer's own fetch path (it already reads orch via a client) rather than touch the worker's narrow `GetSkill`. Trivial glue (~30 LoC ME side).

**Safety verification (additive JSON fields).** Confirmed safe: Go's default `json.Unmarshal` silently drops unknown keys. The worker's `SkillSnapshot` decode of the GetSkill response will ignore `skill_name`/`scope`/`applies_when` — no break. PR1's contract test (below) asserts this explicitly.

**Contract test (PR1, orch side).** Marshal `getSkillResp` from a fixture skill, then unmarshal the bytes into a **copy of the worker's `SkillSnapshot` struct** (vendored verbatim into the orch test as `type workerSkillSnapshot struct {…}` matching `skills_client.go:36-42`), and assert field-for-field equality on the 5 narrow fields + the 7 metrics sub-fields. This freezes the cross-repo contract inside an orch test without importing the ME module. RED first (no handler) → GREEN (handler+service+route).

**Rationale.** Additive JSON is the lowest-risk way to feed the proposer without a second endpoint or a breaking change to the pinned `SkillSnapshot` contract.

---

### D-M3-3 — StructuralContext domain move (PR2, Option A)

**Choice.** Move `StructuralContext` and its supporting types from `internal/application/init/detector/types.go` to a NEW pure package `internal/domain/structural/context.go`.

**Exact package path:** `internal/domain/structural`, package name `structural`.

**What moves (verbatim, same field shapes + JSON tags):**
- `StructuralContext` struct (all fields, `types.go:21-70`)
- `LanguageInfo` (`types.go:73-83`)
- `FrameworkInfo` (`types.go:86-97`)
- `GraphSummary` (`types.go:101-113`)
- `StructuralContextSchemaV1` const → renamed in-package to `SchemaV1` (was `StructuralContextSchemaV1 = 1`). Keep value `1`.

**What does NOT move:** `SophiaDetectorVer` const (`types.go:16`) — it is detector-logic versioning, a `detector`-package concern (cache-key component), not a structural value. Stays in `detector`.

**Transition strategy for `init/detector` (keep compiling):** use a **type alias** during the move so the ~unknown number of detector/persister/cache consumers keep compiling without a big-bang rename:

```go
// internal/application/init/detector/types.go (after move)
package detector

import "github.com/.../internal/domain/structural"

// StructuralContext is re-exported from domain/structural for transitional
// source compatibility. Detector code may continue referencing
// detector.StructuralContext; new code SHOULD import domain/structural directly.
type StructuralContext = structural.StructuralContext
type LanguageInfo      = structural.LanguageInfo
type FrameworkInfo     = structural.FrameworkInfo
type GraphSummary      = structural.GraphSummary

const StructuralContextSchemaV1 = structural.SchemaV1
```

Type aliases (`=`) make `detector.StructuralContext` and `structural.StructuralContext` THE SAME TYPE — no conversion at any call site. This is the lowest-blast-radius move; detector consumers need zero edits. `sdd-tasks` decides whether to additionally sweep direct imports (cosmetic) or leave the aliases permanently.

**Import-cycle check (verified intent).** `domain/structural` imports only `time` (StructuralContext uses `time.Time`). `discipline` and `init/detector` both import `domain/structural`. `domain` never imports `application` → no cycle. Confirmed: `structural` is a leaf domain package.

**Two consumer rewires (NOT three — see C-1):**
- `discipline.PriorContext.StructuralCtx`: `*StructuralContextRef` → `*structural.StructuralContext` (`prior_context.go:25`).
- `discipline.SkillQuery.StructuralContext`: `*StructuralContextRef` → `*structural.StructuralContext` (`skill_matcher.go:50`).

After the rewire, delete `type StructuralContextRef struct{}` (`prior_context.go:64`). Update the two tests that reference it (`prior_context_test.go:110,138`; `skill_matcher_test.go:30,84-95`) to construct a real `*structural.StructuralContext` (or nil) instead of `&StructuralContextRef{}`.

`discipline` importing `domain/structural` is allowed (application→domain). Verify no NEW cycle: `domain/structural` does not import `discipline`.

**Rationale.** Single concrete pure-data type, three known consumers → Option A (move) over Option B (interface). Type alias bridges the transition with zero detector churn.

**Rejected.** Big-bang import rewrite of all detector consumers in PR2 — rejected: unbounded blast radius; the alias achieves the same end-state incrementally.

---

### D-M3-4 — Matcher structural filters (PR2)

**Prerequisite (C-2).** ADD to `skill.AppliesWhen` (`lifecycle.go:117-121`):

```go
type AppliesWhen struct {
    FeatureType  []string `json:"feature_type,omitempty"`
    TouchedPaths []string `json:"touched_paths,omitempty"`
    ExcludePaths []string `json:"exclude_paths,omitempty"`
    Framework    []string `json:"framework,omitempty"`  // NEW (M3)
    Language     []string `json:"language,omitempty"`   // NEW (M3)
}
```

These persist in the existing JSONB `applies_when` column — no schema migration (JSONB is schemaless); only the marshal/unmarshal round-trip in `skill_repo.go` scanAppliesWhen path must include the new keys. Add a round-trip test mirroring the existing `Metrics.LastStackVersion` round-trip test pattern.

**Filter placement.** In `PGSkillMatcher.SkillsForContext` (`skill_matcher.go:58-122`), add a structural filter **AFTER the `appliesWhenMatches` feature_type/paths check (line 97-103) and BEFORE the `MaxRiskLevel` check (line 107)**. Implemented as a new pure function `structuralMatches(aw skill.AppliesWhen, q discipline.SkillQuery) (string, bool)`:

```go
// structuralMatches checks applies_when.framework / applies_when.language
// against the live StructuralContext carried on the query.
//
// Nil-context skip semantics: when q.StructuralContext is nil (pre-INIT-0 or
// degraded INIT), the structural filter is a NO-OP — every skill passes this
// gate. A skill that DECLARES framework/language constraints but runs against
// a change with no StructuralContext is NOT skipped (fail-open): the absence
// of structural data must not silently hide skills. Matching is case-insensitive
// substring/equality on detected names.
func structuralMatches(aw skill.AppliesWhen, q discipline.SkillQuery) (string, bool) {
    if q.StructuralContext == nil {
        return "", true // no structural data → no structural filtering
    }
    sc := q.StructuralContext
    if len(aw.Framework) > 0 {
        if !anyFrameworkPresent(aw.Framework, sc.Frameworks) {
            return discipline.SkipReasonStructuralMismatch, false
        }
    }
    if len(aw.Language) > 0 {
        if !anyLanguagePresent(aw.Language, sc.Languages) {
            return discipline.SkipReasonStructuralMismatch, false
        }
    }
    return "", true
}
```

`anyFrameworkPresent` matches each declared `aw.Framework` name (case-insensitive) against `sc.Frameworks[].Name`; `anyLanguagePresent` against `sc.Languages[].Name`. A skill with empty `Framework` AND empty `Language` is structurally unconstrained → always passes.

**New SkipReason constant** (`skill_matcher.go`, alongside the existing reasons):

```go
// SkipReasonStructuralMismatch is returned when a skill's applies_when
// framework/language constraints do not match the live StructuralContext.
const SkipReasonStructuralMismatch = "structural_mismatch"
```

**Nil-safety.** Three layers: (1) nil `q.StructuralContext` → filter no-op (above); (2) nil `sc.Frameworks`/`sc.Languages` slices → `anyFrameworkPresent`/`anyLanguagePresent` return false only when the skill DECLARES a constraint, otherwise pass; (3) the matcher already loads all skills and fails open on infra error.

**Rationale.** Placing structural AFTER applies_when keeps the established skip-reason precedence (status → phase → scope → applies_when → structural → risk) and lets the existing observability list distinguish a structural skip from an applies_when skip. Fail-open on nil context preserves M0.5/M1 behaviour for pre-INIT-0 changes (acceptance criterion nil-safety).

**Rejected.** SQL push-down of the JSONB framework/language filter — rejected: matcher is in-memory by design (`<100` rows, D from M1); push-down is an M2+ note, not M3.

---

### D-M3-5 — Skills into PriorContext (PR3)

**`RenderedSkill` becomes real** (`prior_context.go:59`):

```go
// RenderedSkill is a flattened, render-ready projection of a skill.Skill for
// PriorContext. The callsite maps domain skills (post-match) into this shape;
// Render emits it. Kept render-ready (no domain methods) so Render stays a
// pure value→string function.
type RenderedSkill struct {
    Name       string   // skill.Name()
    Version    string   // skill.Version()
    Status     string   // skill.Status() stringified — ALWAYS "active" (matcher gate)
    Source     string   // skill.ActivationSource() stringified — attribution source
    Techniques []string // skill.TechniqueStrings()
    Content    string   // skill.Content() verbatim
}
```

**Who hydrates.** The CALLSITES (`phase/service.go:buildPriorContext` and `apply` path), NOT `Render` and NOT the matcher. The matcher returns `[]*skill.Skill`; the callsite maps each into a `RenderedSkill` via a small helper `toRenderedSkill(*skill.Skill) RenderedSkill` (lives in `discipline`, pure). This keeps `Render` deterministic and the matcher adapter clean.

**Render order.** Skills render FIRST (before memory layers), matching the current prompt where the `# Skill` section precedes `# Prior Context`. Layer order inside `Render` (D-M3-11): Skills → StructuralCtx → Episodes → ChangeDigests → BusinessRules → PhaseIdentity → RawMemoryBlob (RawMemoryBlob now always empty for the phase path; retained only until the field is deleted).

**prompt_builder.go changes:**
- DELETE the sibling `# Skill` injection block (`prompt_builder.go:97-102`).
- DELETE `renderSkillSection` (`prompt_builder.go:247-282`).
- DELETE the `Skills []*skill.Skill` field from `PromptInput` (`prompt_builder.go:46`) — **fate: removed.** Skills now arrive inside `PriorContext` (rendered into the `in.PriorContext` string by `Render`). The `import ".../domain/skill"` in prompt_builder.go is removed if no longer referenced.
- The `# Prior Context` block (`prompt_builder.go:104-108`) is unchanged structurally — it now receives the enriched, skills-included rendered string.

**Callsite consequence.** The three callsites that previously set `PromptInput.Skills` (`phase/service.go:426`, `teamlead.go:397,499`) STOP setting it. Skills flow through `PriorContext.Skills` → `Render` → `PromptInput.PriorContext`. `recordSkillUsageInjection` / skill_usage tracking still runs at hydration time (it consumes the matched `[]*skill.Skill` BEFORE the map to `RenderedSkill`).

**Rationale.** V4.1 §16: "PriorContext includes only active skills filtered by SkillMatcher." Consolidating skills into the canonical struct retires the divergent sibling section and gives one render path with budget+attribution.

**Rejected.** Keeping `PromptInput.Skills` as a parallel channel — rejected: it would re-introduce two skill render paths, defeating the consolidation and the "0 sibling section" goal.

---

### D-M3-6 — Memory layers (PR3)

**Stub types become real** (`prior_context.go:68-76`):

```go
// EpisodeRef is a relevant episodic memory surfaced for the phase prompt.
type EpisodeRef struct {
    ID      string // memory record ID (attribution)
    Content string // record body verbatim
}

// ChangeDigestRef is a prior change digest (M2 consolidation output).
type ChangeDigestRef struct {
    ChangeID string // source change (attribution)
    Content  string // digest YAML verbatim
}

// RuleRef is a project business rule (decision or heuristic).
type RuleRef struct {
    ID     string // record ID (attribution)
    Kind   string // "decision" | "heuristic"
    Content string
}
```

`RoutineOutput` and `AuxiliaryBlock` stay empty stubs (proposal non-goals: Routines/Auxiliary populate in a future milestone).

**BuildContext section → layer mapping table:**

| ME bundle section (`context_builder.go`) | PriorContext layer | Notes |
|---|---|---|
| `recent_episodic` (FTS) | `[]EpisodeRef` | requires `Query` set (D-M3-6 episodic path) |
| `decisions` | `[]RuleRef{Kind:"decision"}` | always present |
| `heuristics` | `[]RuleRef{Kind:"heuristic"}` | always present |
| `related` (graph) | `[]EpisodeRef` OR dropped | recommend: fold into Episodes (lowest priority, budget-cut first) |
| change digests (`Type:semantic`, `Tags:change_digest`) | `[]ChangeDigestRef` | via DG-1 dedicated Search, NOT BuildContext |

**Digests (DG-1).** A dedicated `Memory.Search(SearchQuery{Query:"change digest", Scope:{ProjectID,TenantID}, Types:["semantic"], Limit:3})` call. Map each `SearchResult` (or its fetched content) into `ChangeDigestRef`. `Limit:3` matches V4.1 §12.2 "digests top-3". `SearchQuery.Types` IS honoured by ME (`search.go:67`). No outbound-port change (Search already on `MemoryClient`).

**`IncludeTypes` workaround — formally retired (C-3).** Replaced by DG-1. `sdd-tasks` MUST NOT plumb `IncludeTypes:["semantic"]` expecting digests — it is inert.

**RawMemoryBlob deletion path.** `buildPriorContext` STOPS writing `RawMemoryBlob`. The phase-service path now populates Episodes/Rules/Digests instead. The `RawMemoryBlob` FIELD is deleted from the struct (`prior_context.go:54`) and from `Render` (`prior_context.go:131-133`) once no callsite writes it. Verify with `rg 'RawMemoryBlob'` returns zero non-test references before deletion.

**apply/run.go `loadPriorContext` + `refreshApplyProgress` — what layer do spec/design/progress become?** **DECISION: keep them in `PhaseIdentity`, UNCHANGED.** `loadPriorContext` (`run.go:853-890`) assembles `## spec / ## design` and `refreshApplyProgress` (`run.go:812-836`) appends `## Recent progress`. These are CHANGE-SCOPED ARTIFACT DOCUMENTS (the change's own spec/design/progress), not project-wide episodic/rule/digest memories. They are categorically different from the enrichment layers and map cleanly to `PhaseIdentity` as today. The apply path is NOT re-decomposed in M3 — only the phase-service path (`RawMemoryBlob`) is decomposed. This keeps the apply golden fixtures byte-stable EXCEPT for any skill-section change (the apply path also gets skills-in-PriorContext via D-M3-5).

**Rationale.** Honest mapping: project-wide knowledge → typed layers; change-scoped docs → PhaseIdentity. Avoids over-engineering the apply path which already has clean named sections.

**Rejected.** Mapping spec/design into Episodes/Rules — rejected: semantically wrong (they are not episodic memories) and would corrupt budget accounting.

---

### D-M3-7 — Budget algorithm (PR3)

**Activation.** `RenderOpts.TokenBudget` moves from no-op to active. Budget is a BYTE cap (the M0.5 field is documented "caps total bytes"; M3 keeps byte semantics for determinism — a real tokenizer is non-deterministic and out of scope). V4.1 §12.2 per-layer split.

**Per-layer budget split rule (V4.1 §12.2).** When `TokenBudget > 0`, allocate per layer by fixed share, then redistribute unused:

| Layer | Share | Cut rule |
|---|---|---|
| Skills | 40% | sort-cut: keep matcher order (already risk-asc); drop trailing skills that overflow |
| Episodes | 20% | top-K by arrival order from `recent_episodic` (already scored by ME) |
| ChangeDigests | 15% | top-3 (DG-1 Limit already caps); cut to fit |
| BusinessRules | 15% | decisions before heuristics; drop trailing |
| PhaseIdentity | 10% | never cut mid-section; if over, truncate with marker at section boundary |

Unused share from earlier layers cascades to later layers (same pattern as ME `ContextBuilder.allocateBudget` redistribution).

**Cut order (when total still overflows after per-layer caps).** Lowest-value first: `related`-derived Episodes → trailing Episodes → trailing Digests → trailing Rules → trailing Skills. PhaseIdentity (the change's own spec/design) is cut LAST — it is the most load-bearing context.

**Truncation marker format.** Deterministic, single line, appended where a layer is cut:

```
\n…[truncated: N {layer} omitted, M bytes over budget]\n
```

e.g. `…[truncated: 3 skills omitted, 1280 bytes over budget]`. The marker text is fixed (no clock/random) so goldens stay stable.

**Zero-value preserved.** `RenderOpts{}` (TokenBudget=0) → NO truncation, NO marker — the M0.5 no-op contract holds and is re-asserted by a retained test.

**Rationale.** Byte budget keeps Render deterministic (golden-testable). Per-layer share + redistribution mirrors the proven ME allocator. PhaseIdentity-cut-last protects the change's own artifacts.

**Rejected.** Real token counting (tiktoken) — rejected: non-deterministic across versions, breaks goldens, out of M3 scope.

---

### D-M3-8 — Attribution format (PR3)

**Activation.** `RenderOpts.EnableAttribution` moves from no-op to active. When true, each layer emits a source-attribution header (V4.1 §12.3). Exact header strings (fixed, deterministic):

```
## Skill: <name> v<version> (active, source=<activation_source>)
## Structural Context (init/<change_name>)
## Episode (<record_id>)
## Change Digest (<change_id>)
## Rule: <kind> (<record_id>)
## <existing PhaseIdentity headers unchanged>
```

- Skills: `## Skill: clean-arch v3 (active, source=consolidation_worker)` — status is always `active` (matcher gate); source from `RenderedSkill.Source`.
- StructuralCtx: one header then a compact framework/language summary line.
- Episodes/Digests/Rules: one header per item with the record/change ID for traceability.
- When `EnableAttribution=false`: layers render content WITHOUT headers (skills still need a minimal `## <name>` separator to stay parseable — match the current `renderSkillSection` `## <name>` shape so the no-attribution path resembles today's output).

**Rationale.** V4.1 §12.3 mandates per-skill `source=` attribution. Fixed header strings keep determinism; IDs give the operator a traceability anchor back to memory-engine records.

---

### D-M3-9 — SkillsForPhase retirement (PR3)

**Deletes:**
- `internal/adapters/outbound/pg/skill_provider.go` — entire file (the `SkillProvider` wrapper).
- `internal/application/discipline/skill_provider.go` — the `SkillProvider` interface + `SkillsForPhase` method. **Port fate: removed.** No consumer survives.

**Dependency rewires:**
- `RunDeps.Skills discipline.SkillProvider` (`apply/run.go:74`) → `RunDeps.Skills discipline.SkillMatcher`.
- phase `ServiceDeps.Skills discipline.SkillProvider` (`phase/service.go:126`) → `discipline.SkillMatcher`.
- `wire.go:215` `var skillProvider discipline.SkillProvider` → `var skillMatcher discipline.SkillMatcher`; `wire.go:219` drop `pg.NewSkillProvider(...)`; pass `skillMatcher` directly into both `Skills:` deps (`wire.go:292,359`). Skills write service (`wire.go:408`) unchanged.

**Callsite migrations (3):**
- `phase/service.go:426`: `s.d.Skills.SkillsForPhase(ctx, p.Type())` → `s.d.Skills.SkillsForContext(ctx, discipline.SkillQuery{Phase: p.Type(), ProjectID: c.Project(), StructuralContext: structuralCtx})` — discard the skipped slice (or log it), keep matched.
- `teamlead.go:385` and `teamlead.go:487` (both call `hydrateSkills`): rewrite `hydrateSkills` (`teamlead.go:598-607`) to call `SkillsForContext` with a `SkillQuery{Phase: pt, ProjectID: c.Project(), StructuralContext: …}`. `hydrateSkills` needs the change to source ProjectID/StructuralContext — pass them in or hold on the RunService.

**StructuralContext source for the query.** The callsite fetches `*structural.StructuralContext` via `Memory.GetByTopicKey` (the structural topic, e.g. `sdd/<change>/structural` or the INIT artifact key — `sdd-tasks` confirms the exact topic key from INIT persistence) and passes it on the `SkillQuery`. Nil when absent (fail-open per D-M3-4).

**Test fakes to migrate.** All `fakeSkillProvider` / `applyFakeSkillProvider` / `fakeSkillProviderWithSkills` fakes (in `phase/service_test.go:1396`, `phase/skill_usage_test.go:77`, `apply/run_test.go:2581`, `apply/skill_usage_test.go:58`) implement `SkillsForPhase` → must be rewritten to implement `SkillsForContext(ctx, SkillQuery) ([]*skill.Skill, []SkippedSkill, error)`. This is the bulk of PR3's test churn beyond goldens.

**Rationale.** V4.1 §16 "0 legacy callsites". One matcher port, context-aware, replaces the phase-only wrapper.

**Rejected.** Keeping `SkillProvider` as a deprecated shim — rejected: the acceptance criterion is literally "0 callsites use SkillsForPhase" and "wrapper deleted".

---

### D-M3-10 — Golden strategy (PR3)

**Baseline-capture-first (M0.5 pattern).** The FIRST PR3 commit re-captures golden baselines for the INTENDED enriched output (run the new Render with `GOLDEN_UPDATE=1` against deterministic fixtures), committing them as the new contract. Then structural assertions replace byte-exact comparison.

**Byte-exact retired as the contract.** New contract is STRUCTURAL:
- required layers present in correct order (Skills → … → PhaseIdentity);
- no blocked/deprecated/archived skill ever rendered (matcher gate + assertion);
- per-layer budget respected (truncation marker present when over budget);
- attribution headers present when `EnableAttribution=true`;
- zero-value `RenderOpts{}` still a no-op (retained M0.5 test).

**Fixture inventory (14 total today):**

Priorcontext (12, `testdata/priorcontext/*.golden.txt`):
- 5 `phase_*` fixtures → re-baselined: the phase path no longer emits `RawMemoryBlob`; it emits typed Episodes/Rules/Digests. These goldens change substantially.
- 7 `apply_*` fixtures → re-baselined ONLY for the skills layer (PhaseIdentity path unchanged per D-M3-6); spec/design/progress bytes stay identical, but if a fixture carries skills the new skill render shape appears.

Prompt_builder (2, `testdata/*.golden`):
- `apply_no_skills_baseline.golden` → re-baselined: sibling `# Skill` section removed; skills (none) now render via PriorContext. With no skills, the `# Skill`/`# Prior Context` boundary shifts.
- `apply_with_skill.golden` → re-baselined: the skill that previously rendered in the sibling `# Skill` section now renders inside `# Prior Context` via `PriorContext.Skills`. This is the canonical "skill moved" diff.

**New fixtures to ADD (enrichment coverage):**
- `phase_with_skills.golden.txt`, `phase_with_episodes.golden.txt`, `phase_with_digests.golden.txt`, `phase_all_layers.golden.txt`
- `render_budget_truncated.golden.txt` (truncation marker present)
- `render_attribution_on.golden.txt` (headers present)
- `render_attribution_off.golden.txt` (no headers)

**Tests that convert to structural assertions.** `TestPriorContext_Render_Goldens` keeps the table+golden mechanism for diff review, but ADD `TestRender_LayerOrdering`, `TestRender_NoBlockedSkillRendered`, `TestRender_BudgetRespected`, `TestRender_AttributionHeaders` as structural (non-byte) assertions. The prompt_builder skills tests (`prompt_builder_test.go:408-448,553-567`) that assert `# Skill` ordering are DELETED/rewritten to assert skills appear inside `# Prior Context` instead.

**Rationale.** Enrichment changes the prompt by design; byte-exact would lock in the OLD output. Structural assertions verify the contract that matters (ordering, gating, budget, attribution).

---

### D-M3-11 — Render() layer order + enforcement (component pseudocode)

```go
func (pc PriorContext) Render(opts RenderOpts) string {
    layers := pc.collectLayers(opts.EnableAttribution) // ordered []layerBlock{name, body}

    if opts.TokenBudget > 0 {
        layers = enforceBudget(layers, opts.TokenBudget) // per-layer share + cut order + markers
    }

    var b strings.Builder
    for _, l := range layers {
        b.WriteString(l.body)
    }
    return b.String()
}

// collectLayers builds blocks in canonical order. Each block's body already
// includes attribution headers when enabled. Empty layers are skipped.
func (pc PriorContext) collectLayers(attr bool) []layerBlock {
    var ls []layerBlock
    if len(pc.Skills) > 0        { ls = append(ls, renderSkills(pc.Skills, attr)) }
    if pc.StructuralCtx != nil   { ls = append(ls, renderStructural(pc.StructuralCtx, attr)) }
    if len(pc.Episodes) > 0      { ls = append(ls, renderEpisodes(pc.Episodes, attr)) }
    if len(pc.ChangeDigests) > 0 { ls = append(ls, renderDigests(pc.ChangeDigests, attr)) }
    if len(pc.BusinessRules) > 0 { ls = append(ls, renderRules(pc.BusinessRules, attr)) }
    if pc.PhaseIdentity != ""    { ls = append(ls, layerBlock{"phase_identity", pc.PhaseIdentity}) }
    // RawMemoryBlob removed in M3 (D-M3-6); no longer collected.
    return ls
}
```

`enforceBudget` allocates each layer its share (D-M3-7), redistributes unused forward, and on overflow applies the cut order, appending the fixed truncation marker. PhaseIdentity is cut last. All deterministic (no clock/random/map iteration).

---

## 3. Component design — Go shapes (consolidated)

| Type / change | File | PR |
|---|---|---|
| `getSkillResp` + nested DTOs, `GetSkill` handler, `toGetSkillResp` | `handlers/skills.go` | PR1 |
| `SkillService.GetSkill` method | `ports/inbound/skill.go` + impl `application/skill/service.go` | PR1 |
| `GET /{skill_id}` route | `router.go:143-146` | PR1 |
| `SkillActivationProposal` + `proposerSkillView` (4 new fields) | ME `consolidation/proposer.go` | PR1 |
| package `structural` (StructuralContext, LanguageInfo, FrameworkInfo, GraphSummary, SchemaV1) | NEW `internal/domain/structural/context.go` | PR2 |
| type aliases for transition | `init/detector/types.go` | PR2 |
| `AppliesWhen.Framework`, `AppliesWhen.Language` | `domain/skill/lifecycle.go` | PR2 |
| `structuralMatches` + `SkipReasonStructuralMismatch` | `pg/skill_matcher.go` | PR2 |
| `PriorContext.StructuralCtx`/`SkillQuery.StructuralContext` retype; delete `StructuralContextRef` | `discipline/prior_context.go`, `skill_matcher.go` | PR2 |
| `RenderedSkill`/`EpisodeRef`/`ChangeDigestRef`/`RuleRef` real fields | `discipline/prior_context.go` | PR3 |
| `Render` layers + `enforceBudget` + attribution + `collectLayers` | `discipline/prior_context.go` | PR3 |
| `toRenderedSkill` helper | `discipline/prior_context.go` | PR3 |
| delete sibling `# Skill` + `renderSkillSection` + `PromptInput.Skills` | `discipline/prompt_builder.go` | PR3 |
| `buildPriorContext` decompose (Query set, Episodes/Rules/Digests, DG-1 Search, structural fetch) | `phase/service.go:1015-1048` | PR3 |
| callsite migrations to `SkillsForContext` | `phase/service.go:426`, `apply/teamlead.go:385,487,598` | PR3 |
| delete `pg/skill_provider.go` + `discipline/skill_provider.go`; rewire `RunDeps`/`ServiceDeps`/`wire.go` | multiple | PR3 |
| golden re-baseline + structural assertions + new fixtures | `discipline/testdata/**`, test files | PR3 |

---

## 4. Test strategy per PR (strict TDD: RED→GREEN→REFACTOR)

**PR1**
1. RED: contract test — marshal `getSkillResp`, unmarshal into vendored `workerSkillSnapshot`, assert 5+7 fields equal → FAILS (no handler/route).
2. GREEN: add `SkillService.GetSkill` + handler + route.
3. RED: `GetSkill` 404 test (unknown id → `ErrNotFound` → 404 JSON).
4. ME side RED: proposer-shape test asserting emitted YAML has `skill_name/scope/applies_when/risk_level` → GREEN reconcile.
5. Integration: live promote/demote fires (GetSkill no longer 404) — acceptance criterion 10.

**PR2**
1. RED: `AppliesWhen` Framework/Language JSONB round-trip test → GREEN add fields + scan.
2. RED: matcher framework/language filter test (skill with `framework:[angular]` + StructuralContext{Frameworks:[Angular]} matches; mismatch skipped with `structural_mismatch`) → GREEN `structuralMatches`.
3. RED: nil-StructuralContext fail-open test (constrained skill, nil context → NOT skipped) → GREEN nil guard.
4. Compile/move tests: `structural` package builds; detector aliases resolve; `skill_matcher_test.go`/`prior_context_test.go` updated to real type.

**PR3**
1. **Baseline golden re-capture FIRST** (`GOLDEN_UPDATE=1`) — commit the new enriched contract.
2. RED structural assertions: layer ordering, no-blocked-skill, budget-respected, attribution-on/off → GREEN Render enrichment + budget + attribution.
3. RED: `buildPriorContext` decomposition test (BuildContext sections → Episodes/Rules; DG-1 Search → Digests; Query set) → GREEN.
4. GREEN: SkillsForContext callsite migration; all `fakeSkillProvider`→`fakeSkillMatcher` fakes rewritten; delete wrapper + port.
5. RED→GREEN: retained `TestRenderOpts_ZeroValue_IsNoOp` still passes (no-op preserved).
6. p95 benchmark gate (D-M3-12).

**PR3 split contingency (proposal §PR3 split).** If `sdd-tasks` Review Workload Forecast exceeds budget: PR3a = Render enrichment (stub types real + layers + budget + attribution + decomposition + golden re-baseline); PR3b = SkillsForPhase removal (wrapper+port delete + 3 callsite + fakes migration). PR3a is independently shippable (skills still flow via the sibling section until PR3b)… **caveat:** D-M3-5 deletes the sibling section in PR3a, so PR3a must ALSO migrate the callsites to feed `PriorContext.Skills`. Recommendation: if split, move the callsite migration into PR3a and limit PR3b to ONLY the wrapper/port file deletes + fake cleanups. `sdd-tasks` finalises the cut line.

---

## 5. p95 validation (acceptance criterion 4)

**Target.** p95 `buildPriorContext` < 250ms with 50 active skills.

**Approach.** Benchmark `BenchmarkBuildPriorContext_50Skills` in `phase/` with:
- a fake `MemoryClient` returning a fixed bundle (decisions+heuristics+recent_episodic) + a fixed digest Search result + a fixed structural record — all in-memory, zero network (isolates the orch-side cost: matcher filter + map + Render + budget).
- a fake `SkillMatcher` (or real `PGSkillMatcher` over a fixture repo) loaded with 50 active skills spanning frameworks/languages so `structuralMatches` runs the full filter chain.
- measure wall time over `b.N`; report p95 in the PR body (NOT a hard CI gate — benchmark variance on shared runners, per M0.5 D-M05-bench convention).

**Network note.** Real p95 includes ~3 round-trips (BuildContext + GetByTopicKey + Search). The benchmark isolates orch CPU; the round-trip budget (~3×10–50ms < 200ms) is argued analytically in the PR body and validated in the integration smoke. Matcher in-process over ~100 rows < 50ms.

---

## 6. Risks revisited (concrete mitigations)

| Risk | Mitigation |
|---|---|
| **IncludeTypes digest workaround is inert (C-3)** | RETIRED. DG-1 dedicated `Search(Types:[semantic], Limit:3)` — `Types` IS honoured by ME search. No ME change. |
| **Episodes never populate (no Query) (C-3)** | PR3 sets `ContextRequest.Query = c.Name()` so `recent_episodic` surfaces. One-line add, no extra round-trip. |
| **AppliesWhen lacks Framework/Language (C-2)** | PR2 adds the fields + JSONB round-trip BEFORE filter activation. No schema migration (JSONB). |
| **Phantom 3rd nil-marker (C-1)** | Only 2 markers retyped; `Metrics.LastStackVersion` stays `*string`, out of scope. |
| **Golden cascade (14 fixtures)** | Baseline-capture-first; byte-exact retired; structural assertions; new enrichment fixtures enumerated (D-M3-10). |
| **StructuralContext nil for pre-INIT-0/degraded** | Fail-open at matcher (D-M3-4) + Render nil-guard (`collectLayers` skips nil). |
| **PR3 size inflation** | Split contingency (PR3a/PR3b) with the callsite-line caveat resolved in §4. |
| **GetSkill JSON drift from SkillSnapshot** | Contract test unmarshals into vendored worker struct; additive fields proven ignorable (D-M3-2). |
| **p95 regression** | ~3 round-trips < 200ms; matcher < 50ms; benchmark + integration smoke (§5). |
| **Detector consumers break on the move** | Type aliases keep `detector.StructuralContext` identical to `structural.StructuralContext` — zero call-site edits (D-M3-3). |

---

## 7. Out of scope (reaffirmed)

- Webhook outbox / at-least-once delivery (M4).
- LLM critic opt-in; D-M2-12 lint guard retained (M4).
- GET /usage `skill_id` param (M4).
- `rollback_count` / `deprecated_api_hits` real instrumentation (M4+; served, not incremented).
- governance-core HTTP surface (future).
- Routines layer population (Graphify/Context7/LSP pre-phase hooks) — stub stays empty.
- AuxiliaryMemory layer population — stays nil.
- `AppliesWhen.StateModel` (godoc mentions it; only Framework/Language are M3).
- Real tokenizer for budget (byte budget only — determinism).
- Making ME `BuildContext` honour `IncludeTypes` (DG-2 rejected).
- apply-path re-decomposition (spec/design/progress stay in PhaseIdentity — D-M3-6).
- `Metrics.LastStackVersion` retype (stays `*string` — C-1).

---

## 8. Open questions

**None blocking design.** Three items deferred to `sdd-tasks` as MECHANICAL finalisations (not architectural forks):
1. Exact topic key for the StructuralContext memory record (read by the callsite for `SkillQuery.StructuralContext`) — confirm from INIT persistence.
2. Whether to sweep detector direct imports after the alias, or leave aliases permanently (cosmetic).
3. Final PR3 split line IF the Review Workload Forecast exceeds budget (callsite-migration placement resolved in §4).
