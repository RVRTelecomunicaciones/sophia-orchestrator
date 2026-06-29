package skill_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// fakeAuditRepo is an in-memory ReevalAuditRepository for unit tests.
type fakeAuditRepo struct {
	runs                    map[string]outbound.ReevalRun
	order                   []string // insertion order, newest last
	saveErr                 error
	existsByRevertsRunID    map[string]bool // keyed by originalRunID
	existsByRevertsRunIDErr error
}

func newFakeAuditRepo() *fakeAuditRepo {
	return &fakeAuditRepo{
		runs:                 map[string]outbound.ReevalRun{},
		existsByRevertsRunID: map[string]bool{},
	}
}

func (f *fakeAuditRepo) Save(_ context.Context, run outbound.ReevalRun) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if _, ok := f.runs[run.ID]; !ok {
		f.order = append(f.order, run.ID)
	}
	f.runs[run.ID] = run
	return nil
}

func (f *fakeAuditRepo) FindByID(_ context.Context, runID string) (outbound.ReevalRun, error) {
	r, ok := f.runs[runID]
	if !ok {
		return outbound.ReevalRun{}, outbound.ErrNotFound
	}
	return r, nil
}

func (f *fakeAuditRepo) FindLatest(_ context.Context) (outbound.ReevalRun, error) {
	if len(f.order) == 0 {
		return outbound.ReevalRun{}, outbound.ErrNotFound
	}
	return f.runs[f.order[len(f.order)-1]], nil
}

func (f *fakeAuditRepo) ExistsByRevertsRunID(_ context.Context, originalRunID string) (bool, error) {
	if f.existsByRevertsRunIDErr != nil {
		return false, f.existsByRevertsRunIDErr
	}
	return f.existsByRevertsRunID[originalRunID], nil
}

// fakeMetricsPatcher captures PatchMetrics calls for assertion.
type fakeMetricsPatcher struct {
	calls []metricsCall
	err   error
}

type metricsCall struct {
	skillID string
	delta   inbound.MetricsDelta
}

func (f *fakeMetricsPatcher) PatchMetrics(_ context.Context, skillID string, delta inbound.MetricsDelta) error {
	if f.err != nil {
		return f.err
	}
	f.calls = append(f.calls, metricsCall{skillID: skillID, delta: delta})
	return nil
}

// TestReevaluator_RevertRun_NilMetricsPatcher verifies that when a Reevaluator
// is built WITHOUT a MetricsPatcher (dry-run constructor path), revertRun
// completes without panic and makes no PatchMetrics calls.
//
// WU-2 RED: compiles only after MetricsPatcher interface + Reevaluator field exist.
func TestReevaluator_RevertRun_NilMetricsPatcher(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID:   "RUN0000000000000000000001",
		Mode: "apply",
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusDeprecated,
	})
	// NewReevaluatorWithAudit without a MetricsPatcher — nil patcher must not panic.
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"}),
		nil, // nil MetricsPatcher: emission must be skipped without panic
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.True(t, result[0].Reverted, "revert must succeed without a MetricsPatcher")
}

// chainPatcher tracks current status per skill and enforces the real
// allowedTransitions guard so multi-hop walks are exercised end to end.
type chainPatcher struct {
	status map[string]domainskill.Status
	calls  []patchCall
	// failOn forces an error when a hop targets the given status, simulating a
	// mid-chain failure so partial-walk auditing can be exercised.
	failOn map[domainskill.Status]error
}

func newChainPatcher(initial map[string]domainskill.Status) *chainPatcher {
	cp := &chainPatcher{status: map[string]domainskill.Status{}, failOn: map[domainskill.Status]error{}}
	for k, v := range initial {
		cp.status[k] = v
	}
	return cp
}

// CurrentStatus exposes the live tracked status so revert can compute the path
// from the skill's actual current status (idempotency), not the recorded one.
func (c *chainPatcher) CurrentStatus(_ context.Context, skillID string) (domainskill.Status, error) {
	return c.status[skillID], nil
}

