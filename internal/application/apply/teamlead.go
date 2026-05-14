package apply

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// runTeamLead executes one team-lead workflow for a single Group: claim
// each task, spawn implement-agents in parallel (bounded by config),
// aggregate per-task outcomes, mark the group completed/failed.
//
// Iron Law #5 enforcement: each implement-agent retries up to MaxAttempts
// times (apply.MaxAttempts = 3); on the third failure the task is marked
// BLOCKED and ErrEscalationRequired propagates to the group outcome.
func (s *RunService) runTeamLead(ctx context.Context, c *change.Change, p *phase.Phase, b *apply.Board, group *apply.Group, priorContext string) groupOutcome {
	// Mark group running.
	if err := group.Start(); err != nil {
		return groupOutcome{failed: true, err: fmt.Errorf("group start: %w", err)}
	}
	if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
		return groupOutcome{failed: true, err: fmt.Errorf("save group: %w", err)}
	}

	// Create the team-lead agent session up front (audit + observability).
	teamLeadSess, err := s.makeSession(ctx, c, p, group, session.RoleTeamLead, "team-lead orchestrating "+group.Name())
	if err != nil {
		return groupOutcome{failed: true, err: err}
	}
	s.publishEvent(p.ID(), "apply.team_lead.spawned", map[string]any{
		"session_id": teamLeadSess.ID().String(),
		"group_id":   group.ID().String(),
	})

	// Run implement-agents in parallel within the group.
	implSem := make(chan struct{}, s.d.Config.MaxParallelImplementsPerGroup)
	var wg sync.WaitGroup
	taskOutcomes := make(map[ids.TaskID]bool, len(group.Tasks()))
	var mu sync.Mutex

	for _, t := range group.Tasks() {
		wg.Add(1)
		go func(task *apply.Task) {
			defer wg.Done()

			select {
			case implSem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-implSem }()

			ok := s.runImplementWithRetry(ctx, c, p, b, group, task, priorContext)
			mu.Lock()
			taskOutcomes[task.ID()] = ok
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	// Aggregate.
	var done int
	var hadFailure bool
	for _, ok := range taskOutcomes {
		if ok {
			done++
		} else {
			hadFailure = true
		}
	}

	// Mark group complete or failed.
	if hadFailure {
		_ = group.Fail()
	} else {
		_ = group.Complete()
	}
	_ = s.d.BoardRepo.SaveGroup(ctx, group)

	// Record team-lead session outcome.
	teamLeadEnv := &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(phase.PhaseApply),
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           envelope.StatusDone,
		Confidence:       0.85,
		ExecutiveSummary: fmt.Sprintf("group %s: %d/%d tasks done", group.Name(), done, len(group.Tasks())),
	}
	if hadFailure {
		teamLeadEnv.Status = envelope.StatusBlocked
		teamLeadEnv.Confidence = 0
	}
	exitCode := 0
	if hadFailure {
		exitCode = 1
	}
	_ = teamLeadSess.RecordOutcome(teamLeadEnv, exitCode, s.d.Clock.Now())
	_ = s.d.SessionRepo.Save(ctx, teamLeadSess)

	if hadFailure {
		return groupOutcome{failed: true, err: ErrGroupFailed, tasksDone: done}
	}
	return groupOutcome{failed: false, tasksDone: done}
}

// runImplementWithRetry runs one task through up to MaxAttempts implement-
// agent invocations, with SpawnGovernor gating per attempt and Iron Law #5
// escalation on the 3rd consecutive failure. Returns true iff the task
// reached envelope.StatusDone.
func (s *RunService) runImplementWithRetry(ctx context.Context, c *change.Change, p *phase.Phase, b *apply.Board, group *apply.Group, task *apply.Task, priorContext string) bool {
	// Atomically claim the task before spending compute on it. If another
	// in-flight team-lead claimed the same task (shouldn't happen given
	// one team-lead per group, but defensive) we early-out as success
	// (the other lead owns it).
	implSession, err := s.makeSession(ctx, c, p, group, session.RoleImplement, task.Description())
	if err != nil {
		s.publishEvent(p.ID(), "apply.implement.spawn_failed", map[string]any{
			"task_id": task.ID().String(), "err": err.Error(),
		})
		return false
	}
	claimed, err := s.d.BoardRepo.ClaimTask(ctx, task.ID(), implSession.ID())
	if err != nil || !claimed {
		s.publishEvent(p.ID(), "apply.task.claim_skipped", map[string]any{
			"task_id": task.ID().String(),
			"err":     fmtErr(err),
		})
		return false
	}
	s.publishEvent(p.ID(), "apply.task.claimed", map[string]any{
		"task_id":    task.ID().String(),
		"session_id": implSession.ID().String(),
	})

	for attempt := 0; attempt < apply.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false
		}
		// SpawnGovernor gating per implement attempt.
		if err := s.d.SpawnGov.Acquire(ctx); err != nil {
			s.publishEvent(p.ID(), "apply.implement.spawn_governor_error", map[string]any{
				"task_id": task.ID().String(), "err": err.Error(),
			})
			return false
		}
		ok := s.dispatchImplement(ctx, c, p, b, group, task, implSession, priorContext)
		_ = s.d.SpawnGov.Release(ctx)

		// Iron Law #5: record attempt; escalation triggers BLOCKED on 3rd fail.
		recordErr := task.RecordAttempt(ok)
		_ = s.d.BoardRepo.SaveTask(ctx, task)
		if ok {
			return true
		}
		if recordErr != nil {
			// Escalation: 3rd consecutive failure.
			s.publishEvent(p.ID(), "apply.task.escalated", map[string]any{
				"task_id":  task.ID().String(),
				"attempts": task.Attempts(),
				"reason":   recordErr.Error(),
			})
			return false
		}
		s.publishEvent(p.ID(), "apply.task.retry", map[string]any{
			"task_id":  task.ID().String(),
			"attempts": task.Attempts(),
		})
	}
	return false
}

