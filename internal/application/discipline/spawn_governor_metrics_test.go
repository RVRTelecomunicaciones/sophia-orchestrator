package discipline_test

// spawn_governor_metrics_test.go — Commit 2 TDD: SpawnGovernor metrics.
//
// Exercises: SpawnGovernorWaitMS, SpawnGovernorActive (inc/dec lifecycle).
// SpawnGovernorThrottled is tested in apply/teamlead_metrics_test.go
// because acquireWithSaturationRetries lives there.

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
)

// Compile-time check that testutil is used.
var _ = testutil.ToFloat64

func newSGWithMetrics(t *testing.T, m *obs.Metrics) (*discipline.SpawnGovernor, *fakeWaiter, *fakeSleeper) {
	t.Helper()
	repo := &fakeRepo{acquireResp: []bool{true}}
	clk := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	cfg := discipline.SpawnGovernorConfig{
		Max:          4,
		StaggerMin:   0,
		StaggerMax:   0,
		WaitInterval: 50 * time.Millisecond,
		MaxWait:      30 * time.Second,
	}
	sg, err := discipline.NewSpawnGovernor(repo, cfg, clk, m)
	require.NoError(t, err)
	w := &fakeWaiter{}
	s := &fakeSleeper{}
	sg.WithDeps(w, s, fixedJitter{d: 0})
	return sg, w, s
}

func TestSpawnGovernor_Active_IncOnAcquire(t *testing.T) {
	m := obs.NewMetrics()
	sg, _, _ := newSGWithMetrics(t, m)

	before := testutil.ToFloat64(m.SpawnGovernorActive)
	require.NoError(t, sg.Acquire(context.Background()))
	after := testutil.ToFloat64(m.SpawnGovernorActive)

	require.Equal(t, before+1, after, "SpawnGovernorActive should increment on Acquire")
}

func TestSpawnGovernor_Active_DecOnRelease(t *testing.T) {
	m := obs.NewMetrics()
	sg, _, _ := newSGWithMetrics(t, m)

	require.NoError(t, sg.Acquire(context.Background()))
	afterAcquire := testutil.ToFloat64(m.SpawnGovernorActive)
	require.NoError(t, sg.Release(context.Background()))
	afterRelease := testutil.ToFloat64(m.SpawnGovernorActive)

	require.Equal(t, afterAcquire-1, afterRelease, "SpawnGovernorActive should decrement on Release")
}

func TestSpawnGovernor_WaitMS_Records(t *testing.T) {
	m := obs.NewMetrics()
	sg, _, _ := newSGWithMetrics(t, m)

	// Use testutil.ToFloat64 on _count suffix: before first observation it is 0.
	// After Acquire it should be 1 (one observation recorded).
	//
	// We can't directly introspect sample count via testutil, so we verify
	// the histogram was observed using the Registry output via ToFloat64 on
	// the sum (which stays 0 if nothing was observed, but has a defined value
	// after at least one observation — even if waitMS is 0).
	//
	// Simplest approach: assert CollectAndCount increases, noting the histogram
	// always emits its families but the _count changes from 0 to 1.
	// Use prometheus.MustRegisterCollector: nope — just check sum > -1.
	//
	// Actually: use the fact that we can call Gather on the private registry
	// to check SampleCount.
	reg := m.Registry()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	var beforeCount uint64
	for _, mf := range mfs {
		if mf.GetName() == "sophia_orchestator_spawn_governor_wait_ms" {
			for _, metric := range mf.GetMetric() {
				beforeCount = metric.GetHistogram().GetSampleCount()
			}
		}
	}

	require.NoError(t, sg.Acquire(context.Background()))

	mfs, err = reg.Gather()
	require.NoError(t, err)
	var afterCount uint64
	for _, mf := range mfs {
		if mf.GetName() == "sophia_orchestator_spawn_governor_wait_ms" {
			for _, metric := range mf.GetMetric() {
				afterCount = metric.GetHistogram().GetSampleCount()
			}
		}
	}
	require.Equal(t, beforeCount+1, afterCount, "SpawnGovernorWaitMS should record exactly one observation after Acquire")
}

func TestSpawnGovernor_NilMetrics_NoOp(t *testing.T) {
	// Verify nil metrics don't panic (backward compat with test doubles).
	repo := &fakeRepo{acquireResp: []bool{true}}
	clk := shared.FixedClock(time.Now())
	sg, err := discipline.NewSpawnGovernor(repo, discipline.DefaultConfig(), clk, nil)
	require.NoError(t, err)
	w := &fakeWaiter{}
	s := &fakeSleeper{}
	sg.WithDeps(w, s, fixedJitter{d: 0})
	require.NoError(t, sg.Acquire(context.Background()))
	require.NoError(t, sg.Release(context.Background()))
}
