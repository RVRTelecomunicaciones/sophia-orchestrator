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
	"strings"
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
	// SourceRepoPath is the path (from the runtime container's
	// perspective) to copy into each new worktree via `cp -aR
	// <source>/. <worktree>/` BEFORE dispatching the implement agent.
	// Empty preserves the legacy V1 behaviour of an empty `mkdir -p`
	// directory — fine for create-only smoke tasks, but the implement
	// agent has no source to read or edit. Spec #65 (BUG-19).
	SourceRepoPath string
	// TargetPath is the OPERATOR-FACING destination where successful
	// group worktrees are materialized at the end of the apply phase
	// (BUG-29). Empty preserves the legacy behaviour — worktrees stay
	// isolated under WorktreeRoot/<change_id>/<group_name>/ and the
	// operator must copy them manually. When set to e.g.
	// "/Users/x/Documents/myproject", a successful apply phase copies
	// each successful group's worktree into
	// <TargetPath>/<group_name>/ before returning. Failed (or degraded
	// + failed) groups are NOT materialized — partial deliveries don't
	// pollute the operator's target tree.
	//
	// Loaded from SOPHIA_APPLY_TARGET_PATH.
	TargetPath string
	// WorktreeInit selects how createWorktrees populates each newly-
	// created worktree before dispatching the implement agent.
	//
	//   "" or "source_clone" — default. When SourceRepoPath is non-empty,
	//                          copy the source tree into the worktree
	//                          via `cp -aR <src>/. <worktree>/` (the
	//                          BUG-19 behaviour). Correct for cycles
	//                          that EDIT the orch's own source tree.
	//   "empty"             — skip the source copy regardless of
	//                          SourceRepoPath. The worktree is just
	//                          `mkdir -p`'d. Use this for cross-language
	//                          NEW-FEATURE cycles where seeing the orch's
	//                          Go source confuses the implement LLM into
	//                          returning BLOCKED (BUG-27, observed in the
	//                          2026-05-27 Node 22 todolist smoke).
	//
	// Loaded from SOPHIA_APPLY_WORKTREE_INIT.
	WorktreeInit string
	// Environment is the orchestrator's deployment env ("dev" | "staging" |
	// "prod"). Forwarded as a memory-engine scope filter on topic-key lookups
	// so we read records saved within the same environment.
	Environment string
}

