package apply

import "errors"

// Sentinel errors raised by RunService and helpers.
var (
	ErrNoTasksList         = errors.New("apply: tasks list missing in memory")
	ErrInvalidTasksList    = errors.New("apply: tasks list malformed")
	ErrDependencyTimeout   = errors.New("apply: group dependency wait timeout")
	ErrGroupFailed         = errors.New("apply: group failed")
	ErrNoBoard             = errors.New("apply: no board for phase")
	ErrEscalationRequired  = errors.New("apply: architectural escalation required (Iron Law #5)")
)