// allowed mirrors service.go allowedTransitions for the test guard.
var allowed = map[domainskill.Status]map[domainskill.Status]bool{
	domainskill.StatusCandidate:  {domainskill.StatusValidated: true, domainskill.StatusBlocked: true},
	domainskill.StatusValidated:  {domainskill.StatusActive: true, domainskill.StatusBlocked: true},
	domainskill.StatusActive:     {domainskill.StatusDeprecated: true, domainskill.StatusBlocked: true},
	domainskill.StatusDeprecated: {domainskill.StatusBlocked: true},
	domainskill.StatusBlocked:    {domainskill.StatusCandidate: true},
	domainskill.StatusArchived:   {},
}

func (c *chainPatcher) PatchStatus(_ context.Context, skillID, status, _ string) error {
	cur := c.status[skillID]
	next := domainskill.Status(status)
	if err := c.failOn[next]; err != nil {
		return err
	}
	if !allowed[cur][next] {
		return skillapp.ErrForbiddenStatusTransition
	}
	c.status[skillID] = next
	c.calls = append(c.calls, patchCall{skillID: skillID, status: status})
	return nil
}

func fixedClockAt(t time.Time) shared.Clock { return shared.FixedClock(t) }

// J.1 RED — confirm persists an apply audit run.

// TestApply_PersistsAuditRunOnConfirm verifies that --apply --confirm records a
// reeval-run snapshot with one item per applied transition (prior/new status).
func TestApply_PersistsAuditRunOnConfirm(t *testing.T) {
	audit := newFakeAuditRepo()
	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusActive, // attempts=3 → demote → deprecated
	})
	clk := fixedClockAt(time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC))
	idgen := shared.FixedIDGenerator([]string{"RUN0000000000000000000001", "ITEM000000000000000000001"})

	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{rows: []skillapp.Evidence{ev(testSkillID1, domainskill.StatusActive, 0.333, 3)}},
		patcher, audit, clk, idgen, nil,
	)

	report, err := r.Apply(context.Background(), true)
	require.NoError(t, err)
	require.Len(t, report, 1)
	assert.True(t, report[0].Applied)

	run, err := audit.FindLatest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "RUN0000000000000000000001", run.ID)
	assert.Equal(t, "apply", run.Mode)
	assert.Equal(t, clk.Now(), run.CreatedAt)
	require.Len(t, run.Items, 1)
	assert.Equal(t, testSkillID1, run.Items[0].SkillID)
	assert.Equal(t, string(domainskill.StatusActive), run.Items[0].PriorStatus)
	assert.Equal(t, string(domainskill.StatusDeprecated), run.Items[0].NewStatus)
}

// TestApply_NoConfirmRecordsNoAudit verifies dry-run never writes an audit run.
func TestApply_NoConfirmRecordsNoAudit(t *testing.T) {
	audit := newFakeAuditRepo()
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{rows: []skillapp.Evidence{ev(testSkillID1, domainskill.StatusActive, 0.333, 3)}},
		newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusActive}),
		audit,
		fixedClockAt(time.Now()),
		shared.FixedIDGenerator([]string{"X"}),
		nil,
	)

	_, err := r.Apply(context.Background(), false)
	require.NoError(t, err)
	_, err = audit.FindLatest(context.Background())
	assert.ErrorIs(t, err, outbound.ErrNotFound, "dry-run must not persist an audit run")
}

// K.1 RED — revert reverses to the prior status (multi-hop).

// TestRevert_ReversesToPriorStatusMultiHop verifies a recorded promotion
// (validated→active) is reversed back to validated. validated→active inverse is
// active→validated which is multi-hop (active→blocked→candidate→validated).
func TestRevert_ReversesToPriorStatusMultiHop(t *testing.T) {
	audit := newFakeAuditRepo()
	// Seed an apply run: skill1 promoted validated→active.
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "validated", NewStatus: "active"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusActive})
	idgen := shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"})

	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)), idgen, nil,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.True(t, result[0].Reverted)
	assert.Equal(t, domainskill.StatusValidated, patcher.status[testSkillID1],
		"skill must be walked back to its prior status")
}

// K.2 RED — revert walks the multi-hop chain where the direct inverse is forbidden.

