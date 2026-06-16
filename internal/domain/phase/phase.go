package phase

import (
	"fmt"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// Phase is one execution of a SDD phase within a Change. The aggregate owns
// the state machine (pending → running → terminal) and the retry budget. The
// Envelope is set on Complete; ConfidenceThreshold gating is enforced here.
type Phase struct {
	id          ids.PhaseID
	changeID    ids.ChangeID
	pType       PhaseType
	status      PhaseStatus
	envelope    *envelope.Envelope
	confidence  float64
	retryBudget int
	attempts    int
	startedAt   *time.Time
	completedAt *time.Time
	concerns    []Concern
}

// New constructs a Phase in PhaseStatusPending.
func New(id ids.PhaseID, changeID ids.ChangeID, pt PhaseType, retryBudget int) (*Phase, error) {
	if !pt.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidType, pt)
	}
	if retryBudget <= 0 {
		retryBudget = 1
	}
	return &Phase{
		id:          id,
		changeID:    changeID,
		pType:       pt,
		status:      PhaseStatusPending,
		retryBudget: retryBudget,
	}, nil
}

// Hydrate reconstructs a Phase from persisted fields.
func Hydrate(
	id ids.PhaseID,
	changeID ids.ChangeID,
	pt PhaseType,
	status PhaseStatus,
	env *envelope.Envelope,
	confidence float64,
	retryBudget, attempts int,
	startedAt, completedAt *time.Time,
) *Phase {
	return &Phase{
		id: id, changeID: changeID, pType: pt,
		status: status, envelope: env, confidence: confidence,
		retryBudget: retryBudget, attempts: attempts,
		startedAt: startedAt, completedAt: completedAt,
	}
}

// ID returns the phase identifier.
func (p *Phase) ID() ids.PhaseID { return p.id }

// ChangeID returns the parent Change identifier.
func (p *Phase) ChangeID() ids.ChangeID { return p.changeID }

// Type returns the phase type (init / explore / ... / archive).
func (p *Phase) Type() PhaseType { return p.pType }

// Status returns the current lifecycle status.
func (p *Phase) Status() PhaseStatus { return p.status }

// Envelope returns the recorded envelope or nil if pending/running.
func (p *Phase) Envelope() *envelope.Envelope { return p.envelope }

// Confidence returns the recorded confidence (0 if no envelope yet).
func (p *Phase) Confidence() float64 { return p.confidence }

// Attempts returns the number of times the phase has entered Running.
func (p *Phase) Attempts() int { return p.attempts }

// RetryBudget returns the maximum number of attempts permitted.
func (p *Phase) RetryBudget() int { return p.retryBudget }

// StartedAt returns the most recent start timestamp or nil if never started.
func (p *Phase) StartedAt() *time.Time { return p.startedAt }

// CompletedAt returns the completion timestamp or nil.
func (p *Phase) CompletedAt() *time.Time { return p.completedAt }

// Concerns returns the advisory concerns attached to this phase (design
// GAP B / D-GA-2). Empty when no critic ran or the critic raised none —
// the opted-out path is byte-identical to today.
func (p *Phase) Concerns() []Concern { return p.concerns }

// SetConcerns attaches advisory concerns to the phase. Strictly advisory:
// it does NOT change the phase status. The done_with_concerns status is
// derived by Complete from a DONE_WITH_CONCERNS envelope via the existing
// switch; concerns ride alongside for post-hoc operator review and SSE.
func (p *Phase) SetConcerns(concerns []Concern) { p.concerns = concerns }

// Start moves the phase to PhaseStatusRunning and increments attempts. Fails
// if the phase is terminal or the retry budget is exhausted.
func (p *Phase) Start(now time.Time) error {
	if p.status.IsTerminal() {
		return ErrTerminal
	}
	if p.attempts >= p.retryBudget {
		return ErrBudgetExhausted
	}
	p.status = PhaseStatusRunning
	p.attempts++
	p.startedAt = &now
	return nil
}

// Complete transitions a running phase to a terminal status based on the
// envelope. The envelope's Phase field must match this phase's type, and a
// DONE envelope must have confidence ≥ threshold; otherwise an error is
// returned and the caller should record the envelope as DONE_WITH_CONCERNS
// or treat it as needs_context.
func (p *Phase) Complete(e *envelope.Envelope, now time.Time) error {
	if p.status != PhaseStatusRunning {
		return ErrNotRunning
	}
	if e == nil {
		return ErrEnvelopeNil
	}
	if e.Phase != string(p.pType) {
		return fmt.Errorf("%w: envelope.phase=%q phase.type=%q", ErrEnvelopeMismatch, e.Phase, p.pType)
	}
	threshold := p.pType.ConfidenceThreshold()
	if e.Status == envelope.StatusDone && e.Confidence < threshold {
		return fmt.Errorf("%w: got %v want >= %v", ErrBelowThreshold, e.Confidence, threshold)
	}
	p.envelope = e
	p.confidence = e.Confidence
	p.completedAt = &now
	switch e.Status {
	case envelope.StatusDone:
		p.status = PhaseStatusDone
	case envelope.StatusDoneWithConcerns:
		p.status = PhaseStatusDoneWithConcerns
	case envelope.StatusBlocked:
		p.status = PhaseStatusBlocked
	case envelope.StatusNeedsContext:
		p.status = PhaseStatusNeedsContext
	}
	return nil
}

// MarkInterrupted transitions the phase to PhaseStatusInterrupted (used on
// orchestrator startup recovery scan).
func (p *Phase) MarkInterrupted() error {
	if p.status.IsTerminal() {
		return ErrTerminal
	}
	p.status = PhaseStatusInterrupted
	return nil
}

// Restart transitions a blocked Phase back to PhaseStatusRunning so the
// orchestrator can replay Execute against the same phase_id (BUG-28).
// Unlike Start, Restart is gated to PhaseStatusBlocked specifically —
// done / done_with_concerns are intentionally non-retryable because the
// phase already produced an accepted envelope.
//
// On success: status → running, attempts++ (consumes retry budget),
// startedAt refreshed, completedAt cleared, prior envelope/confidence
// reset so the new Execute writes its own outcome cleanly. Returns
// ErrBudgetExhausted when the retry budget is at zero, ErrTerminal when
// called on a non-blocked terminal status, and ErrNotBlocked when the
// status is not terminal at all (use Start / Resume on those).
func (p *Phase) Restart(now time.Time) error {
	if p.status != PhaseStatusBlocked {
		if p.status.IsTerminal() {
			return ErrTerminal
		}
		return ErrNotBlocked
	}
	if p.attempts >= p.retryBudget {
		return ErrBudgetExhausted
	}
	p.status = PhaseStatusRunning
	p.attempts++
	p.startedAt = &now
	p.completedAt = nil
	p.envelope = nil
	p.confidence = 0
	return nil
}
