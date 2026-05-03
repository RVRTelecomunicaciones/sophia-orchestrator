package change_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func mkChangeID(t *testing.T) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	require.NoError(t, err)
	return id
}

func now() time.Time {
	return time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
}

func TestStatus_IsValid(t *testing.T) {
	require.True(t, change.StatusActive.IsValid())
	require.True(t, change.StatusCompleted.IsValid())
	require.True(t, change.StatusAborted.IsValid())
	require.False(t, change.Status("nope").IsValid())
}

func TestArtifactStoreMode_IsValid(t *testing.T) {
	require.True(t, change.ArtifactStoreMemoryEngine.IsValid())
	require.True(t, change.ArtifactStoreOpenspec.IsValid())
	require.True(t, change.ArtifactStoreHybrid.IsValid())
	require.True(t, change.ArtifactStoreNone.IsValid())
	require.False(t, change.ArtifactStoreMode("nope").IsValid())
}

func TestArtifactStoreMode_Values(t *testing.T) {
	require.Equal(t, "memory-engine", string(change.ArtifactStoreMemoryEngine))
	require.Equal(t, "openspec", string(change.ArtifactStoreOpenspec))
	require.Equal(t, "hybrid", string(change.ArtifactStoreHybrid))
	require.Equal(t, "none", string(change.ArtifactStoreNone))
}

func TestNew_Valid(t *testing.T) {
	c, err := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "main", now())
	require.NoError(t, err)
	require.Equal(t, "feat-x", c.Name())
	require.Equal(t, "proj", c.Project())
	require.Equal(t, change.StatusActive, c.Status())
	require.Equal(t, phase.PhaseInit, c.CurrentPhase())
	require.Equal(t, change.ArtifactStoreMemoryEngine, c.ArtifactStore())
	require.Equal(t, "main", c.BaseRef())
}

func TestNew_RejectsEmptyName(t *testing.T) {
	_, err := change.New(mkChangeID(t), "", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.ErrorIs(t, err, change.ErrEmptyName)
}

func TestNew_RejectsEmptyProject(t *testing.T) {
	_, err := change.New(mkChangeID(t), "feat-x", "", change.ArtifactStoreMemoryEngine, "", now())
	require.ErrorIs(t, err, change.ErrEmptyProject)
}

func TestNew_RejectsInvalidArtifactStore(t *testing.T) {
	_, err := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMode("nope"), "", now())
	require.ErrorIs(t, err, change.ErrInvalidArtifactStore)
}

func TestAdvancePhase_ValidTransition(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.AdvancePhase(phase.PhaseExplore, now()))
	require.Equal(t, phase.PhaseExplore, c.CurrentPhase())
}

func TestAdvancePhase_InvalidTransition(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	err := c.AdvancePhase(phase.PhaseApply, now())
	require.ErrorIs(t, err, change.ErrInvalidTransition)
}

func TestAdvancePhase_ProposalAllowsSpecOrDesign(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.AdvancePhase(phase.PhaseExplore, now()))
	require.NoError(t, c.AdvancePhase(phase.PhaseProposal, now()))
	// From proposal we can go to either spec or design.
	require.NoError(t, c.AdvancePhase(phase.PhaseDesign, now()))

	// Reset and try spec instead.
	c2, _ := change.New(mkChangeID(t), "feat-y", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c2.AdvancePhase(phase.PhaseExplore, now()))
	require.NoError(t, c2.AdvancePhase(phase.PhaseProposal, now()))
	require.NoError(t, c2.AdvancePhase(phase.PhaseSpec, now()))
}

func TestAbort_FromActive(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.Abort("user request", now()))
	require.Equal(t, change.StatusAborted, c.Status())
}

func TestAbort_RejectsAlreadyTerminal(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.Abort("first", now()))
	err := c.Abort("again", now())
	require.ErrorIs(t, err, change.ErrAlreadyTerminal)
}

func TestMarkCompleted_FromActive(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.MarkCompleted(now()))
	require.Equal(t, change.StatusCompleted, c.Status())
}

func TestAdvancePhase_RejectsAfterAbort(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.Abort("reason", now()))
	err := c.AdvancePhase(phase.PhaseExplore, now())
	require.ErrorIs(t, err, change.ErrAlreadyTerminal)
}

func TestMarkCompleted_RejectsAfterAbort(t *testing.T) {
	c, _ := change.New(mkChangeID(t), "feat-x", "proj", change.ArtifactStoreMemoryEngine, "", now())
	require.NoError(t, c.Abort("first", now()))
	err := c.MarkCompleted(now())
	require.ErrorIs(t, err, change.ErrAlreadyTerminal)
}

func TestChangeGetters_AllExposed(t *testing.T) {
	id := mkChangeID(t)
	c, err := change.New(id, "feat-x", "proj", change.ArtifactStoreMemoryEngine, "main", now())
	require.NoError(t, err)
	require.Equal(t, id, c.ID())
	require.Equal(t, "feat-x", c.Name())
	require.Equal(t, "proj", c.Project())
	require.Equal(t, "main", c.BaseRef())
	require.Equal(t, now(), c.CreatedAt())
	require.Equal(t, now(), c.UpdatedAt())
}

func TestHydrate_Roundtrip(t *testing.T) {
	c := change.Hydrate(
		mkChangeID(t),
		"feat-x", "proj",
		change.StatusActive,
		phase.PhaseSpec,
		change.ArtifactStoreHybrid,
		"main",
		now(), now(),
	)
	require.Equal(t, change.StatusActive, c.Status())
	require.Equal(t, phase.PhaseSpec, c.CurrentPhase())
	require.Equal(t, change.ArtifactStoreHybrid, c.ArtifactStore())
}
