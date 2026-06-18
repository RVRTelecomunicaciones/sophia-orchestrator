package pg

// scan_loop_skip_test.go — Loop-level SKIP guard for corrupt ULID rows (N-1)
//
// These tests drive the REAL scan loops (iterateGroupRows / iterateTaskRows)
// via fakeRows — a fake pgRows implementation — to verify that:
//   - a middle row whose ULID fails to parse is SKIPPED (N-1 result)
//   - the returned slice has length 2 (not 3, not 0)
//   - both returned elements carry a non-zero/valid ID
//   - no error is returned for the partial-but-valid result
//
// No DB required. All tests are pure unit tests.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// ── fakeRows ─────────────────────────────────────────────────────────────────

// fakeRows implements pgRows using a slice of fakeScannable rows.
// Next() advances a cursor; Scan() delegates to the current row; Close/Err
// are no-ops (no DB connection to release).
type fakeRows struct {
	rows   []*fakeScannable
	cursor int
}

func newFakeRows(rows ...*fakeScannable) *fakeRows {
	return &fakeRows{rows: rows, cursor: -1}
}

func (f *fakeRows) Next() bool {
	f.cursor++
	return f.cursor < len(f.rows)
}

func (f *fakeRows) Scan(dest ...any) error {
	return f.rows[f.cursor].Scan(dest...)
}

func (f *fakeRows) Close() {}

func (f *fakeRows) Err() error { return nil }

// ── helpers ───────────────────────────────────────────────────────────────────

const validULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
const validULID2 = "01ARZ3NDEKTSV4RRFFQ69G5FAW"

// goodGroupRow returns a fakeScannable for a valid group row.
func goodGroupRow(groupID, boardID string) *fakeScannable {
	return &fakeScannable{values: []any{
		groupID,    // id
		boardID,    // board_id
		"alpha",    // name
		[]string{}, // depends_on
		"pending",  // status
		"/tmp/wt",  // worktree_path
		"feat/x",   // branch_name
		"none",     // build_status
		0,          // build_attempts
	}}
}

// corruptGroupRow returns a fakeScannable for a group row with a malformed group_id.
func corruptGroupRow(boardID string) *fakeScannable {
	return &fakeScannable{values: []any{
		"not-a-ulid", // id — malformed
		boardID,
		"corrupt-group",
		[]string{},
		"pending",
		"/tmp/wt",
		"feat/x",
		"none",
		0,
	}}
}

// goodTaskRow returns a fakeScannable for a valid task row.
func goodTaskRow(taskID, groupID string) *fakeScannable {
	return &fakeScannable{values: []any{
		taskID,              // id
		groupID,            // group_id
		"write the tests",  // description
		[]string{"*.go"},   // files_pattern
		"pending",          // status
		(*string)(nil),     // claimed_by — NULL
		0,                  // attempts
		[]byte(nil),        // envelope — NULL
	}}
}

// corruptTaskRow returns a fakeScannable for a task row with a malformed task_id.
func corruptTaskRow(groupID string) *fakeScannable {
	return &fakeScannable{values: []any{
		"not-a-ulid", // id — malformed
		groupID,
		"corrupt task",
		[]string{"*.go"},
		"pending",
		(*string)(nil),
		0,
		[]byte(nil),
	}}
}

// noopTaskFinder is a taskFinder stub that returns an empty task list.
func noopTaskFinder(_ ids.GroupID) ([]*apply.Task, error) {
	return nil, nil
}

// ── iterateGroupRows loop-level tests ────────────────────────────────────────

// TestIterateGroupRows_SkipsCorruptMiddleRow verifies that when 3 group rows
// are fed and the MIDDLE one has a malformed group_id, iterateGroupRows:
//   - returns length 2 (not 0, not 3)
//   - returns no error
//   - both returned groups have a non-empty/non-zero ID
func TestIterateGroupRows_SkipsCorruptMiddleRow(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		goodGroupRow(validULID, validULID2),
		corruptGroupRow(validULID2),
		goodGroupRow(validULID2, validULID2),
	)

	got, err := iterateGroupRows(rows, noopTaskFinder)

	require.NoError(t, err, "partial result with corrupt middle row must not return error")
	assert.Len(t, got, 2, "must return N-1=2 rows after skipping the corrupt one")
	for i, g := range got {
		assert.NotEmpty(t, g.ID().String(), "group[%d] must have non-empty ID (no zero-value escape)", i)
	}
}

