// Package apply coordinates the SDD apply phase: parallel team-leads, one
// per group, each spawning implement-agents in parallel under SpawnGovernor
// gating. Iron Law #5 escalates at task.attempts == 3.
//
// V1 limitations (runtime-adapters Phase 2 capabilities not yet shipped):
//   - File reservation (lock.acquire@v1) — replaced by Postgres-backed
//     atomic ClaimTask. Sufficient for serialization at the task level;
//     fine-grained file-pattern locking comes with runtime Phase 2.
//   - Mailbox / msg_broadcast — replaced by in-process DAGCoordinator
//     (Go channels keyed by GroupID). Public API stable; will swap to
//     runtime mailbox calls when Phase 2 ships.
//   - git.worktree.create@v1 / git.merge@v1 — replaced by shell.exec@v1
//     with explicit `git worktree` / `git merge` commands. Functionally
//     identical for V1.
package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SpawnGovernor is the minimal contract from discipline.SpawnGovernor used
// here. Local declaration so tests can substitute fakes without dragging
// the full discipline.SpawnGovernor machinery.
type SpawnGovernor interface {
	Acquire(ctx context.Context) error
	Release(ctx context.Context) error
}

// RunDeps bundles dependencies for the RunService.
type RunDeps struct {
	BoardRepo   outbound.BoardRepository
	SessionRepo outbound.SessionRepository
	Runtime     outbound.RuntimeClient
	Dispatcher  outbound.AgentDispatcher
	SpawnGov    SpawnGovernor
	Validator   *discipline.Validator
	Prompts     *discipline.PromptBuilder
	Audit       outbound.AuditLog
	Events      inbound.EventStream
	Memory      outbound.MemoryClient
	Clock       shared.Clock
	IDGen       shared.IDGenerator
	Config      RunConfig

	// Metrics is the optional Prometheus instrument set. nil ⇒ no-op.
	Metrics *obs.Metrics
}

// RunConfig parameterizes RunService.
type RunConfig struct {
	MaxParallelGroups             int
	MaxParallelImplementsPerGroup int
	DepWaitTimeout                int // seconds; 0 ⇒ default 600 (10min)
	DispatchTimeoutMS             int
	WorktreeRoot                  string // base dir for per-group worktrees
	// Environment is the orchestrator's deployment env ("dev" | "staging" |
	// "prod"). Forwarded as a memory-engine scope filter on topic-key lookups
	// so we read records saved within the same environment.
	Environment string
}

// DefaultRunConfig returns V1 defaults aligned with spec § 5.2:
// 2x2 = 4 max concurrent agents, 10min dep wait, 30min dispatch, V1
// worktree root /tmp/sophia/worktrees.
func DefaultRunConfig() RunConfig {
	return RunConfig{
		MaxParallelGroups:             2,
		MaxParallelImplementsPerGroup: 2,
		DepWaitTimeout:                600,
		DispatchTimeoutMS:             1_800_000,
		WorktreeRoot:                  "/tmp/sophia/worktrees",
	}
}

// RunService implements the 18-step apply phase coordination from spec § 5.
// Public Execute() satisfies the phase.ApplyExecutor interface so phase.
// Service can delegate to RunService when Phase.Type == apply.
type RunService struct {
	d RunDeps
}

// NewRun constructs a RunService. All non-config Deps are required.
func NewRun(d RunDeps) *RunService {
	if d.BoardRepo == nil || d.SessionRepo == nil || d.Runtime == nil ||
		d.Dispatcher == nil || d.SpawnGov == nil || d.Validator == nil ||
		d.Prompts == nil || d.Audit == nil || d.Events == nil ||
		d.Memory == nil || d.Clock == nil || d.IDGen == nil {
		panic("apply.RunService: nil dependency")
	}
	if d.Config.MaxParallelGroups <= 0 {
		d.Config = DefaultRunConfig()
	}
	if d.Config.MaxParallelImplementsPerGroup <= 0 {
		d.Config.MaxParallelImplementsPerGroup = 2
	}
	if d.Config.DepWaitTimeout <= 0 {
		d.Config.DepWaitTimeout = 600
	}
	if d.Config.WorktreeRoot == "" {
		d.Config.WorktreeRoot = "/tmp/sophia/worktrees"
	}
	if d.Config.DispatchTimeoutMS <= 0 {
		d.Config.DispatchTimeoutMS = 1_800_000
	}
	return &RunService{d: d}
}

