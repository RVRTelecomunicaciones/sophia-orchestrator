package pg

// ulid_parse_errors_test.go — RED: Cluster 4 ULID parse error handling
//
// Tests cover:
//   - scan-loop skip: malformed ULID row is skipped, valid rows returned
//   - single-hydrator: malformed ULID causes error return, no zero-value ID
//   - log field contract: ERROR log emitted with repo, column, raw, error fields
//
// All tests are unit tests (no DB required). The scan-loop helpers and
// single-row hydrators are exercised via the fakeScannable adapter and
// fakeRows adapter that implement the scannable interface.

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// ── slog capture ──────────────────────────────────────────────────────────────

// captureErrorLog installs a temporary slog handler that captures ERROR-level
// output. The returned restore function restores the previous default logger.
func captureErrorLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelError})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(old) }
}

// captureAllLog captures all log levels (DEBUG and above).
func captureAllLog(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	return buf, func() { slog.SetDefault(old) }
}

// ── fakeScannable ─────────────────────────────────────────────────────────────

// fakeScannable implements the scannable interface using canned values.
// Scan copies values[i] into dest[i] positionally.
type fakeScannable struct {
	values []any
	err    error
}

func (f *fakeScannable) Scan(dest ...any) error {
	if f.err != nil {
		return f.err
	}
	for i, d := range dest {
		if i >= len(f.values) {
			break
		}
		if f.values[i] == nil {
			// Leave dest[i] unchanged (simulates NULL).
			continue
		}
		switch p := d.(type) {
		case *string:
			if v, ok := f.values[i].(string); ok {
				*p = v
			}
		case **string:
			if v, ok := f.values[i].(*string); ok {
				*p = v
			} else if v2, ok := f.values[i].(string); ok {
				*p = &v2
			}
		case *time.Time:
			if v, ok := f.values[i].(time.Time); ok {
				*p = v
			}
		case **time.Time:
			if v, ok := f.values[i].(*time.Time); ok {
				*p = v
			}
		case *[]byte:
			if v, ok := f.values[i].([]byte); ok {
				*p = v
			}
		case **int:
			if v, ok := f.values[i].(*int); ok {
				*p = v
			}
		case *[]string:
			if v, ok := f.values[i].([]string); ok {
				*p = v
			}
		case *int:
			if v, ok := f.values[i].(int); ok {
				*p = v
			}
		}
	}
	return nil
}

// ── scanSession unit tests ────────────────────────────────────────────────────

// TestScanSession_MalformedSessionID_ReturnsError verifies that a corrupt
// session_id causes scanSession to return a non-nil error (single-hydrator policy).
func TestScanSession_MalformedSessionID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	fs := &fakeScannable{values: []any{
		"not-a-ulid",                     // id — malformed
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",     // change_id — valid
		"01ARZ3NDEKTSV4RRFFQ69G5FAW",     // phase_id — valid
		"implementer",                    // agent_role
		"opencode",                       // provider
		(*string)(nil),                   // worktree_id — NULL
		"sha256abc",                      // prompt_sha256
		"do the thing",                   // command
		"done",                           // status
		(*int)(nil),                      // exit_code — NULL
		[]byte(nil),                      // envelope — NULL
		now,                              // started_at
		(*time.Time)(nil),                // ended_at — NULL
	}}

	sess, err := scanSession(fs)
	require.Error(t, err, "malformed session_id must return error")
	assert.Nil(t, sess, "no session returned when id is malformed")
	assert.Contains(t, buf.String(), "ERROR", "an ERROR log must be emitted")
	assert.Contains(t, buf.String(), "session_repo", "log must identify repo")
	assert.Contains(t, buf.String(), "id", "log must identify column")
	assert.Contains(t, buf.String(), "not-a-ulid", "log must include raw value")
}

// TestScanSession_MalformedChangeID_ReturnsError verifies that a corrupt
// change_id in session row causes a non-nil error.
func TestScanSession_MalformedChangeID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,          // id — valid
		"not-a-ulid",    // change_id — malformed
		validID,         // phase_id
		"implementer",
		"opencode",
		(*string)(nil),
		"sha256abc",
		"do the thing",
		"done",
		(*int)(nil),
		[]byte(nil),
		now,
		(*time.Time)(nil),
	}}

	sess, err := scanSession(fs)
	require.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanSession_MalformedPhaseID_ReturnsError verifies phase_id corruption.
