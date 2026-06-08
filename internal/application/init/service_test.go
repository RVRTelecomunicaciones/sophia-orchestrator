package initphase_test

// service_test.go — F.1–F.5 (Strict TDD: RED tests first)
//
// Tests for InitService.Run: cache hit, cache miss with parallel detect+spawn,
// degraded mode, detector hard fail, persister fail.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

// --- fakes for service tests ---

type fakeDetectorF struct {
	mu    sync.Mutex
	calls int
	result initphase.DetectorResult
	err   error
}

func (f *fakeDetectorF) Detect(_ context.Context, _ string) (initphase.DetectorResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.result, f.err
}

type fakeSpawnerF struct {
	mu    sync.Mutex
	calls int
	summary *detector.GraphSummary
	version string
	err    error
}

func (f *fakeSpawnerF) Build(_ context.Context, _ string) (*detector.GraphSummary, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.summary, f.version, f.err
}

type fakePersisterF struct {
	calls int32 // atomic for concurrent access
	err   error
}

func (f *fakePersisterF) Persist(_ context.Context, _ detector.StructuralContext, _ string) error {
	atomic.AddInt32(&f.calls, 1)
	return f.err
}

type fakeCacheF struct {
	mu         sync.Mutex
	lookupHit  bool
	lookupSC   *detector.StructuralContext
	lookupErr  error
	writeCalls int
}

func (f *fakeCacheF) Lookup(_ context.Context, _ string) (*detector.StructuralContext, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lookupSC, f.lookupHit, f.lookupErr
}

func (f *fakeCacheF) Write(_ context.Context, _ string, _ detector.StructuralContext) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writeCalls++
	return nil
}

type fakeKeyBuilderF struct {
	key string
	err error
}

func (f *fakeKeyBuilderF) Build(_ context.Context, _, _ string) (string, error) {
	return f.key, f.err
}

// buildChange constructs a minimal change.Change for InitService tests.
func buildChange(t *testing.T) *change.Change {
	t.Helper()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	c, err := change.New(cid, "init-test", "proj", change.ArtifactStoreMemoryEngine, "main", time.Now())
	require.NoError(t, err)
	return c
}

// buildDeps builds initphase.Deps with the given fakes.
func buildDeps(det initphase.SophiaDetector, spwn initphase.GraphifySpawner, pers initphase.StructuralPersister, cacheStore initphase.CacheStore, kb initphase.CacheKeyBuilder) initphase.Deps {
	return initphase.Deps{
		Detector:  det,
		Spawner:   spwn,
		Persister: pers,
		Cache:     cacheStore,
		CacheKey:  kb,
		Clock:     shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)),
		IDGen:     shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5X01"}),
		CacheTTL:  24 * time.Hour,
	}
}

// --- tests ---

// F.1: InitService.Run on cache hit returns cached StructuralContext WITHOUT calling
// SophiaDetector or GraphifySpawner.
func TestInitService_CacheHit_SkipsDetection(t *testing.T) {
	cached := &detector.StructuralContext{
		SchemaVersion:  detector.StructuralContextSchemaV1,
		ChangeName:     "init-test",
		GraphAvailable: true,
	}

	det := &fakeDetectorF{}
	spwn := &fakeSpawnerF{}
	pers := &fakePersisterF{}
	cacheStore := &fakeCacheF{lookupHit: true, lookupSC: cached}
	kb := &fakeKeyBuilderF{key: "hit-key"}

	svc := initphase.NewService(buildDeps(det, spwn, pers, cacheStore, kb))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err)
	require.NotNil(t, env, "envelope must be returned even on cache hit")
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)
	require.Equal(t, 0, det.calls, "Detector must NOT be called on cache hit")
	require.Equal(t, 0, spwn.calls, "Spawner must NOT be called on cache hit")
}

// F.2: InitService.Run on cache miss runs detector + spawner via errgroup in
// parallel; merges into StructuralContext{SchemaVersion=1}; calls Persister.
func TestInitService_CacheMiss_RunsDetectionAndSpawn(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Languages: []detector.LanguageInfo{{Name: "Go"}},
	}}
	spwn := &fakeSpawnerF{
		summary: &detector.GraphSummary{TotalNodes: 10, TotalEdges: 20},
		version: "0.8.35",
	}
	pers := &fakePersisterF{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "miss-key"}

	svc := initphase.NewService(buildDeps(det, spwn, pers, cacheStore, kb))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err)
	require.NotNil(t, env)
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)
	require.Equal(t, 1, det.calls, "Detector must be called once on cache miss")
	require.Equal(t, 1, spwn.calls, "Spawner must be called once on cache miss")
	require.NotEmpty(t, sc.Languages, "Languages must be merged from detector result")
	require.NotNil(t, sc.GraphSummary, "GraphSummary must be populated from spawner")
	require.True(t, sc.GraphAvailable)
	require.Equal(t, int32(1), atomic.LoadInt32(&pers.calls), "Persister must be called once")
}

// F.3: InitService.Run when spawner returns ErrGraphifyDegraded → StructuralContext
// has GraphAvailable=false, DegradedReason populated; phase still completes (no error).
func TestInitService_SpawnerDegraded(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Languages: []detector.LanguageInfo{{Name: "Go"}},
	}}
	spwn := &fakeSpawnerF{err: fmt.Errorf("%w: graphify not found", initphase.ErrGraphifyDegraded)}
	pers := &fakePersisterF{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "degraded-key"}

	svc := initphase.NewService(buildDeps(det, spwn, pers, cacheStore, kb))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err, "spawner degraded must NOT return an error")
	require.NotNil(t, env)
	require.False(t, sc.GraphAvailable, "GraphAvailable must be false in degraded mode")
	require.NotEmpty(t, sc.DegradedReason, "DegradedReason must be populated")
	require.Equal(t, int32(1), atomic.LoadInt32(&pers.calls), "Persister must still be called in degraded mode")
}

// F.4: InitService.Run when detector returns error → propagates as HARD error.
func TestInitService_DetectorHardFail(t *testing.T) {
	hardErr := errors.New("detector: permission denied")
	det := &fakeDetectorF{err: hardErr}
	spwn := &fakeSpawnerF{}
	pers := &fakePersisterF{}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "hard-key"}

	svc := initphase.NewService(buildDeps(det, spwn, pers, cacheStore, kb))
	_, _, err := svc.Run(context.Background(), buildChange(t))

	require.Error(t, err, "detector hard fail must return error")
}

// F.5: InitService.Run when persister returns error → logs WARN; INIT completes;
// returns StructuralContext (persister failure is non-fatal to phase completion).
func TestInitService_PersisterFail_NonFatal(t *testing.T) {
	det := &fakeDetectorF{result: initphase.DetectorResult{
		Languages: []detector.LanguageInfo{{Name: "Go"}},
	}}
	spwn := &fakeSpawnerF{version: "0.8.35"}
	pers := &fakePersisterF{err: errors.New("memory-engine 503")}
	cacheStore := &fakeCacheF{lookupErr: initphase.ErrCacheMiss}
	kb := &fakeKeyBuilderF{key: "persist-fail-key"}

	svc := initphase.NewService(buildDeps(det, spwn, pers, cacheStore, kb))
	sc, env, err := svc.Run(context.Background(), buildChange(t))

	require.NoError(t, err, "persister failure must be non-fatal")
	require.NotNil(t, env)
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)
}

// keep fmt imported
var _ = fmt.Sprintf
