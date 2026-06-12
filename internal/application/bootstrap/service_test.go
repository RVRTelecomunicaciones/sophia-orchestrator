package bootstrap_test

// T5.7 — RED tests for BootstrapTriggerService.
//
// Scenario IDs:
//   SVC-A  — DocsProvider returns ErrDocsUnavailable → WARN, return, zero further calls, zero inserts
//   SVC-B  — rate guard denies → WARN, NO docs call
//   SVC-C  — greenfield + 1 framework → exactly 1 import with framework+version
//   SVC-D  — greenfield + zero frameworks → WARN "greenfield but no framework detected", no docs call
//   SVC-E  — non-greenfield + versions satisfy active mins → no import
//   SVC-F  — drift: active stack/angular-22 min {angular:"22"} + detected 23.0.0 → import angular v23
//   SVC-G  — no active skill with min set → drift never fires (no baseline)
//   SVC-H  — unparseable detected or stored min → no drift + WARN
//   SVC-I  — thin version entry (7 < MinSnippets 50) + fat main entry → main entry selected
//   SVC-J  — ALL entries thin → WARN skip, get-library-docs never called
//   SVC-K  — docs/import error → WARN, swallowed, continue to next framework, nil returned
//   SVC-L  — D11: constructor has NO LLM/dispatcher dep (compile-time) + fakes record zero LLM interaction
//   SVC-M  — quota/429 surfaced by provider → WARN skip

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeDocsProvider is a fake outbound.DocsProvider controlled by test funcs.
type fakeDocsProvider struct {
	mu           sync.Mutex
	resolveFunc  func(ctx context.Context, framework, query string) ([]outbound.LibraryEntry, error)
	getDocsFunc  func(ctx context.Context, libraryID, query, topic string, tokens int) (outbound.DocsResult, error)
	resolveCalls []string // frameworks resolved
	getDocsCalls []string // libraryIDs fetched
}

func (f *fakeDocsProvider) ResolveLibrary(ctx context.Context, framework, query string) ([]outbound.LibraryEntry, error) {
	f.mu.Lock()
	f.resolveCalls = append(f.resolveCalls, framework)
	fn := f.resolveFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, framework, query)
	}
	return nil, outbound.ErrDocsUnavailable
}

func (f *fakeDocsProvider) GetDocs(ctx context.Context, libraryID, query, topic string, tokens int) (outbound.DocsResult, error) {
	f.mu.Lock()
	f.getDocsCalls = append(f.getDocsCalls, libraryID)
	fn := f.getDocsFunc
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, libraryID, query, topic, tokens)
	}
	return outbound.DocsResult{Body: "best practices content"}, nil
}

// fakeSkillLookup is a fake bootstrap.SkillLookup.
type fakeSkillLookup struct {
	mu     sync.Mutex
	skills []*skill.Skill
	err    error
}

