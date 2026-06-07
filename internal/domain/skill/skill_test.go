package skill_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func mustSkillID(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

var (
	validID      = "01ARZ3NDEKTSV4RRFFQ69G5SK1"
	validName    = "apply-implement-safely"
	validPhase   = []phase.PhaseType{phase.PhaseApply}
	validContent = "Use constitutional self-critique to review each code change before committing."
	validTechs   = []skill.Technique{skill.TechniqueConstitutionalSelfCritique, skill.TechniqueInlineWhy}
	now          = time.Date(2026, 6, 7, 0, 0, 0, 0, time.UTC)
)

// ── New — happy path ──────────────────────────────────────────────────────────

func TestNew_ValidSkill(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, validName, s.Name())
	require.Equal(t, validContent, s.Content())
	require.Equal(t, []phase.PhaseType{phase.PhaseApply}, s.Phases())
	require.Equal(t, validTechs, s.Techniques())
	require.Equal(t, now, s.CreatedAt())
	require.Equal(t, now, s.UpdatedAt())
}

// ── New — invariant violations ────────────────────────────────────────────────

func TestNew_EmptyName(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), "", validPhase, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyName)
}

func TestNew_WhitespaceName(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), "   ", validPhase, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyName)
}

func TestNew_EmptyContent(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, "", validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyContent)
}

func TestNew_WhitespaceContent(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, "   ", validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyContent)
}

func TestNew_NoPhases(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, nil, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrNoValidPhases)
}

func TestNew_EmptyPhaseSlice(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, []phase.PhaseType{}, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrNoValidPhases)
}

func TestNew_NoTechniques(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, nil, now)
	require.ErrorIs(t, err, skill.ErrNoTechniques)
}

func TestNew_EmptyTechniqueSlice(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, []skill.Technique{}, now)
	require.ErrorIs(t, err, skill.ErrNoTechniques)
}

func TestNew_InvalidTechniqueTag(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent,
		[]skill.Technique{"freeform-thinking"}, now)
	require.ErrorIs(t, err, skill.ErrInvalidTechnique)
}

func TestNew_MixedValidAndInvalidTechniques(t *testing.T) {
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent,
		[]skill.Technique{skill.TechniqueReAct, "not-a-tag"}, now)
	require.ErrorIs(t, err, skill.ErrInvalidTechnique)
}

// ── Phase deduplication and canonical order ───────────────────────────────────

func TestNew_DuplicatePhasesDeduped(t *testing.T) {
	phases := []phase.PhaseType{phase.PhaseApply, phase.PhaseApply, phase.PhaseVerify}
	s, err := skill.New(mustSkillID(t, validID), validName, phases, validContent, validTechs, now)
	require.NoError(t, err)
	// apply comes before verify in canonical order; no duplicates.
	require.Equal(t, []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify}, s.Phases())
}

func TestNew_PhasesCanonicalOrder(t *testing.T) {
	// Input in reverse order — output must be canonical.
	phases := []phase.PhaseType{phase.PhaseVerify, phase.PhaseApply, phase.PhaseDesign}
	s, err := skill.New(mustSkillID(t, validID), validName, phases, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, []phase.PhaseType{phase.PhaseDesign, phase.PhaseApply, phase.PhaseVerify}, s.Phases())
}

func TestNew_AllPhasesCanonicalOrder(t *testing.T) {
	all := phase.AllPhaseTypes()
	// Reverse to ensure sorting happens.
	reversed := make([]phase.PhaseType, len(all))
	for i, p := range all {
		reversed[len(all)-1-i] = p
	}
	s, err := skill.New(mustSkillID(t, validID), validName, reversed, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, all, s.Phases())
}

// Phases slice returned by getter must be a copy (mutation-safe).
func TestNew_PhasesGetterDefensiveCopy(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	got := s.Phases()
	got[0] = phase.PhaseInit
	require.Equal(t, []phase.PhaseType{phase.PhaseApply}, s.Phases())
}

// ── Technique deduplication ───────────────────────────────────────────────────

func TestNew_DuplicateTechniquesDeduped(t *testing.T) {
	techs := []skill.Technique{
		skill.TechniqueInlineWhy,
		skill.TechniqueInlineWhy,
		skill.TechniqueReAct,
	}
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, techs, now)
	require.NoError(t, err)
	require.Equal(t, []skill.Technique{skill.TechniqueInlineWhy, skill.TechniqueReAct}, s.Techniques())
}

// Techniques getter must return a copy.
func TestNew_TechniquesGetterDefensiveCopy(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	got := s.Techniques()
	got[0] = skill.TechniqueReAct
	require.Equal(t, validTechs[0], s.Techniques()[0])
}

// ── Hydrate ───────────────────────────────────────────────────────────────────

