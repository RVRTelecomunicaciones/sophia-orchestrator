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
