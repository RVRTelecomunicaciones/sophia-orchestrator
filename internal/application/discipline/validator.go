package discipline

import (
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// Validator parses raw envelope JSON, cross-checks the phase, and applies
// confidence-threshold coercion (DONE → DONE_WITH_CONCERNS when confidence
// is below the phase's threshold). It is the single source of truth for
// "is this envelope acceptable as a phase result".
type Validator struct{}

// NewValidator constructs a stateless Validator.
func NewValidator() *Validator { return &Validator{} }

// Validate parses raw and runs all validation checks. On success returns a
// possibly-coerced envelope. On failure returns a wrapped sentinel error.
func (v *Validator) Validate(raw []byte, expected phase.PhaseType) (*envelope.Envelope, error) {
	if !expected.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPhase, expected)
	}

	e, err := envelope.Parse(raw)
	if err != nil {
		return nil, err
	}

	if e.Phase != string(expected) {
		return nil, fmt.Errorf("%w: envelope.phase=%q expected=%q", ErrPhaseMismatch, e.Phase, expected)
	}

	if e.Status == envelope.StatusDone && e.Confidence < expected.ConfidenceThreshold() {
		// Coerce: agent claimed DONE but threshold not met. Downgrade to
		// DONE_WITH_CONCERNS so the orchestrator does NOT auto-transition
		// CurrentPhase but still records the work.
		e.Status = envelope.StatusDoneWithConcerns
	}

	return e, nil
}
