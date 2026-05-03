package apply

import (
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// MaxAttempts is the Iron Law #5 escalation threshold: 3 failures and the
// Task is marked BLOCKED with ErrEscalationRequired returned.
const MaxAttempts = 3

// Task is a single coding work item within a Group. The aggregate enforces
// claim/release semantics, attempt counting, and Iron Law #5 escalation.
type Task struct {
	id           ids.TaskID
	groupID      ids.GroupID
	description  string
	filesPattern []string
	status       TaskStatus
	claimedBy    *ids.SessionID
	attempts     int
	envelope     *envelope.Envelope
}

// NewTask constructs a pending Task.
func NewTask(id ids.TaskID, groupID ids.GroupID, description string, filesPattern []string) (*Task, error) {
	if description == "" {
		return nil, ErrEmptyDescription
	}
	if len(filesPattern) == 0 {
		return nil, ErrEmptyFilesPattern
	}
	patterns := make([]string, len(filesPattern))
	copy(patterns, filesPattern)
	return &Task{
		id: id, groupID: groupID,
		description: description, filesPattern: patterns,
		status: TaskStatusPending,
	}, nil
}

// ID returns the task id.
func (t *Task) ID() ids.TaskID { return t.id }

// GroupID returns the parent group id.
func (t *Task) GroupID() ids.GroupID { return t.groupID }

// Description returns the task body text.
func (t *Task) Description() string { return t.description }

// FilesPattern returns a defensive copy of the glob patterns owned by this
// task (passed to runtime lock.acquire).
func (t *Task) FilesPattern() []string {
	out := make([]string, len(t.filesPattern))
	copy(out, t.filesPattern)
	return out
}

// Status returns the current task status.
func (t *Task) Status() TaskStatus { return t.status }

// ClaimedBy returns the session id that claimed the task, or nil if pending.
func (t *Task) ClaimedBy() *ids.SessionID { return t.claimedBy }

// Attempts returns how many times the task has been attempted.
func (t *Task) Attempts() int { return t.attempts }

// Envelope returns the recorded envelope or nil.
func (t *Task) Envelope() *envelope.Envelope { return t.envelope }

// Claim transitions Pending → Claimed and records the claimer.
func (t *Task) Claim(sid ids.SessionID) error {
	if t.status != TaskStatusPending {
		return ErrAlreadyClaimed
	}
	t.status = TaskStatusClaimed
	t.claimedBy = &sid
	return nil
}

// Release transitions Claimed/Running → Pending and clears the claimer
// (used when a session crashed mid-task and the lock TTL released its files).
func (t *Task) Release() error {
	if t.status != TaskStatusClaimed && t.status != TaskStatusRunning {
		return ErrInvalidTaskTransition
	}
	t.status = TaskStatusPending
	t.claimedBy = nil
	return nil
}

// MarkRunning transitions Claimed → Running.
func (t *Task) MarkRunning() error {
	if t.status != TaskStatusClaimed {
		return ErrInvalidTaskTransition
	}
	t.status = TaskStatusRunning
	return nil
}

// Complete transitions Running → Done with the recorded envelope.
func (t *Task) Complete(e *envelope.Envelope) error {
	if t.status != TaskStatusRunning {
		return ErrInvalidTaskTransition
	}
	if e == nil {
		return ErrInvalidTaskTransition
	}
	t.envelope = e
	t.status = TaskStatusDone
	return nil
}

// RecordAttempt increments the attempt counter and updates status. On the
// 3rd consecutive failure (Iron Law #5), the task is marked Blocked and
// ErrEscalationRequired is returned. Successful attempts mark the task Done.
// Non-final failures mark the task Failed (which the team-lead may retry).
func (t *Task) RecordAttempt(success bool) error {
	t.attempts++
	if success {
		t.status = TaskStatusDone
		return nil
	}
	if t.attempts >= MaxAttempts {
		t.status = TaskStatusBlocked
		return ErrEscalationRequired
	}
	t.status = TaskStatusFailed
	return nil
}
