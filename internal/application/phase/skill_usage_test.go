package phase_test

// C.1, C.3, C.4 RED — phase/service.go records skill_usage rows.
// These tests fail until SkillUsageRepo is wired into phase.Deps and the
// service calls repo.Insert at the injection callsite and repo.UpdateOutcome
// at phase completion.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	appphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	skdomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// fakeSkillUsageRepo is a spy repo that records Insert and UpdateOutcome calls.
type fakeSkillUsageRepo struct {
	mu           sync.Mutex
	inserts      []*skillusage.SkillUsage
	updateCalls  []updateCall
	insertErr    error
	updateErr    error
}

type updateCall struct {
	id      ids.SkillUsageID
	outcome skillusage.Outcome
}

func (r *fakeSkillUsageRepo) Insert(_ context.Context, su *skillusage.SkillUsage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertErr != nil {
		return r.insertErr
	}
	r.inserts = append(r.inserts, su)
	return nil
}

func (r *fakeSkillUsageRepo) UpdateOutcome(_ context.Context, id ids.SkillUsageID, outcome skillusage.Outcome) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updateCalls = append(r.updateCalls, updateCall{id: id, outcome: outcome})
	return nil
}

func (r *fakeSkillUsageRepo) FindByChange(_ context.Context, _ ids.ChangeID) ([]*skillusage.SkillUsage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return nil, nil
}

func (r *fakeSkillUsageRepo) FindBySkill(_ context.Context, _ ids.SkillID) ([]*skillusage.SkillUsage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return nil, nil
}

var _ outbound.SkillUsageRepository = (*fakeSkillUsageRepo)(nil)

// fakeSkillProviderWithSkills implements discipline.SkillMatcher for usage-tracking tests.
// Updated in M3 PR3a (K.4 GREEN): SkillsForPhase → SkillsForContext.
type fakeSkillProviderWithSkills struct {
	skills []*skdomain.Skill
}

func (f *fakeSkillProviderWithSkills) SkillsForContext(_ context.Context, _ discipline.SkillQuery) ([]*skdomain.Skill, []discipline.SkippedSkill, error) {
	return f.skills, nil, nil
}

// buildActiveSkill creates an active skill for test injection.
func buildActiveSkill(t *testing.T) *skdomain.Skill {
	t.Helper()
	sid, err := ids.ParseSkillID("01ARZ3NDEKTSV4RRFFQ69G5SK1")
	require.NoError(t, err)
	s, err := skdomain.New(
		sid, "test-skill",
		[]phase.PhaseType{phase.PhaseSpec},
		"test content",
		[]skdomain.Technique{skdomain.TechniqueInlineWhy},
		skdomain.LifecycleInput{
			Status:           skdomain.StatusActive,
			Version:          "v1",
			RiskLevel:        skdomain.RiskLow,
			ActivationSource: skdomain.SourceManual,
		},
		time.Now(),
	)
	require.NoError(t, err)
	return s
}

// newHarnessWithSkillsAndUsageRepo creates a harness wired with SkillMatcher
// AND SkillUsageRepo so we can observe injection writes.
// Updated in M3 PR3a (K.4 GREEN): SkillProvider → SkillMatcher.
func newHarnessWithSkillsAndUsageRepo(t *testing.T, sp discipline.SkillMatcher, usageRepo outbound.SkillUsageRepository) *harness {
	t.Helper()
	h := newHarness(t)

	h.svc = appphase.New(appphase.Deps{
		ChangeRepo:      h.changeRepo,
		PhaseRepo:       h.phaseRepo,
		SessionRepo:     h.sessRepo,
		Governance:      h.governance,
		Memory:          h.memory,
		Dispatcher:      h.dispatcher,
		SpawnGov:        h.spawn,
		Validator:       discipline.NewValidator(),
		IronLaw:         discipline.NewIronLawChecker(),
		Prompts:         discipline.NewPromptBuilder(),
		Audit:           h.audit,
		Events:          h.events,
		Clock:           h.clock,
		IDGen: shared.FixedIDGenerator([]string{
			"01ARZ3NDEKTSV4RRFFQ69G5P01",
			"01ARZ3NDEKTSV4RRFFQ69G5S01",
			"01ARZ3NDEKTSV4RRFFQ69G5SU1",
		}),
		Scheduler:       appphase.SyncScheduler,
		Skills:          sp,
		SkillUsageRepo:  usageRepo,
	})
	return h
}

// TestRun_SkillUsage_RowWrittenOnInjection (C.1 RED):
// When phase service injects skills, it must write a skill_usage row with
// outcome=pending for each injected skill.
func TestRun_SkillUsage_RowWrittenOnInjection(t *testing.T) {
	activeSkill := buildActiveSkill(t)
	sp := &fakeSkillProviderWithSkills{skills: []*skdomain.Skill{activeSkill}}
	usageRepo := &fakeSkillUsageRepo{}

	h := newHarnessWithSkillsAndUsageRepo(t, sp, usageRepo)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)

	usageRepo.mu.Lock()
	defer usageRepo.mu.Unlock()
	require.NotEmpty(t, usageRepo.inserts,
		"skill_usage rows must be written when skills are injected")

	row := usageRepo.inserts[0]
	assert.Equal(t, cid.String(), row.ChangeID().String())
	assert.Equal(t, string(phase.PhaseSpec), row.PhaseType())
	assert.Equal(t, activeSkill.ID().String(), row.SkillID().String())
	assert.Equal(t, activeSkill.Version(), row.SkillVersion())
	assert.Equal(t, skillusage.OutcomePending, row.Outcome())
}

// TestRun_SkillUsage_OutcomeUpdatedOnDone (C.3 RED):
// When a phase envelope reaches done, the skill_usage outcome must be updated
// to success.
func TestRun_SkillUsage_OutcomeUpdatedOnDone(t *testing.T) {
	activeSkill := buildActiveSkill(t)
	sp := &fakeSkillProviderWithSkills{skills: []*skdomain.Skill{activeSkill}}
	usageRepo := &fakeSkillUsageRepo{}

	h := newHarnessWithSkillsAndUsageRepo(t, sp, usageRepo)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)

	// The fake dispatcher in harness returns a successful envelope (StatusDone).
	// So UpdateOutcome must be called with success.
	usageRepo.mu.Lock()
	defer usageRepo.mu.Unlock()
	require.NotEmpty(t, usageRepo.updateCalls,
		"UpdateOutcome must be called after phase completes with StatusDone")

	call := usageRepo.updateCalls[0]
	assert.Equal(t, skillusage.OutcomeSuccess, call.outcome)
}

// TestRun_SkillUsage_NilRepo_PhaseRunsNormally (C.1 nil-tolerance):
// When SkillUsageRepo is nil, phase must still run successfully (fail-soft).
func TestRun_SkillUsage_NilRepo_PhaseRunsNormally(t *testing.T) {
	activeSkill := buildActiveSkill(t)
	sp := &fakeSkillProviderWithSkills{skills: []*skdomain.Skill{activeSkill}}

	h := newHarnessWithSkillsAndUsageRepo(t, sp, nil)
	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")

	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:  cid,
		PhaseType: phase.PhaseSpec,
	})
	// Should not panic or return error because SkillUsageRepo is nil.
	require.NoError(t, err)
}
