package skill_test

// skill_lifecycle_test.go contains TDD RED tests for Group D lifecycle
// extensions to the Skill aggregate. These tests are written FIRST and will
// fail until lifecycle.go + the skill.go extensions land (D.9-D.10 GREEN).

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// ── D.2: New() zero lifecycle input uses V4.1 §7 defaults ────────────────────

func TestNew_ZeroLifecycle_UsesDefaults(t *testing.T) {
	s, err := skill.New(
		mustSkillID(t, validID),
		validName, validPhase, validContent, validTechs,
		skill.LifecycleInput{}, // zero value
		now,
	)
	require.NoError(t, err)

	require.Equal(t, skill.StatusCandidate, s.Status(), "default status must be candidate")
	require.Equal(t, "v1", s.Version(), "default version must be v1")
	require.Equal(t, skill.RiskMedium, s.RiskLevel(), "default risk level must be medium")
	require.Equal(t, skill.SourceManual, s.ActivationSource(), "default source must be manual")
	require.Nil(t, s.LastUsedAt(), "default LastUsedAt must be nil")
	require.Nil(t, s.LastValidatedAt(), "default LastValidatedAt must be nil")
	require.Equal(t, 0, s.Metrics().UsageCount, "default metrics usage_count must be 0")
}

// ── D.3: New() explicit lifecycle fields returned correctly ───────────────────

func TestNew_ExplicitLifecycle_FieldsPreserved(t *testing.T) {
	lastUsed := now.Add(-24 * time.Hour)
	lastVal := now.Add(-48 * time.Hour)

	lc := skill.LifecycleInput{
		Status:           skill.StatusActive,
		Version:          "v2",
		RiskLevel:        skill.RiskHigh,
		ActivationSource: skill.SourceLegacySeed,
		Scope: skill.Scope{
			ProjectID: "*",
			RepoID:    "*",
			Phases:    []string{"apply"},
		},
		AppliesWhen: skill.AppliesWhen{
			FeatureType: []string{"bugfix"},
		},
		Metrics: skill.Metrics{
			UsageCount: 5,
		},
		LastUsedAt:      &lastUsed,
		LastValidatedAt: &lastVal,
	}

	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, lc, now)
	require.NoError(t, err)

	require.Equal(t, skill.StatusActive, s.Status())
	require.Equal(t, "v2", s.Version())
	require.Equal(t, skill.RiskHigh, s.RiskLevel())
	require.Equal(t, skill.SourceLegacySeed, s.ActivationSource())
	require.Equal(t, "*", s.Scope().ProjectID)
	require.Equal(t, []string{"bugfix"}, s.AppliesWhen().FeatureType)
	require.Equal(t, 5, s.Metrics().UsageCount)
	require.NotNil(t, s.LastUsedAt())
	require.True(t, s.LastUsedAt().Equal(lastUsed))
	require.NotNil(t, s.LastValidatedAt())
	require.True(t, s.LastValidatedAt().Equal(lastVal))
}

// ── D.4: New() rejects invalid lifecycle fields ───────────────────────────────

func TestNew_InvalidStatus_Rejected(t *testing.T) {
	lc := skill.LifecycleInput{Status: skill.Status("bad-status")}
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, lc, now)
	require.ErrorIs(t, err, skill.ErrInvalidStatus)
}

func TestNew_EmptyVersion_Rejected(t *testing.T) {
	lc := skill.LifecycleInput{
		Status:  skill.StatusActive, // valid status
		Version: "  ",               // whitespace only
	}
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, lc, now)
	require.ErrorIs(t, err, skill.ErrEmptyVersion)
}

func TestNew_InvalidRiskLevel_Rejected(t *testing.T) {
	lc := skill.LifecycleInput{
		Status:    skill.StatusActive,
		Version:   "v1",
		RiskLevel: skill.RiskLevel("extreme"),
	}
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, lc, now)
	require.ErrorIs(t, err, skill.ErrInvalidRiskLevel)
}