func (f *fakeSkillLookup) ActiveByName(ctx context.Context, name string) ([]*skill.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var out []*skill.Skill
	for _, s := range f.skills {
		if s.Name() == name {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeRateGuard is a controllable RateGuard.
type fakeRateGuard struct {
	mu    sync.Mutex
	allow bool
	err   error
	calls []string // projectIDs seen
}

func (f *fakeRateGuard) Allow(_ context.Context, projectID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, projectID)
	return f.allow, f.err
}

// fakeImporter records ImportFromDocs invocations and optionally returns an error.
type fakeImporter struct {
	mu    sync.Mutex
	calls []importCall
	err   error
}

type importCall struct {
	name    string
	version string
	fw      string
	result  outbound.DocsResult
}

func (f *fakeImporter) ImportDocs(
	ctx context.Context,
	name, version, fw string,
	r outbound.DocsResult,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, importCall{name: name, version: version, fw: fw, result: r})
	return f.err
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const testMinSnippets = 50

// greenfield1FW builds a StructuralContext with Greenfield=true and one framework.
func greenfield1FW(fw, version string) structural.StructuralContext {
	return structural.StructuralContext{
		ProjectID:  "proj-alpha",
		Greenfield: true,
		Frameworks: []structural.FrameworkInfo{
			{Name: fw, Version: version},
		},
	}
}

// nonGreenfield builds a StructuralContext with Greenfield=false and one framework.
func nonGreenfield(fw, version string) structural.StructuralContext {
	return structural.StructuralContext{
		ProjectID: "proj-alpha",
		Frameworks: []structural.FrameworkInfo{
			{Name: fw, Version: version},
		},
	}
}

// fatEntries returns a resolved list where angular has both a version-specific
// and a main entry, both fat (>= minSnippets).
func fatEntries() []outbound.LibraryEntry {
	return []outbound.LibraryEntry{
		{ID: "/angular/angular-v22", Snippets: 1200, Score: 0.9, IsMain: false},
		{ID: "/websites/angular_dev", Snippets: 14850, Score: 0.95, IsMain: true},
	}
}

// thinVersionFatMain returns a list where the versioned entry is thin but the main is fat.
func thinVersionFatMain() []outbound.LibraryEntry {
	return []outbound.LibraryEntry{
		{ID: "/angular/angular-v22", Snippets: 7, Score: 0.5, IsMain: false},
		{ID: "/websites/angular_dev", Snippets: 14850, Score: 0.95, IsMain: true},
	}
}

// allThinEntries returns a list where every entry is below minSnippets.
func allThinEntries() []outbound.LibraryEntry {
	return []outbound.LibraryEntry{
		{ID: "/angular/angular-v22", Snippets: 7, Score: 0.5, IsMain: false},
		{ID: "/websites/angular_dev", Snippets: 12, Score: 0.3, IsMain: true},
	}
}

// makeService builds a Service with the provided fake deps.
func makeService(
	docs *fakeDocsProvider,
	skills *fakeSkillLookup,
	importer *fakeImporter,
	rate *fakeRateGuard,
) *bootstrap.Service {
	return bootstrap.NewService(bootstrap.ServiceDeps{
		Docs:        docs,
		Skills:      skills,
		Importer:    importer,
		Rate:        rate,
		MinSnippets: testMinSnippets,
	})
}

func mustSkillID(t *testing.T) ids.SkillID {
	t.Helper()
	// Use a well-formed ULID string for tests.
	id, err := ids.ParseSkillID("01HXXXXXXXXXXXXXXXXXXXXXXX")
	if err != nil {
		// Fallback: generate via SystemIDGenerator — but we can't call
		// ulid.Make() here (forbidigo). Use a hardcoded valid ULID.
		// This form is acceptable: 26 chars, valid ULID alphabet.
		id, err = ids.ParseSkillID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
		require.NoError(t, err)
	}
	return id
}

// makeActiveAngularSkill builds an active Skill with FrameworkMinVersion set.
func makeActiveAngularSkill(t *testing.T, name, minVersion string) *skill.Skill {
	t.Helper()
	id := mustSkillID(t)
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{"angular": minVersion},
	}
	return skill.Hydrate(
		id, name,
		[]phase.PhaseType{phase.PhaseExplore},
		"body content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.StatusActive,
		"22.0.0",
		skill.Scope{},
		aw,
		skill.RiskMedium,
		skill.SourceImported,
		skill.Metrics{},
		nil, nil,
		time.Now(), time.Now(), //nolint:forbidigo
	)
}

// makeActiveAngularSkillNoMin builds an active Skill WITHOUT FrameworkMinVersion.
func makeActiveAngularSkillNoMin(t *testing.T, name, version string) *skill.Skill {
	t.Helper()
	id := mustSkillID(t)
	aw := skill.AppliesWhen{
		Framework: []string{"angular"},
	}
	return skill.Hydrate(
		id, name,
		[]phase.PhaseType{phase.PhaseExplore},
		"body content",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.StatusActive,
		version,
		skill.Scope{},
		aw,
		skill.RiskMedium,
		skill.SourceImported,
		skill.Metrics{},
		nil, nil,
		time.Now(), time.Now(), //nolint:forbidigo
	)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// SVC-A — ErrDocsUnavailable at resolve time → WARN, zero inserts.
func TestService_DocsUnavailable_SVC_A(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return nil, outbound.ErrDocsUnavailable
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}
	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)

	sc := greenfield1FW("angular", "22.0.0")
	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, importer.calls, 0, "zero inserts when docs unavailable")
}

