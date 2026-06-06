package config_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
	"github.com/stretchr/testify/require"
)

// Spec #61 (BUG-15) — ApplyConfig.WorktreeRoot must load from
// SOPHIA_APPLY_WORKTREE_ROOT so operators using the MCP bridge can
// point worktrees at a host-mounted path. Unset preserves the legacy
// container-local default by leaving the field empty (the bootstrap
// only overrides apply.DefaultRunConfig().WorktreeRoot when non-empty).

func TestApplyConfig_WorktreeRoot_LoadsFromEnv(t *testing.T) {
	want := "/Users/op/workspace/.sophia-worktrees"
	minimalEnv(t, "SOPHIA_APPLY_WORKTREE_ROOT", want)

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, want, cfg.Apply.WorktreeRoot,
		"WorktreeRoot must load from SOPHIA_APPLY_WORKTREE_ROOT")
}

func TestApplyConfig_WorktreeRoot_EmptyWhenUnset(t *testing.T) {
	// minimalEnv stamps only the four URL/key requirements; it does
	// NOT touch SOPHIA_APPLY_WORKTREE_ROOT, so any parent-env value
	// remains. Tests run with -test.run filters so cross-test
	// pollution is possible — guard by setting an explicit empty
	// value via Setenv, which still satisfies LookupEnv("..., ok=true)
	// → envStr returns the empty string → c.Apply.WorktreeRoot empty.
	minimalEnv(t)
	t.Setenv("SOPHIA_APPLY_WORKTREE_ROOT", "")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Empty(t, cfg.Apply.WorktreeRoot,
		"unset env must leave WorktreeRoot empty so bootstrap keeps DefaultRunConfig")
}

// ---------------------------------------------------------------------------
// ADR-0010 Slice 3: SOPHIA_DISPATCH_TIMEOUT_MS — configurable short dispatch
// timeout. Default 180_000 ms (3 min) in apply.DefaultRunConfig; operator
// override forwarded via ApplyConfig.DispatchTimeoutMS → wire.go → RunConfig.
// ---------------------------------------------------------------------------

// TestApplyConfig_DispatchTimeoutMS_DefaultIsZero verifies that when
// SOPHIA_DISPATCH_TIMEOUT_MS is not set, ApplyConfig.DispatchTimeoutMS is
// zero — the bootstrap then leaves apply.DefaultRunConfig's value (180_000)
// in effect rather than applying a spurious override.
func TestApplyConfig_DispatchTimeoutMS_DefaultIsZero(t *testing.T) {
	minimalEnv(t)
	t.Setenv("SOPHIA_DISPATCH_TIMEOUT_MS", "") // guard against parent-env pollution

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Zero(t, cfg.Apply.DispatchTimeoutMS,
		"unset SOPHIA_DISPATCH_TIMEOUT_MS must leave DispatchTimeoutMS zero "+
			"so bootstrap falls back to apply.DefaultRunConfig (180_000 ms)")
}

// TestApplyConfig_DispatchTimeoutMS_ParsedFromEnv verifies that a valid
// positive integer in SOPHIA_DISPATCH_TIMEOUT_MS is loaded into
// ApplyConfig.DispatchTimeoutMS.
func TestApplyConfig_DispatchTimeoutMS_ParsedFromEnv(t *testing.T) {
	minimalEnv(t, "SOPHIA_DISPATCH_TIMEOUT_MS", "300000")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Equal(t, 300_000, cfg.Apply.DispatchTimeoutMS,
		"SOPHIA_DISPATCH_TIMEOUT_MS=300000 must parse to 300_000 ms")
}

// TestApplyConfig_DispatchTimeoutMS_InvalidFallsToZero verifies that a
// non-integer value in SOPHIA_DISPATCH_TIMEOUT_MS is ignored — the field
// stays zero so bootstrap uses apply.DefaultRunConfig's default. This
// matches the envInt helper's "invalid → return default" behaviour.
func TestApplyConfig_DispatchTimeoutMS_InvalidFallsToZero(t *testing.T) {
	minimalEnv(t, "SOPHIA_DISPATCH_TIMEOUT_MS", "not-a-number")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Zero(t, cfg.Apply.DispatchTimeoutMS,
		"invalid SOPHIA_DISPATCH_TIMEOUT_MS must be ignored (zero), "+
			"keeping apply.DefaultRunConfig default in effect")
}

// TestApplyConfig_DispatchTimeoutMS_ZeroValueIgnored verifies that
// SOPHIA_DISPATCH_TIMEOUT_MS=0 is treated as "unset" — zero is not a
// valid timeout (would mean instant expiry) so bootstrap must not forward
// it to RunConfig.DispatchTimeoutMS.
func TestApplyConfig_DispatchTimeoutMS_ZeroValueIgnored(t *testing.T) {
	minimalEnv(t, "SOPHIA_DISPATCH_TIMEOUT_MS", "0")

	cfg, err := config.Load()
	require.NoError(t, err)
	require.Zero(t, cfg.Apply.DispatchTimeoutMS,
		"SOPHIA_DISPATCH_TIMEOUT_MS=0 must be treated as unset — "+
			"zero is not a valid timeout value")
}
