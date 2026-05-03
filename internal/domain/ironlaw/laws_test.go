package ironlaw_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ironlaw"
	"github.com/stretchr/testify/require"
)

func TestAll_ReturnsFiveLaws(t *testing.T) {
	require.Len(t, ironlaw.All(), 5)
}

func TestAll_StableOrder(t *testing.T) {
	laws := ironlaw.All()
	expected := []ironlaw.ID{
		ironlaw.IronLaw1,
		ironlaw.IronLaw2,
		ironlaw.IronLaw3,
		ironlaw.IronLaw4,
		ironlaw.IronLaw5,
	}
	for i, l := range laws {
		require.Equal(t, expected[i], l.ID)
	}
}

func TestAll_EveryLawHasIDDescriptionRationale(t *testing.T) {
	for _, l := range ironlaw.All() {
		require.NotEmpty(t, l.ID, "id must not be empty")
		require.NotEmpty(t, l.Description, "description must not be empty for %s", l.ID)
		require.NotEmpty(t, l.Rationale, "rationale must not be empty for %s", l.ID)
	}
}

func TestAll_ReturnsCopy(t *testing.T) {
	laws := ironlaw.All()
	original := laws[0].Description
	laws[0].Description = "tampered"
	require.Equal(t, original, ironlaw.All()[0].Description, "All() must return a defensive copy")
}

func TestByID_Found(t *testing.T) {
	cases := []ironlaw.ID{
		ironlaw.IronLaw1, ironlaw.IronLaw2, ironlaw.IronLaw3,
		ironlaw.IronLaw4, ironlaw.IronLaw5,
	}
	for _, id := range cases {
		l, ok := ironlaw.ByID(id)
		require.True(t, ok, "id %s must be findable", id)
		require.Equal(t, id, l.ID)
	}
}

func TestByID_NotFound(t *testing.T) {
	_, ok := ironlaw.ByID("IL99_NONEXISTENT")
	require.False(t, ok)
}

func TestID_IsValid(t *testing.T) {
	require.True(t, ironlaw.IronLaw1.IsValid())
	require.False(t, ironlaw.ID("nope").IsValid())
}
