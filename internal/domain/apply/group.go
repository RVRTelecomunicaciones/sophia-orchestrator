package apply

import "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"

// Group is a set of Tasks in an Apply Board. Groups can depend on other
// Groups via DependsOn; the dependency graph is a DAG (validated by
// ValidateDAG).
//
// Build-gate lifecycle (GroupBuildStatus) is orthogonal to GroupStatus:
//   - GroupBuildStatusPending  — not yet evaluated (initial state).
//   - GroupBuildStatusSkipped  — no recognized manifest; gate bypassed.
//   - GroupBuildStatusPassed   — build exited 0; group may Complete.
//   - GroupBuildStatusFailed   — budget exhausted; group must Fail.
type Group struct {
	id            ids.GroupID
	boardID       ids.BoardID
	name          string
	dependsOn     []ids.GroupID
	tasks         []*Task
	status        GroupStatus
	worktreePath  string
	branchName    string
	buildStatus   GroupBuildStatus
	buildAttempts int
}

// NewGroup constructs a Group in GroupStatusPending with no build state.
func NewGroup(id ids.GroupID, boardID ids.BoardID, name string, dependsOn []ids.GroupID) *Group {
	return &Group{
		id: id, boardID: boardID, name: name,
		dependsOn:   dependsOn,
		status:      GroupStatusPending,
		buildStatus: GroupBuildStatusPending,
	}
}

// HydrateGroup reconstructs a Group from persisted state without replaying
// transitions. Used exclusively by repository adapters on resume.
func HydrateGroup(
	id ids.GroupID,
	boardID ids.BoardID,
	name string,
	dependsOn []ids.GroupID,
	status GroupStatus,
	worktreePath, branchName string,
	buildStatus GroupBuildStatus,
	buildAttempts int,
) *Group {
	return &Group{
		id:            id,
		boardID:       boardID,
		name:          name,
		dependsOn:     dependsOn,
		status:        status,
		worktreePath:  worktreePath,
		branchName:    branchName,
		buildStatus:   buildStatus,
		buildAttempts: buildAttempts,
	}
}

// ID returns the group id.
func (g *Group) ID() ids.GroupID { return g.id }

// BoardID returns the parent board id.
func (g *Group) BoardID() ids.BoardID { return g.boardID }

// Name returns the human-readable group name.
func (g *Group) Name() string { return g.name }

// DependsOn returns the upstream group ids that must complete before this
// group's team-lead may proceed.
func (g *Group) DependsOn() []ids.GroupID { return g.dependsOn }

// Tasks returns the tasks in insertion order.
func (g *Group) Tasks() []*Task { return g.tasks }

// Status returns the current group status.
func (g *Group) Status() GroupStatus { return g.status }

// WorktreePath returns the absolute worktree path assigned to this group's
// team-lead, or "" if not yet assigned.
func (g *Group) WorktreePath() string { return g.worktreePath }

// BranchName returns the git branch used by this group's worktree.
func (g *Group) BranchName() string { return g.branchName }

// BuildStatus returns the current build-gate status for this group.
func (g *Group) BuildStatus() GroupBuildStatus { return g.buildStatus }

// BuildAttempts returns how many build attempts have been made for this group.
// The value is capped by MaxAttempts before a new attempt is allowed.
func (g *Group) BuildAttempts() int { return g.buildAttempts }

// AddTask appends a task. Allowed during pending; once running, tasks are
// claimed via the Board, not added.
func (g *Group) AddTask(t *Task) error {
	if g.status != GroupStatusPending {
		return ErrInvalidGroupTransition
	}
	g.tasks = append(g.tasks, t)
	return nil
}

// AssignWorktree records the worktree path + branch for this group.
func (g *Group) AssignWorktree(path, branch string) {
	g.worktreePath = path
	g.branchName = branch
}

// Start transitions Pending → Running.
func (g *Group) Start() error {
	if g.status != GroupStatusPending {
		return ErrInvalidGroupTransition
	}
	g.status = GroupStatusRunning
	return nil
}

// Complete transitions Running → Completed. The caller is responsible for
// ensuring the build gate is satisfied (passed or skipped) before calling
// Complete; this method does not re-validate the build gate.
func (g *Group) Complete() error {
	if g.status != GroupStatusRunning {
		return ErrInvalidGroupTransition
	}
	g.status = GroupStatusCompleted
	return nil
}

// Fail transitions Running/Pending → Failed.
func (g *Group) Fail() error {
	if g.status == GroupStatusCompleted || g.status == GroupStatusFailed {
		return ErrInvalidGroupTransition
	}
	g.status = GroupStatusFailed
	return nil
}

// SkipBuild records that no recognized manifest was found and the build gate
// is bypassed. Allowed from GroupBuildStatusPending only.
func (g *Group) SkipBuild() error {
	if g.buildStatus != GroupBuildStatusPending {
		return ErrInvalidGroupBuildTransition
	}
	g.buildStatus = GroupBuildStatusSkipped
	return nil
}

// RecordBuildAttempt increments the build-attempt counter and transitions the
// build status:
//   - success=true  → GroupBuildStatusPassed
//   - success=false AND attempts < MaxAttempts → status stays Pending (retry allowed)
//   - success=false AND attempts >= MaxAttempts → GroupBuildStatusFailed +
//     ErrBuildBudgetExhausted
//
// RecordBuildAttempt is symmetric with Task.RecordAttempt and enforces the
// same MaxAttempts cap so the group build budget mirrors the task retry budget.
func (g *Group) RecordBuildAttempt(success bool) error {
	g.buildAttempts++
	if success {
		g.buildStatus = GroupBuildStatusPassed
		return nil
	}
	if g.buildAttempts >= MaxAttempts {
		g.buildStatus = GroupBuildStatusFailed
		return ErrBuildBudgetExhausted
	}
	// Remain in Pending so the application layer may retry.
	return nil
}

// AttachTaskToGroup injects a pre-hydrated task directly into a group's task
// slice, bypassing the AddTask transition guard. This MUST only be called by
// repository adapters that have already verified the task belongs to the group
// and that are reconstructing in-memory state from persisted data.
//
// The function is intentionally package-level (not a method) so it stays
// visible to the pg adapter while remaining clearly distinct from the normal
// AddTask workflow.
func AttachTaskToGroup(g *Group, t *Task) {
	g.tasks = append(g.tasks, t)
}

// ValidateDAG returns ErrCycle if the depends_on graph among the supplied
// groups has a cycle. Groups whose depends_on references unknown ids are
// ignored at this layer; the application layer must check existence.
func ValidateDAG(groups []*Group) error {
	const (
		unseen   = 0
		visiting = 1
		done     = 2
	)
	state := map[ids.GroupID]int{}
	byID := map[ids.GroupID]*Group{}
	for _, g := range groups {
		byID[g.id] = g
	}
	var visit func(g *Group) error
	visit = func(g *Group) error {
		switch state[g.id] {
		case visiting:
			return ErrCycle
		case done:
			return nil
		}
		state[g.id] = visiting
		for _, depID := range g.dependsOn {
			if dep, ok := byID[depID]; ok {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[g.id] = done
		return nil
	}
	for _, g := range groups {
		if err := visit(g); err != nil {
			return err
		}
	}
	return nil
}
