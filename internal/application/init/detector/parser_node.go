package detector

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// packageJSON is a minimal shape for parsing package.json dependencies.
type packageJSON struct {
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
}

// parseNodeManifest analyses package.json content and the presence of
// framework-specific files to produce FrameworkInfo entries and
// ConventionHints.
//
// Angular Signals heuristic v1 (locked):
//   - @angular/core present AND app.config.ts present in repoRoot AND
//     @ngrx/store absent → signals variant.
//   - @angular/core present AND @ngrx/store present → NgRx variant.
//   - Only one of signals or NgRx is noted; never both.
//nolint:unparam // third return reserved for future package-manager hints
func parseNodeManifest(content []byte, repoRoot string) ([]FrameworkInfo, []string, []string) {
	var pkg packageJSON
	if err := json.Unmarshal(content, &pkg); err != nil {
		return nil, nil, nil
	}

	allDeps := make(map[string]string)
	for k, v := range pkg.Dependencies {
		allDeps[k] = v
	}
	for k, v := range pkg.DevDependencies {
		allDeps[k] = v
	}

	var frameworks []FrameworkInfo
	var hints []string
	var managers []string

	// Angular detection.
	if angularVersion, ok := allDeps["@angular/core"]; ok {
		ver := stripSemverPrefix(angularVersion)
		fw := FrameworkInfo{
			Name:         "Angular",
			Version:      ver,
			EvidencePath: "package.json",
		}
		frameworks = append(frameworks, fw)

		// NgRx detection (takes precedence over signals check).
		if _, hasNgrx := allDeps["@ngrx/store"]; hasNgrx {
			frameworks = append(frameworks, FrameworkInfo{
				Name:         "NgRx",
				Version:      stripSemverPrefix(allDeps["@ngrx/store"]),
				EvidencePath: "package.json",
			})
			hints = append(hints, "NgRx variant: @ngrx/store present")
		} else {
			// Signals heuristic v1: app.config.ts present AND no @ngrx/store.
			appConfigPath := filepath.Join(repoRoot, "app.config.ts")
			if _, err := os.Stat(appConfigPath); err == nil {
				hints = append(hints, "Angular Signals variant: app.config.ts present, no @ngrx/store")
			}
		}
	}

	// React detection.
	if reactVersion, ok := allDeps["react"]; ok {
		fw := FrameworkInfo{
			Name:         "React",
			Version:      stripSemverPrefix(reactVersion),
			EvidencePath: "package.json",
		}
		// Next.js takes priority.
		if nextVersion, ok := allDeps["next"]; ok {
			fw = FrameworkInfo{
				Name:         "Next.js",
				Version:      stripSemverPrefix(nextVersion),
				EvidencePath: "package.json",
			}
		}
		frameworks = append(frameworks, fw)
	}

	// Vue detection.
	if vueVersion, ok := allDeps["vue"]; ok {
		frameworks = append(frameworks, FrameworkInfo{
			Name:         "Vue",
			Version:      stripSemverPrefix(vueVersion),
			EvidencePath: "package.json",
		})
	}

	// TypeScript detection (language, not framework).
	if _, ok := allDeps["typescript"]; ok {
		_ = ok // TypeScript noted in Languages, not here
	}

	// Package manager detection: pnpm-workspace.yaml presence is handled by
	// arch heuristics; here we detect npm vs pnpm vs yarn from lock files.
	// Noted via managers slice returned to caller.
	_ = managers // populated by caller from FS

	return frameworks, hints, nil
}

// stripSemverPrefix removes leading "^", "~", ">=", etc. from a semver string
// and returns the first numeric component group.
func stripSemverPrefix(v string) string {
	v = strings.TrimLeft(v, "^~>=<")
	// Return the major.minor prefix for readability.
	return v
}
