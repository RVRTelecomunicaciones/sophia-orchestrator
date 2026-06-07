// export_test.go exposes package-internal symbols to tests in bootstrap_test
// package. Only add here; do not call from production code.
package bootstrap

import "time"

import "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"

// ExportedBuildSeedSkills exposes buildSeedSkills for unit testing.
// This allows tests to assert counts, names, phases, and techniques
// without requiring a real database.
func ExportedBuildSeedSkills(now time.Time) ([]*skill.Skill, error) {
	return buildSeedSkills(now)
}
