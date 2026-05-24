package mcp

import (
	"context"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec #60 (BUG-14) — the bridge's cwd allowlist is exact-match and
// rarely contains the bridge process pwd, so the orch must forward an
// absolute, allowlisted host directory whenever the caller asks for the
// relative sentinel "" or ".". Without this substitution every
// pre-apply phase fails with `provider_error: cwd_not_allowed` and the
// joint smoke can't even reach the parser fix (PR #37 / bridge BUG-13
// mirror).

func TestResolveCWDForBridge_DefaultEmpty_PreservesLegacyBehaviour(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"dot stays dot", ".", "."},
		{"absolute path unchanged", "/repo/worktrees/wt1", "/repo/worktrees/wt1"},
		{"relative path unchanged", "subdir", "subdir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveCWDForBridge(tc.in, ""))
		})
	}
}

func TestResolveCWDForBridge_DefaultSet_SubstitutesForSentinels(t *testing.T) {
	const def = "/Users/op/workspace"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty substituted", "", def},
		{"dot substituted", ".", def},
		{"absolute path unchanged", "/repo/worktrees/wt1", "/repo/worktrees/wt1"},
		{"non-sentinel relative path passes through (operator concern)", "subdir", "subdir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, resolveCWDForBridge(tc.in, def))
		})
	}
}

// Integration: when Config.DefaultCWD is set and the caller passes ".",
// Dispatch must forward the absolute default in args["cwd"]. This is the
// exact scenario that broke yesterday's joint smoke (explore phase
// rejected with cwd_not_allowed).
func TestDispatch_UsesDefaultCWD_WhenWorktreePathIsDotOrEmpty(t *testing.T) {
	cases := []struct {
		name        string
		worktree    string
		wantArgsCWD string
	}{
		{"dot is substituted", ".", "/Users/op/workspace"},
		{"empty is substituted", "", "/Users/op/workspace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envelope := map[string]any{"status": "ok"}
			sess := &fakeSession{
				result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 5)),
			}
			d := newTestDispatcher(singleOpener(sess), func(c *Config) {
				c.Provider = "opencode"
				c.DefaultCWD = "/Users/op/workspace"
			})

			_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
				Prompt:       "do thing",
				WorktreePath: tc.worktree,
				TimeoutMS:    3000,
			})
			require.NoError(t, err)
			require.Len(t, sess.callArgs, 1)
			args := sess.callArgs[0]
			assert.Equal(t, tc.wantArgsCWD, args["cwd"],
				"sentinel WorktreePath must be replaced with Config.DefaultCWD")
		})
	}
}

// Negative: an absolute WorktreePath (typical apply-phase shape) must
// NEVER be rewritten even when DefaultCWD is set. Apply needs the
// per-group worktree path verbatim.
func TestDispatch_AbsoluteWorktreePath_NotRewritten_EvenWithDefault(t *testing.T) {
	envelope := map[string]any{"status": "ok"}
	sess := &fakeSession{
		result: mkTextResult(bridgeJSON("ok", "", "", "", "", envelope, 0, 5)),
	}
	d := newTestDispatcher(singleOpener(sess), func(c *Config) {
		c.Provider = "opencode"
		c.DefaultCWD = "/Users/op/workspace"
	})

	_, err := d.Dispatch(context.Background(), outbound.DispatchRequest{
		Prompt:       "apply",
		WorktreePath: "/tmp/sophia/worktrees/abc/g1-greeting",
		TimeoutMS:    3000,
	})
	require.NoError(t, err)
	require.Len(t, sess.callArgs, 1)
	assert.Equal(t, "/tmp/sophia/worktrees/abc/g1-greeting",
		sess.callArgs[0]["cwd"],
		"absolute worktree path must not be substituted by DefaultCWD")
}
