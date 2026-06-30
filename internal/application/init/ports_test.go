package initphase_test

// ports_test.go — B.3 (Strict TDD: RED test first)
//
// Compile-time test verifying that the five core port interfaces are defined
// in the initphase package and that a fake can satisfy them. This test fails
// to compile (and therefore fails RED) until ports.go is created.

import (
	"context"
	"testing"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/stretchr/testify/require"
)

// fakeProfileExtractorB3 satisfies ProfileExtractor.
type fakeProfileExtractorB3 struct{}

func (f *fakeProfileExtractorB3) Extract(_ context.Context, _ string, _ detector.StructuralContext) (*convention.ConventionProfile, error) {
	return nil, nil
}

// fakeDetectorB3 satisfies SophiaDetector.
type fakeDetectorB3 struct{}

func (f *fakeDetectorB3) Detect(_ context.Context, _ string) (initphase.DetectorResult, error) {
	return initphase.DetectorResult{}, nil
}

// fakeSpawnerB3 satisfies GraphifySpawner.
type fakeSpawnerB3 struct{}

func (f *fakeSpawnerB3) Build(_ context.Context, _ string) (*detector.GraphSummary, string, error) {
	return nil, "", nil
}

// fakePersisterB3 satisfies StructuralPersister.
type fakePersisterB3 struct{}

func (f *fakePersisterB3) Persist(_ context.Context, _ detector.StructuralContext, _ string) error {
	return nil
}

// fakeCacheB3 satisfies CacheStore.
type fakeCacheB3 struct{}

func (f *fakeCacheB3) Lookup(_ context.Context, _ string) (*detector.StructuralContext, bool, error) {
	return nil, false, nil
}

func (f *fakeCacheB3) Write(_ context.Context, _ string, _ detector.StructuralContext) error {
	return nil
}

// fakeKeyBuilderB3 satisfies CacheKeyBuilder.
type fakeKeyBuilderB3 struct{}

func (f *fakeKeyBuilderB3) Build(_ context.Context, _, _ string) (string, error) {
	return "fake-key", nil
}

// TestPortInterfaces verifies the interfaces compile and can be assigned.
func TestPortInterfaces(t *testing.T) {
	var _ initphase.SophiaDetector = &fakeDetectorB3{}
	var _ initphase.GraphifySpawner = &fakeSpawnerB3{}
	var _ initphase.StructuralPersister = &fakePersisterB3{}
	var _ initphase.CacheStore = &fakeCacheB3{}
	var _ initphase.CacheKeyBuilder = &fakeKeyBuilderB3{}

	// ProfileExtractor interface is satisfied by fakeProfileExtractorB3.
	var _ initphase.ProfileExtractor = &fakeProfileExtractorB3{}

	// Sentinel errors exist.
	require.NotNil(t, initphase.ErrGraphifyDegraded)
	require.NotNil(t, initphase.ErrCacheMiss)
	require.NotNil(t, initphase.ErrSchemaVersionMismatch)
}
