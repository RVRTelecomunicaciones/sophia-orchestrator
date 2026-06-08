package detector

import (
	"os"
	"path/filepath"
)

// detectArchStyle inspects the top-level directory layout to produce
// architecture style labels. No subprocess, no network.
//
// Rules (in priority order):
//  1. Monorepo: pnpm-workspace.yaml OR go.work at root.
//  2. Hexagonal: domain/ AND application/ AND infrastructure/ all present.
//  3. Microservices: cmd/*/ (multiple subdirs) OR services/*/ present.
//  4. Default: "monolith" — always added when no other style is detected.
func detectArchStyle(repoRoot string) []string {
	var styles []string

	// Monorepo check.
	if fileExists(filepath.Join(repoRoot, "pnpm-workspace.yaml")) ||
		fileExists(filepath.Join(repoRoot, "go.work")) {
		styles = append(styles, "monorepo")
	}

	// Hexagonal check: all three canonical layers must exist as directories.
	if dirExists(filepath.Join(repoRoot, "domain")) &&
		dirExists(filepath.Join(repoRoot, "application")) &&
		dirExists(filepath.Join(repoRoot, "infrastructure")) {
		styles = append(styles, "hexagonal")
	}

	// Microservices check.
	if hasMultipleSubdirs(filepath.Join(repoRoot, "cmd")) ||
		hasMultipleSubdirs(filepath.Join(repoRoot, "services")) {
		styles = append(styles, "microservices")
	}

	// Default fallback: monolith.
	if len(styles) == 0 {
		styles = append(styles, "monolith")
	}

	return styles
}

// fileExists reports whether path is a regular file (or symlink to one).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists reports whether path is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// hasMultipleSubdirs returns true when path is a directory containing at least
// two subdirectory entries (the "cmd/*" or "services/*" microservices pattern).
func hasMultipleSubdirs(path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
			if count >= 2 {
				return true
			}
		}
	}
	return false
}
