package apply

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// captureRuntime is a stub RuntimeClient that captures the dispatched
// ExecutionRequest and returns a canned ExecutionReceipt — same pattern
// as the dispatcher adapters' fakeRuntime, but local to this test file
// so the synthesize tests don't drag run_test.go's broader fixture
// machinery into their setup.
type captureRuntime struct {
	captured outbound.ExecutionRequest
	recp     *outbound.ExecutionReceipt
	err      error
}

func (c *captureRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	c.captured = req
	if c.err != nil {
		return nil, c.err
	}
	return c.recp, nil
}

// --- parsePorcelainV1 ---

func TestParsePorcelainV1_ParsesTheFiveActionCodes(t *testing.T) {
	// Single fixture covers M (modified), A (created via add), D
	// (deleted), R (renamed) with the "old -> new" path syntax, and
	// ?? (untracked = created from the apply executor's perspective).
	stdout := " M cmd/server/main.go\n" +
		"A  internal/auth/jwt.go\n" +
		" D legacy/old.go\n" +
		"R  internal/old/path.go -> internal/new/path.go\n" +
		"?? .env.local\n"

	got := parsePorcelainV1(stdout)
	require.Len(t, got, 5)
	require.Equal(t, edit{Path: "cmd/server/main.go", Action: "modified"}, got[0])
	require.Equal(t, edit{Path: "internal/auth/jwt.go", Action: "created"}, got[1])
	require.Equal(t, edit{Path: "legacy/old.go", Action: "deleted"}, got[2])
	require.Equal(t, edit{Path: "internal/new/path.go", Action: "renamed"}, got[3],
		"renamed entries must report the NEW path, not the old one")
	require.Equal(t, edit{Path: ".env.local", Action: "created"}, got[4],
		"untracked files (??) must be reported as created — they're new in the worktree")
}

func TestParsePorcelainV1_EmptyStdoutReturnsEmptySlice(t *testing.T) {
	// Critical for the PARTIAL status path: the synthesize helper must
	// produce a non-nil empty slice (not nil) so the JSON envelope's
	// "edits" key always serializes as `[]`, not `null`.
	got := parsePorcelainV1("")
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestParsePorcelainV1_SkipsMalformedLines(t *testing.T) {
	// Defensive: lines shorter than the porcelain v1 minimum (2 status
	// chars + space + at least 1 path char = 4) are silently skipped
	// rather than panicking on a slice index. Trailing newlines are
	// the most common cause of empty lines.
	stdout := "\n M ok.go\n??\nshort\n"
	got := parsePorcelainV1(stdout)
	require.Len(t, got, 1)
	require.Equal(t, "ok.go", got[0].Path)
}

func TestParsePorcelainV1_UnknownStatusCodeSkipped(t *testing.T) {
	// Forward-compat: unknown porcelain codes (e.g. a hypothetical "T"
	// for type-change in some future git release) are skipped rather
	// than failing the whole envelope. The known codes that ARE in the
	// fixture still come through.
	stdout := "ZZ unknown.go\n M known.go\n"
	got := parsePorcelainV1(stdout)
	require.Len(t, got, 1)
	require.Equal(t, "known.go", got[0].Path)
}

// --- classifyStatus ---

func TestClassifyStatus_AllKnownCodes(t *testing.T) {
	tests := map[string]struct {
		x, y byte
		want string
	}{
		"M unstaged":      {' ', 'M', "modified"},
		"M staged":        {'M', ' ', "modified"},
		"A":               {'A', ' ', "created"},
		"untracked":       {'?', '?', "created"},
		"D":               {' ', 'D', "deleted"},
		"R":               {'R', ' ', "renamed"},
		"C":               {'C', ' ', "renamed"},
		"U conflict":      {'U', 'U', "modified"},
		"both unset":      {' ', ' ', ""},
		"unknown code":    {'Z', ' ', ""},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.want, classifyStatus(tt.x, tt.y))
		})
	}
}

// --- synthesizeEnvelopeFromGit ---

func TestSynthesizeEnvelopeFromGit_HappyPath_StatusDONE(t *testing.T) {
	rt := &captureRuntime{recp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte(" M foo.go\nA  bar.go\n"),
	}}

	raw, err := synthesizeEnvelopeFromGit(context.Background(), rt, "/tmp/wt")
	require.NoError(t, err)

	// Verify the runtime call shape: shell.exec@v1 with `git status
	// --porcelain=v1` and working_dir set to the worktree.
	require.Equal(t, "shell.exec@v1", rt.captured.Capability)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rt.captured.Payload, &payload))
	require.Equal(t, "git", payload["command"])
	args := payload["args"].([]any)
	require.Equal(t, []any{"status", "--porcelain=v1"}, args)
	require.Equal(t, "/tmp/wt", payload["working_dir"])

	// Verify envelope shape.
	var env map[string]any
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Equal(t, "v1", env["schema_version"])
	require.Equal(t, "apply", env["phase"])
	require.Equal(t, "DONE", env["status"], "non-empty git status must yield DONE")
	edits := env["edits"].([]any)
	require.Len(t, edits, 2)
}

func TestSynthesizeEnvelopeFromGit_EmptyStdoutYieldsPARTIAL(t *testing.T) {
	// Aider ran cleanly but didn't change anything (model refused, or
	// the worktree already matched the request). The synthesize helper
	// reports PARTIAL so downstream verify can decide whether the
	// absence of edits is acceptable for this task. The envelope is
	// still well-formed — empty edits, not nil.
	rt := &captureRuntime{recp: &outbound.ExecutionReceipt{
		Status: outbound.ReceiptSuccess,
		Stdout: []byte(""),
	}}

	raw, err := synthesizeEnvelopeFromGit(context.Background(), rt, "/tmp/wt")
	require.NoError(t, err)

	var env map[string]any
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Equal(t, "PARTIAL", env["status"])
	edits := env["edits"].([]any)
	require.NotNil(t, edits, "edits must be [] not null even when empty")
	require.Empty(t, edits)
}

func TestSynthesizeEnvelopeFromGit_RuntimeErrorPropagates(t *testing.T) {
	rt := &captureRuntime{err: errors.New("runtime down")}

	_, err := synthesizeEnvelopeFromGit(context.Background(), rt, "/tmp/wt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "runtime")
}

func TestSynthesizeEnvelopeFromGit_NonSuccessReceiptIsAnError(t *testing.T) {
	// `git status` failing (e.g. worktree not a git repo) MUST NOT
	// produce a synthetic envelope claiming PARTIAL — the operator
	// needs to see the failure as envelope-validation-failed so they
	// debug the worktree state, not interpret it as "aider made no
	// changes".
	rt := &captureRuntime{recp: &outbound.ExecutionReceipt{
		Status:   outbound.ReceiptFailure,
		Stderr:   []byte("fatal: not a git repository"),
		ExitCode: 128,
	}}

	_, err := synthesizeEnvelopeFromGit(context.Background(), rt, "/tmp/wt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "git status")
}

func TestSynthesizeEnvelopeFromGit_EmptyWorktreePathFailsFast(t *testing.T) {
	// Defensive: never shell out without an explicit working_dir. The
	// runtime would default to its own cwd which gives misleading
	// results.
	rt := &captureRuntime{}
	_, err := synthesizeEnvelopeFromGit(context.Background(), rt, "")
	require.Error(t, err)
	require.Empty(t, rt.captured.Payload, "must not call runtime when worktree path is empty")
}
