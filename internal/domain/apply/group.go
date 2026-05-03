package apply

import "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"

// Group is a set of Tasks in an Apply Board. Groups can depend on other
// Groups via DependsOn; the dependency graph is a DAG (validated by
// ValidateDAG).
type Group struct {
	id           ids.GroupID
	boardID      ids.BoardID
	name         string
	dependsOn    []ids.GroupID
	tasks        []*Task
	status       GroupStatus
	worktreePath string
	branchName   string
}

// NewGroup constructs a Group in GroupStatusPending.
func NewGroup(id ids.GroupID, boardID ids.BoardID, name string, dependsOn []ids.GroupID) *Group {
	return &Group{
		id: id, boardID: boardID, name: name,
		dependsOn: dependsOn, status: GroupStatusPending,
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

// Complete transitions Running → Completed.
func (g *Group) Complete() error {
	if g.status != GroupStatusRunning {
		return ErrInvalidGroupTransition
	}
	g.status = GroupStatusCompleted
	return nil
}

// Fail transitions Running → Failed.
func (g *Group) Fail() error {
	if g.status == GroupStatusCompleted || g.status == GroupStatusFailed {
		return ErrInvalidGroupTransition
	}
	g.status = GroupStatusFailed
	return nil
}

// ValidateDAG returns ErrCycle if the depends_on graph among the supplied
// groups has a cycle. Groups whose depends_on references unknown ids are
// ignored at this layer; the application layer must check existence.
func ValidateDAG(groups []*Group) error {
	const (
		unseen = 0
		visiting = 1
		done = 2
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
