package change_test

// metrics_test.go — Commit 4 TDD: ChangesTotal + ChangesInFlight.
//
// change/service.go already wires these metrics. These tests verify
// the wiring is correct and observable.

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	appchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

func newSvcWithMetrics(t *testing.T, m *obs.Metrics) *appchange.Service {
	t.Helper()
	repo := newFakeRepo()
	clk := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	idGen := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5D01",
		"01ARZ3NDEKTSV4RRFFQ69G5D02",
	})
	return appchange.New(repo, clk, idGen).WithMetrics(m)
}

func TestMetrics_ChangesTotal_IncrementOnCreate(t *testing.T) {
	m := obs.NewMetrics()
	svc := newSvcWithMetrics(t, m)

	before := testutil.ToFloat64(m.ChangesTotal.WithLabelValues("proj", string(domainchange.StatusActive)))
	_, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name:              "feat-z",
		Project:           "proj",
		ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
		BaseRef:           "main",
	})
	require.NoError(t, err)
	after := testutil.ToFloat64(m.ChangesTotal.WithLabelValues("proj", string(domainchange.StatusActive)))

	require.Equal(t, before+1, after,
		"ChangesTotal{project=proj, status=active} should increment on Create")
}

func TestMetrics_ChangesInFlight_IncOnCreate_DecOnAbort(t *testing.T) {
	m := obs.NewMetrics()
	svc := newSvcWithMetrics(t, m)

	beforeCreate := testutil.ToFloat64(m.ChangesInFlight)
	c, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name:              "feat-abort",
		Project:           "proj",
		ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
		BaseRef:           "main",
	})
	require.NoError(t, err)
	afterCreate := testutil.ToFloat64(m.ChangesInFlight)
	require.Equal(t, beforeCreate+1, afterCreate,
		"ChangesInFlight should increment on Create")

	err = svc.Abort(context.Background(), c.ID(), "test abort")
	require.NoError(t, err)
	afterAbort := testutil.ToFloat64(m.ChangesInFlight)
	require.Equal(t, afterCreate-1, afterAbort,
		"ChangesInFlight should decrement on Abort")
}

func TestMetrics_ChangesTotal_IncrementOnAbort(t *testing.T) {
	m := obs.NewMetrics()
	svc := newSvcWithMetrics(t, m)

	c, err := svc.Create(context.Background(), inbound.CreateChangeInput{
		Name:              "feat-abort2",
		Project:           "proj",
		ArtifactStoreMode: domainchange.ArtifactStoreMemoryEngine,
		BaseRef:           "main",
	})
	require.NoError(t, err)

	// After abort, ChangesTotal should gain an "aborted" entry.
	beforeAbort := testutil.ToFloat64(m.ChangesTotal.WithLabelValues("proj", string(domainchange.StatusAborted)))
	err = svc.Abort(context.Background(), c.ID(), "test abort")
	require.NoError(t, err)
	afterAbort := testutil.ToFloat64(m.ChangesTotal.WithLabelValues("proj", string(domainchange.StatusAborted)))

	require.Equal(t, beforeAbort+1, afterAbort,
		"ChangesTotal{project=proj, status=aborted} should increment on Abort")
}
