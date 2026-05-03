package apply

import "errors"

// Sentinel errors raised by Apply aggregates.
var (
	ErrInvalidBoardTransition = errors.New("apply.board: invalid transition")
	ErrInvalidGroupTransition = errors.New("apply.group: invalid transition")
	ErrInvalidTaskTransition  = errors.New("apply.task: invalid transition")
	ErrAlreadyClaimed         = errors.New("apply.task: already claimed")
	ErrNotClaimed             = errors.New("apply.task: not claimed")
	ErrEscalationRequired     = errors.New("apply.task: escalation required (3 failures)")
	ErrCycle                  = errors.New("apply.dag: cycle detected")
	ErrEmptyDescription       = errors.New("apply.task: empty description")
	ErrEmptyFilesPattern      = errors.New("apply.task: files_pattern empty")
)