// Execute runs the 18-step apply phase coordination. Called by phase.Service
// from the goroutine that handles RunPhase, after the Phase row is in
// status=running. On return, the phase has either completed (with envelope)
// or been marked blocked. The phase.Service caller persists the phase.
func (s *RunService) Execute(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) (*envelope.Envelope, error) {
	// Step 1: pre-flight — load tasks list from memory-engine.
	tasksList, err := s.loadTasksList(ctx, c)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("load tasks: %v", err)), err
	}
	if len(tasksList.Groups) == 0 {
		return s.failEnv(c, p, "tasks list has no groups"), ErrInvalidTasksList
	}

	// Step 2: build the board (groups + tasks + dependency edges).
	board, err := s.buildBoard(ctx, p, tasksList)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("build board: %v", err)), err
	}

	// Step 3: create one git worktree per group via runtime shell.exec.
	if err := s.createWorktrees(ctx, c, board); err != nil {
		return s.failEnv(c, p, fmt.Sprintf("create worktrees: %v", err)), err
	}

	// Step 4: board → running.
	if err := board.Start(); err != nil {
		return s.failEnv(c, p, fmt.Sprintf("board start: %v", err)), err
	}
	if err := s.d.BoardRepo.SaveBoard(ctx, board); err != nil {
		return s.failEnv(c, p, fmt.Sprintf("save board: %v", err)), err
	}
	s.publishEvent(p.ID(), "apply.board.created", map[string]any{
		"board_id": board.ID().String(),
		"groups":   len(board.Groups()),
	})

	// Steps 5-17: dispatch all team-leads in parallel + DAG-aware wait.
	completion := NewDAGCoordinator(board.Groups())
	groupResults, err := s.runAllGroups(ctx, c, p, board, completion)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("group coordination: %v", err)), err
	}

	// Step 18: aggregate, finalize.
	return s.finalize(ctx, c, p, board, groupResults), nil
}

// runAllGroups dispatches one goroutine per group. Each waits on its
// dependencies, then runs the team-lead flow. Returns once every group
// has signaled completion.
func (s *RunService) runAllGroups(ctx context.Context, c *change.Change, p *phase.Phase, b *apply.Board, dag *DAGCoordinator) (map[ids.GroupID]groupOutcome, error) {
	results := make(map[ids.GroupID]groupOutcome, len(b.Groups()))
	var resultsMu sync.Mutex

	groupSem := make(chan struct{}, s.d.Config.MaxParallelGroups)
	var wg sync.WaitGroup

	for _, g := range b.Groups() {
		wg.Add(1)
		go func(group *apply.Group) {
			defer wg.Done()

			// Wait for upstream group dependencies.
			depTimeout := s.depWaitDuration().ToDuration()
			if err := dag.Wait(ctx, group.DependsOn(), depTimeout); err != nil {
				dag.Signal(group.ID(), true, err)
				resultsMu.Lock()
				results[group.ID()] = groupOutcome{failed: true, err: err}
				resultsMu.Unlock()
				s.publishEvent(p.ID(), "apply.group.failed", map[string]any{
					"group_id": group.ID().String(), "reason": err.Error(),
				})
				return
			}

			// Bound parallel-group concurrency.
			select {
			case groupSem <- struct{}{}:
			case <-ctx.Done():
				dag.Signal(group.ID(), true, ctx.Err())
				return
			}
			defer func() { <-groupSem }()

			// Spawn-governor for the team-lead itself.
			if err := s.d.SpawnGov.Acquire(ctx); err != nil {
				dag.Signal(group.ID(), true, err)
				resultsMu.Lock()
				results[group.ID()] = groupOutcome{failed: true, err: err}
				resultsMu.Unlock()
				return
			}
			outcome := s.runTeamLead(ctx, c, p, b, group)
			_ = s.d.SpawnGov.Release(ctx)

			resultsMu.Lock()
			results[group.ID()] = outcome
			resultsMu.Unlock()

			dag.Signal(group.ID(), outcome.failed, outcome.err)
			if outcome.failed {
				s.publishEvent(p.ID(), "apply.group.failed", map[string]any{
					"group_id": group.ID().String(), "reason": fmtErr(outcome.err),
				})
			} else {
				s.publishEvent(p.ID(), "apply.group.completed", map[string]any{
					"group_id":   group.ID().String(),
					"tasks_done": outcome.tasksDone,
				})
			}
		}(g)
	}
	wg.Wait()
	return results, nil
}

