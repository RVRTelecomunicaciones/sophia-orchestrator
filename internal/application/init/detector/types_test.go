package detector_test

// types_test.go — B.1 + B.2 (Strict TDD: RED tests first)
//
// Tests that StructuralContext JSON round-trip preserves all fields and that
// StructuralContextSchemaV1 constant equals 1.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/stretchr/testify/require"
)

// B.1: JSON marshal/unmarshal round-trip preserves all fields including SchemaVersion=1.
func TestStructuralContext_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 8, 10, 0, 0, 0, time.UTC)
	orig := detector.StructuralContext{
		SchemaVersion: detector.StructuralContextSchemaV1,
		ProjectID:     "proj-1",
		ChangeID:      "ch-1",
		ChangeName:    "my-change",
		Languages: []detector.LanguageInfo{
			{Name: "Go", VersionEvidence: "go 1.26", FilesCount: 42},
		},
		Frameworks: []detector.FrameworkInfo{
			{Name: "Gin", Version: "1.9", EvidencePath: "go.mod"},
		},
		PackageManagers: []string{"go modules"},
		ArchStyle:       []string{"hexagonal"},
		GraphSummary: &detector.GraphSummary{
			TotalNodes:     100,
			TotalEdges:     200,
			CommunityCount: 5,
			GodNodes:       []string{"main.go"},
		},
		AffectedModules:   []string{"internal/application"},
		ConventionHints:   []string{"use injectable clock"},
		GraphAvailable:    true,
		DegradedReason:    "",
		DetectedAt:        now,
		GraphifyVersion:   "0.8.35",
		SophiaDetectorVer: "v1.1.0",
	}

	raw, err := json.Marshal(orig)
	require.NoError(t, err)

	var got detector.StructuralContext
	require.NoError(t, json.Unmarshal(raw, &got))

	require.Equal(t, detector.StructuralContextSchemaV1, got.SchemaVersion)
	require.Equal(t, orig.ProjectID, got.ProjectID)
	require.Equal(t, orig.ChangeID, got.ChangeID)
	require.Equal(t, orig.ChangeName, got.ChangeName)
	require.Equal(t, orig.Languages, got.Languages)
	require.Equal(t, orig.Frameworks, got.Frameworks)
	require.Equal(t, orig.PackageManagers, got.PackageManagers)
	require.Equal(t, orig.ArchStyle, got.ArchStyle)
	require.NotNil(t, got.GraphSummary)
	require.Equal(t, orig.GraphSummary.TotalNodes, got.GraphSummary.TotalNodes)
	require.Equal(t, orig.GraphSummary.TotalEdges, got.GraphSummary.TotalEdges)
	require.Equal(t, orig.GraphSummary.CommunityCount, got.GraphSummary.CommunityCount)
	require.Equal(t, orig.GraphSummary.GodNodes, got.GraphSummary.GodNodes)
	require.Equal(t, orig.AffectedModules, got.AffectedModules)
	require.Equal(t, orig.ConventionHints, got.ConventionHints)
	require.Equal(t, orig.GraphAvailable, got.GraphAvailable)
	require.Equal(t, orig.DegradedReason, got.DegradedReason)
	require.True(t, orig.DetectedAt.Equal(got.DetectedAt))
	require.Equal(t, orig.GraphifyVersion, got.GraphifyVersion)
	require.Equal(t, orig.SophiaDetectorVer, got.SophiaDetectorVer)
}

// B.3 (T2.5 RED): Greenfield=false is omitted in JSON (omitempty).
func TestStructuralContext_Greenfield_FalseOmitted(t *testing.T) {
	sc := detector.StructuralContext{
		SchemaVersion: detector.StructuralContextSchemaV1,
		Greenfield:    false,
	}
	raw, err := json.Marshal(sc)
	require.NoError(t, err)
	require.NotContains(t, string(raw), `"greenfield"`,
		"Greenfield=false must be omitted from JSON (omitempty)")
}

// B.4 (T2.5 RED): Greenfield=true is present in JSON.
func TestStructuralContext_Greenfield_TruePresent(t *testing.T) {
	sc := detector.StructuralContext{
		SchemaVersion: detector.StructuralContextSchemaV1,
		Greenfield:    true,
	}
	raw, err := json.Marshal(sc)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"greenfield":true`,
		"Greenfield=true must appear in JSON")
}

// B.5 (T2.5 RED): Backward compat — JSON without "greenfield" → Greenfield=false, no error.
func TestStructuralContext_Greenfield_BackwardCompat(t *testing.T) {
	oldJSON := `{"schema_version":1,"project_id":"p","change_id":"c","change_name":"n","graph_available":false,"detected_at":"2026-01-01T00:00:00Z","sophia_detector_ver":"v1.0.0"}`
	var sc detector.StructuralContext
	require.NoError(t, json.Unmarshal([]byte(oldJSON), &sc))
	require.False(t, sc.Greenfield,
		"Greenfield must be false (zero value) when key is absent in JSON")
}

// B.2: StructuralContextSchemaV1 constant equals 1.
func TestStructuralContextSchemaV1_Constant(t *testing.T) {
	require.Equal(t, 1, detector.StructuralContextSchemaV1,
		"StructuralContextSchemaV1 must be 1; bump only with a migration plan")
}
