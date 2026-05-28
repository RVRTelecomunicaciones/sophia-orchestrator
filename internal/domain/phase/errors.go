package phase

import "errors"

// Sentinel errors raised by the Phase aggregate.
var (
	ErrInvalidType     = errors.New("phase: invalid type")
	ErrTerminal        = errors.New("phase: already terminal")
	ErrNotRunning      = errors.New("phase: not running")
	ErrBudgetExhausted = errors.New("phase: retry budget exhausted")
	ErrBelowThreshold  = errors.New("phase: confidence below threshold")
	ErrEnvelopeNil     = errors.New("phase: envelope nil")
	ErrEnvelopeMismatch = errors.New("phase: envelope phase mismatch")
	// ErrNotBlocked is returned by Phase.Restart when called on a phase
	// whose status is not PhaseStatusBlocked AND not terminal. Restart is
	// the BUG-28 retry path for blocked phases specifically; running /
	// pending / needs_context / interrupted callers should use Start or
	// Resume instead.
	ErrNotBlocked = errors.New("phase: not blocked")
)
