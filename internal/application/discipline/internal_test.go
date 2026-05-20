package discipline

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

// TestRealSleeper_NoPanicOnZero covers realSleeper.Sleep without burning real time.
func TestRealSleeper_NoPanicOnZero(t *testing.T) {
	realSleeper{}.Sleep(0)
}

// TestRealJitter_ZeroSpan covers the early-return branch.
func TestRealJitter_ZeroSpan(t *testing.T) {
	j := realJitter{}
	require.Equal(t, time.Duration(0), j.Jitter(0))
	require.Equal(t, time.Duration(0), j.Jitter(-1))
}

// TestRealJitter_PositiveSpan exercises the rand.Int64N branch.
func TestRealJitter_PositiveSpan(t *testing.T) {
	j := realJitter{}
	for i := 0; i < 32; i++ {
		got := j.Jitter(100 * time.Millisecond)
		require.GreaterOrEqual(t, got, time.Duration(0))
		require.Less(t, got, 100*time.Millisecond)
	}
}

// TestHardGatesFor_DefaultBranch exercises the unreachable default for coverage.
func TestHardGatesFor_DefaultBranch(t *testing.T) {
	// hardGatesFor uses an exhaustive switch on PhaseType plus a default
	// branch; the default exists as a defensive guard for invalid types.
	require.Nil(t, hardGatesFor(PromptInput{Phase: phase.PhaseType("synthetic-invalid")}))
}
