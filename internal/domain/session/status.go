package session

// Status is the lifecycle of an AgentSession.
type Status string

// AgentSession statuses.
const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
	StatusTimeout Status = "timeout"
)

// IsValid reports whether s is a known session status.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusDone, StatusFailed, StatusTimeout:
		return true
	}
	return false
}

// IsTerminal reports whether s is a terminal state.
func (s Status) IsTerminal() bool {
	return s == StatusDone || s == StatusFailed || s == StatusTimeout
}
