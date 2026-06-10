package apply_test

// C.2 RED — apply/teamlead.go records skill_usage rows at hydrateSkills callsites.
// These tests fail until SkillUsageRepo is wired into apply.RunDeps and the
// teamlead records injection events.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	skdomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// applyFakeSkillUsageRepo is a spy that records Insert calls in the apply
// package test context.
type applyFakeSkillUsageRepo struct {
	mu        sync.Mutex
	inserts   []*skillusage.SkillUsage
	insertErr error
}

func (r *applyFakeSkillUsageRepo) Insert(_ context.Context, su *skillusage.SkillUsage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertErr != nil {
		return r.insertErr
	}
	r.inserts = append(r.inserts, su)
	return nil
}

func (r *applyFakeSkillUsageRepo) UpdateOutcome(_ context.Context, _ ids.SkillUsageID, _ skillusage.Outcome) error {
	return nil
}

func (r *applyFakeSkillUsageRepo) FindByChange(_ context.Context, _ ids.ChangeID) ([]*skillusage.SkillUsage, error) {
	return nil, nil
}

func (r *applyFakeSkillUsageRepo) FindBySkill(_ context.Context, _ ids.SkillID) ([]*skillusage.SkillUsage, error) {
	return nil, nil
}

var _ outbound.SkillUsageRepository = (*applyFakeSkillUsageRepo)(nil)

// applyFakeSkillMatcher implements discipline.SkillMatcher for apply skill_usage_test.go.
type applyFakeSkillMatcher struct {
	skills []*skdomain.Skill
}

func (f *applyFakeSkillMatcher) SkillsForContext(_ context.Context, _ discipline.SkillQuery) ([]*skdomain.Skill, []discipline.SkippedSkill, error) {
	return f.skills, nil, nil
}

// buildApplyActiveSkill creates an active apply-phase skill.
func buildApplyActiveSkill(t *testing.T) *skdomain.Skill {
	t.Helper()
	sid, err := ids.ParseSkillID("01ARZ3NDEKTSV4RRFFQ69G5SK2")
	require.NoError(t, err)
	s, err := skdomain.New(
		sid, "apply-skill",
		[]phase.PhaseType{phase.PhaseApply},
		"apply skill content",
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

// TestApply_SkillUsage_RowWrittenAtHydrateSkills (C.2 RED):
// When apply teamlead hydrates skills during dispatchImplement, it must write
// skill_usage rows for the injected skills.
func TestApply_SkillUsage_RowWrittenAtHydrateSkills(t *testing.T) {
	activeSkill := buildApplyActiveSkill(t)
	sp := &applyFakeSkillMatcher{skills: []*skdomain.Skill{activeSkill}}
	usageRepo := &applyFakeSkillUsageRepo{}

	svc, _, _, _, _, mem := newRunService(t, func(d *apply.RunDeps) {
		d.Skills = sp
		d.SkillUsageRepo = usageRepo
	})
	mem.putTasksList("feat-x", defaultTasksListJSON())

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, runErr := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	_ = runErr // Execute may return error for other reasons; we only care about inserts

	usageRepo.mu.Lock()
	defer usageRepo.mu.Unlock()
	assert.NotEmpty(t, usageRepo.inserts,
		"apply teamlead must write skill_usage rows when skills are injected")

	if len(usageRepo.inserts) > 0 {
		row := usageRepo.inserts[0]
		assert.Equal(t, string(phase.PhaseApply), row.PhaseType())
		assert.Equal(t, skillusage.OutcomePending, row.Outcome())
	}
}

// TestApply_SkillUsage_NilRepo_PhaseRunsNormally (C.2 nil-tolerance):
// When SkillUsageRepo is nil, apply phase must still run normally without panic.
func TestApply_SkillUsage_NilRepo_PhaseRunsNormally(t *testing.T) {
	activeSkill := buildApplyActiveSkill(t)
	sp := &applyFakeSkillMatcher{skills: []*skdomain.Skill{activeSkill}}

	svc, _, _, _, _, mem := newRunService(t, func(d *apply.RunDeps) {
		d.Skills = sp
		d.SkillUsageRepo = nil // explicitly nil — must not panic
	})
	mem.putTasksList("feat-x", defaultTasksListJSON())

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	// Should not panic with nil SkillUsageRepo.
	require.NotPanics(t, func() {
		_, _ = svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
			ChangeID:  c.ID(),
			PhaseType: phase.PhaseApply,
		})
	})
}