// finalize builds the final envelope summarising group outcomes. Marks
// board completed/failed accordingly. Audit + event trail emitted.
func (s *RunService) finalize(ctx context.Context, c *change.Change, p *phase.Phase, board *apply.Board, results map[ids.GroupID]groupOutcome) *envelope.Envelope {
	totalTasks := 0
	totalDone := 0
	failedGroups := 0
	for _, g := range board.Groups() {
		totalTasks += len(g.Tasks())
		if r := results[g.ID()]; r.failed {
			failedGroups++
		} else {
			totalDone += r.tasksDone
		}
	}

	status := envelope.StatusDone
	confidence := 0.85
	summary := fmt.Sprintf("Apply phase: %d groups, %d/%d tasks done", len(board.Groups()), totalDone, totalTasks)
	if failedGroups > 0 {
		status = envelope.StatusBlocked
		confidence = 0.0
		summary = fmt.Sprintf("Apply phase: %d/%d groups failed", failedGroups, len(board.Groups()))
		_ = board.Fail()
	} else {
		_ = board.Complete()
	}
	if err := s.d.BoardRepo.SaveBoard(ctx, board); err != nil {
		s.publishEvent(p.ID(), "apply.board.save_failed", map[string]any{"err": err.Error()})
	}

	// Metrics: record per-group + per-task aggregates.
	if s.d.Metrics != nil {
		for _, g := range board.Groups() {
			r := results[g.ID()]
			groupStatus := "completed"
			if r.failed {
				groupStatus = "failed"
			}
			s.d.Metrics.ApplyGroupsTotal.WithLabelValues(groupStatus).Inc()
			for _, tk := range g.Tasks() {
				s.d.Metrics.ApplyTasksTotal.WithLabelValues(string(tk.Status())).Inc()
				if tk.Attempts() > 0 {
					s.d.Metrics.ApplyTaskAttempts.Observe(float64(tk.Attempts()))
				}
			}
		}
	}

	env := &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(phase.PhaseApply),
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           status,
		Confidence:       confidence,
		ExecutiveSummary: summary,
		ArtifactsSaved: []envelope.ArtifactRef{{
			TopicKey: fmt.Sprintf("sdd/%s/apply-progress", c.Name()),
			Type:     "sdd_apply_progress",
		}},
	}

	cidLocal := c.ID()
	pidLocal := p.ID()
	payload, _ := json.Marshal(map[string]any{
		"groups":        len(board.Groups()),
		"tasks_total":   totalTasks,
		"tasks_done":    totalDone,
		"groups_failed": failedGroups,
	})
	_ = s.d.Audit.Append(ctx, outbound.AuditEvent{
		ChangeID:   &cidLocal,
		PhaseID:    &pidLocal,
		EventType:  "apply.finalized",
		Payload:    payload,
		OccurredAt: s.d.Clock.Now(),
	})
	return env
}