// TestIterateGroupRows_AllValid_ReturnsAll verifies the happy path: 3 valid
// rows yield 3 groups, no error.
func TestIterateGroupRows_AllValid_ReturnsAll(t *testing.T) {
	_, restore := captureAllLog(t)
	defer restore()

	rows := newFakeRows(
		goodGroupRow(validULID, validULID2),
		goodGroupRow(validULID2, validULID2),
		goodGroupRow(validULID, validULID2),
	)

	got, err := iterateGroupRows(rows, noopTaskFinder)

	require.NoError(t, err)
	assert.Len(t, got, 3)
}

// TestIterateGroupRows_FirstRowCorrupt verifies skip when first row is corrupt
// and the other two are valid.
func TestIterateGroupRows_FirstRowCorrupt(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		corruptGroupRow(validULID2),
		goodGroupRow(validULID, validULID2),
		goodGroupRow(validULID2, validULID2),
	)

	got, err := iterateGroupRows(rows, noopTaskFinder)

	require.NoError(t, err)
	assert.Len(t, got, 2, "must return 2 when first row is corrupt")
	for i, g := range got {
		assert.NotEmpty(t, g.ID().String(), "group[%d] must have non-zero ID", i)
	}
}

// TestIterateGroupRows_LastRowCorrupt verifies skip when last row is corrupt.
func TestIterateGroupRows_LastRowCorrupt(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		goodGroupRow(validULID, validULID2),
		goodGroupRow(validULID2, validULID2),
		corruptGroupRow(validULID2),
	)

	got, err := iterateGroupRows(rows, noopTaskFinder)

	require.NoError(t, err)
	assert.Len(t, got, 2, "must return 2 when last row is corrupt")
	for i, g := range got {
		assert.NotEmpty(t, g.ID().String(), "group[%d] must have non-zero ID", i)
	}
}

// ── iterateTaskRows loop-level tests ─────────────────────────────────────────

// TestIterateTaskRows_SkipsCorruptMiddleRow verifies that when 3 task rows
// are fed and the MIDDLE one has a malformed task_id, iterateTaskRows:
//   - returns length 2 (not 0, not 3)
//   - returns no error
//   - both returned tasks have a non-empty/non-zero ID
func TestIterateTaskRows_SkipsCorruptMiddleRow(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		goodTaskRow(validULID, validULID2),
		corruptTaskRow(validULID2),
		goodTaskRow(validULID2, validULID2),
	)

	got, err := iterateTaskRows(rows)

	require.NoError(t, err, "partial result with corrupt middle row must not return error")
	assert.Len(t, got, 2, "must return N-1=2 rows after skipping the corrupt one")
	for i, task := range got {
		assert.NotEmpty(t, task.ID().String(), "task[%d] must have non-empty ID (no zero-value escape)", i)
	}
}

// TestIterateTaskRows_AllValid_ReturnsAll verifies the happy path: 3 valid
// rows yield 3 tasks, no error.
func TestIterateTaskRows_AllValid_ReturnsAll(t *testing.T) {
	_, restore := captureAllLog(t)
	defer restore()

	rows := newFakeRows(
		goodTaskRow(validULID, validULID2),
		goodTaskRow(validULID2, validULID2),
		goodTaskRow(validULID, validULID2),
	)

	got, err := iterateTaskRows(rows)

	require.NoError(t, err)
	assert.Len(t, got, 3)
}

// TestIterateTaskRows_FirstRowCorrupt verifies skip when first task row is corrupt.
func TestIterateTaskRows_FirstRowCorrupt(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		corruptTaskRow(validULID2),
		goodTaskRow(validULID, validULID2),
		goodTaskRow(validULID2, validULID2),
	)

	got, err := iterateTaskRows(rows)

	require.NoError(t, err)
	assert.Len(t, got, 2, "must return 2 when first row is corrupt")
	for i, task := range got {
		assert.NotEmpty(t, task.ID().String(), "task[%d] must have non-zero ID", i)
	}
}

// TestIterateTaskRows_LastRowCorrupt verifies skip when last task row is corrupt.
func TestIterateTaskRows_LastRowCorrupt(t *testing.T) {
	_, restore := captureErrorLog(t)
	defer restore()

	rows := newFakeRows(
		goodTaskRow(validULID, validULID2),
		goodTaskRow(validULID2, validULID2),
		corruptTaskRow(validULID2),
	)

	got, err := iterateTaskRows(rows)

	require.NoError(t, err)
	assert.Len(t, got, 2, "must return 2 when last row is corrupt")
	for i, task := range got {
		assert.NotEmpty(t, task.ID().String(), "task[%d] must have non-zero ID", i)
	}
}
