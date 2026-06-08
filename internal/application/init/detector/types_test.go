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
		SophiaDetectorVer: "v1.0.0",
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

// B.2: StructuralContextSchemaV1 constant equals 1.
func TestStructuralContextSchemaV1_Constant(t *testing.T) {
	require.Equal(t, 1, detector.StructuralContextSchemaV1,
		"StructuralContextSchemaV1 must be 1; bump only with a migration plan")
}
