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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// fakeAuditRepo is an in-memory ReevalAuditRepository for unit tests.
type fakeAuditRepo struct {
	runs    map[string]outbound.ReevalRun
	order   []string // insertion order, newest last
	saveErr error
}

func newFakeAuditRepo() *fakeAuditRepo {
	return &fakeAuditRepo{runs: map[string]outbound.ReevalRun{}}
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
		patcher, audit, clk, idgen,
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
		fixedClockAt(time.Date(2026, 6, 16, 13, 0, 0, 0, time.UTC)), idgen,
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
