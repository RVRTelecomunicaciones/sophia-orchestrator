package discipline

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// SkillProvider is the inbound application port through which phase and apply
// services obtain the hydrated Skills applicable to a given SDD phase.
//
// Implementations are expected to delegate to SkillRepository.FindByPhase.
// The caller (phase/service.go, apply/run.go, apply/teamlead.go) passes the
// returned slice as PromptInput.Skills so that PromptBuilder remains pure.
//
// Contract:
//   - Returns an empty slice (not an error) when no Skills match the phase.
//   - Returns an error only on infrastructure failure (DB timeout, etc.).
//   - On error the caller MUST fail-soft: pass nil Skills and proceed with
//     the prompt unchanged (byte-identical to the pre-change baseline).
type SkillProvider interface {
	SkillsForPhase(ctx context.Context, pt phase.PhaseType) ([]*skill.Skill, error)
}
