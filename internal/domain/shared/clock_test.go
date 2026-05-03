package shared_test

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/require"
)

func TestFixedClock_ReturnsConfiguredTime(t *testing.T) {
	fixed := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	c := shared.FixedClock(fixed)
	require.Equal(t, fixed, c.Now())
	require.Equal(t, fixed, c.Now()) // idempotent
}

func TestSystemClock_ReturnsRecentTime(t *testing.T) {
	c := shared.SystemClock{}
	before := time.Now()
	got := c.Now()
	after := time.Now()
	require.False(t, got.Before(before))
	require.False(t, got.After(after))
}