// TestRevert_WalksMultiHopChain verifies a recorded demotion (active→deprecated)
// is reverted via the legal chain deprecated→blocked→candidate→validated→active.
func TestRevert_WalksMultiHopChain(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusDeprecated})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"}),
		nil,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.True(t, result[0].Reverted)
	assert.Equal(t, domainskill.StatusActive, patcher.status[testSkillID1])

	// The walk performed the full legal chain of PatchStatus hops.
	wantHops := []string{"blocked", "candidate", "validated", "active"}
	gotHops := make([]string, 0, len(patcher.calls))
	for _, c := range patcher.calls {
		gotHops = append(gotHops, c.status)
	}
	assert.Equal(t, wantHops, gotHops, "revert must walk the full legal chain via PatchStatus")
}

// K.3 RED — revert skips and reports when no legal path exists.

// TestRevert_SkipsWhenNoLegalPath verifies that when the prior status is
// unreachable (archived is terminal), the skill is skipped and reported, never
// forced through a raw write.
func TestRevert_SkipsWhenNoLegalPath(t *testing.T) {
	audit := newFakeAuditRepo()
	// Recorded transition: skill moved active→archived (hypothetical); reverting
	// archived→active is impossible (archived is terminal in the guard).
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "archived"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusArchived})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002"}),
		nil,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.False(t, result[0].Reverted, "no legal path → not reverted")
	assert.True(t, result[0].Skipped)
	require.Error(t, result[0].RevertErr)
	assert.Empty(t, patcher.calls, "no raw write may bypass the guard")
}

// K.4 RED — revert is itself audited.

// TestRevert_IsItselfAudited verifies the revert records a new run with
// mode='revert' referencing the original apply run.
func TestRevert_IsItselfAudited(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusDeprecated})
	clk := fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC))
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit, clk,
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"}),
		nil,
	)

	_, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)

	revRun, err := audit.FindLatest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "RUN0000000000000000000002", revRun.ID)
	assert.Equal(t, "revert", revRun.Mode)
	assert.Equal(t, "RUN0000000000000000000001", revRun.RevertsRunID)
	assert.Equal(t, clk.Now(), revRun.CreatedAt)
	require.Len(t, revRun.Items, 1)
	// The revert item's prior/new is the INVERSE of the original.
	assert.Equal(t, "deprecated", revRun.Items[0].PriorStatus)
	assert.Equal(t, "active", revRun.Items[0].NewStatus)
}

// TestRevert_UnknownRunIDErrors verifies an unknown run id surfaces ErrNotFound.
func TestRevert_UnknownRunIDErrors(t *testing.T) {
	audit := newFakeAuditRepo()
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, newChainPatcher(nil), audit,
		fixedClockAt(time.Now()), shared.FixedIDGenerator([]string{"X"}),
		nil,
	)
	_, err := r.Revert(context.Background(), "RUNDOESNOTEXIST0000000001")
	assert.True(t, errors.Is(err, outbound.ErrNotFound))
}

// TestRevertLast_UsesLatestRun verifies RevertLast resolves the most recent run.
func TestRevertLast_UsesLatestRun(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "validated", NewStatus: "active"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusActive})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"}),
		nil,
	)

	result, err := r.RevertLast(context.Background())
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, domainskill.StatusValidated, patcher.status[testSkillID1])
}

// MEDIUM-2 RED — reverting the same run twice is idempotent (no churn).

// TestRevert_DoubleRevertIsIdempotent verifies that reverting a run a second time,
// when the skill already sits at the prior status, performs zero PatchStatus calls
// and reports a no-op rather than re-walking a spurious full lifecycle.
func TestRevert_DoubleRevertIsIdempotent(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusDeprecated})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{
			"RUN0000000000000000000002", "ITEM000000000000000000002",
			"RUN0000000000000000000003", "ITEM000000000000000000003",
		}),
		nil,
	)

	// First revert: deprecated → active via the legal chain.
	first, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, first, 1)
	assert.True(t, first[0].Reverted)
	assert.Equal(t, domainskill.StatusActive, patcher.status[testSkillID1])
	firstCalls := len(patcher.calls)
	require.NotZero(t, firstCalls)

	// Second revert of the SAME run: skill is already at the prior status (active),
	// so it must be a no-op — zero additional PatchStatus calls, reported skipped.
	second, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.False(t, second[0].Reverted, "already at target → not a fresh revert")
	assert.True(t, second[0].Skipped, "no-op idempotent revert is reported as skipped")
	assert.Equal(t, firstCalls, len(patcher.calls), "second revert must not churn the lifecycle")
	assert.Equal(t, domainskill.StatusActive, patcher.status[testSkillID1], "status unchanged")
}