func TestNew_InvalidActivationSource_Rejected(t *testing.T) {
	lc := skill.LifecycleInput{
		Status:           skill.StatusActive,
		Version:          "v1",
		RiskLevel:        skill.RiskMedium,
		ActivationSource: skill.ActivationSource("bad-source"),
	}
	_, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, lc, now)
	require.ErrorIs(t, err, skill.ErrInvalidActivationSource)
}

// ── D.5: Update() rejects invalid Status; aggregate state unchanged on error ──

func TestUpdate_InvalidStatus_Rejected_StateUnchanged(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs, skill.LifecycleInput{}, now)
	require.NoError(t, err)

	originalStatus := s.Status()

	lc := skill.LifecycleInput{Status: skill.Status("invalid")}
	err = s.Update(validName, validPhase, validContent, validTechs, lc, now.Add(time.Hour))
	require.ErrorIs(t, err, skill.ErrInvalidStatus,
		"Update must reject invalid status")
	require.Equal(t, originalStatus, s.Status(),
		"aggregate status must be unchanged after rejected Update")
}

// ── D.6: Update() valid status transition succeeds ────────────────────────────

func TestUpdate_ValidStatusTransition(t *testing.T) {
	s, err := skill.New(mustSkillID(t, validID), validName, validPhase, validContent, validTechs,
		skill.LifecycleInput{Status: skill.StatusCandidate}, now)
	require.NoError(t, err)

	lc := skill.LifecycleInput{Status: skill.StatusActive}
	err = s.Update(validName, validPhase, validContent, validTechs, lc, now.Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, skill.StatusActive, s.Status())
}

// ── D.7: Hydrate() accepts any persisted values without error ─────────────────

func TestHydrate_AcceptsAllPersistedLifecycleValues(t *testing.T) {
	// Hydrate trusts the persistence layer — it must not validate lifecycle enums.
	s := skill.Hydrate(
		mustSkillID(t, validID),
		validName, validPhase, validContent, validTechs,
		skill.Status("candidate"), // valid
		"v1",
		skill.Scope{ProjectID: "*", RepoID: "*"},
		skill.AppliesWhen{},
		skill.RiskLevel("medium"), // valid
		skill.ActivationSource("legacy_seed"), // valid
		skill.Metrics{},
		nil, nil, // lastUsedAt, lastValidatedAt
		now, now,
	)
	require.NotNil(t, s)
	require.Equal(t, skill.Status("candidate"), s.Status())
	require.Equal(t, "v1", s.Version())
}

// ── D.8 (already covered in lifecycle_test.go): JSON round-trip for
// Scope / AppliesWhen / Metrics value types — tested in lifecycle_test.go.

// ── NewLegacy: convenience constructor ───────────────────────────────────────

func TestNewLegacy_ProducesCorrectLifecyclePayload(t *testing.T) {
	s, err := skill.NewLegacy(
		mustSkillID(t, validID),
		validName, validPhase, validContent, validTechs,
		now,
	)
	require.NoError(t, err)

	require.Equal(t, skill.StatusActive, s.Status(), "NewLegacy: status must be active")
	require.Equal(t, "v1", s.Version(), "NewLegacy: version must be v1")
	require.Equal(t, skill.SourceLegacySeed, s.ActivationSource(), "NewLegacy: source must be legacy_seed")
	require.Equal(t, skill.RiskMedium, s.RiskLevel(), "NewLegacy: risk must be medium")
	require.Equal(t, "*", s.Scope().ProjectID, "NewLegacy: scope.project_id must be '*'")
	require.Equal(t, "*", s.Scope().RepoID, "NewLegacy: scope.repo_id must be '*'")

	// Scope.Phases must contain all input phase strings.
	phaseStrings := s.Scope().Phases
	require.NotEmpty(t, phaseStrings, "NewLegacy: scope.phases must be populated from input phases")
	require.Contains(t, phaseStrings, string(phase.PhaseApply))
}

func TestNewLegacy_MultiplePhases(t *testing.T) {
	phases := []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify}
	s, err := skill.NewLegacy(
		mustSkillID(t, validID),
		validName, phases, validContent, validTechs,
		now,
	)
	require.NoError(t, err)
	require.Contains(t, s.Scope().Phases, "apply")
	require.Contains(t, s.Scope().Phases, "verify")
}