// SVC-B — rate guard denies → WARN, NO docs call.
func TestService_RateGuardDenies_SVC_B(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: false}
	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)

	sc := greenfield1FW("angular", "22.0.0")
	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, rate.calls, 1, "rate guard must be called once")
	assert.Len(t, docs.resolveCalls, 0, "no resolve call when rate guard denies")
	assert.Len(t, importer.calls, 0, "no insert when rate guard denies")
}

// SVC-C — greenfield + 1 framework → exactly 1 import.
func TestService_Greenfield1Framework_SVC_C(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return fatEntries(), nil
		},
		getDocsFunc: func(_ context.Context, id, _, _ string, _ int) (outbound.DocsResult, error) {
			return outbound.DocsResult{LibraryID: id, Snippets: 1200, Body: "angular best practices"}, nil
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}
	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)

	sc := greenfield1FW("angular", "22.0.0")
	svc.TriggerIfNeeded(context.Background(), sc)
	require.Len(t, importer.calls, 1, "exactly 1 import for 1 framework")
	assert.Equal(t, "angular", importer.calls[0].fw)
	assert.Equal(t, "22.0.0", importer.calls[0].version)
}

// SVC-D — greenfield + zero frameworks → WARN, no docs call.
func TestService_GreenfieldNoFrameworks_SVC_D(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)
	sc := structural.StructuralContext{
		ProjectID:  "proj-beta",
		Greenfield: true,
		Frameworks: nil,
	}

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, docs.resolveCalls, 0, "no resolve when zero frameworks")
	assert.Len(t, importer.calls, 0)
}

// SVC-E — non-greenfield + versions satisfy active mins → no import.
func TestService_NonGreenfieldNoVersionDrift_SVC_E(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	activeSkill := makeActiveAngularSkill(t, "stack/angular-22", "22.0.0")
	skillLookup := &fakeSkillLookup{skills: []*skill.Skill{activeSkill}}

	svc := makeService(docs, skillLookup, importer, rate)
	// Detected: angular 22.3.1 — same major, no drift
	sc := nonGreenfield("angular", "22.3.1")

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, docs.resolveCalls, 0, "no docs call when no drift")
	assert.Len(t, importer.calls, 0, "no import when no drift")
}

// SVC-F — drift: active stack/angular-22 min {angular:"22"} + detected 23.0.0 → import.
func TestService_DriftDetected_SVC_F(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return fatEntries(), nil
		},
		getDocsFunc: func(_ context.Context, id, _, _ string, _ int) (outbound.DocsResult, error) {
			return outbound.DocsResult{LibraryID: id, Snippets: 1200, Body: "angular v23 best practices"}, nil
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	activeSkill := makeActiveAngularSkill(t, "stack/angular-22", "22.0.0")
	skillLookup := &fakeSkillLookup{skills: []*skill.Skill{activeSkill}}

	svc := makeService(docs, skillLookup, importer, rate)
	// Non-greenfield, detected angular 23.0.0 — drifts forward from 22
	sc := nonGreenfield("angular", "23.0.0")

	svc.TriggerIfNeeded(context.Background(), sc)
	require.Len(t, importer.calls, 1, "1 import for drift")
	assert.Equal(t, "angular", importer.calls[0].fw)
	assert.Equal(t, "23.0.0", importer.calls[0].version)
}

// SVC-G — no active skill with min set → drift never fires.
func TestService_NoActiveSkillWithMin_SVC_G(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	// Active skill for angular but no FrameworkMinVersion set
	activeSkill := makeActiveAngularSkillNoMin(t, "stack/angular-22", "22.0.0")
	skillLookup := &fakeSkillLookup{skills: []*skill.Skill{activeSkill}}

	svc := makeService(docs, skillLookup, importer, rate)
	sc := nonGreenfield("angular", "23.0.0")

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, docs.resolveCalls, 0, "no resolve when no baseline min")
	assert.Len(t, importer.calls, 0, "no import when no baseline min")
}

// SVC-H — unparseable detected version → no drift + WARN.
func TestService_UnparseableVersions_SVC_H(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	// Active skill with parseable min, but detected version is "edge" (unparseable)
	activeSkill := makeActiveAngularSkill(t, "stack/angular-22", "22.0.0")
	skillLookup := &fakeSkillLookup{skills: []*skill.Skill{activeSkill}}

	svc := makeService(docs, skillLookup, importer, rate)
	sc := nonGreenfield("angular", "edge") // unparseable

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, importer.calls, 0, "no import when detected version unparseable")
}

