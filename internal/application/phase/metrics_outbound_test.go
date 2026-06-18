package phase_test

// metrics_outbound_test.go — Commit 1 TDD: outbound call counters.
//
// Exercises: DispatcherCallsTotal, DispatcherCallDurationMS,
// GovernanceCallsTotal, MemoryCallsTotal.
//
// Pattern: each test wires obs.NewMetrics() into the phase service,
// runs a happy-path phase, and asserts the counter moved from 0.
// Uses prometheus/testutil against the private registry via m.Registry().

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// newHarnessWithMetrics returns a service harness with a live Metrics set.
func newHarnessWithMetrics(t *testing.T) (*harness, *obs.Metrics) {
	t.Helper()
	m := obs.NewMetrics()
	h := newHarness(t)
	h.svc = rebuildServiceWithMetrics(t, h, m)
	return h, m
}

// rebuildServiceWithMetrics reconstructs the Service with the given metrics handle
// but reusing all the fakes from h.
func rebuildServiceWithMetrics(t *testing.T, h *harness, m *obs.Metrics) *appphase.Service {
	t.Helper()

	idGen := shared_idgen_for_harness(t)
	return appphase.New(appphase.Deps{
		ChangeRepo:  h.changeRepo,
		PhaseRepo:   h.phaseRepo,
		SessionRepo: h.sessRepo,
		Governance:  h.governance,
		Memory:      h.memory,
		Dispatcher:  h.dispatcher,
		SpawnGov:    h.spawn,
		Validator:   disciplineValidator(),
		IronLaw:     disciplineIronLaw(),
		Prompts:     disciplinePrompts(),
		Audit:       h.audit,
		Events:      h.events,
		Clock:       h.clock,
		IDGen:       idGen,
		Scheduler:   appphase.SyncScheduler,
		Metrics:     m,
	})
}

// runHappyPhase exercises the spec phase end-to-end using the harness.
// Returns the phase ID from Run so tests can do further queries.
func runHappyPhase(t *testing.T, h *harness) {
	t.Helper()
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "draft spec",
		RetryBudget:     3,
	})
	require.NoError(t, err)
}

func TestMetrics_DispatcherCallsTotal_Increments(t *testing.T) {
	h, m := newHarnessWithMetrics(t)

	before := testutil.ToFloat64(m.DispatcherCallsTotal.WithLabelValues("opencode", "ok"))
	runHappyPhase(t, h)
	after := testutil.ToFloat64(m.DispatcherCallsTotal.WithLabelValues("opencode", "ok"))

	require.Greater(t, after, before, "DispatcherCallsTotal should increment after a dispatch")
}

func TestMetrics_DispatcherCallDurationMS_Records(t *testing.T) {
	h, m := newHarnessWithMetrics(t)

	countBefore := testutil.CollectAndCount(m.DispatcherCallDurationMS)
	runHappyPhase(t, h)
	countAfter := testutil.CollectAndCount(m.DispatcherCallDurationMS)

	// A histogram emits at least one series when observed.
	require.GreaterOrEqual(t, countAfter, countBefore+1,
		"DispatcherCallDurationMS should gain at least one histogram series after dispatch")
}

func TestMetrics_GovernanceCallsTotal_Increments(t *testing.T) {
	h, m := newHarnessWithMetrics(t)

	before := testutil.ToFloat64(m.GovernanceCallsTotal.WithLabelValues("evaluate_phase", "ok"))
	runHappyPhase(t, h)
	after := testutil.ToFloat64(m.GovernanceCallsTotal.WithLabelValues("evaluate_phase", "ok"))

	require.Greater(t, after, before, "GovernanceCallsTotal should increment after a governance call")
}

func TestMetrics_MemoryCallsTotal_BuildContext_Increments(t *testing.T) {
	h, m := newHarnessWithMetrics(t)

	before := testutil.ToFloat64(m.MemoryCallsTotal.WithLabelValues("build_context", "ok"))
	runHappyPhase(t, h)
	after := testutil.ToFloat64(m.MemoryCallsTotal.WithLabelValues("build_context", "ok"))

	require.Greater(t, after, before, "MemoryCallsTotal{op=build_context} should increment")
}

func TestMetrics_MemoryCallsTotal_Ingest_Increments(t *testing.T) {
	m := obs.NewMetrics()
	h := newHarness(t)
	// Override dispatcher result to return an envelope with artifacts_saved
	// so persistArtifactsToMemory calls Ingest at least once.
	h.dispatcher.result = &outbound.DispatchResult{
		ExitCode:    0,
		Stdout:      []byte{},
		EnvelopeRaw: mustEnvelopeWithArtifacts(t, phase.PhaseSpec),
	}
	h.svc = rebuildServiceWithMetrics(t, h, m)

	before := testutil.ToFloat64(m.MemoryCallsTotal.WithLabelValues("ingest", "ok"))
	runHappyPhase(t, h)
	after := testutil.ToFloat64(m.MemoryCallsTotal.WithLabelValues("ingest", "ok"))

	require.Greater(t, after, before, "MemoryCallsTotal{op=ingest} should increment when artifacts are persisted")
}

// mustEnvelopeWithArtifacts returns an envelope with one artifact_saved so
// persistArtifactsToMemory calls Ingest at least once.
// Note: Status must be uppercase ("DONE") as required by envelope.StatusDone.
func mustEnvelopeWithArtifacts(t *testing.T, pt phase.PhaseType) []byte {
	t.Helper()
	body := map[string]any{
		"schema_version":    "v1",
		"phase":             string(pt),
		"change_name":       "feat-x",
		"project":           "proj",
		"status":            "DONE",
		"confidence":        0.85,
		"executive_summary": "ok",
		"artifacts_saved": []map[string]any{
			{"topic_key": "sdd/feat-x/spec", "type": "sdd_spec"},
		},
		"next_recommended": []string{},
		"risks":            []map[string]string{},
		"data":             map[string]any{},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return raw
}

// --- helpers to avoid full imports in this test file ---

// These are thin wrappers over discipline constructors used by newHarness.
// newHarness in service_test.go is unexported (lowercase) and typed via *harness;
// we share the same test binary so shared_idgen_for_harness below is not needed
// if we reuse newHarness directly. Keeping the helpers for clarity.
func disciplineValidator() *discipline.Validator { return discipline.NewValidator() }
func disciplineIronLaw() *discipline.IronLawChecker { return discipline.NewIronLawChecker() }
func disciplinePrompts() *discipline.PromptBuilder  { return discipline.NewPromptBuilder() }

// shared_idgen_for_harness returns a fresh fixed ID generator.
func shared_idgen_for_harness(t *testing.T) shared.IDGenerator {
	t.Helper()
	return shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5P01",
		"01ARZ3NDEKTSV4RRFFQ69G5S01",
		"01ARZ3NDEKTSV4RRFFQ69G5P02",
		"01ARZ3NDEKTSV4RRFFQ69G5S02",
	})
}
