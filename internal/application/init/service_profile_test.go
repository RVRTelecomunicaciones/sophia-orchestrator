package initphase_test

// service_profile_test.go — P.1–P.3 (Strict TDD: RED tests first)
//
// Tests for InitService.Run ProfileExtractor integration:
//   P.1 nil ProfileExtractor → no panic, valid envelope emitted, Ingest NOT called
//       for type="convention_profile".
//   P.2 ProfileExtractor returns error → WARN logged, envelope emitted (non-fatal),
//       PersistProfile NOT called.
//   P.3 ProfileExtractor returns valid profile → PersistProfile called exactly once
//       with type="convention_profile" and correct topic key.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fakes for profile tests
// ─────────────────────────────────────────────────────────────────────────────

// fakeProfileExtractorP is a controllable ProfileExtractor for P.* tests.
type fakeProfileExtractorP struct {
	mu      sync.Mutex
	calls   int
	profile *convention.ConventionProfile
	err     error
}

func (f *fakeProfileExtractorP) Extract(_ context.Context, _ string, _ detector.StructuralContext) (*convention.ConventionProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.profile, f.err
}

// fakePersisterP wraps fakePersisterF and additionally records PersistProfile calls.
type fakePersisterP struct {
	mu                  sync.Mutex
	persistCalls        int
	persistProfileCalls int
	persistProfileType  []string // records type strings passed
	persistProfileKey   []string // records topic keys passed
	err                 error
	profileErr          error
}

func (f *fakePersisterP) Persist(_ context.Context, _ detector.StructuralContext, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistCalls++
	return f.err
}

func (f *fakePersisterP) PersistProfile(_ context.Context, profile convention.ConventionProfile) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persistProfileCalls++
	// record the topic key using projectID + framework
	key := "convention/" + profile.ProjectID() + "/" + profile.Framework()
	f.persistProfileKey = append(f.persistProfileKey, key)
	f.persistProfileType = append(f.persistProfileType, "convention_profile")
	return f.profileErr
}

// ─────────────────────────────────────────────────────────────────────────────
// Helper
// ─────────────────────────────────────────────────────────────────────────────

func buildProfileDeps(
	det initphase.SophiaDetector,
	spwn initphase.GraphifySpawner,
	pers initphase.StructuralPersister,
	profilePers initphase.ProfilePersister,
	cacheStore initphase.CacheStore,
	kb initphase.CacheKeyBuilder,
	pe initphase.ProfileExtractor,
) initphase.Deps {
	return initphase.Deps{
		Detector:         det,
		Spawner:          spwn,
		Persister:        pers,
		ProfilePersister: profilePers,
		Cache:            cacheStore,
		CacheKey:         kb,
		Clock:            shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)),
		IDGen:            shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5X01"}),
		CacheTTL:         24 * time.Hour,
		ProfileExtractor: pe,
	}
}

// minimalProfile builds a minimal valid ConventionProfile for use in test P.3.
func minimalProfile(t *testing.T) *convention.ConventionProfile {
	t.Helper()
	clock := shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
	p, err := convention.NewConventionProfile(
		"proj",
		"nestjs",
		"11",
		clock,
		[]convention.PatternEntry{
			{
				Pattern:    "nestjs-extends-crudservice",
				Source:     convention.SourceDetectedFromCode,
				Confidence: 0.85,
				Evidence:   []string{"motivo/motivo.service.ts"},
				Rule:       "Services MUST extend CrudService.",
			},
		},
	)
	require.NoError(t, err)
	return p
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// P.1: nil ProfileExtractor → no panic, valid envelope emitted, PersistProfile NOT called.
func TestInitService_NilProfileExtractor_GracefulSkip(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Languages: []detector.LanguageInfo{{Name: "TypeScript"}},
	}}
	spwn := &fakeSpawnerF{}
	structPers := &fakePersisterF{}
	profilePers := &fakePersisterP{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "p1-key"}

	svc := initphase.NewService(buildProfileDeps(det, spwn, structPers, profilePers, cacheStore, kb, nil))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err)
	require.NotNil(t, env, "envelope must be returned even without profile extractor")
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)

	profilePers.mu.Lock()
	ppCalls := profilePers.persistProfileCalls
	profilePers.mu.Unlock()
	require.Equal(t, 0, ppCalls, "PersistProfile must NOT be called when extractor is nil")
}

// P.2: ProfileExtractor returns error → WARN only; envelope emitted; PersistProfile NOT called.
func TestInitService_ProfileExtractorError_NonFatal(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Languages: []detector.LanguageInfo{{Name: "TypeScript"}},
	}}
	spwn := &fakeSpawnerF{}
	structPers := &fakePersisterF{}
	profilePers := &fakePersisterP{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "p2-key"}
	pe := &fakeProfileExtractorP{err: errors.New("profile: FS read denied")}

	svc := initphase.NewService(buildProfileDeps(det, spwn, structPers, profilePers, cacheStore, kb, pe))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err, "extractor error must be non-fatal")
	require.NotNil(t, env)
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)

	pe.mu.Lock()
	calls := pe.calls
	pe.mu.Unlock()
	require.Equal(t, 1, calls, "ProfileExtractor must have been called once")

	profilePers.mu.Lock()
	ppCalls := profilePers.persistProfileCalls
	profilePers.mu.Unlock()
	require.Equal(t, 0, ppCalls, "PersistProfile must NOT be called on extractor error")
}

// P.3: valid profile → PersistProfile called exactly once with correct topic key.
func TestInitService_ValidProfile_PersistCalled(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Frameworks: []detector.FrameworkInfo{{Name: "nestjs", Version: "11"}},
	}}
	spwn := &fakeSpawnerF{}
	structPers := &fakePersisterF{}
	profilePers := &fakePersisterP{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "p3-key"}
	profile := minimalProfile(t)
	pe := &fakeProfileExtractorP{profile: profile}

	svc := initphase.NewService(buildProfileDeps(det, spwn, structPers, profilePers, cacheStore, kb, pe))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err)
	require.NotNil(t, env)
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)

	pe.mu.Lock()
	peCalls := pe.calls
	pe.mu.Unlock()
	require.Equal(t, 1, peCalls, "ProfileExtractor must have been called once")

	profilePers.mu.Lock()
	ppCalls := profilePers.persistProfileCalls
	ppKeys := profilePers.persistProfileKey
	profilePers.mu.Unlock()
	require.Equal(t, 1, ppCalls, "PersistProfile must be called exactly once")
	require.Len(t, ppKeys, 1)
	require.True(t, strings.HasPrefix(ppKeys[0], "convention/"), "topic key must start with convention/")
}

// P.4: PersistProfile fails → WARN only; INIT envelope still emitted (soft failure).
func TestInitService_PersistProfileFails_NonFatal(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Frameworks: []detector.FrameworkInfo{{Name: "nestjs", Version: "11"}},
	}}
	spwn := &fakeSpawnerF{}
	structPers := &fakePersisterF{}
	profilePers := &fakePersisterP{profileErr: errors.New("memory-engine: 503")}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "p4-key"}
	profile := minimalProfile(t)
	pe := &fakeProfileExtractorP{profile: profile}

	svc := initphase.NewService(buildProfileDeps(det, spwn, structPers, profilePers, cacheStore, kb, pe))
	_, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err, "PersistProfile failure must be non-fatal")
	require.NotNil(t, env, "envelope must be returned even when PersistProfile fails")
}
