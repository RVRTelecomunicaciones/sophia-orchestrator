package apply_test

// metrics_spawn_throttled_test.go — Commit 2 TDD: SpawnGovernorThrottled.
//
// acquireWithSaturationRetries is a package-private function called by
// runImplementWithRetry. We verify SpawnGovernorThrottled increments by
// running Execute with a governor that saturates the first N calls then
// succeeds (matching the BUG-26 saturation test pattern).

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

func TestMetrics_SpawnGovernorThrottled_Increments(t *testing.T) {
	m := obs.NewMetrics()

	// Inject metrics into RunDeps via the WithMetrics option pattern.
	svc, _, _, spawn, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Metrics = m
	})

	// Saturate the first 3 Acquire calls (call 1 = team-lead, calls 2+3 = implementer
	// saturation retries) so acquireWithSaturationRetries retries twice.
	spawn.saturateUntilCall = 3

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	before := testutil.ToFloat64(m.SpawnGovernorThrottled.WithLabelValues("saturated"))
	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)
	require.NotNil(t, env)
	after := testutil.ToFloat64(m.SpawnGovernorThrottled.WithLabelValues("saturated"))

	// The implementer hit ErrSaturated at calls 2 and 3 → 2 throttle increments.
	require.Greater(t, after, before,
		"SpawnGovernorThrottled should increment when Acquire returns ErrSaturated")
}