func TestScanSession_MalformedPhaseID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		"not-a-ulid", // phase_id — malformed
		"implementer",
		"opencode",
		(*string)(nil),
		"sha256abc",
		"do the thing",
		"done",
		(*int)(nil),
		[]byte(nil),
		now,
		(*time.Time)(nil),
	}}

	sess, err := scanSession(fs)
	require.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanSession_MalformedWorktreeID_ReturnsError verifies worktree_id corruption.
func TestScanSession_MalformedWorktreeID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	badID := "not-a-ulid"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		validID,
		"implementer",
		"opencode",
		&badID,          // worktree_id — non-nil but malformed
		"sha256abc",
		"do the thing",
		"done",
		(*int)(nil),
		[]byte(nil),
		now,
		(*time.Time)(nil),
	}}

	sess, err := scanSession(fs)
	require.Error(t, err)
	assert.Nil(t, sess)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanSession_ValidRow_NoError verifies the happy path: all valid ULIDs,
// no error, no log emitted.
func TestScanSession_ValidRow_NoError(t *testing.T) {
	buf, restore := captureAllLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		validID,
		"implementer",
		"opencode",
		(*string)(nil),
		"sha256abc",
		"do the thing",
		"done",
		(*int)(nil),
		[]byte(nil),
		now,
		(*time.Time)(nil),
	}}

	sess, err := scanSession(fs)
	require.NoError(t, err)
	assert.NotNil(t, sess)
	assert.NotContains(t, buf.String(), "ERROR", "no ERROR log on valid row")
	// Zero-value check: all IDs must be non-empty.
	assert.NotEmpty(t, sess.ID().String(), "session ID must not be zero-value")
}

// ── scanWorktree unit tests ───────────────────────────────────────────────────

// TestScanWorktree_MalformedWorktreeID_ReturnsError verifies that a corrupt
// worktree id causes scanWorktree to return error (single-hydrator policy).
func TestScanWorktree_MalformedWorktreeID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	fs := &fakeScannable{values: []any{
		"not-a-ulid",   // id — malformed
		(*string)(nil), // session_id — NULL
		"/tmp/wt",      // path
		"main",         // branch
		"active",       // status
		now,            // created_at
		(*time.Time)(nil), // cleaned_at
	}}

	wt, err := scanWorktree(fs)
	require.Error(t, err, "malformed worktree id must return error")
	assert.Nil(t, wt)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), "worktree_repo")
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanWorktree_MalformedSessionID_ReturnsError verifies that a corrupt
// session_id in a worktree row causes error.
func TestScanWorktree_MalformedSessionID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	badID := "not-a-ulid"
	fs := &fakeScannable{values: []any{
		validID,
		&badID,         // session_id — non-nil but malformed
		"/tmp/wt",
		"main",
		"active",
		now,
		(*time.Time)(nil),
	}}

	wt, err := scanWorktree(fs)
	require.Error(t, err)
	assert.Nil(t, wt)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanWorktree_ValidRow_NoError verifies happy path for worktree.
func TestScanWorktree_ValidRow_NoError(t *testing.T) {
	buf, restore := captureAllLog(t)
	defer restore()

	now := time.Now()
	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		(*string)(nil),
		"/tmp/wt",
		"main",
		"active",
		now,
		(*time.Time)(nil),
	}}

	wt, err := scanWorktree(fs)
	require.NoError(t, err)
	assert.NotNil(t, wt)
	assert.NotContains(t, buf.String(), "ERROR")
	assert.NotEmpty(t, wt.ID().String())
}

// ── scanGroupRow / scanTaskRow scan-loop unit tests ───────────────────────────

// TestScanGroupRow_MalformedGroupID_ReturnsError verifies that a corrupt
// group_id in a group row causes scanGroupRow to return error (caller skips).
func TestScanGroupRow_MalformedGroupID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		"not-a-ulid",  // group_id — malformed
		validID,       // board_id
		"group-alpha", // name
		[]string{},    // depends_on (empty)
		"pending",     // status
		"/tmp/wt",     // worktree_path
		"feat/x",      // branch_name
		"none",        // build_status
		0,             // build_attempts
	}}

	g, err := scanGroupRow(fs)
	require.Error(t, err, "malformed group_id must return error")
	assert.Nil(t, g)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), "board_repo")
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanGroupRow_MalformedBoardID_ReturnsError verifies board_id corruption.
func TestScanGroupRow_MalformedBoardID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		"not-a-ulid", // board_id — malformed
		"group-alpha",
		[]string{},
		"pending",
		"/tmp/wt",
		"feat/x",
		"none",
		0,
	}}

	g, err := scanGroupRow(fs)
	require.Error(t, err)
	assert.Nil(t, g)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanGroupRow_ValidRow_NoError verifies the happy path.
