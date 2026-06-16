// Package critic provides the orchestrator's outbound advisory critic
// adapters. The V1 production implementation is StubCritic, a deterministic
// rule-based critic that derives advisory concerns purely from envelope
// contents (design D-GA-3). A future LLM-backed critic swaps in behind the
// same outbound.CriticPort.
package critic

import (
	"context"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// lowConfidenceThreshold is the cutoff below which the stub raises a
// medium/confidence advisory concern. Held as a package constant so the
// mapping stays deterministic and reviewable.
const lowConfidenceThreshold = 0.5

// StubCritic is the deterministic V1 advisory critic. It uses NO clock and
// NO randomness (CLAUDE.md rule 5): the same envelope always yields the same
// concerns, so the SSE wire output is reproducible and auditable.
type StubCritic struct{}

// NewStub constructs the deterministic StubCritic.
func NewStub() *StubCritic { return &StubCritic{} }

// Review derives advisory concerns from the envelope:
//   - any Risk with Level == "high" → one high/risk concern (the first such);
//   - Confidence < lowConfidenceThreshold → one medium/confidence concern;
//   - otherwise no concerns.
//
// A nil envelope yields no concerns and no error — the critic must never
// break a phase (design D-GA-5). Review never returns a non-nil error in the
// stub; the error return exists for the LLM-backed impl that swaps in later.
func (c *StubCritic) Review(_ context.Context, in outbound.CriticInput) ([]phase.Concern, error) {
	env := in.Envelope
	if env == nil {
		return nil, nil
	}

	var concerns []phase.Concern

	for i, r := range env.Risks {
		if r.Level == "high" {
			concerns = append(concerns, phase.Concern{
				Severity: "high",
				Category: "risk",
				Message:  "envelope reports a high-level risk: " + r.Description,
				Evidence: fmt.Sprintf("risks[%d].level=high", i),
			})
			break
		}
	}

	if env.Confidence < lowConfidenceThreshold {
		concerns = append(concerns, phase.Concern{
			Severity: "medium",
			Category: "confidence",
			Message:  "envelope confidence is below the advisory threshold",
			Evidence: fmt.Sprintf("confidence=%.2f<%.2f", env.Confidence, lowConfidenceThreshold),
		})
	}

	return concerns, nil
}
