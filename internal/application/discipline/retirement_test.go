package discipline_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSkillsForPhase_ZeroProductionReferences (O.1) asserts that no production
// (non-test) Go source file in the orch module references the retired symbols:
//   - SkillsForPhase  — deprecated wrapper method deleted in M3 PR3b
//   - SkillProvider   — deprecated port interface deleted in M3 PR3b
//   - RawMemoryBlob   — M0.5-interim field deleted in M3 PR3b
//
// This test is documentation + a runtime assertion. It MUST fail (RED) until
// O.2/O.3/O.5 (prod file deletions + field removal) complete.
//
// Test files (_test.go) are excluded because legacy regression integration tests
// still reference SkillsForPhase until they are removed in Group P.
func TestSkillsForPhase_ZeroProductionReferences(t *testing.T) {
	// Resolve module root from this file's location.
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = .../internal/application/discipline/retirement_test.go
	// modRoot  = .../sophia-orchestator
	modRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

	patterns := []string{
		`SkillsForPhase`,
		`\bSkillProvider\b`,
		`\bRawMemoryBlob\b`,
	}

	retired := regexp.MustCompile(strings.Join(patterns, "|"))

	var violations []string

	walkErr := filepath.WalkDir(modRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// Skip directories that are never production code.
			if name == "vendor" || name == ".git" || name == "node_modules" || name == "openspec" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			// Skip pure comment lines — deprecation notices and "will be removed"
			// notices are acceptable in comment-only lines.
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			if retired.MatchString(line) {
				rel, _ := filepath.Rel(modRoot, path)
				violations = append(violations, fmt.Sprintf("%s:%d: %s", rel, i+1, trimmed))
			}
		}
		return nil
	})

	assert.NoError(t, walkErr, "WalkDir must not fail")
	assert.Empty(t, violations,
		"retired symbols (SkillsForPhase, SkillProvider, RawMemoryBlob) must have zero"+
			" production-code references after M3 PR3b — run O.2/O.3/O.5 deletions first;\n"+
			"violations:\n%s", strings.Join(violations, "\n"))
}
