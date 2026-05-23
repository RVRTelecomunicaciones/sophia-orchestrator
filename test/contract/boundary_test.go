// Package contract holds boundary assertions that CI enforces without any
// external process or network dependency.
//
// boundary_test.go: filesystem-level assertions for scenario E4 of the
// mcp-host-bridge SDD change.  These tests ALWAYS run (no build tag) and
// confirm:
//
//  1. The `dispatcher/mcp` import path is absent from every repo that MUST
//     NOT depend on it (sophia-cli, sophia-memory-engine,
//     sophia-runtime-adapters, agent-governance-core).
//
//  2. The sophia-runtime-adapters source tree contains no Go source files
//     that import any package from this change's new packages
//     (dispatcher/mcp, sophia-agent-mcp/...).
//
// Spec ref: E4 — "zero files under sophia-runtime-adapters/ MUST be modified
// by this change AND a CI check (or manual filesystem assertion) MUST confirm
// this invariant."
package contract

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workspaceRoot locates the workspace root by walking up from this test file's
// directory until a go.work file is found.  Fails the test if not found.
func workspaceRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.work — is the workspace root missing?")
		}
		dir = parent
	}
}

// goSourceFiles returns all *.go file paths under root (recursive).
func goSourceFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			files = append(files, path)
		}
		return nil
	})
	require.NoError(t, err, "walking %s", root)
	return files
}

// containsForbiddenImport reads a Go source file and returns the first line
// that contains any of the forbidden import substrings.  Returns ("", false)
// if none found.
func containsForbiddenImport(t *testing.T, filePath string, forbidden []string) (line string, found bool) {
	t.Helper()
	data, err := os.ReadFile(filePath)
	require.NoError(t, err, "reading %s", filePath)
	for _, l := range strings.Split(string(data), "\n") {
		for _, f := range forbidden {
			if strings.Contains(l, f) {
				return l, true
			}
		}
	}
	return "", false
}

// TestBoundary_DispatcherMCPNotInForbiddenRepos asserts that no Go source
// file in the forbidden repos imports the dispatcher/mcp package that was
// introduced by the mcp-host-bridge change.
//
// Forbidden repos (must remain untouched):
//   - sophia-cli
//   - sophia-memory-engine
//   - sophia-runtime-adapters
//   - agent-governance-core
//
// Spec: E4 — "a CI check MUST confirm this invariant."
func TestBoundary_DispatcherMCPNotInForbiddenRepos(t *testing.T) {
	root := workspaceRoot(t)

	// Repos that MUST NOT import or reference dispatcher/mcp or sophia-agent-mcp.
	forbiddenRepos := []struct {
		name string
		dir  string
	}{
		{"sophia-cli", filepath.Join(root, "sophia-cli")},
		{"sophia-memory-engine", filepath.Join(root, "sophia-memory-engine")},
		{"sophia-runtime-adapters", filepath.Join(root, "sophia-runtime-adapters")},
		{"agent-governance-core", filepath.Join(root, "agent-governance-core")},
	}

	// Import substrings that must NOT appear in any source file of those repos.
	forbiddenImports := []string{
		"dispatcher/mcp",                   // the new orchestrator adapter package
		"sophia-agent-mcp",                 // the new host-bridge module
		"adapters/outbound/dispatcher/mcp", // full package path variant
	}

	for _, repo := range forbiddenRepos {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			if _, err := os.Stat(repo.dir); os.IsNotExist(err) {
				t.Skipf("repo directory not found at %s — skipping (workspace may be partial)", repo.dir)
				return
			}

			files := goSourceFiles(t, repo.dir)
			require.NotEmpty(t, files, "expected Go source files in %s", repo.dir)

			for _, f := range files {
				line, found := containsForbiddenImport(t, f, forbiddenImports)
				assert.Falsef(t, found,
					"forbidden import found in %s\n  line: %s\n  This repo must not depend on dispatcher/mcp or sophia-agent-mcp (spec E4)",
					f, strings.TrimSpace(line),
				)
			}
		})
	}
}

// TestBoundary_RuntimeAdaptersUntouched asserts that sophia-runtime-adapters
// does not import any package introduced by the mcp-host-bridge change, and
// that its go.mod does not reference the new sophia-agent-mcp module.
//
// This gives an extra layer of confidence beyond the import scan: if
// runtime-adapters' go.mod were modified to add a dependency on
// sophia-agent-mcp, the build would break the isolation boundary defined by
// the change's cross-repo wiring rules (design §9 / spec E4).
//
// Spec: E4 — "zero files under sophia-runtime-adapters/ MUST be modified by
// this change."
func TestBoundary_RuntimeAdaptersUntouched(t *testing.T) {
	root := workspaceRoot(t)
	rtDir := filepath.Join(root, "sophia-runtime-adapters")

	if _, err := os.Stat(rtDir); os.IsNotExist(err) {
		t.Skipf("sophia-runtime-adapters not found at %s — skipping", rtDir)
		return
	}

	// 1. go.mod must not reference sophia-agent-mcp.
	goModPath := filepath.Join(rtDir, "go.mod")
	goModData, err := os.ReadFile(goModPath)
	require.NoError(t, err, "reading sophia-runtime-adapters/go.mod")

	assert.NotContains(t, string(goModData), "sophia-agent-mcp",
		"sophia-runtime-adapters/go.mod must not reference sophia-agent-mcp (spec E4)")
	assert.NotContains(t, string(goModData), "dispatcher/mcp",
		"sophia-runtime-adapters/go.mod must not reference dispatcher/mcp (spec E4)")

	// 2. No Go source file imports the new packages.
	forbiddenImports := []string{
		"dispatcher/mcp",
		"sophia-agent-mcp",
		"adapters/outbound/dispatcher/mcp",
	}

	files := goSourceFiles(t, rtDir)
	require.NotEmpty(t, files, "expected Go source files in sophia-runtime-adapters")

	for _, f := range files {
		line, found := containsForbiddenImport(t, f, forbiddenImports)
		assert.Falsef(t, found,
			"forbidden import found in sophia-runtime-adapters file %s\n  line: %s\n  (spec E4: this repo must remain untouched by the mcp-host-bridge change)",
			f, strings.TrimSpace(line),
		)
	}
}
