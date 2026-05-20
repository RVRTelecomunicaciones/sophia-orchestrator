package inbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// RunPhaseInput is the inbound shape for PhaseService.Run.
type RunPhaseInput struct {
	ChangeID         ids.ChangeID
	PhaseType        phase.PhaseType
	TaskDescription  string
	ContextOverrides map[string]any
	RetryBudget      int
	// PriorPhasesStatus is the orchestrator-verified status of each
	// prior phase in the change (e.g. {"proposal": "done", "spec":
	// "done"}). Populated internally by phase.Service before
	// dispatching the phase — HTTP/CLI callers leave it empty and the
	// orchestrator fills it from PhaseRepo. Passed downstream into
	// discipline.PromptInput.PriorPhasesStatus so the LLM sees factual
	// evidence of prior-phase completion instead of searching for it
	// locally and blocking when none is found (Spec #51).
	PriorPhasesStatus map[phase.PhaseType]string
}

// RunPhaseOutput is returned 202-style: the phase has been kicked off in a
// goroutine; the caller follows progress via SSE on EventsURL.
type RunPhaseOutput struct {
	PhaseID   ids.PhaseID
	Status    phase.PhaseStatus
	EventsURL string
	StartedAt string
}

// PhaseService is the application service for phase execution.
type PhaseService interface {
	Run(ctx context.Context, in RunPhaseInput) (*RunPhaseOutput, error)
	Get(ctx context.Context, id ids.PhaseID) (*phase.Phase, error)
	Resume(ctx context.Context, id ids.PhaseID) (*RunPhaseOutput, error)
	Approve(ctx context.Context, id ids.PhaseID, approver, reason string) error
	Reject(ctx context.Context, id ids.PhaseID, approver, reason string) error
}