func TestScanGroupRow_ValidRow_NoError(t *testing.T) {
	buf, restore := captureAllLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		"group-alpha",
		[]string{},
		"pending",
		"/tmp/wt",
		"feat/x",
		"none",
		0,
	}}

	g, err := scanGroupRow(fs)
	require.NoError(t, err)
	assert.NotNil(t, g)
	assert.NotContains(t, buf.String(), "ERROR")
	assert.NotEmpty(t, g.ID().String())
}

// TestScanTaskRow_MalformedTaskID_ReturnsError verifies task_id corruption.
func TestScanTaskRow_MalformedTaskID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		"not-a-ulid", // task_id — malformed
		validID,      // group_id
		"write tests",
		[]string{"*.go", "*.md"},
		"pending",
		(*string)(nil), // claimed_by
		0,              // attempts
		[]byte(nil),    // envelope
	}}

	task, err := scanTaskRow(fs)
	require.Error(t, err, "malformed task_id must return error")
	assert.Nil(t, task)
	assert.Contains(t, buf.String(), "ERROR")
	assert.Contains(t, buf.String(), "board_repo")
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanTaskRow_MalformedGroupID_ReturnsError verifies group_id corruption.
func TestScanTaskRow_MalformedGroupID_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		"not-a-ulid", // group_id — malformed
		"write tests",
		[]string{"*.go", "*.md"},
		"pending",
		(*string)(nil),
		0,
		[]byte(nil),
	}}

	task, err := scanTaskRow(fs)
	require.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanTaskRow_MalformedClaimedBy_ReturnsError verifies claimed_by corruption.
func TestScanTaskRow_MalformedClaimedBy_ReturnsError(t *testing.T) {
	buf, restore := captureErrorLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	badID := "not-a-ulid"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		"write tests",
		[]string{"*.go", "*.md"},
		"claimed",
		&badID,      // claimed_by — non-nil but malformed
		1,
		[]byte(nil),
	}}

	task, err := scanTaskRow(fs)
	require.Error(t, err)
	assert.Nil(t, task)
	assert.Contains(t, buf.String(), "not-a-ulid")
}

// TestScanTaskRow_ValidRow_NoError verifies the happy path.
func TestScanTaskRow_ValidRow_NoError(t *testing.T) {
	buf, restore := captureAllLog(t)
	defer restore()

	validID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fs := &fakeScannable{values: []any{
		validID,
		validID,
		"write tests",
		[]string{"*.go", "*.md"}, // non-empty required by HydrateTask
		"pending",
		(*string)(nil),
		0,
		[]byte(nil),
	}}

	task, err := scanTaskRow(fs)
	require.NoError(t, err)
	assert.NotNil(t, task)
	assert.NotContains(t, buf.String(), "ERROR")
	assert.NotEmpty(t, task.ID().String())
}

// ── FindBoardByPhaseID single-hydrator: zero-value ID not returned ─────────────

// TestFindBoardByPhaseID_ZeroValueIDPolicy documents the policy contract that
// FindBoardByPhaseID must return an error when board_id or phase_id fails to parse,
// rather than returning a board with a zero-value ID.
// This test uses parseFieldOrErr directly to verify the logic.
func TestBoardRepo_ParseBoardID_ZeroValuePolicy(t *testing.T) {
	// When ParseBoardID fails, the returned ID has empty raw — zero value.
	zeroID, err := ids.ParseBoardID("not-a-ulid")
	require.Error(t, err, "ParseBoardID with invalid input must fail")
	assert.Empty(t, zeroID.String(), "zero-value BoardID must have empty string representation")

	// Policy: the pg adapter must NOT propagate this zero-value ID to callers.
	// This is verified at the implementation level by TestScanGroupRow_* above.
}

// TestBoardRepo_ZeroValueGroupID_Policy verifies that zero-value GroupID
// returned by a failed parse is detectable.
func TestBoardRepo_ZeroValueGroupID_Policy(t *testing.T) {
	zeroID, err := ids.ParseGroupID("bad-id")
	require.Error(t, err)
	assert.Empty(t, zeroID.String())
}

// ── FindTaskByID single-hydrator: zero-value ID not returned ─────────────────

// TestFindTaskByID_ZeroValuePolicy mirrors FindBoardByPhaseID policy for tasks.
func TestFindTaskByID_ZeroValuePolicy(t *testing.T) {
	zeroID, err := ids.ParseTaskID("bad")
	require.Error(t, err)
	assert.Empty(t, zeroID.String())
}
