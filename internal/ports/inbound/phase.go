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