// WU-3 — rollback metric emission + idempotency.

// TestRevertRun_EmitsDeltaPerRevertedSkill verifies that revertRun emits
// RollbackDelta=1 for each skill whose walk completed (row.Reverted==true).
// SPEC: "Multiple reverted skills each receive exactly one delta."
func TestRevertRun_EmitsDeltaPerRevertedSkill(t *testing.T) {
	testSkillID3 := "01ARZ3NDEKTSV4RRFFQ69G5US3"
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID:   "RUN0000000000000000000001",
		Mode: "apply",
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
			{ID: "ITEM2", SkillID: testSkillID3, PriorStatus: "validated", NewStatus: "active"},
		},
	}))

	mp := &fakeMetricsPatcher{}
	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusDeprecated,
		testSkillID3: domainskill.StatusActive,
	})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{
			"RUN0000000000000000000002",
			"ITEM000000000000000000002",
			"ITEM000000000000000000003",
		}),
		mp,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 2)
	assert.True(t, result[0].Reverted)
	assert.True(t, result[1].Reverted)

	require.Len(t, mp.calls, 2, "exactly 2 PatchMetrics calls expected (one per reverted skill)")
	skillIDs := []string{mp.calls[0].skillID, mp.calls[1].skillID}
	assert.ElementsMatch(t, []string{testSkillID1, testSkillID3}, skillIDs)
	for _, c := range mp.calls {
		assert.Equal(t, 1, c.delta.RollbackDelta, "RollbackDelta must be 1 per reverted skill")
	}
}

// TestRevertRun_SkipsNonRevertedSkills verifies that only skills where
// row.Reverted==true emit a RollbackDelta; skills that were skipped (e.g. no
// legal path) do not.
// SPEC: "Non-reverted skills in the same change are not incremented."
func TestRevertRun_SkipsNonRevertedSkills(t *testing.T) {
	testSkillID3 := "01ARZ3NDEKTSV4RRFFQ69G5US3"
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID:   "RUN0000000000000000000001",
		Mode: "apply",
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
			// testSkillID3 has no legal path (archived is terminal).
			{ID: "ITEM2", SkillID: testSkillID3, PriorStatus: "active", NewStatus: "archived"},
		},
	}))

	mp := &fakeMetricsPatcher{}
	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusDeprecated,
		testSkillID3: domainskill.StatusArchived, // no path back → skipped
	})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{
			"RUN0000000000000000000002",
			"ITEM000000000000000000002",
		}),
		mp,
	)

	_, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)

	require.Len(t, mp.calls, 1, "only the reverted skill should emit a delta")
	assert.Equal(t, testSkillID1, mp.calls[0].skillID)
	assert.Equal(t, 1, mp.calls[0].delta.RollbackDelta)
}

// TestRevertRun_Idempotency_SameRunIDSkipsEmission verifies that when
// ExistsByRevertsRunID returns true for run.ID, PatchMetrics is NOT called,
// but status walks still execute (the from==to no-op guard makes them no-ops).
// SPEC: "Repeated execution of the same run is a no-op for metric emission."
func TestRevertRun_Idempotency_SameRunIDSkipsEmission(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID:   "RUN0000000000000000000001",
		Mode: "apply",
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	// Signal that this revert run has already been processed.
	audit.existsByRevertsRunID["RUN0000000000000000000001"] = true

	mp := &fakeMetricsPatcher{}
	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusDeprecated,
	})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{
			"RUN0000000000000000000002",
			"ITEM000000000000000000002",
		}),
		mp,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)

	// Metric emission must be skipped entirely.
	assert.Len(t, mp.calls, 0, "idempotency: PatchMetrics must not be called on re-run")

	// Status walks still executed (the from==to no-op guard skips them, but the
	// loop ran — meaning the result row was produced).
	assert.True(t, result[0].Reverted, "status walk must still run on idempotent re-run")
}

