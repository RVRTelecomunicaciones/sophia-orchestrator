package skill_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// ── Status.IsValid ────────────────────────────────────────────────────────────

func TestStatus_IsValid_AllSixValues(t *testing.T) {
	valid := []skill.Status{
		skill.StatusCandidate,
		skill.StatusValidated,
		skill.StatusActive,
		skill.StatusDeprecated,
		skill.StatusBlocked,
		skill.StatusArchived,
	}
	for _, s := range valid {
		require.True(t, s.IsValid(), "expected status %q to be valid", s)
	}
}

func TestStatus_IsValid_InvalidValue(t *testing.T) {
	require.False(t, skill.Status("unknown").IsValid(), "unknown status must be invalid")
	require.False(t, skill.Status("").IsValid(), "empty status must be invalid")
	require.False(t, skill.Status("Active").IsValid(), "case-sensitive: 'Active' is not a valid status")
}

func TestStatus_String(t *testing.T) {
	require.Equal(t, "candidate", skill.StatusCandidate.String())
	require.Equal(t, "active", skill.StatusActive.String())
	require.Equal(t, "archived", skill.StatusArchived.String())
}

// ── RiskLevel.IsValid ─────────────────────────────────────────────────────────

func TestRiskLevel_IsValid_AllFourValues(t *testing.T) {
	valid := []skill.RiskLevel{
		skill.RiskLow,
		skill.RiskMedium,
		skill.RiskHigh,
		skill.RiskCritical,
	}
	for _, r := range valid {
		require.True(t, r.IsValid(), "expected risk level %q to be valid", r)
	}
}

func TestRiskLevel_IsValid_InvalidValue(t *testing.T) {
	require.False(t, skill.RiskLevel("extreme").IsValid(), "extreme is not a valid risk level")
	require.False(t, skill.RiskLevel("").IsValid(), "empty risk level must be invalid")
}

func TestRiskLevel_String(t *testing.T) {
	require.Equal(t, "low", skill.RiskLow.String())
	require.Equal(t, "critical", skill.RiskCritical.String())
}

// ── ActivationSource.IsValid ──────────────────────────────────────────────────

func TestActivationSource_IsValid_AllFiveValues(t *testing.T) {
	valid := []skill.ActivationSource{
		skill.SourceManual,
		skill.SourceLegacySeed,
		skill.SourceArchiveWorker,
		skill.SourceLLMProposal,
		skill.SourceImported,
	}
	for _, a := range valid {
		require.True(t, a.IsValid(), "expected activation source %q to be valid", a)
	}
}

func TestActivationSource_IsValid_InvalidValue(t *testing.T) {
	require.False(t, skill.ActivationSource("promoted").IsValid(), "promoted is not valid in V4.1 §5.2")
	require.False(t, skill.ActivationSource("").IsValid(), "empty activation source must be invalid")
}

func TestActivationSource_String(t *testing.T) {
	require.Equal(t, "manual", skill.SourceManual.String())
	require.Equal(t, "legacy_seed", skill.SourceLegacySeed.String())
}

// ── Scope JSON round-trip ─────────────────────────────────────────────────────

func TestScope_JSONRoundTrip(t *testing.T) {
	s := skill.Scope{
		ProjectID: "proj-1",
		RepoID:    "repo-1",
		Phases:    []string{"apply", "verify"},
	}
	data, err := json.Marshal(s)
	require.NoError(t, err)

	var got skill.Scope
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, s, got)
}

// ── AppliesWhen JSON round-trip ───────────────────────────────────────────────

func TestAppliesWhen_JSONRoundTrip(t *testing.T) {
	aw := skill.AppliesWhen{
		FeatureType:  []string{"bugfix"},
		TouchedPaths: []string{"internal/**/*.go"},
		ExcludePaths: []string{"vendor/**"},
	}
	data, err := json.Marshal(aw)
	require.NoError(t, err)

	var got skill.AppliesWhen
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, aw, got)
}

// ── Metrics JSON round-trip ───────────────────────────────────────────────────

func TestMetrics_JSONRoundTrip(t *testing.T) {
	m := skill.Metrics{
		UsageCount:        10,
		SuccessCount:      8,
		FailureCount:      2,
		TestsPassedCount:  5,
		DeprecatedAPIHits: 1,
		RollbackCount:     0,
		AvgRetryReduction: 0.25,
		LastStackVersion:  nil,
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)

	var got skill.Metrics
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, m, got)
}

func TestMetrics_JSONRoundTrip_WithLastStackVersion(t *testing.T) {
	v := "v1.2.3"
	m := skill.Metrics{LastStackVersion: &v}
	data, err := json.Marshal(m)
	require.NoError(t, err)

	var got skill.Metrics
	require.NoError(t, json.Unmarshal(data, &got))
	require.NotNil(t, got.LastStackVersion)
	require.Equal(t, v, *got.LastStackVersion)
}
