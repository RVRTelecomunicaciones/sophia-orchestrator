# Verify Report: context7-bootstrap (V1)

**Date:** 2026-06-12
**Phase:** sdd-verify
**Verdict:** **PASS_WITH_WARNINGS**

The change is correctly and completely implemented against the 7 specs, design (DG-C7-1…10),
and tasks (all 54 checked). All reconciliation items R1–R7 and the DG-C7-8 transport banner are
honored. All unit/lint suites are green in both repos; PG integration tests pass except the 3
pre-existing R-4 failures (unchanged, no new failures). Findings are SUGGESTION-level only; no
CRITICAL or blocking WARNING.

---

## Test-Run Evidence

| Suite | Command | Result |
|-------|---------|--------|
| orch unit | `make test-unit` | PASS — all packages incl. `bootstrap`, `docs/context7`, `init`, `phase`, `skill`, `config` |
| orch lint | `make lint` | PASS — 0 issues (forbidigo/wrapcheck/errorlint clean) |
| orch PG integration | `go test -tags=integration ./internal/adapters/outbound/pg/...` | PASS except 3 pre-existing R-4 failures; 12 new T4.6/T5.9 tests PASS |
| agent-mcp test | `make test` (-race) | PASS — all packages |
| agent-mcp lint | `make lint` | PASS — 0 issues |

**R-4 pre-existing failures (confirmed still the ONLY failures, NOT introduced by this change):**
- `TestMigration010_PreState` (expects 7 cols, sees 16 — predates change)
- `TestMigration010_RoundTrip` (same)
- `TestSkillRepo_FindByPhase_MatchingRow` (FindByPhase returns 0 rows — predates change)

**New integration tests verified PASS:** `TestSkillRepo_ImportedCandidate_InsertAndNotVisibleToMatcher`,
`_IdempotentSecondInsert`, `_ConcurrentInsert_ExactlyOneRow`, `_DriftRowCoexistsWithActive`,
`TestSkillRepo_ActiveByName_*` (4 variants).

---

## Per-Capability Table

| # | Capability | Spec → Impl evidence | Status |
|---|------------|----------------------|--------|
| 1 | context7-provider-registration (DG-C7-1) | `sophia-agent-mcp/configs/example.toml:258-266` — id=context7, npx command, persistent, `startup_timeout_s=20`, exactly `["resolve-library-id","get-library-docs"]`, env `CONTEXT7_API_KEY`. graphify block unchanged. Zero proxy code. | PASS |
| 2 | manifest-hash-cache-invalidation (DG-C7-2, R2) | `key_builder.go:75-113` — 8th component over 7-name sorted set, name+NUL framing, `absentSentinel`, 16-hex truncation. `computeCacheKeyHash` 8-arg; agrees with `cache.CacheKey.Hash()`. D-C7-7 acceptance test (identical porcelain + diff manifest bytes → diff key) in `key_builder_test.go`. | PASS |
| 3 | greenfield-detection (DG-C7-3, DG-C7-5, R6) | `context.go` `Greenfield bool omitempty`; detector sets `len(Frameworks)==0 && len(Languages)==0` as last step; `SophiaDetectorVer="v1.1.0"`; async fire in `phase/service.go:765-785` step 7 post-persist/advance, traceBackground, 60s default, `recover()`, nil-safe. | PASS |
| 4 | applies-when-version-semantics (DG-C7-4, DG-C7-9, R7) | `skill/semver.go` `MajorOf`/`DriftsForward` (stdlib-only, tolerates "22.0.0","go 1.26","^18","v3.2"); `AppliesWhen.FrameworkMinVersion map omitempty`; matcher gate `skill_matcher.go:277-313` fail-open + WARN, empty-map path unchanged. | PASS |
| 5 | skill-importer-deterministic (DG-C7-10, DG-C7-7, R3/R4/R5) | `importer.go` — fixed template (REFERENCE-DATA banner + Best practices + Provenance), `sanitizeBody` escapes `## Rule:/Routine:/Skill:` + ```system/```tool fences, 24 KiB `truncateBody` with rune-boundary, version=full detected (DG-C7-7), phases explore/proposal/apply, no LLM/MCP. Determinism test (not testdata goldens — accepted). | PASS |
| 6 | bootstrap-trigger-service (DG-C7-6, DG-C7-8 banner) | `service.go` — key guard → rate guard → greenfield branch → drift branch → resolve/threshold-fallback/GetDocs(tokens=8000)/import; all failures WARN+swallow; never panics; no LLM. 13 spec scenarios SVC-A…M tested. `MemoryRateGuard` in-memory sliding window (R1). | PASS |
| 7 | skill-matcher-structural (DG-C7-9) | `anyFrameworkMatches` — optional gate active only when map non-empty for matched fw; `detected_major >= min_major`; parse-fail → fail-open + WARN; name-only path byte-unchanged. | PASS |

---

## Invariant Checklist