// WorktreeInitMode constants for RunConfig.WorktreeInit.
const (
	WorktreeInitSourceClone = "source_clone"
	WorktreeInitEmpty       = "empty"
)

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
//
// priorPhasesStatus is set per Execute() invocation from
// inbound.RunPhaseInput.PriorPhasesStatus and read by dispatchImplement
// when building each implement-agent prompt. The field is reset on every
// call so concurrent changes (one RunService instance across requests)
// observe a consistent snapshot for the duration of their Execute.
// Spec #51.
type RunService struct {
	d                 RunDeps
	priorPhasesStatus map[phase.PhaseType]string
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
	// Spec #51: capture the orchestrator-verified prior-phase status
	// snapshot for the duration of this apply run. Read by
	// dispatchImplement when building each implement-agent prompt.
	s.priorPhasesStatus = in.PriorPhasesStatus

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
	//
	// BUG-28: when this is a Resume of a previously-blocked apply phase,
	// the board (reused via FindBoardByPhaseID) is already past Building
	// and Start would error with ErrInvalidBoardTransition. Skip the
	// transition in that case — the board's lifecycle status doesn't
	// need to round-trip back to running for the per-group retry to
	// work. The finalize step at the end of Execute will re-evaluate the
	// board's terminal status (Complete/Fail) from the aggregated group
	// outcomes regardless.
	if board.Status() == apply.BoardStatusBuilding {
		if err := board.Start(); err != nil {
			return s.failEnv(c, p, fmt.Sprintf("board start: %v", err)), err
		}
		if err := s.d.BoardRepo.SaveBoard(ctx, board); err != nil {
			return s.failEnv(c, p, fmt.Sprintf("save board: %v", err)), err
		}
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
	finalEnv := s.finalize(ctx, c, p, board, groupResults)

	// BUG-29: materialize successful group worktrees into the
	// operator-facing TargetPath when configured. Failed groups are
	// skipped so the target tree never carries half-broken deliveries.
	// Errors are surfaced via apply.materialize.error but do NOT mutate
	// the phase outcome — the source-of-truth is the per-group worktree
	// under WorktreeRoot, and the operator can re-materialize manually.
	if s.d.Config.TargetPath != "" {
		s.materializeWorktrees(ctx, p, board, groupResults)
	}

	return finalEnv, nil
}

// materializeWorktrees copies each successful group's worktree into
// <TargetPath>/<group_name>/ via shell.exec cp. Best-effort: per-group
// errors are emitted as apply.materialize.error but do not abort the
// remaining groups. See BUG-29.
func (s *RunService) materializeWorktrees(ctx context.Context, p *phase.Phase, board *apply.Board, results map[ids.GroupID]groupOutcome) {
	s.publishEvent(ctx, p.ID(), inbound.EventApplyMaterializeStarted, inbound.ApplyMaterializeStartedPayload{
		TargetPath: s.d.Config.TargetPath,
	})
	groupsMaterialized := 0
	for _, g := range board.Groups() {
		outcome, ok := results[g.ID()]
		if !ok || outcome.failed {
			continue
		}
		src := g.WorktreePath()
		if src == "" {
			continue
		}
		dst := filepath.Join(s.d.Config.TargetPath, g.Name())
		mkdirPayload, _ := json.Marshal(map[string]any{
			"command": "mkdir",
			"args":    []string{"-p", dst},
		})
		if _, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
			Capability: "shell.exec@v1",
			Payload:    mkdirPayload,
			TimeoutMS:  30_000,
		}); err != nil {
			s.publishEvent(ctx, p.ID(), inbound.EventApplyMaterializeError, inbound.ApplyMaterializeErrorPayload{
				GroupID: g.ID().String(),
				Err:     fmt.Sprintf("mkdir target: %v", err),
			})
			continue
		}
		cpPayload, _ := json.Marshal(map[string]any{
			"command": "cp",
			"args":    []string{"-aR", src + "/.", dst + "/"},
		})
		if _, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
			Capability: "shell.exec@v1",
			Payload:    cpPayload,
			TimeoutMS:  300_000,
		}); err != nil {
			s.publishEvent(ctx, p.ID(), inbound.EventApplyMaterializeError, inbound.ApplyMaterializeErrorPayload{
				GroupID: g.ID().String(),
				Err:     fmt.Sprintf("cp to target: %v", err),
			})
			continue
		}
		groupsMaterialized++
	}
	s.publishEvent(ctx, p.ID(), inbound.EventApplyMaterializeCompleted, inbound.ApplyMaterializeCompletedPayload{
		TargetPath:         s.d.Config.TargetPath,
		GroupsMaterialized: groupsMaterialized,
	})
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

			// BUG-28: skip groups that already completed in a previous
			// Execute attempt (Service.Resume reused the existing board
			// for this phase_id). We signal success to the DAG without
			// re-running the team-lead so downstream dependents see the
			// completion without waiting for redundant LLM work.
			if group.Status() == apply.GroupStatusCompleted {
				dag.Signal(group.ID(), false, nil)
				resultsMu.Lock()
				results[group.ID()] = groupOutcome{failed: false, tasksDone: len(group.Tasks())}
				resultsMu.Unlock()
				return
			}

			// Wait for upstream group dependencies.
			//
			// BUG-30 (cascade soften): when an upstream dep FAILED
			// (ErrGroupFailed), we used to mark this group failed
			// without ever attempting its tasks — a single LLM
			// regression at the top of the DAG cascaded to every
			// downstream group and an entire apply phase blocked.
			// Now we distinguish: ErrGroupFailed → emit
			// apply.group.degraded and CONTINUE; ctx cancel or
			// ErrDependencyTimeout still hard-fail (those are
			// environmental, not LLM, signals).
			depTimeout := s.depWaitDuration().ToDuration()
			if err := dag.Wait(ctx, group.DependsOn(), depTimeout); err != nil {
				if errors.Is(err, ErrGroupFailed) {
					failedDep, depErr := extractFailedDep(err)
					s.publishEvent(ctx, p.ID(), inbound.EventApplyGroupDegraded, inbound.ApplyGroupDegradedPayload{
						GroupID:      group.ID().String(),
						FailedDep:    failedDep,
						FailedDepErr: depErr,
						ContinuedRun: true,
					})
					// fall through — execute the group anyway
				} else {
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
			}

			// Bound parallel-group concurrency.
			select {
			case groupSem <- struct{}{}:
			case <-ctx.Done():
				dag.Signal(group.ID(), true, ctx.Err())
				return
			}
			defer func() { <-groupSem }()

			// Spawn-governor for the team-lead itself. BUG-26: retry
			// on transient saturation via the shared helper before
			// failing the entire group.
			if err := acquireWithSaturationRetries(ctx, s.d.SpawnGov); err != nil {
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
		return nil, fmt.Errorf("unwrapArtifactData: %w", err)
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
	// BUG-28: when the same phase_id already carries a board (operator
	// resumed a previously-blocked apply phase via Service.Resume), reuse
	// the existing board so all per-group / per-task statuses survive the
	// retry. The run loops downstream (runAllGroups, runTeamLead,
	// runImplementWithRetry) consult those statuses to skip work that
	// already succeeded — only the failed groups attempt again, the
	// successful worktrees stay intact, and the retry budget at the
	// phase level was already incremented by Phase.Restart in the Service
	// Resume path.
	if existing, err := s.d.BoardRepo.FindBoardByPhaseID(ctx, p.ID()); err == nil && existing != nil {
		return existing, nil
	}

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

// createWorktrees creates one per-group worktree directory and (when
// configured) pre-populates it with a copy of the source repository
// so the implement agent has source code to read and edit.
//
// Two execution modes, switched by RunConfig.SourceRepoPath:
//
//   - Empty (legacy V1, Spec § 5.2): single `mkdir -p <path>` call.
//     The worktree is an empty directory; the implement agent can
//     ONLY create files. Sufficient for create-only smoke tasks
//     (greeting.go) but blocks any edit-existing-code task because
//     the agent's cwd has no source to discover. Comment on this
//     branch dated the proper fix as "V1.5 will swap to
//     git.worktree.create@v1 once runtime ships the typed capability
//     AND we have an upstream repo to clone from" — that capability
//     never landed; this commit takes the pragmatic intermediate path
//     instead.
//
//   - Set (Spec #65 / BUG-19): two shell.exec calls per group —
//     `mkdir -p <path>` followed by `cp -aR <source>/. <path>/`. The
//     copy uses `-a` (preserve mode/timestamps/symlinks) and the
//     `/.` source suffix (copy contents, not the source directory
//     itself, so the destination keeps its own name). Includes the
//     repo's `.git` so the implement agent can run `git diff` to
//     understand the baseline. Source path is interpreted from the
//     runtime container's namespace (e.g. "/workspace/<repo>" under
//     the read-only workspace bind mount).
//
// Both modes use shell.exec@v1 because runtime-adapters has not
// shipped a typed git.worktree.create@v1 capability. When that lands
// the SourceRepoPath branch should swap to it; the empty branch
// stays as the smoke/no-source fallback.
//
// Errors from any sub-call are reported via apply.worktree.error
// SSE event but NEVER fail the phase. The implement agent will hit
// its own error if the cwd is missing or empty; we surface the cause
// in observability rather than aborting the apply pipeline here.
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

		// Step 1: mkdir the worktree path. Runs in both modes — `cp -aR`
		// would create the leaf directory automatically but `mkdir -p`
		// also creates the change-id parent and any missing root
		// segments under WorktreeRoot.
		mkdirPayload, _ := json.Marshal(map[string]any{
			"command": "mkdir",
			"args":    []string{"-p", path},
		})
		if _, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
			Capability: "shell.exec@v1",
			Payload:    mkdirPayload,
			TimeoutMS:  30_000,
		}); err != nil {
			s.publishEvent(ctx, board.PhaseID(), inbound.EventApplyWorktreeError, inbound.ApplyWorktreeErrorPayload{
				GroupID: g.ID().String(),
				Err:     fmt.Sprintf("mkdir: %v", err),
			})
			continue
		}

		// Step 2: when SourceRepoPath is set, copy the source tree into
		// the freshly-created worktree (Spec #65 / BUG-19). The "/."
		// suffix on source tells cp to copy the contents rather than
		// the source directory itself — destination/.git/, destination/
		// internal/ etc., not destination/<src>/...
		//
		// BUG-27: skip the copy entirely when WorktreeInit == "empty".
		// Cross-language NEW-FEATURE cycles (e.g. building a Node 22
		// TODO API from the orch's Go workspace) hit semantic mismatch
		// when the implement LLM lands in a worktree full of Go source
		// it isn't supposed to touch — observed in the 2026-05-27
		// todolist smoke where every implement attempt returned BLOCKED
		// and IL5-escalated. The operator picks the mode per cycle via
		// SOPHIA_APPLY_WORKTREE_INIT; default remains source_clone so
		// existing self-modification cycles are unaffected.
		if s.d.Config.WorktreeInit == WorktreeInitEmpty {
			continue
		}
		if src := s.d.Config.SourceRepoPath; src != "" {
			cpPayload, _ := json.Marshal(map[string]any{
				"command": "cp",
				"args":    []string{"-aR", src + "/.", path + "/"},
			})
			if _, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
				Capability: "shell.exec@v1",
				Payload:    cpPayload,
				// Copy can be slower than mkdir — give it a fuller
				// budget. 5 minutes is still well under the apply
				// dispatch timeout.
				TimeoutMS: 300_000,
			}); err != nil {
				s.publishEvent(ctx, board.PhaseID(), inbound.EventApplyWorktreeError, inbound.ApplyWorktreeErrorPayload{
					GroupID: g.ID().String(),
					Err:     fmt.Sprintf("cp source repo: %v", err),
				})
			}
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

// extractFailedDep parses the dag.Wait ErrGroupFailed error and returns
// (failed_dep_id, root_cause_err). The shape matches dag.go's wrap:
//
//	"apply: group failed: dependency <gid> failed: <root>"
//
// On unexpected shape we return ("", err.Error()) so the degraded event
// still carries the message even if the dep id can't be peeled off.
// Spec / BUG-30.
func extractFailedDep(err error) (string, string) {
	msg := err.Error()
	const marker = "dependency "
	idx := strings.Index(msg, marker)
	if idx < 0 {
		return "", msg
	}
	rest := msg[idx+len(marker):]
	endIdx := strings.Index(rest, " failed: ")
	if endIdx < 0 {
		return "", msg
	}
	return rest[:endIdx], strings.TrimPrefix(rest[endIdx:], " failed: ")
}
