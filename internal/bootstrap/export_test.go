// export_test.go exposes package-internal symbols to tests in bootstrap_test
// package. Only add here; do not call from production code.
package bootstrap

import "time"

import (
	criticadapter "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/critic"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ExportedBuildSeedSkills exposes buildSeedSkills for unit testing.
// This allows tests to assert counts, names, phases, and techniques
// without requiring a real database.
func ExportedBuildSeedSkills(now time.Time) ([]*skill.Skill, error) {
	return buildSeedSkills(now)
}

// ExportedSelectCritic exposes selectCritic for unit testing the stub-vs-llm
// wiring decision without standing up a full Wire.
func ExportedSelectCritic(cfg config.Config, d criticadapter.Dispatcher, g criticadapter.SpawnGovernor) outbound.CriticPort {
	return selectCritic(cfg, d, g)
}
