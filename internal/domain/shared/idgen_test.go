package shared_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

func TestFixedIDGenerator_ReturnsConfiguredIDs(t *testing.T) {
	g := shared.FixedIDGenerator([]string{
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"01ARZ3NDEKTSV4RRFFQ69G5FAW",
	})
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5FAV", g.NewID())
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5FAW", g.NewID())
}

func TestFixedIDGenerator_ExhaustedReturnsEmpty(t *testing.T) {
	g := shared.FixedIDGenerator([]string{"01ARZ3NDEKTSV4RRFFQ69G5FAV"})
	_ = g.NewID()
	require.Empty(t, g.NewID())
}

func TestSystemIDGenerator_GeneratesValidULIDs(t *testing.T) {
	c := shared.FixedClock(time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC))
	g := shared.NewSystemIDGenerator(c)
	id1 := g.NewID()
	id2 := g.NewID()
	require.Len(t, id1, 26)
	require.Len(t, id2, 26)
	require.NotEqual(t, id1, id2) // monotonic entropy ensures uniqueness
}
