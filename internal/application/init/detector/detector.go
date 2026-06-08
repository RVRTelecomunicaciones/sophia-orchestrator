package detector

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Detector implements structural analysis via pure-Go FS reads.
// No subprocesses, no HTTP. Satisfies the SophiaDetector port interface
// defined in internal/application/init/ports.go.
type Detector struct{}

// New constructs a Detector. No dependencies needed — all detection is
// pure-FS.
func New() *Detector {
	return &Detector{}
}

// Detect analyses repoRoot and returns a partially-built StructuralContext
// (no GraphSummary — that comes from GraphifySpawner). SchemaVersion is
// always set to StructuralContextSchemaV1.
//
// Detection is best-effort: missing manifest files are silently skipped.
// Only genuine FS errors (permission denied, invalid path) are returned.
func (d *Detector) Detect(_ context.Context, repoRoot string) (StructuralContext, error) {
	sc := StructuralContext{
		SchemaVersion:     StructuralContextSchemaV1,
		SophiaDetectorVer: SophiaDetectorVer,
	}

	// --- go.mod ---
	if content, err := readIfExists(filepath.Join(repoRoot, "go.mod")); err == nil && len(content) > 0 {
		if li := parseGoMod(content); li != nil {
			sc.Languages = append(sc.Languages, *li)
			sc.PackageManagers = appendUniq(sc.PackageManagers, "go modules")
		}
	}

	// --- package.json (Node / TypeScript / JavaScript) ---
	if content, err := readIfExists(filepath.Join(repoRoot, "package.json")); err == nil && len(content) > 0 {
		frameworks, hints, _ := parseNodeManifest(content, repoRoot)
		sc.Frameworks = append(sc.Frameworks, frameworks...)
		sc.ConventionHints = append(sc.ConventionHints, hints...)

		// Detect package manager from lock files.
		switch {
		case fileExists(filepath.Join(repoRoot, "pnpm-lock.yaml")):
			sc.PackageManagers = appendUniq(sc.PackageManagers, "pnpm")
		case fileExists(filepath.Join(repoRoot, "yarn.lock")):
			sc.PackageManagers = appendUniq(sc.PackageManagers, "yarn")
		default:
			sc.PackageManagers = appendUniq(sc.PackageManagers, "npm")
		}

		// TypeScript detection from tsconfig or typescript dep.
		if fileExists(filepath.Join(repoRoot, "tsconfig.json")) {
			sc.Languages = appendLangUniq(sc.Languages, LanguageInfo{
				Name:            "TypeScript",
				VersionEvidence: "tsconfig.json",
			})
		}
	}

	// --- pyproject.toml ---
	if content, err := readIfExists(filepath.Join(repoRoot, "pyproject.toml")); err == nil && len(content) > 0 {
		sc.Languages = appendLangUniq(sc.Languages, LanguageInfo{
			Name:            "Python",
			VersionEvidence: "pyproject.toml",
		})
		sc.PackageManagers = appendUniq(sc.PackageManagers, "uv/pip")
		for _, fw := range parsePyprojectToml(content) {
			sc.Frameworks = appendFwUniq(sc.Frameworks, fw)
		}
	}

	// --- requirements.txt ---
	if content, err := readIfExists(filepath.Join(repoRoot, "requirements.txt")); err == nil && len(content) > 0 {
		sc.Languages = appendLangUniq(sc.Languages, LanguageInfo{
			Name:            "Python",
			VersionEvidence: "requirements.txt",
		})
		for _, fw := range parseRequirementsTxt(content) {
			sc.Frameworks = appendFwUniq(sc.Frameworks, fw)
		}
	}

	// --- Cargo.toml ---
	if content, err := readIfExists(filepath.Join(repoRoot, "Cargo.toml")); err == nil && len(content) > 0 {
		if li := parseCargoToml(content); li != nil {
			sc.Languages = appendLangUniq(sc.Languages, *li)
			sc.PackageManagers = appendUniq(sc.PackageManagers, "cargo")
		}
	}

	// --- build.gradle ---
	if content, err := readIfExists(filepath.Join(repoRoot, "build.gradle")); err == nil && len(content) > 0 {
		langs, fws := parseBuildGradle(content)
		for _, l := range langs {
			sc.Languages = appendLangUniq(sc.Languages, l)
		}
		for _, fw := range fws {
			sc.Frameworks = appendFwUniq(sc.Frameworks, fw)
		}
		sc.PackageManagers = appendUniq(sc.PackageManagers, "gradle")
	}

	// --- pom.xml ---
	if content, err := readIfExists(filepath.Join(repoRoot, "pom.xml")); err == nil && len(content) > 0 {
		langs, fws := parsePomXML(content)
		for _, l := range langs {
			sc.Languages = appendLangUniq(sc.Languages, l)
		}
		for _, fw := range fws {
			sc.Frameworks = appendFwUniq(sc.Frameworks, fw)
		}
		sc.PackageManagers = appendUniq(sc.PackageManagers, "maven")
	}

	// --- Architecture style heuristics ---
	sc.ArchStyle = detectArchStyle(repoRoot)

	return sc, nil
}

// readIfExists reads a file and returns (nil, nil) if it does not exist.
func readIfExists(path string) ([]byte, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- path is built from repoRoot + known manifest name
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("detector readfile: %w", err)
	}
	return b, nil
}

func appendUniq(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

func appendLangUniq(slice []LanguageInfo, l LanguageInfo) []LanguageInfo {
	for _, v := range slice {
		if v.Name == l.Name {
			return slice
		}
	}
	return append(slice, l)
}

func appendFwUniq(slice []FrameworkInfo, fw FrameworkInfo) []FrameworkInfo {
	for _, v := range slice {
		if v.Name == fw.Name {
			return slice
		}
	}
	return append(slice, fw)
}
