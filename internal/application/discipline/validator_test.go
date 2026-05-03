package discipline_test

import (
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func validRaw(t *testing.T, overrides map[string]any) []byte {
	t.Helper()
	base := map[string]any{
		"schema_version":    "v1",
		"phase":             "spec",
		"change_name":       "user-auth",
		"project":           "ms-cotizacion",
		"status":            "DONE",
		"confidence":        0.85,
		"executive_summary": "spec drafted",
		"artifacts_saved":   []map[string]any{},
		"next_recommended":  []string{"design"},
		"risks":             []map[string]string{},
		"data":              map[string]any{},
	}
	for k, v := range overrides {
		base[k] = v
	}
	raw, err := json.Marshal(base)
	require.NoError(t, err)
	return raw
}

func TestValidator_Valid(t *testing.T) {
	v := discipline.NewValidator()
	e, err := v.Validate(validRaw(t, nil), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusDone, e.Status)
	require.InDelta(t, 0.85, e.Confidence, 0.0001)
}

func TestValidator_RejectsInvalidExpected(t *testing.T) {
	v := discipline.NewValidator()
	_, err := v.Validate(validRaw(t, nil), phase.PhaseType("nope"))
	require.ErrorIs(t, err, discipline.ErrInvalidPhase)
}

func TestValidator_PhaseMismatch(t *testing.T) {
	v := discipline.NewValidator()
	_, err := v.Validate(validRaw(t, map[string]any{"phase": "design"}), phase.PhaseSpec)
	require.ErrorIs(t, err, discipline.ErrPhaseMismatch)
}

func TestValidator_DoneBelowThreshold_CoercesToConcerns(t *testing.T) {
	v := discipline.NewValidator()
	// spec threshold = 0.8; agent claimed DONE with 0.5
	e, err := v.Validate(validRaw(t, map[string]any{"confidence": 0.5}), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusDoneWithConcerns, e.Status, "must coerce DONE → DONE_WITH_CONCERNS below threshold")
	require.InDelta(t, 0.5, e.Confidence, 0.0001, "confidence preserved")
}

func TestValidator_DoneWithConcerns_NoCoercion(t *testing.T) {
	v := discipline.NewValidator()
	e, err := v.Validate(validRaw(t, map[string]any{"status": "DONE_WITH_CONCERNS", "confidence": 0.4}), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusDoneWithConcerns, e.Status)
}

func TestValidator_BlockedKeepsStatus(t *testing.T) {
	v := discipline.NewValidator()
	e, err := v.Validate(validRaw(t, map[string]any{"status": "BLOCKED", "confidence": 0.0}), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusBlocked, e.Status)
}

func TestValidator_NeedsContextKeepsStatus(t *testing.T) {
	v := discipline.NewValidator()
	e, err := v.Validate(validRaw(t, map[string]any{"status": "NEEDS_CONTEXT", "confidence": 0.3}), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusNeedsContext, e.Status)
}

func TestValidator_PropagatesParseError(t *testing.T) {
	v := discipline.NewValidator()
	_, err := v.Validate([]byte(`{not json`), phase.PhaseSpec)
	require.ErrorIs(t, err, envelope.ErrInvalidJSON)
}

func TestValidator_DoneAtThreshold_Allowed(t *testing.T) {
	v := discipline.NewValidator()
	// spec threshold exactly 0.8 → DONE allowed
	e, err := v.Validate(validRaw(t, map[string]any{"confidence": 0.8}), phase.PhaseSpec)
	require.NoError(t, err)
	require.Equal(t, envelope.StatusDone, e.Status)
}
