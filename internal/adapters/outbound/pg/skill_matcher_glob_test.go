package pg_test

import (
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDoublestarGlob_RecursiveWildcard verifies ** globs match nested paths.
func TestDoublestarGlob_RecursiveWildcard(t *testing.T) {
	match, err := doublestar.Match("internal/domain/**", "internal/domain/skill/skill.go")
	require.NoError(t, err)
	assert.True(t, match, `"internal/domain/**" must match "internal/domain/skill/skill.go"`)
}

// TestDoublestarGlob_TestFileSuffix verifies ** + suffix matches deeply nested files.
func TestDoublestarGlob_TestFileSuffix(t *testing.T) {
	match, err := doublestar.Match("**/*_test.go", "a/b/c_test.go")
	require.NoError(t, err)
	assert.True(t, match, `"**/*_test.go" must match "a/b/c_test.go"`)
}

// TestDoublestarGlob_VendorExclude verifies vendor/** matches vendor paths (for exclude_paths use).
func TestDoublestarGlob_VendorExclude(t *testing.T) {
	match, err := doublestar.Match("vendor/**", "vendor/lib/foo.go")
	require.NoError(t, err)
	assert.True(t, match, `"vendor/**" must match "vendor/lib/foo.go"`)
}

// TestDoublestarGlob_ScopeWildcard verifies * matches in simple flat names.
func TestDoublestarGlob_ScopeWildcard(t *testing.T) {
	match, err := doublestar.Match("*", "my-project")
	require.NoError(t, err)
	assert.True(t, match, `"*" must match any single-segment value`)
}

// TestDoublestarGlob_NoMatch verifies non-matching paths are rejected.
func TestDoublestarGlob_NoMatch(t *testing.T) {
	match, err := doublestar.Match("internal/domain/**", "cmd/sophia-orchestator/main.go")
	require.NoError(t, err)
	assert.False(t, match, `"internal/domain/**" must NOT match cmd/ paths`)
}