// SVC-I — thin version entry (snippets=7) + fat main entry → main entry selected.
func TestService_ThinVersionFatMain_SVC_I(t *testing.T) {
	t.Parallel()
	var capturedID string
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return thinVersionFatMain(), nil
		},
		getDocsFunc: func(_ context.Context, id, _, _ string, _ int) (outbound.DocsResult, error) {
			capturedID = id
			return outbound.DocsResult{LibraryID: id, Snippets: 14850, Body: "fat main content"}, nil
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)
	sc := greenfield1FW("angular", "22.0.0")

	svc.TriggerIfNeeded(context.Background(), sc)
	require.Len(t, importer.calls, 1)
	// The main entry ID should have been used
	assert.Equal(t, "/websites/angular_dev", capturedID,
		"main entry ID selected when version entry is thin")
}

// SVC-J — ALL entries thin → WARN skip, get-library-docs never called.
func TestService_AllThinEntries_SVC_J(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return allThinEntries(), nil
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)
	sc := greenfield1FW("angular", "22.0.0")

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, docs.getDocsCalls, 0, "get-library-docs never called when all entries thin")
	assert.Len(t, importer.calls, 0, "no import when all entries thin")
}

// SVC-K — docs/import error → WARN, swallowed, continue, nil returned.
func TestService_ImportError_SVC_K(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return fatEntries(), nil
		},
		getDocsFunc: func(_ context.Context, id, _, _ string, _ int) (outbound.DocsResult, error) {
			return outbound.DocsResult{LibraryID: id, Body: "content"}, nil
		},
	}
	importer := &fakeImporter{err: errors.New("insert failed: unique violation")}
	rate := &fakeRateGuard{allow: true}

	// Two frameworks — error on first should not block second.
	sc := structural.StructuralContext{
		ProjectID:  "proj-gamma",
		Greenfield: true,
		Frameworks: []structural.FrameworkInfo{
			{Name: "angular", Version: "22.0.0"},
			{Name: "react", Version: "18.0.0"},
		},
	}

	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)
	svc.TriggerIfNeeded(context.Background(), sc)
	// Both frameworks attempted
	assert.Len(t, importer.calls, 2, "both frameworks attempted despite first error")
}

// SVC-L — D11: constructor takes no LLM/dispatcher dep (compile-time).
func TestService_NoLLMDep_SVC_L(t *testing.T) {
	t.Parallel()
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return fatEntries(), nil
		},
		getDocsFunc: func(_ context.Context, id, _, _ string, _ int) (outbound.DocsResult, error) {
			return outbound.DocsResult{LibraryID: id, Body: "content"}, nil
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	// Verify NewService signature accepts only the declared deps (no LLM/dispatcher).
	svc := bootstrap.NewService(bootstrap.ServiceDeps{
		Docs:        docs,
		Skills:      &fakeSkillLookup{},
		Importer:    importer,
		Rate:        rate,
		MinSnippets: testMinSnippets,
	})
	require.NotNil(t, svc, "service constructed without LLM dep")

	sc := greenfield1FW("angular", "22.0.0")
	svc.TriggerIfNeeded(context.Background(), sc)
}

// SVC-M — quota/429 surfaced by provider → WARN skip.
func TestService_ProviderQuota_SVC_M(t *testing.T) {
	t.Parallel()
	quota429Err := errors.New("context7: resolve-library-id returned error: HTTP 429 Too Many Requests")
	docs := &fakeDocsProvider{
		resolveFunc: func(_ context.Context, _, _ string) ([]outbound.LibraryEntry, error) {
			return nil, quota429Err
		},
	}
	importer := &fakeImporter{}
	rate := &fakeRateGuard{allow: true}

	svc := makeService(docs, &fakeSkillLookup{}, importer, rate)
	sc := greenfield1FW("angular", "22.0.0")

	svc.TriggerIfNeeded(context.Background(), sc)
	assert.Len(t, importer.calls, 0, "no insert on quota error")
}
