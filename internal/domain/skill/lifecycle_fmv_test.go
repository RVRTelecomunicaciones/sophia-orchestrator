package skill_test

// lifecycle_fmv_test.go — T3.4 RED: AppliesWhen.FrameworkMinVersion field
// JSON serialisation/deserialisation tests (DG-C7-4).
//
// Test layer: unit, pure JSON round-trip, no I/O.
// RED — FrameworkMinVersion field does not exist until T3.5 GREEN.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// TestAppliesWhen_FrameworkMinVersion_AbsentInLegacyJSON verifies backward
// compatibility: a skill persisted before this change (no framework_min_version
// key) deserialises with a nil map and no error.
func TestAppliesWhen_FrameworkMinVersion_AbsentInLegacyJSON(t *testing.T) {
	raw := `{"framework":["angular"]}`
	var aw skill.AppliesWhen
	require.NoError(t, json.Unmarshal([]byte(raw), &aw))
	assert.Equal(t, []string{"angular"}, aw.Framework)
	assert.Nil(t, aw.FrameworkMinVersion, "absent key must deserialise as nil map")
}

// TestAppliesWhen_FrameworkMinVersion_PresentJSON verifies that the map field
// is correctly deserialised when present alongside Framework.
func TestAppliesWhen_FrameworkMinVersion_PresentJSON(t *testing.T) {
	raw := `{"framework":["angular"],"framework_min_version":{"angular":"22.0.0"}}`
	var aw skill.AppliesWhen
	require.NoError(t, json.Unmarshal([]byte(raw), &aw))
	assert.Equal(t, []string{"angular"}, aw.Framework)
	require.NotNil(t, aw.FrameworkMinVersion)
	assert.Equal(t, "22.0.0", aw.FrameworkMinVersion["angular"])
}

// TestAppliesWhen_FrameworkMinVersion_OmittedWhenNil verifies omitempty: when
// FrameworkMinVersion is nil, the key must be absent from marshalled JSON.
func TestAppliesWhen_FrameworkMinVersion_OmittedWhenNil(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework: []string{"angular"},
	}
	data, err := json.Marshal(aw)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "framework_min_version",
		"nil map must be absent from JSON")
}

// TestAppliesWhen_FrameworkMinVersion_OmittedWhenEmpty verifies omitempty: when
// FrameworkMinVersion is an allocated but empty map, the key must also be absent.
func TestAppliesWhen_FrameworkMinVersion_OmittedWhenEmpty(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular"},
		FrameworkMinVersion: map[string]string{},
	}
	data, err := json.Marshal(aw)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "framework_min_version",
		"empty map must be absent from JSON")
}

// TestAppliesWhen_FrameworkMinVersion_RoundTrip verifies full marshal/unmarshal
// cycle preserves both Framework and FrameworkMinVersion correctly.
func TestAppliesWhen_FrameworkMinVersion_RoundTrip(t *testing.T) {
	aw := skill.AppliesWhen{
		Framework:           []string{"angular", "react"},
		FrameworkMinVersion: map[string]string{"angular": "22.0.0", "react": "18.0.0"},
	}
	data, err := json.Marshal(aw)
	require.NoError(t, err)

	var got skill.AppliesWhen
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, aw.Framework, got.Framework)
	assert.Equal(t, aw.FrameworkMinVersion, got.FrameworkMinVersion)
}