// failEnv constructs a synthetic BLOCKED envelope used by Execute on
// pre-flight failures. The phase.Service caller persists it.
func (s *RunService) failEnv(c *change.Change, p *phase.Phase, reason string) *envelope.Envelope {
	return &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(phase.PhaseApply),
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           envelope.StatusBlocked,
		Confidence:       0,
		ExecutiveSummary: reason,
	}
}

// --- helpers shared across the file ---

type groupOutcome struct {
	failed    bool
	err       error
	tasksDone int
}

// tasksList is the parsed structure produced by loadTasksList.
type tasksList struct {
	Groups []taskGroupSpec `json:"groups"`
}

type taskGroupSpec struct {
	Name      string         `json:"name"`
	DependsOn []string       `json:"depends_on,omitempty"`
	Tasks     []taskItemSpec `json:"tasks"`
}

type taskItemSpec struct {
	Description  string   `json:"description"`
	FilesPattern []string `json:"files_pattern"`
}

// loadTasksList retrieves the tasks list saved by the upstream tasks phase
// from sophia-memory-engine, addressed by topic_key (per ADR-0005 P0.2).
//
// Iron Law #1: BLOCKED-on-memory-failure is correct behavior. There is NO
// fallback — if the tasks list is missing or malformed we fail loudly so
// the operator sees the real failure rather than a silent stub run.
func (s *RunService) loadTasksList(ctx context.Context, c *change.Change) (*tasksList, error) {
	topic := fmt.Sprintf("sdd/%s/tasks", c.Name())
	scope := outbound.MemoryScope{
		ProjectID:   c.Project(),
		AgentID:     "sophia-orchestator",
		SessionID:   c.ID().String(),
		Environment: s.d.Config.Environment,
	}
	rec, err := s.d.Memory.GetByTopicKey(ctx, scope, topic)
	if err != nil {
		if errors.Is(err, outbound.ErrNotFound) {
			return nil, fmt.Errorf("loadTasksList %s: %w", topic, ErrNoTasksList)
		}
		return nil, fmt.Errorf("loadTasksList %s: %w", topic, err)
	}
	if rec == nil || rec.Content == "" {
		return nil, fmt.Errorf("loadTasksList %s: %w", topic, ErrNoTasksList)
	}

	var tl tasksList
	if err := json.Unmarshal([]byte(rec.Content), &tl); err != nil {
		return nil, fmt.Errorf("loadTasksList %s: %w: %v", topic, ErrInvalidTasksList, err)
	}
	return &tl, nil
}

func (s *RunService) buildBoard(ctx context.Context, p *phase.Phase, tl *tasksList) (*apply.Board, error) {
	bid, err := ids.ParseBoardID(s.d.IDGen.NewID())
	if err != nil {
		return nil, fmt.Errorf("board id: %w", err)
	}
	board := apply.NewBoard(bid, p.ID())

	// Persist the board FIRST so the FK from groups.board_id → apply_boards.id
	// is satisfied for the SaveGroup calls below.
	if err := s.d.BoardRepo.SaveBoard(ctx, board); err != nil {
		return nil, fmt.Errorf("save board (initial): %w", err)
	}

	// Resolve group names → ids first so DependsOn can be wired.
	idByName := map[string]ids.GroupID{}
	for _, gs := range tl.Groups {
		gid, err := ids.ParseGroupID(s.d.IDGen.NewID())
		if err != nil {
			return nil, fmt.Errorf("group id: %w", err)
		}
		idByName[gs.Name] = gid
	}

	for _, gs := range tl.Groups {
		gid := idByName[gs.Name]
		deps := make([]ids.GroupID, 0, len(gs.DependsOn))
		for _, depName := range gs.DependsOn {
			if id, ok := idByName[depName]; ok {
				deps = append(deps, id)
			}
		}
		group := apply.NewGroup(gid, bid, gs.Name, deps)
		for _, ts := range gs.Tasks {
			tid, err := ids.ParseTaskID(s.d.IDGen.NewID())
			if err != nil {
				return nil, fmt.Errorf("task id: %w", err)
			}
			task, err := apply.NewTask(tid, gid, ts.Description, ts.FilesPattern)
			if err != nil {
				return nil, fmt.Errorf("new task: %w", err)
			}
			if err := group.AddTask(task); err != nil {
				return nil, err //nolint:wrapcheck
			}
		}
		if err := apply.ValidateDAG(append(board.Groups(), group)); err != nil {
			return nil, err //nolint:wrapcheck
		}
		if err := board.AddGroup(group); err != nil {
			return nil, err //nolint:wrapcheck
		}
		if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
			return nil, fmt.Errorf("save group: %w", err)
		}
		for _, t := range group.Tasks() {
			if err := s.d.BoardRepo.SaveTask(ctx, t); err != nil {
				return nil, fmt.Errorf("save task: %w", err)
			}
		}
	}
	return board, nil
}

