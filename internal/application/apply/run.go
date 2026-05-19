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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
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
func (s *RunService) Execute(ctx context.Context, c *change.Change, p *phase.Phase, _ inbound.RunPhaseInput) (*envelope.Envelope, error) {
	// Step 1: pre-flight — load tasks list from memory-engine.
	tasksList, err := s.loadTasksList(ctx, c)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("load tasks: %v", err)), err
	}
	if len(tasksList.Groups) == 0 {
		return s.failEnv(c, p, "tasks list has no groups"), ErrInvalidTasksList
	}

	// Step 1b: pull prior context (spec + design) so every implement-agent
	// gets architectural background alongside its per-task description.
	// Non-fatal if either phase is absent (ErrNotFound is silently dropped
	// inside loadPriorContext); other errors propagate.
	priorContext, err := s.loadPriorContext(ctx, c)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("load prior context: %v", err)), err
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
	s.publishEvent(ctx, p.ID(), inbound.EventApplyBoardCreated, inbound.ApplyBoardCreatedPayload{
		BoardID: board.ID().String(),
		Groups:  len(board.Groups()),
	})

	// Steps 5-17: dispatch all team-leads in parallel + DAG-aware wait.
	completion := NewDAGCoordinator(board.Groups())
	groupResults, err := s.runAllGroups(ctx, c, p, board, completion, priorContext)
	if err != nil {
		return s.failEnv(c, p, fmt.Sprintf("group coordination: %v", err)), err
	}

	// Step 18: aggregate, finalize.
	return s.finalize(ctx, c, p, board, groupResults), nil
}