| Invariant | Result | Evidence |
|-----------|--------|----------|
| D11 — INIT never creates skills / calls LLM | PASS | Bootstrap is a separate `application/bootstrap` service; `ServiceDeps`/`SkillImporter` have NO LLM/dispatcher port (compile-time); `TestSkillImporter_NoDeps_NoLLM` + SVC-L assert zero LLM interaction. |
| Imported skills are candidate + imported only | PASS | `importer.go:115-118` `StatusCandidate` + `SourceImported`; integration test confirms not visible to active-only matcher. |
| No auto-activation anywhere | PASS | Only `InsertIfAbsent` mutates; no status promotion in any path. |
| Drift = new (name,version) row via InsertIfAbsent; old row untouched | PASS | `TestSkillRepo_ImportedCandidate_DriftRowCoexistsWithActive`; service never reads-to-modify the active row. |
| Bootstrap fires AFTER envelope persist (DG-C7-5) | PASS | `phase/service.go` step 7 runs after `recordPhaseTerminal`+`advanceChange`+`publishEvent`. |
| No time.Now()/ulid.Make() in domain/application added by this change | PASS | `semver.go` stdlib-only pure; importer uses injected `clock`/`idgen`; rate guard uses injected `clock`; lint clean. |
| Degraded-first on every failure path | PASS | Missing key/bridge → `ErrDocsUnavailable` (no dial); rate deny → WARN; proxy/timeout/quota → WARN+swallow; thin/all-thin → WARN; import error → WARN+continue; goroutine `recover()`. |
| ContextCrush — docs stored as sanitized DATA, never instructions | PASS | `sanitizeBody`/`sanitizeLine` escape spoof headers + role fences; REFERENCE-DATA banner; stored only in `skill.Content`; `TestSkillImporter_Sanitization_EscapesControlHeaders`. |
| Cache-key fix covers manual manifest edit | PASS | D-C7-7 acceptance test: identical `git status --porcelain` + manifest bytes ^22→^23 → different key. One-time global invalidation documented (design Migration/rollout). |
| Wiring nil-safe + config defaults (60s/5/50/24576) | PASS | `wire.go:319-401` constructs guard/adapter/importer/service, injects `Bootstrap`+`BootstrapTimeout`; `config.go:404-408` defaults exactly `60s/5/50/24576`; agent-mcp example.toml safe-by-default (degrades without key). |
| Tasks all 54 checked | PASS | `tasks.md` — every T1.1…T5.14 marked `[x]`. |

---

## Findings

### CRITICAL
None.

### WARNING
None.

### SUGGESTION

- **S1 — Drift heuristic narrows to consecutive majors (design-bounded, not a spec violation).**
  `service.go:166-216` `runDriftCheck` looks up only `stack/<fw>-<detectedMajor-1>` (the immediately
  preceding major). A skip-major jump (e.g. detected v24 with an active `stack/<fw>-22`, no v23) will
  NOT fire drift because `stack/<fw>-23` does not exist. The `SkillLookup.ActiveByName(ctx, name)`
  port (design DG-C7-9, data-flow line 424 `ActiveByName("stack/angular-22")`) only supports
  name-based exact lookup, so this is consistent with the design's chosen mechanism, and the spec's
  drift scenario (22→23, consecutive) is satisfied and tested (SVC-F). Flagged as a known V1 boundary
  worth a backlog note for multi-major jumps; not blocking.

- **S2 — `extractMajor` not unified with `skill.MajorOf` (optional PR3c-ii refactor R, skipped).**
  `importer.go:240` `extractMajor` (returns string major) coexists with domain `skill.MajorOf`
  (returns int). Both are correct and tested; the optional unification noted in the apply context was
  NOT performed. Pure cleanup opportunity — no functional impact.

- **S3 — Importer goldens via determinism test, not committed testdata files (accepted deviation).**
  `importer_test.go:67` asserts byte-identical content across two builds with a fixed clock + ID,
  plus structural `Contains` assertions, instead of a `testdata/*.golden` file. Equivalent
  determinism guarantee; documented as an accepted deviation in the apply reports.

- **S4 — `isDocsUnavailable` matches the sentinel via `strings.Contains(err.Error(), ...)`.**
  `service.go:267-269` detects `ErrDocsUnavailable` by substring rather than `errors.Is`. The adapter
  returns the bare sentinel (so it works today), but an `errors.Is` check would be more robust against
  future wrapping. Cosmetic; behavior is correct under current wrapping.

---

## Notes on Reconciliation (verified, NOT findings)

R1 (in-memory rate guard vs durable store), R2 (7-name hash vs 4-name e3b0c4 constant), R3 (single
sanitized body vs 3-section template), R4 (importer signature + service-side resolve), R5 (version =
full detected, operator decision #2), R6 (gating inside TriggerIfNeeded, nil Bootstrap in PR2),
R7 (`stack/go-1` from "1.26") — all implemented as the design/tasks specify. Per the verify
instructions these divergences from spec literal text are authoritative and are NOT raised as findings.

DG-C7-8 transport reinterpretation (new `DocsProvider` port + adapter reusing the dispatcher's
streamable-HTTP transport) implemented in `ports/outbound/docs.go` +
`adapters/outbound/docs/context7/client.go` — authoritative banner honored.

---

## Next Recommended

`sdd-archive` — implementation is complete and verified; all 54 tasks done; suites green modulo the
pre-existing R-4 backlog item. Recommend archiving the change and tracking S1 (multi-major drift) and
R-4 (pre-existing pg failures) as backlog notes.