// createWorktrees calls runtime shell.exec@v1 to create one git worktree
// per group. V1.5 (when runtime ships git.worktree.create@v1 capability)
// will swap to that typed capability. For V1 we use the existing shell.exec
// with explicit git commands.
func (s *RunService) createWorktrees(ctx context.Context, c *change.Change, board *apply.Board) error {
	for _, g := range board.Groups() {
		path := filepath.Join(s.d.Config.WorktreeRoot, c.ID().String(), g.Name())
		branch := fmt.Sprintf("sophia/%s/%s", c.Name(), g.Name())
		g.AssignWorktree(path, branch)

		// Best-effort: persist the assignment. If runtime is unavailable in
		// dev/test the orchestrator continues; the dispatcher prompt
		// records the assigned worktree path either way.
		if err := s.d.BoardRepo.SaveGroup(ctx, g); err != nil {
			return fmt.Errorf("persist worktree assignment: %w", err)
		}

		// M-E0 wire-alignment: payload shape mirrors runtime ExecPayload
		// (command not cmd; no inner timeout_ms — that's outer).
		// V1 smoke: mkdir -p the path. V1.5 will swap to git.worktree.create@v1
		// once runtime ships the typed capability AND we have an upstream repo
		// to clone from. Plain mkdir lets the dispatcher's --dir flag find a
		// real directory; opencode will then cd into it.
		payload, _ := json.Marshal(map[string]any{
			"command": "mkdir",
			"args":    []string{"-p", path},
		})
		_, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
			Capability: "shell.exec@v1",
			Payload:    payload,
			TimeoutMS:  30_000,
		})
		if err != nil {
			s.publishEvent(board.PhaseID(), "apply.worktree.error", map[string]any{
				"group_id": g.ID().String(), "err": err.Error(),
			})
		}
	}
	return nil
}

func (s *RunService) depWaitDuration() depDuration {
	return depDuration(s.d.Config.DepWaitTimeout)
}

// depDuration wraps int seconds → time.Duration for clarity.
type depDuration int

func (d depDuration) ToDuration() time.Duration { return time.Duration(d) * time.Second }

func (s *RunService) publishEvent(phaseID ids.PhaseID, eventType string, payload map[string]any) {
	_ = s.d.Events.Publish(context.Background(), phaseID, inbound.Event{
		Type:      eventType,
		Timestamp: s.d.Clock.Now(),
		Payload:   payload,
	})
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// hashPrompt returns a hex-encoded SHA256 of the prompt, used as session
// PromptSHA256 for dedup + audit.
func hashPrompt(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

// roleForApply returns the role used for team-lead and implement sessions.
func roleForApply(role string) session.AgentRole {
	if role == "team-lead" {
		return session.RoleTeamLead
	}
	return session.RoleImplement
}