// runAllGroups dispatches one goroutine per group. Each waits on its
// dependencies, then runs the team-lead flow. Returns once every group
// has signaled completion. priorContext is forwarded to every team-lead
// and eventually injected into the implement-agent prompts.
func (s *RunService) runAllGroups(ctx context.Context, c *change.Change, p *phase.Phase, b *apply.Board, dag *DAGCoordinator, priorContext string) (map[ids.GroupID]groupOutcome, error) {
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
				s.publishEvent(ctx, p.ID(), inbound.EventApplyGroupFailed, inbound.ApplyGroupFailedPayload{
					GroupID: group.ID().String(),
					Reason:  err.Error(),
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
			outcome := s.runTeamLead(ctx, c, p, b, group, priorContext)
			_ = s.d.SpawnGov.Release(ctx)

			resultsMu.Lock()
			results[group.ID()] = outcome
			resultsMu.Unlock()

			dag.Signal(group.ID(), outcome.failed, outcome.err)
			if outcome.failed {
				s.publishEvent(ctx, p.ID(), inbound.EventApplyGroupFailed, inbound.ApplyGroupFailedPayload{
					GroupID: group.ID().String(),
					Reason:  fmtErr(outcome.err),
				})
			} else {
				s.publishEvent(ctx, p.ID(), inbound.EventApplyGroupCompleted, inbound.ApplyGroupCompletedPayload{
					GroupID:   group.ID().String(),
					TasksDone: outcome.tasksDone,
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
		s.publishEvent(ctx, p.ID(), inbound.EventApplyBoardSaveFailed, inbound.ApplyBoardSaveFailedPayload{Err: err.Error()})
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
func (s *RunService) failEnv(c *change.Change, _ *phase.Phase, reason string) *envelope.Envelope {
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

// unwrapArtifactData extracts the "data" field from a persisted full envelope
// (produced by persist_artifacts.go) or returns the original bytes unchanged
// when the input is already a bare payload.
//
// Detection rule (Spec #44): if the JSON object contains "data" AND at least
// one envelope marker key (schema_version, status, phase, change_name,
// project), the value of "data" is returned. Otherwise the original bytes
// are returned unchanged to preserve backward-compatibility with legacy bare
// {groups:[...]} payloads.
//
// An invalid JSON input returns an error immediately so callers can surface a
// proper ErrInvalidTasksList rather than a confusing empty-groups block.
func unwrapArtifactData(raw string) ([]byte, error) {
	if raw == "" {
		return []byte(raw), nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil {
		return nil, err
	}

	dataField, hasData := top["data"]
	if !hasData {
		// Bare payload — pass through unchanged.
		return []byte(raw), nil
	}

	// Envelope markers: presence of any one is sufficient to confirm this is a
	// full envelope rather than a data document that happens to have a "data" key.
	envelopeMarkers := []string{"schema_version", "status", "phase", "change_name", "project"}
	isEnvelope := false
	for _, m := range envelopeMarkers {
		if _, ok := top[m]; ok {
			isEnvelope = true
			break
		}
	}
	if !isEnvelope {
		return []byte(raw), nil
	}
	return dataField, nil
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

	payload, err := unwrapArtifactData(rec.Content)
	if err != nil {
		return nil, fmt.Errorf("loadTasksList %s: %w: %w", topic, ErrInvalidTasksList, err)
	}

	var tl tasksList
	if err := json.Unmarshal(payload, &tl); err != nil {
		return nil, fmt.Errorf("loadTasksList %s: %w: %w", topic, ErrInvalidTasksList, err)
	}
	return &tl, nil
}

// refreshApplyProgress enriches the base prior context (spec + design,
// loaded once in Execute) with the latest apply-progress snapshot from
// memory-engine. Called per-implement so each attempt sees the freshest
// view of what sibling tasks in the same phase have finished — useful
// when a later task needs to avoid duplicating files a sibling already
// created, or wants to know what got merged before its turn.
//
// Returns the combined context: base + "\n\n## Recent progress …" when
// a progress record exists, or base unchanged otherwise.
//
// Failure semantics: FAIL-SOFT. Any error (ErrNotFound, transport,
// deserialization) returns the base unchanged. The refresh is
// enrichment — losing it does NOT block the implement attempt. This
// differs from loadPriorContext which propagates non-NotFound errors
// per Iron Law #1 (those happen pre-fan-out, in the synchronous prep).
func (s *RunService) refreshApplyProgress(ctx context.Context, c *change.Change, base string) string {
	scope := outbound.MemoryScope{
		ProjectID:   c.Project(),
		AgentID:     "sophia-orchestator",
		SessionID:   c.ID().String(),
		Environment: s.d.Config.Environment,
	}
	topic := fmt.Sprintf("sdd/%s/apply-progress", c.Name())
	rec, err := s.d.Memory.GetByTopicKey(ctx, scope, topic)
	if err != nil || rec == nil || rec.Content == "" {
		// Silent fail-soft: enrichment optional, base context is enough
		// to keep the implement-agent informed about spec + design.
		return base
	}
	section := fmt.Sprintf("## Recent progress (sdd/%s/apply-progress)\n\n%s",
		c.Name(), rec.Content)
	if base == "" {
		return section
	}
	return base + "\n\n" + section
}

// loadPriorContext pulls the spec and design artifacts from memory-engine
// (saved by their respective phases via topic_key) and concatenates them
// with section headers so the implement-agent gets architectural context
// alongside the per-task description.
//
// Failure semantics differ from loadTasksList:
//   - ErrNotFound on either phase is non-fatal — we proceed with whatever
//     subset is present. The apply phase is required to have tasks
//     approved (IL2), but spec/design may legitimately be absent for
//     very small changes that go straight from proposal to tasks.
//   - Any other error (transport, deserialization) is propagated so the
//     caller can decide whether to BLOCK or proceed empty.
//
// Returns "" when no records are found. Closes the V1.5 follow-up at
// teamlead.go:197 (PriorContext was hardcoded to "" until this change).
func (s *RunService) loadPriorContext(ctx context.Context, c *change.Change) (string, error) {
	scope := outbound.MemoryScope{
		ProjectID:   c.Project(),
		AgentID:     "sophia-orchestator",
		SessionID:   c.ID().String(),
		Environment: s.d.Config.Environment,
	}
	sections := make([]string, 0, 2)
	for _, phaseKey := range []string{"spec", "design"} {
		topic := fmt.Sprintf("sdd/%s/%s", c.Name(), phaseKey)
		rec, err := s.d.Memory.GetByTopicKey(ctx, scope, topic)
		if err != nil {
			if errors.Is(err, outbound.ErrNotFound) {
				continue
			}
			return "", fmt.Errorf("loadPriorContext %s: %w", topic, err)
		}
		if rec == nil || rec.Content == "" {
			continue
		}
		// Header tags the section so the LLM can distinguish spec from
		// design when both are present. Keeps the prompt structure
		// stable across changes.
		sections = append(sections, fmt.Sprintf("## %s (sdd/%s/%s)\n\n%s",
			phaseKey, c.Name(), phaseKey, rec.Content))
	}
	if len(sections) == 0 {
		return "", nil
	}
	// Two newlines between sections so markdown renders cleanly when the
	// agent (or a debug tool) prints the full prompt.
	out := sections[0]
	for _, s := range sections[1:] {
		out += "\n\n" + s
	}
	return out, nil
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
			s.publishEvent(ctx, board.PhaseID(), inbound.EventApplyWorktreeError, inbound.ApplyWorktreeErrorPayload{
				GroupID: g.ID().String(),
				Err:     err.Error(),
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

// publishEvent emits an SSE event with the given typed payload.
// payload should be one of the typed structs from
// internal/ports/inbound/event_payloads.go (e.g. ApplyTaskClaimedPayload)
// so the producer gets compile-time validation of field names.
// map[string]any is still accepted for tests and gradual migration.
//
// ctx must carry the request's Trace (via trace.NewContext) so the persisted
// Event row keeps trace_id correlation with the originating HTTP request.
func (s *RunService) publishEvent(ctx context.Context, phaseID ids.PhaseID, eventType string, payload any) {
	var traceID string
	if t, ok := trace.FromContext(ctx); ok {
		traceID = t.TraceID
	}
	_ = s.d.Events.Publish(ctx, phaseID, inbound.Event{
		Type:      eventType,
		Timestamp: s.d.Clock.Now(),
		Payload:   payload,
		TraceID:   traceID,
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