// dispatchImplement runs ONE implement attempt: build prompt, dispatch via
// AgentDispatcher, validate envelope. Returns true on envelope.StatusDone.
//
// V1 simplifications:
//   - File reservation (lock.acquire@v1) is replaced by the atomic ClaimTask
//     above. Per-file locking is V1.5 once runtime ships Phase 2.
//   - priorContext (spec + design) is loaded once in RunService.Execute
//     and forwarded through runTeamLead → runImplementWithRetry. A
//     follow-up V1.5 item will refresh it per-implement to pick up
//     downstream artifacts written by sibling tasks (e.g., apply-progress
//     mid-run); for now it's stable for the duration of the apply phase.
func (s *RunService) dispatchImplement(ctx context.Context, c *change.Change, p *phase.Phase, _ *apply.Board, group *apply.Group, task *apply.Task, sess *session.Session, priorContext string) bool {
	prompt, err := s.d.Prompts.Build(discipline.PromptInput{
		Phase:           phase.PhaseApply,
		ChangeName:      c.Name(),
		Project:         c.Project(),
		PriorContext:    priorContext,
		TaskDescription: fmt.Sprintf("%s\n\nWorktree: %s\nFiles: %v", task.Description(), group.WorktreePath(), task.FilesPattern()),
	})
	if err != nil {
		return false
	}

	if err := sess.MarkRunning(); err != nil && err.Error() != "" {
		// Already running on a previous attempt — that's fine, continue.
	}

	res, err := s.d.Dispatcher.Dispatch(ctx, outbound.DispatchRequest{
		Prompt:       prompt,
		WorktreePath: group.WorktreePath(),
		TimeoutMS:    s.d.Config.DispatchTimeoutMS,
		EnvelopeOut:  "stdout-fenced-json",
	})
	if err != nil {
		// M-E0 #3: distinguish runtime-level dispatch failure from transport errors.
		// ErrDispatchFailed means the agent CLI never ran (e.g. binary not found,
		// shell.exec timeout). This is NOT an envelope validation failure.
		if errors.Is(err, outbound.ErrDispatchFailed) {
			s.publishEvent(p.ID(), "runtime.dispatch_failed", map[string]any{
				"task_id": task.ID().String(),
				"err":     err.Error(),
			})
			return false
		}
		// Transport-level failure (HTTP error, context cancellation, etc.).
		s.publishEvent(p.ID(), "apply.dispatch.error", map[string]any{
			"task_id": task.ID().String(), "err": err.Error(),
		})
		return false
	}

	// Defensive guard: should not happen after hardening (Dispatch returns
	// ErrDispatchFailed instead of nil-result on non-success receipts).
	// Preserved for forward-compatibility with other AgentDispatcher impls.
	if res.EnvelopeRaw == nil {
		s.publishEvent(p.ID(), "apply.envelope.validation_failed", map[string]any{
			"task_id": task.ID().String(), "err": "agent produced no fenced JSON envelope",
		})
		return false
	}

	env, err := s.d.Validator.Validate(res.EnvelopeRaw, phase.PhaseApply)
	if err != nil {
		// TRUE meaning of validation_failed: agent ran (receipt.Status="success")
		// but its output is invalid JSON or fails the envelope schema.
		s.publishEvent(p.ID(), "apply.envelope.validation_failed", map[string]any{
			"task_id": task.ID().String(), "err": err.Error(),
		})
		return false
	}

	_ = sess.RecordOutcome(env, res.ExitCode, s.d.Clock.Now())
	_ = s.d.SessionRepo.Save(ctx, sess)
	_ = task.Complete(env)
	_ = s.d.BoardRepo.SaveTask(ctx, task)

	// Hash audit payload for prompt provenance.
	_ = hashPrompt(prompt)
	_ = roleForApply // keep helper referenced

	return env.Status == envelope.StatusDone || env.Status == envelope.StatusDoneWithConcerns
}

func (s *RunService) makeSession(ctx context.Context, c *change.Change, p *phase.Phase, _ *apply.Group, role session.AgentRole, command string) (*session.Session, error) {
	sid, err := ids.ParseSessionID(s.d.IDGen.NewID())
	if err != nil {
		return nil, fmt.Errorf("session id: %w", err)
	}
	sess, err := session.New(sid, c.ID(), p.ID(), role, s.d.Dispatcher.Provider(), hashPrompt(command), command, s.d.Clock.Now())
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	if err := s.d.SessionRepo.Save(ctx, sess); err != nil {
		return nil, fmt.Errorf("save session: %w", err)
	}
	return sess, nil
}
