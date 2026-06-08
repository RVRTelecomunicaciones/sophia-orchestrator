package detector_test

// detector_test.go — C.1–C.8 (Strict TDD: RED tests first)
//
// Fixture-based unit tests for the structural detector. Each test uses a
// pre-built testdata fixture directory. No subprocesses — detector is pure Go
// FS reads.

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/stretchr/testify/require"
)

// fixtureDir returns the absolute path to a fixture directory under testdata/.
func fixtureDir(name string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", name)
}

// newDetector constructs a fresh Detector for each test.
// This function fails to compile until detector.New() is defined (RED).
func newDetector(t *testing.T) *detector.Detector {
	t.Helper()
	return detector.New()
}

// C.1: Go project with go.mod only → Languages includes {Name:"Go"}, Frameworks empty.
func TestDetect_GoSimple(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("go-simple"))
	require.NoError(t, err)
	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)

	// Must detect Go.
	require.NotEmpty(t, sc.Languages, "expected at least one language")
	var found bool
	for _, l := range sc.Languages {
		if l.Name == "Go" {
			found = true
		}
	}
	require.True(t, found, "expected Languages to include Go")

	// No frameworks for a bare go.mod.
	require.Empty(t, sc.Frameworks, "expected no frameworks for bare go.mod")
}

// C.2: Angular 17 + app.config.ts + no @ngrx/store → Angular 17 + signals hint.
func TestDetect_Angular17Signals(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("angular17-signals"))
	require.NoError(t, err)

	// Must detect Angular with version 17.
	var angular *detector.FrameworkInfo
	for i := range sc.Frameworks {
		if sc.Frameworks[i].Name == "Angular" {
			angular = &sc.Frameworks[i]
		}
	}
	require.NotNil(t, angular, "expected Angular in Frameworks")
	require.Contains(t, angular.Version, "17", "expected version to contain '17'")

	// Signals heuristic must appear in ConventionHints.
	var signalsHint bool
	for _, h := range sc.ConventionHints {
		if containsAny(h, "signals", "Signals") {
			signalsHint = true
		}
	}
	require.True(t, signalsHint, "expected signals heuristic in ConventionHints; got %v", sc.ConventionHints)
}

// C.3: Angular 14 + @ngrx/store → Angular 14 + NgRx noted; no signals heuristic.
func TestDetect_Angular14NgRx(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("angular14-ngrx"))
	require.NoError(t, err)

	var angular *detector.FrameworkInfo
	for i := range sc.Frameworks {
		if sc.Frameworks[i].Name == "Angular" {
			angular = &sc.Frameworks[i]
		}
	}
	require.NotNil(t, angular, "expected Angular in Frameworks")
	require.Contains(t, angular.Version, "14")

	// NgRx must be noted somewhere (Framework or ConventionHint).
	var ngrxFound bool
	for _, f := range sc.Frameworks {
		if containsAny(f.Name, "NgRx", "ngrx") {
			ngrxFound = true
		}
	}
	for _, h := range sc.ConventionHints {
		if containsAny(h, "NgRx", "ngrx") {
			ngrxFound = true
		}
	}
	require.True(t, ngrxFound, "expected NgRx noted; frameworks=%v hints=%v", sc.Frameworks, sc.ConventionHints)

	// No signals heuristic when @ngrx/store present.
	for _, h := range sc.ConventionHints {
		require.False(t, containsAny(h, "signals", "Signals"),
			"signals heuristic must NOT fire when @ngrx/store present; hint=%q", h)
	}
}

// C.4: Spring Boot fixture → Frameworks includes {Name:"Spring Boot"}.
func TestDetect_SpringBoot(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("spring-boot"))
	require.NoError(t, err)

	var found bool
	for _, f := range sc.Frameworks {
		if f.Name == "Spring Boot" {
			found = true
		}
	}
	require.True(t, found, "expected Spring Boot in Frameworks; got %v", sc.Frameworks)
}

// C.5: Python FastAPI fixture → Frameworks includes {Name:"FastAPI"}.
func TestDetect_PythonFastAPI(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("python-fastapi"))
	require.NoError(t, err)

	var found bool
	for _, f := range sc.Frameworks {
		if f.Name == "FastAPI" {
			found = true
		}
	}
	require.True(t, found, "expected FastAPI in Frameworks; got %v", sc.Frameworks)
}

// C.6: Hexagonal layout (domain/ + application/ + infrastructure/) → ArchStyle includes "hexagonal".
func TestDetect_HexagonalArch(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("hexagonal"))
	require.NoError(t, err)

	var found bool
	for _, a := range sc.ArchStyle {
		if a == "hexagonal" {
			found = true
		}
	}
	require.True(t, found, "expected 'hexagonal' in ArchStyle; got %v", sc.ArchStyle)
}

// C.7: Monorepo with pnpm-workspace.yaml → ArchStyle includes "monorepo".
func TestDetect_Monorepo(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	sc, err := d.Detect(ctx, fixtureDir("monorepo"))
	require.NoError(t, err)

	var found bool
	for _, a := range sc.ArchStyle {
		if a == "monorepo" {
			found = true
		}
	}
	require.True(t, found, "expected 'monorepo' in ArchStyle; got %v", sc.ArchStyle)
}

// C.8: Empty directory → empty StructuralContext with SchemaVersion=1, no error.
func TestDetect_NoManifests(t *testing.T) {
	d := newDetector(t)
	ctx := context.Background()

	// Use t.TempDir() for truly empty directory (no files).
	sc, err := d.Detect(ctx, t.TempDir())
	require.NoError(t, err)

	require.Equal(t, detector.StructuralContextSchemaV1, sc.SchemaVersion)
	require.Empty(t, sc.Languages, "expected no languages")
	require.Empty(t, sc.Frameworks, "expected no frameworks")
}

// containsAny returns true if s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
