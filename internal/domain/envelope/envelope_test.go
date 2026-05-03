package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
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
		"artifacts_saved":   []map[string]any{{"topic_key": "sdd/user-auth/spec", "type": "spec"}},
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

func TestParse_Valid(t *testing.T) {
	e, err := envelope.Parse(validRaw(t, nil))
	require.NoError(t, err)
	require.Equal(t, "v1", e.SchemaVersion)
	require.Equal(t, "spec", e.Phase)
	require.Equal(t, envelope.StatusDone, e.Status)
	require.InDelta(t, 0.85, e.Confidence, 0.0001)
	require.Len(t, e.ArtifactsSaved, 1)
	require.Equal(t, "sdd/user-auth/spec", e.ArtifactsSaved[0].TopicKey)
}

func TestParse_RejectsWrongSchemaVersion(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"schema_version": "v0"}))
	require.ErrorIs(t, err, envelope.ErrUnsupportedSchemaVersion)
}

func TestParse_RejectsInvalidStatus(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"status": "WHATEVER"}))
	require.ErrorIs(t, err, envelope.ErrInvalidStatus)
}

func TestParse_RejectsConfidenceTooHigh(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"confidence": 1.5}))
	require.ErrorIs(t, err, envelope.ErrConfidenceOutOfRange)
}

func TestParse_RejectsConfidenceTooLow(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"confidence": -0.1}))
	require.ErrorIs(t, err, envelope.ErrConfidenceOutOfRange)
}

func TestParse_RejectsEmptyPhase(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"phase": ""}))
	require.ErrorIs(t, err, envelope.ErrEmptyPhase)
}

func TestParse_RejectsEmptyChangeName(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"change_name": ""}))
	require.ErrorIs(t, err, envelope.ErrEmptyChangeName)
}

func TestParse_RejectsEmptyProject(t *testing.T) {
	_, err := envelope.Parse(validRaw(t, map[string]any{"project": ""}))
	require.ErrorIs(t, err, envelope.ErrEmptyProject)
}

func TestParse_RejectsInvalidJSON(t *testing.T) {
	_, err := envelope.Parse([]byte(`{not json`))
	require.ErrorIs(t, err, envelope.ErrInvalidJSON)
}

func TestStatus_IsValid(t *testing.T) {
	require.True(t, envelope.StatusDone.IsValid())
	require.True(t, envelope.StatusDoneWithConcerns.IsValid())
	require.True(t, envelope.StatusBlocked.IsValid())
	require.True(t, envelope.StatusNeedsContext.IsValid())
	require.False(t, envelope.Status("nope").IsValid())
}