// TestRevertRun_DifferentRunIDs_EmitIndependently verifies that two distinct
// revert runs are treated independently: if R1 already exists but R2 is new,
// running R2 emits its deltas normally.
// SPEC: "Different revert runs are independent."
func TestRevertRun_DifferentRunIDs_EmitIndependently(t *testing.T) {
	audit := newFakeAuditRepo()
	// The apply run to revert.
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID:   "RUN0000000000000000000002",
		Mode: "apply",
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	// R1 already processed; R2 (the one we're about to revert) is new.
	audit.existsByRevertsRunID["RUN0000000000000000000001"] = true
	// R2 not in the map → ExistsByRevertsRunID returns false.

	mp := &fakeMetricsPatcher{}
	patcher := newChainPatcher(map[string]domainskill.Status{
		testSkillID1: domainskill.StatusDeprecated,
	})
	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{
			"RUN0000000000000000000003",
			"ITEM000000000000000000003",
		}),
		mp,
	)

	_, err := r.Revert(context.Background(), "RUN0000000000000000000002")
	require.NoError(t, err)

	require.Len(t, mp.calls, 1, "new revert run must emit its delta normally")
	assert.Equal(t, testSkillID1, mp.calls[0].skillID)
	assert.Equal(t, 1, mp.calls[0].delta.RollbackDelta)
}

// MEDIUM-1 RED — a mid-chain walk failure is auditable (stranded state visible).

// TestRevert_PartialWalkFailureIsAudited verifies that when a multi-hop walk fails
// on a later hop, the revert audit run records the actual intermediate status the
// skill was stranded at, plus the error — never silence.
func TestRevert_PartialWalkFailureIsAudited(t *testing.T) {
	audit := newFakeAuditRepo()
	require.NoError(t, audit.Save(context.Background(), outbound.ReevalRun{
		ID: "RUN0000000000000000000001", Mode: "apply",
		CreatedAt: time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC),
		Items: []outbound.ReevalRunItem{
			{ID: "ITEM1", SkillID: testSkillID1, PriorStatus: "active", NewStatus: "deprecated"},
		},
	}))

	// Revert path is deprecated→blocked→candidate→validated→active. Fail on the
	// 2nd hop (candidate) so the skill is stranded at blocked.
	patcher := newChainPatcher(map[string]domainskill.Status{testSkillID1: domainskill.StatusDeprecated})
	patcher.failOn[domainskill.StatusCandidate] = errors.New("simulated hop failure")

	r := skillapp.NewReevaluatorWithAudit(
		staticEvidence{}, patcher, audit,
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)),
		shared.FixedIDGenerator([]string{"RUN0000000000000000000002", "ITEM000000000000000000002"}),
		nil,
	)

	result, err := r.Revert(context.Background(), "RUN0000000000000000000001")
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.False(t, result[0].Reverted, "partial walk did not reach the target")
	assert.True(t, result[0].Skipped)
	require.Error(t, result[0].RevertErr)

	// Only the first hop (blocked) succeeded before the failure.
	assert.Equal(t, domainskill.StatusBlocked, patcher.status[testSkillID1],
		"skill is stranded at the intermediate status")

	// The revert audit run must capture the partial walk: the item reflects the
	// ACTUAL final status reached (blocked), not the intended target (active).
	revRun, err := audit.FindLatest(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "revert", revRun.Mode)
	require.Len(t, revRun.Items, 1, "the partial walk must produce an audit item, not silence")
	assert.Equal(t, "deprecated", revRun.Items[0].PriorStatus,
		"prior is where the skill started this revert")
	assert.Equal(t, "blocked", revRun.Items[0].NewStatus,
		"new status reflects the actual stranded intermediate status")
}
