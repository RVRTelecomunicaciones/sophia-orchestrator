// Package apply contains the Apply Phase aggregates: Board (per-phase task
// coordination), Group (independent or dependent set of tasks), and Task
// (single coding work item). Iron Law #5 (no fix #4 without escalation) is
// enforced by Task.RecordAttempt.
package apply

// BoardStatus is the lifecycle of an Apply Board.
type BoardStatus string

// Apply Board statuses.
const (
	BoardStatusBuilding  BoardStatus = "building"
	BoardStatusRunning   BoardStatus = "running"
	BoardStatusCompleted BoardStatus = "completed"
	BoardStatusFailed    BoardStatus = "failed"
)

// GroupStatus is the lifecycle of a Group within a Board.
type GroupStatus string

// Group statuses.
const (
	GroupStatusPending   GroupStatus = "pending"
	GroupStatusRunning   GroupStatus = "running"
	GroupStatusCompleted GroupStatus = "completed"
	GroupStatusFailed    GroupStatus = "failed"
)

// TaskStatus is the lifecycle of a Task within a Group.
type TaskStatus string

// Task statuses.
const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusClaimed TaskStatus = "claimed"
	TaskStatusRunning TaskStatus = "running"
	TaskStatusDone    TaskStatus = "done"
	TaskStatusFailed  TaskStatus = "failed"
	TaskStatusBlocked TaskStatus = "blocked"
)
