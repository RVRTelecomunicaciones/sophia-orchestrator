package skill_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

const (
	testChangeID = "01ARZ3NDEKTSV4RRFFQ69G5UC1"
	testSkillID1 = "01ARZ3NDEKTSV4RRFFQ69G5US1"
	testSkillID2 = "01ARZ3NDEKTSV4RRFFQ69G5US2"
	testUsageID1 = "01ARZ3NDEKTSV4RRFFQ69G5SU1"
	testUsageID2 = "01ARZ3NDEKTSV4RRFFQ69G5SU2"
)

var testNow = time.Date(2026, 6, 9, 10, 0, 0, 0, time.UTC)

func mustChangeID(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

func mustSkillID(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

func mustUsageID(t *testing.T, raw string) ids.SkillUsageID {
	t.Helper()
	id, err := ids.ParseSkillUsageID(raw)
	require.NoError(t, err)
	return id
}

// fakeUsageRepo is a configurable in-memory SkillUsageRepository for unit tests.
type fakeUsageRepo struct {
	byChange    map[string][]*skillusage.SkillUsage
	attemptsSum map[string]int
	sumCalls    int
}

func newFakeUsageRepo() *fakeUsageRepo {
	return &fakeUsageRepo{
		byChange:    map[string][]*skillusage.SkillUsage{},
		attemptsSum: map[string]int{},
	}
}

func (r *fakeUsageRepo) Insert(_ context.Context, _ *skillusage.SkillUsage) error { return nil }
func (r *fakeUsageRepo) UpdateOutcome(_ context.Context, _ ids.SkillUsageID, _ skillusage.Outcome) error {
	return nil
}

func (r *fakeUsageRepo) FindByChange(_ context.Context, changeID ids.ChangeID) ([]*skillusage.SkillUsage, error) {
	return r.byChange[changeID.String()], nil
}

func (r *fakeUsageRepo) FindBySkill(_ context.Context, _ ids.SkillID) ([]*skillusage.SkillUsage, error) {
	return nil, nil
}

func (r *fakeUsageRepo) SumApplyAttemptsByChange(_ context.Context, changeID ids.ChangeID) (int, error) {
	r.sumCalls++
	return r.attemptsSum[changeID.String()], nil
}

var _ outbound.SkillUsageRepository = (*fakeUsageRepo)(nil)

// fakeSkillRepo is a minimal SkillRepository for unit tests; only FindByID and
// PatchStatus carry behavior, the rest are no-ops.
type fakeSkillRepo struct {
	byID         map[string]*domainskill.Skill
	statusCalls  []statusCall
	patchErr     error
	findErr      error
	patchFailIDs map[string]error
}

type statusCall struct {
	id     string
	status domainskill.Status
}

func newFakeSkillRepo() *fakeSkillRepo {
	return &fakeSkillRepo{
		byID:         map[string]*domainskill.Skill{},
		patchFailIDs: map[string]error{},
	}
}

func (r *fakeSkillRepo) FindByPhase(_ context.Context, _ phase.PhaseType) ([]*domainskill.Skill, error) {
	return nil, nil
}

func (r *fakeSkillRepo) FindByID(_ context.Context, id ids.SkillID) (*domainskill.Skill, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	sk, ok := r.byID[id.String()]
	if !ok {
		return nil, outbound.ErrNotFound
	}
	return sk, nil
}

func (r *fakeSkillRepo) Upsert(_ context.Context, _ *domainskill.Skill) error         { return nil }
func (r *fakeSkillRepo) InsertIfAbsent(_ context.Context, _ *domainskill.Skill) error { return nil }
func (r *fakeSkillRepo) List(_ context.Context) ([]*domainskill.Skill, error) {
	out := make([]*domainskill.Skill, 0, len(r.byID))
	for _, sk := range r.byID {
		out = append(out, sk)
	}
	return out, nil
}

func (r *fakeSkillRepo) PatchMetrics(_ context.Context, _ ids.SkillID, _ domainskill.Metrics, _ time.Time) error {
	return nil
}

func (r *fakeSkillRepo) PatchStatus(_ context.Context, id ids.SkillID, status domainskill.Status, _ time.Time) error {
	if r.patchErr != nil {
		return r.patchErr
	}
	if err := r.patchFailIDs[id.String()]; err != nil {
		return err
	}
	r.statusCalls = append(r.statusCalls, statusCall{id: id.String(), status: status})
	return nil
}

var _ outbound.SkillRepository = (*fakeSkillRepo)(nil)

func newService(usageRepo *fakeUsageRepo, skillRepo *fakeSkillRepo) *skillapp.Service {
	return skillapp.New(skillRepo, usageRepo, shared.FixedClock(testNow))
}

// TestGetUsage_EnrichesApplyAttempts verifies that GetUsage sets ApplyAttempts on
// each row to the real per-change SUM(tasks.attempts), not 0 (D-LH-2, spec
// skill-usage-tracking "apply_attempts reflects real tasks.attempts").
func TestGetUsage_EnrichesApplyAttempts(t *testing.T) {
	usageRepo := newFakeUsageRepo()
	usageRepo.byChange[testChangeID] = []*skillusage.SkillUsage{
		skillusage.New(mustUsageID(t, testUsageID1), mustChangeID(t, testChangeID), "apply", mustSkillID(t, testSkillID1), "v1", testNow),
		skillusage.New(mustUsageID(t, testUsageID2), mustChangeID(t, testChangeID), "verify", mustSkillID(t, testSkillID2), "v1", testNow),
	}
	usageRepo.attemptsSum[testChangeID] = 4

	svc := newService(usageRepo, newFakeSkillRepo())

	rows, err := svc.GetUsage(context.Background(), testChangeID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Equal(t, 4, row.ApplyAttempts, "ApplyAttempts must be the real per-change sum applied to every row")
	}
	assert.Equal(t, 1, usageRepo.sumCalls, "the per-change sum must be queried once, not per-row")
}

// TestGetUsage_ZeroAttemptsStillZero verifies that a change with no apply tasks
// yields ApplyAttempts=0 (honest zero, not the old hardcoded constant).
func TestGetUsage_ZeroAttemptsStillZero(t *testing.T) {
	usageRepo := newFakeUsageRepo()
	usageRepo.byChange[testChangeID] = []*skillusage.SkillUsage{
		skillusage.New(mustUsageID(t, testUsageID1), mustChangeID(t, testChangeID), "apply", mustSkillID(t, testSkillID1), "v1", testNow),
	}
	// attemptsSum left at 0 for testChangeID.

	svc := newService(usageRepo, newFakeSkillRepo())

	rows, err := svc.GetUsage(context.Background(), testChangeID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 0, rows[0].ApplyAttempts)
}
