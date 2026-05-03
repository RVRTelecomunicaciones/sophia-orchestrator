// Package discipline implements the orchestrator's enforcement layer:
// envelope validation, Iron Law checking, prompt building (with HARD-GATE
// markers and envelope schema injection), and the Spawn Governor (rate
// limiter for AI dispatcher subprocesses). Discipline is pure logic on the
// domain side plus one outbound dependency (SpawnGovernorRepo). No I/O
// outside the SpawnGovernorRepo interface.
package discipline

import "errors"

// Sentinel errors raised by Discipline services.
var (
	ErrPhaseMismatch  = errors.New("discipline: envelope phase does not match expected")
	ErrInvalidPhase   = errors.New("discipline: invalid phase")
	ErrSaturated      = errors.New("discipline: spawn governor saturated")
	ErrInvalidConfig  = errors.New("discipline: invalid config")
)
