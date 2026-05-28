package apply

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// saturationRetryBudget caps how many times runImplementWithRetry will
// retry SpawnGov.Acquire after receiving discipline.ErrSaturated before
// surfacing the saturation as a real task failure. Saturation is a
// transient signal: the governor is full, slots will free up as other
// in-flight tasks Release. Without this budget a single saturation hit
// would fail the task with attempts=0 and cascade to a false group
// failure (Spec / BUG-26).
const saturationRetryBudget = 5

// saturationBackoff returns the sleep duration before the next Acquire
// retry attempt. Starts at 500ms and doubles each retry, capping at 4s,
// so total wait across the budget is ~10s — bounded but generous enough
// to ride out typical contention bursts in a 3-group apply phase.
func saturationBackoff(attempt int) time.Duration {
	const (
		base       = 500 * time.Millisecond
		maxBackoff = 4 * time.Second
	)
	d := base << attempt //nolint:gosec // attempt is bounded by saturationRetryBudget
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}

// acquireWithSaturationRetries calls SpawnGov.Acquire up to
// saturationRetryBudget times, sleeping with exponential backoff between
// attempts on discipline.ErrSaturated. Non-saturation errors fail fast
// (ctx cancel, repo error). Saturation is transient: slots free as
// in-flight tasks Release, so a bounded retry rides out contention
// without poisoning the calling phase. See BUG-26.
func acquireWithSaturationRetries(ctx context.Context, gov SpawnGovernor) error {
	var err error
	for attempt := 0; attempt < saturationRetryBudget; attempt++ {
		err = gov.Acquire(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, discipline.ErrSaturated) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(saturationBackoff(attempt)):
		}
	}
	return err
}

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
	s.publishEvent(ctx, p.ID(), inbound.EventApplyTeamLeadSpawned, inbound.ApplyTeamLeadSpawnedPayload{
		SessionID: teamLeadSess.ID().String(),
		GroupID:   group.ID().String(),
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
	// BUG-28: skip tasks that already completed in a previous Execute
	// attempt. The board was reused via FindBoardByPhaseID, so the task
	// status carries forward. Skipping here avoids re-claiming (which
	// would fail anyway because ClaimTask only accepts pending tasks),
	// avoids spending governor budget, and avoids dispatching the LLM
	// for work already done.
	if task.Status() == apply.TaskStatusDone {
		return true
	}

	// Atomically claim the task before spending compute on it. If another
	// in-flight team-lead claimed the same task (shouldn't happen given
	// one team-lead per group, but defensive) we early-out as success
	// (the other lead owns it).
	implSession, err := s.makeSession(ctx, c, p, group, session.RoleImplement, task.Description())
	if err != nil {
		s.publishEvent(ctx, p.ID(), inbound.EventApplyImplementSpawnFailed, inbound.ApplyImplementSpawnFailedPayload{
			TaskID: task.ID().String(),
			Err:    err.Error(),
		})
		return false
	}
	claimed, err := s.d.BoardRepo.ClaimTask(ctx, task.ID(), implSession.ID())
	if err != nil || !claimed {
		s.publishEvent(ctx, p.ID(), inbound.EventApplyTaskClaimSkipped, inbound.ApplyTaskClaimSkippedPayload{
			TaskID: task.ID().String(),
			Err:    fmtErr(err),
		})
		return false
	}
	s.publishEvent(ctx, p.ID(), inbound.EventApplyTaskClaimed, inbound.ApplyTaskClaimedPayload{
		TaskID:    task.ID().String(),
		SessionID: implSession.ID().String(),
	})

	for attempt := 0; attempt < apply.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false
		}
		// SpawnGovernor gating per implement attempt. BUG-26: retry on
		// ErrSaturated before surfacing the saturation as a real
		// failure — saturation is transient (slots free as other
		// in-flight tasks Release). Other Acquire errors (ctx cancel,
		// repo error) still fail fast inside the helper.
		if err := acquireWithSaturationRetries(ctx, s.d.SpawnGov); err != nil {
			s.publishEvent(ctx, p.ID(), inbound.EventApplyImplementSpawnGovernorError, inbound.ApplyImplementSpawnGovernorErrorPayload{
				TaskID: task.ID().String(),
				Err:    err.Error(),
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
			// Escalation: 3rd consecutive failure. Spec #51 — surface the
			// final envelope summary + LLM-declared blocking reasons so
			// SSE consumers see WHY without needing DB access. Both
			// fields stay empty when the task never persisted an
			// envelope (e.g. all 3 attempts were dispatch errors).
			finalSummary := ""
			var blockers []string
			if env := task.Envelope(); env != nil {
				finalSummary = env.ExecutiveSummary
				blockers = extractBlockingReasons(env)
			}
			s.publishEvent(ctx, p.ID(), inbound.EventApplyTaskEscalated, inbound.ApplyTaskEscalatedPayload{
				TaskID:               task.ID().String(),
				Attempts:             task.Attempts(),
				Reason:               recordErr.Error(),
				FinalEnvelopeSummary: finalSummary,
				BlockingRequirements: blockers,
			})
			return false
		}
		s.publishEvent(ctx, p.ID(), inbound.EventApplyTaskRetry, inbound.ApplyTaskRetryPayload{
			TaskID:   task.ID().String(),
			Attempts: task.Attempts(),
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
//     and stays stable for the duration of the apply phase. Per-implement
//     enrichment with apply-progress happens here via refreshApplyProgress,
//     so every attempt sees the freshest snapshot of sibling tasks'
//     outcomes. Fail-soft: a memory failure on the refresh leaves the
//     base context intact rather than blocking the attempt.
func (s *RunService) dispatchImplement(ctx context.Context, c *change.Change, p *phase.Phase, _ *apply.Board, group *apply.Group, task *apply.Task, sess *session.Session, priorContext string) bool {
	enrichedContext := s.refreshApplyProgress(ctx, c, priorContext)
	prompt, err := s.d.Prompts.Build(discipline.PromptInput{
		Phase:             phase.PhaseApply,
		ChangeName:        c.Name(),
		Project:           c.Project(),
		PriorContext:      enrichedContext,
		TaskDescription:   fmt.Sprintf("%s\n\nWorktree: %s\nFiles: %v", task.Description(), group.WorktreePath(), task.FilesPattern()),
		PriorPhasesStatus: s.priorPhasesStatus,
	})
	if err != nil {
		return false
	}

	// MarkRunning is idempotent — a non-nil error means "already running"
	// on a previous attempt, which is fine. Discard the return value
	// rather than guarding with an empty block (revive flags empty blocks).
	_ = sess.MarkRunning()

	res, err := s.d.Dispatcher.Dispatch(ctx, outbound.DispatchRequest{
		Prompt:       prompt,
		WorktreePath: group.WorktreePath(),
		TimeoutMS:    s.d.Config.DispatchTimeoutMS,
		EnvelopeOut:  "stdout-fenced-json",
		PhaseType:    string(p.Type()),
	})
	if err != nil {
		// M-E0 #3: distinguish runtime-level dispatch failure from transport errors.
		// ErrDispatchFailed means the agent CLI never ran (e.g. binary not found,
		// shell.exec timeout). This is NOT an envelope validation failure.
		if errors.Is(err, outbound.ErrDispatchFailed) {
			s.publishEvent(ctx, p.ID(), inbound.EventRuntimeDispatchFailed, inbound.RuntimeDispatchFailedPayload{
				TaskID: task.ID().String(),
				Err:    err.Error(),
			})
			return false
		}
		// Transport-level failure (HTTP error, context cancellation, etc.).
		s.publishEvent(ctx, p.ID(), inbound.EventApplyDispatchError, inbound.ApplyDispatchErrorPayload{
			TaskID: task.ID().String(),
			Err:    err.Error(),
		})
		return false
	}

	// Aider (and any future in-place adapter) sets AdapterID == "aider"
	// and returns EnvelopeRaw=nil because it edits the worktree directly
	// instead of producing a JSON plan. Reconstruct a synthetic envelope
	// from `git status --porcelain` so the rest of this method stays
	// uniform across adapters. If reconstruction itself fails (e.g. the
	// runtime can't reach the worktree), fall through to the validation-
	// failed path below.
	if res.EnvelopeRaw == nil && res.AdapterID == "aider" {
		synth, synthErr := synthesizeEnvelopeFromGit(ctx, s.d.Runtime, group.WorktreePath())
		if synthErr == nil {
			res.EnvelopeRaw = synth
		}
	}

	// Defensive guard: should not happen after hardening (Dispatch returns
	// ErrDispatchFailed instead of nil-result on non-success receipts).
	// Preserved for forward-compatibility with other AgentDispatcher impls
	// AND as the fall-through when aider's git-status reconstruction fails.
	if res.EnvelopeRaw == nil {
		s.publishEvent(ctx, p.ID(), inbound.EventApplyEnvelopeValidationFailed, inbound.ApplyEnvelopeValidationFailedPayload{
			TaskID: task.ID().String(),
			Err:    "agent produced no fenced JSON envelope",
		})
		return false
	}

	env, err := s.d.Validator.Validate(res.EnvelopeRaw, phase.PhaseApply)
	if err != nil {
		// TRUE meaning of validation_failed: agent ran (receipt.Status="success")
		// but its output is invalid JSON or fails the envelope schema.
		s.publishEvent(ctx, p.ID(), inbound.EventApplyEnvelopeValidationFailed, inbound.ApplyEnvelopeValidationFailedPayload{
			TaskID: task.ID().String(),
			Err:    err.Error(),
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
