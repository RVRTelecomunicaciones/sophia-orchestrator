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

// GroupBuildStatus is the build-gate lifecycle of a Group. It is orthogonal to
// GroupStatus: a group may be Running while its build gate has passed, failed,
// or been skipped (no recognized manifest). The zero value "" is treated as
// GroupBuildStatusPending (not yet evaluated).
type GroupBuildStatus string

// Group build-gate statuses.
const (
	// GroupBuildStatusPending is the default — the build gate has not yet run.
	GroupBuildStatusPending GroupBuildStatus = "pending"
	// GroupBuildStatusSkipped means no recognized build manifest was found;
	// the build gate is bypassed and group completion proceeds immediately.
	GroupBuildStatusSkipped GroupBuildStatus = "skipped"
	// GroupBuildStatusPassed means the build exited with code 0.
	GroupBuildStatusPassed GroupBuildStatus = "passed"
	// GroupBuildStatusFailed means the build exhausted its attempt budget
	// without a successful exit. The Group will be marked GroupStatusFailed.
	GroupBuildStatusFailed GroupBuildStatus = "failed"
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
