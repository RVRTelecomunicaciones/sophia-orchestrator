package outbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// CriticInput names the inputs the advisory critic reviews for a single
// enveloped phase (design D-GA-2). The critic derives concerns from the
// Envelope; ChangeID and PhaseType are context only.
type CriticInput struct {
	ChangeID  ids.ChangeID
	PhaseType phase.PhaseType
	Envelope  *envelope.Envelope
}

// CriticPort is the orchestrator's optional, strictly-advisory critic. It
// reviews a completed phase's envelope and returns informational concerns.
//
// Contract (all binding per design GAP B):
//   - Strictly advisory: concerns NEVER block a phase, NEVER escalate to
//     governance, and NEVER force a HARD-GATE.
//   - Per-change opt-in, DEFAULT OFF. When a change does not opt in (or
//     no critic is wired) Review is never called and behaviour is
//     byte-identical to today.
//   - A Review error must be swallowed by the caller (logged, not fatal):
//     an advisory critic must never break a phase.
//
// V1 production impl is the deterministic StubCritic; a future LLM-backed
// impl swaps in behind this same port.
type CriticPort interface {
	Review(ctx context.Context, in CriticInput) ([]phase.Concern, error)
}