func TestHydrate_ReconstructsAllFields(t *testing.T) {
	createdAt := now
	updatedAt := now.Add(time.Hour)
	s := skill.Hydrate(
		mustSkillID(t, validID), validName, validPhase, validContent, validTechs,
		createdAt, updatedAt,
	)
	require.Equal(t, validName, s.Name())
	require.Equal(t, validContent, s.Content())
	require.Equal(t, validPhase, s.Phases())
	require.Equal(t, validTechs, s.Techniques())
	require.Equal(t, createdAt, s.CreatedAt())
	require.Equal(t, updatedAt, s.UpdatedAt())
}

// Hydrate must not re-validate (trusts persistence layer).
func TestHydrate_AcceptsStoredDataWithoutRevalidation(t *testing.T) {
	// Hydrate does not validate — we can confirm it returns without error.
	s := skill.Hydrate(
		mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now, now,
	)
	require.NotNil(t, s)
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUpdate_ChangesFieldsAndBumpsUpdatedAt(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)

	later := now.Add(time.Hour)
	newContent := "Updated guidance: step back before diving in."
	newTechs := []skill.Technique{skill.TechniqueStepBack}

	err = s.Update(validName, validPhase, newContent, newTechs, later)
	require.NoError(t, err)
	require.Equal(t, newContent, s.Content())
	require.Equal(t, newTechs, s.Techniques())
	require.Equal(t, later, s.UpdatedAt())
	require.Equal(t, now, s.CreatedAt(), "createdAt must not change on update")
}

func TestUpdate_EmptyNameRejected(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	err = s.Update("", validPhase, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyName)
}

func TestUpdate_EmptyContentRejected(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	err = s.Update(validName, validPhase, "", validTechs, now)
	require.ErrorIs(t, err, skill.ErrEmptyContent)
}

func TestUpdate_ZeroPhasesRejected(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	err = s.Update(validName, nil, validContent, validTechs, now)
	require.ErrorIs(t, err, skill.ErrNoValidPhases)
}

func TestUpdate_InvalidTechniqueRejected(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	err = s.Update(validName, validPhase, validContent, []skill.Technique{"bad-tag"}, now)
	require.ErrorIs(t, err, skill.ErrInvalidTechnique)
}

func TestUpdate_PhasesDedupedAndOrdered(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	// Reverse order + duplicate.
	phases := []phase.PhaseType{phase.PhaseArchive, phase.PhaseApply, phase.PhaseApply}
	err = s.Update(validName, phases, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, []phase.PhaseType{phase.PhaseApply, phase.PhaseArchive}, s.Phases())
}

// ── AppliesTo ─────────────────────────────────────────────────────────────────

func TestAppliesTo_MatchingPhase(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	require.True(t, s.AppliesTo(phase.PhaseApply))
}

func TestAppliesTo_NonMatchingPhase(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	require.False(t, s.AppliesTo(phase.PhaseDesign))
}

// ── PhaseStrings / TechniqueStrings ───────────────────────────────────────────

func TestPhaseStrings(t *testing.T) {
	phases := []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify}
	s, err := skill.New(mustSkillID(t, validID), validName, phases, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, []string{"apply", "verify"}, s.PhaseStrings())
}

func TestTechniqueStrings(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, []string{"constitutional-self-critique", "inline-why"}, s.TechniqueStrings())
}

// ── ID getter ─────────────────────────────────────────────────────────────────

func TestNew_IDRoundtrip(t *testing.T) {
	id := mustSkillID(t, validID)
	s, err := skill.New(id, validName, validPhase, validContent, validTechs, now)
	require.NoError(t, err)
	require.Equal(t, id, s.ID())
	require.Equal(t, validID, s.ID().String())
}

func TestHydrate_IDRoundtrip(t *testing.T) {
	id := mustSkillID(t, validID)
	s := skill.Hydrate(id, validName, validPhase, validContent, validTechs, now, now)
	require.Equal(t, id, s.ID())
}

// ── Technique IsValid ─────────────────────────────────────────────────────────

func TestTechnique_IsValid_AllAllowed(t *testing.T) {
	allowed := []skill.Technique{
		skill.TechniqueConstitutionalSelfCritique,
		skill.TechniqueChainOfVerification,
		skill.TechniqueExtendedThinking,
		skill.TechniqueSkeletonOfThought,
		skill.TechniqueReAct,
		skill.TechniqueStepBack,
		skill.TechniqueInlineWhy,
	}
	for _, t2 := range allowed {
		require.True(t, t2.IsValid(), "expected %q to be valid", t2)
	}
}

func TestTechnique_IsValid_UnknownTag(t *testing.T) {
	require.False(t, skill.Technique("freeform-thinking").IsValid())
}

func TestTechnique_IsValid_EmptyString(t *testing.T) {
	require.False(t, skill.Technique("").IsValid())
}
