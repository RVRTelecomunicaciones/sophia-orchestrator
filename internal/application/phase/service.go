// Package phase implements the PhaseService inbound port. The Run use case
// is the heart of the orchestrator: it executes the 16-step single-agent
// flow from spec § 4 (validate → governance → discipline → dispatch →
// envelope → persist → audit) with a 202-Accepted-plus-SSE response
// pattern so long-running phases survive Cloudflare's 100s edge timeout.
//
// Concurrency: Run returns immediately (after the synchronous prep work);
// the dispatch + envelope-validation + persistence steps run in a goroutine
// scheduled via the injected Scheduler. Production wires Scheduler =
// AsyncScheduler (real goroutine); tests wire SyncScheduler so the work
// completes before Run returns.
package phase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Sentinel errors raised by the Phase service.
var (
	ErrInvalidTransition  = errors.New("phase service: invalid phase transition")
	ErrPhaseRunning       = errors.New("phase service: another phase already running for this change")
	ErrAlreadyTerminal    = errors.New("phase service: phase already terminal")
	ErrApproverRequired   = errors.New("phase service: approver field is required")
	ErrPhaseNotGated      = errors.New("phase service: phase is not awaiting approval")
	ErrGateAlreadyDecided = errors.New("phase service: approval gate has already been decided")
)

// Scheduler runs work asynchronously. Inject AsyncScheduler in production
// (real goroutine) and SyncScheduler in tests (synchronous, deterministic).
type Scheduler func(work func())

// AsyncScheduler runs work in a new goroutine. Production default.
func AsyncScheduler(work func()) { go work() }

// SyncScheduler runs work in the calling goroutine. For tests.
func SyncScheduler(work func()) { work() }

// ServiceConfig parameterizes the Phase service.
type ServiceConfig struct {
	// EventsURLTemplate produces the SSE URL for a phase, with one %s
	// placeholder for the phase_id. The path must match the route registered
	// in internal/adapters/inbound/http/router.go for the SSE handler;
	// router_test.go contains a wire-contract test that prevents drift.
	EventsURLTemplate string

	// DispatchTimeoutMS is the per-phase dispatch timeout. Default 600s (10min).
	DispatchTimeoutMS int
}

// DefaultServiceConfig returns production defaults.
func DefaultServiceConfig() ServiceConfig {
	return ServiceConfig{
		EventsURLTemplate: "/api/v1/phases/%s/events",
		DispatchTimeoutMS: 600_000,
	}
}

// Deps bundles the Phase service's dependencies. Easier to construct than a
// 14-arg New() function.
type Deps struct {
	ChangeRepo  outbound.ChangeRepository
	PhaseRepo   outbound.PhaseRepository
	SessionRepo outbound.SessionRepository
	Governance  outbound.GovernanceClient
	Memory      outbound.MemoryClient
	Dispatcher  outbound.AgentDispatcher
	SpawnGov    SpawnGovernor
	Validator   *discipline.Validator
	IronLaw     *discipline.IronLawChecker
	Prompts     *discipline.PromptBuilder
	Audit       outbound.AuditLog
	Events      inbound.EventStream
	Clock       shared.Clock
	IDGen       shared.IDGenerator
	Scheduler   Scheduler
	Config      ServiceConfig

	// ApplyExecutor handles apply-phase coordination (parallel team-leads
	// + implements + Iron Law #5 escalation). When non-nil and Phase.Type
	// == apply, the Service delegates to it instead of running the
	// single-agent flow. nil ⇒ apply phases run as single-agent (V1
	// fallback).
	ApplyExecutor ApplyExecutor

	// Metrics is the optional Prometheus instrument set. When nil, all
	// metric record calls are no-ops.
	Metrics *obs.Metrics
}

// ApplyExecutor is the contract phase.Service uses to delegate apply-phase
// coordination. internal/application/apply.RunService implements it.
type ApplyExecutor interface {
	Execute(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) (*envelope.Envelope, error)
}

// SpawnGovernor is the minimal contract from discipline.SpawnGovernor used
// by Phase service. Declared here so tests can substitute fakes.
type SpawnGovernor interface {
	Acquire(ctx context.Context) error
	Release(ctx context.Context) error
}

// Service implements inbound.PhaseService.
type Service struct {
	d Deps
}

// New constructs a Service. All Deps fields are required (panic on nil).
func New(d Deps) *Service {
	if d.ChangeRepo == nil || d.PhaseRepo == nil || d.SessionRepo == nil ||
		d.Governance == nil || d.Memory == nil || d.Dispatcher == nil ||
		d.SpawnGov == nil || d.Validator == nil || d.IronLaw == nil ||
		d.Prompts == nil || d.Audit == nil || d.Events == nil ||
		d.Clock == nil || d.IDGen == nil || d.Scheduler == nil {
		panic("phase.Service: nil dependency")
	}
	if d.Config.EventsURLTemplate == "" {
		d.Config = DefaultServiceConfig()
	}
	if d.Config.DispatchTimeoutMS == 0 {
		d.Config.DispatchTimeoutMS = 600_000
	}
	return &Service{d: d}
}

// Get returns the Phase identified by id.
func (s *Service) Get(ctx context.Context, id ids.PhaseID) (*phase.Phase, error) {
	p, err := s.d.PhaseRepo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find phase: %w", err)
	}
	return p, nil
}

// Run executes the 16-step single-agent phase flow (spec § 4). The
// synchronous prep work (steps 1-9) runs inline; steps 10-16 run in a
// goroutine scheduled via the injected Scheduler. Returns 202-style
// RunPhaseOutput as soon as the Phase row is persisted (step 9 boundary).
func (s *Service) Run(ctx context.Context, in inbound.RunPhaseInput) (*inbound.RunPhaseOutput, error) {
	// Step 2: Validate change exists.
	c, err := s.d.ChangeRepo.FindByID(ctx, in.ChangeID)
	if err != nil {
		return nil, fmt.Errorf("find change: %w", err)
	}

	// Step 2 (cont): Validate phase_type is next-valid.
	if !s.isNextValidTransition(c.CurrentPhase(), in.PhaseType) {
		return nil, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, c.CurrentPhase(), in.PhaseType)
	}

	// Step 2 (cont): Mutex via Postgres advisory lock + running-phase check.
	if err := s.d.PhaseRepo.LockByChange(ctx, in.ChangeID); err != nil {
		return nil, fmt.Errorf("lock change: %w", err)
	}
	running, err := s.d.PhaseRepo.FindRunningByChange(ctx, in.ChangeID)
	if err != nil && !errors.Is(err, outbound.ErrNotFound) {
		return nil, fmt.Errorf("check running: %w", err)
	}
	if running != nil {
		return nil, fmt.Errorf("%w: %s", ErrPhaseRunning, running.Type())
	}

	// Step 3: Create Phase row pending — persist BEFORE goroutine.
	pid, err := ids.ParsePhaseID(s.d.IDGen.NewID())
	if err != nil {
		return nil, fmt.Errorf("generate phase id: %w", err)
	}
	budget := in.RetryBudget
	if budget <= 0 {
		budget = 3
	}
	p, err := phase.New(pid, in.ChangeID, in.PhaseType, budget)
	if err != nil {
		return nil, err //nolint:wrapcheck // domain sentinel
	}
	if err := p.Start(s.d.Clock.Now()); err != nil {
		return nil, err //nolint:wrapcheck // domain sentinel
	}
	if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
		return nil, fmt.Errorf("save phase: %w", err)
	}
	s.recordPhaseStarted(p)

	// Audit + event: phase.started (sophia-wire-v1 §5.3).
	s.appendAudit(ctx, &in.ChangeID, &pid, nil, "phase.started", nil)
	s.publishEvent(ctx, p.ID(), contract.EventPhaseStarted, inbound.PhaseStartedPayload{
		PhaseID:   p.ID().String(),
		PhaseType: string(in.PhaseType),
		ChangeID:  in.ChangeID.String(),
		StartedAt: s.d.Clock.Now().UTC(),
	})

	// Step 9: Schedule async work (steps 10-16).
	output := &inbound.RunPhaseOutput{
		PhaseID:   pid,
		Status:    p.Status(),
		EventsURL: fmt.Sprintf(s.d.Config.EventsURLTemplate, pid),
		StartedAt: s.d.Clock.Now().Format(time.RFC3339),
	}
	bgCtx := traceBackground(ctx)
	s.d.Scheduler(func() {
		// Detach from request ctx so cancellation doesn't kill the work,
		// but propagate the request's Trace so log lines and persisted
		// events keep trace_id correlation. Timeouts come from
		// DispatchTimeoutMS.
		s.runAsync(bgCtx, c, p, in)
	})
	return output, nil
}

// runAsync executes steps 10-16 of the phase flow.
func (s *Service) runAsync(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) {
	// Step 4: governance.
	decision, err := s.d.Governance.EvaluatePhase(ctx, outbound.EvaluatePhaseInput{
		ChangeID:        c.ID(),
		PhaseType:       p.Type(),
		TaskDescription: in.TaskDescription,
	})
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("governance error: %v", err))
		return
	}
	s.publishEvent(ctx, p.ID(), inbound.EventGovernanceDecision, inbound.GovernanceDecisionPayload{
		Decision:  string(decision.Decision),
		Reason:    decision.Reason,
		AgentRole: decision.AgentRole,
	})

	// Step 5: branch on decision.
	switch decision.Decision {
	case outbound.DecisionDeny:
		s.failPhase(ctx, p, "governance denied: "+decision.Reason)
		return
	case outbound.DecisionRequireApproval:
		// V1: pause. Caller must call Approve to resume; the phase row stays
		// at running (Resume will continue from here). Per sophia-wire-v1
		// §5.3 + §8 (approval flow): emit `approval.required` with phase_id,
		// gate_url, reason, plus risk/policy when the governance decision
		// surfaces them (Optional per Phase 1.5 amendment).
		approvalPayload := inbound.ApprovalRequiredPayload{
			PhaseID: p.ID().String(),
			GateURL: s.approvalURL(decision),
			Reason:  decision.Reason,
		}
		s.publishEvent(ctx, p.ID(), contract.EventApprovalRequired, approvalPayload)
		return
	}

	// Apply phase delegates to the parallel coordination ApplyExecutor.
	// Iron Laws + envelope persistence still happen here so the contract
	// stays uniform across phase types.
	if p.Type() == phase.PhaseApply && s.d.ApplyExecutor != nil {
		s.runApplyPhase(ctx, c, p, in)
		return
	}

	// Step 6: Iron Law pre-flight.
	prior, err := s.loadPriorPhases(ctx, c.ID())
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("load prior phases: %v", err))
		return
	}
	violations := s.d.IronLaw.Check(discipline.Context{
		Action:                actionForPhase(p.Type()),
		DesiredPhase:          p.Type(),
		PriorPhases:           prior,
		HasGovernanceDecision: true,
		TaskAttempts:          p.Attempts() - 1,
	})
	if len(violations) > 0 {
		s.failPhase(ctx, p, fmt.Sprintf("iron law violations: %d", len(violations)))
		return
	}

	// Step 8: build prompt.
	priorCtx := s.buildPriorContext(ctx, c)
	prompt, err := s.d.Prompts.Build(discipline.PromptInput{
		Phase:           p.Type(),
		ChangeName:      c.Name(),
		Project:         c.Project(),
		PriorContext:    priorCtx,
		TaskDescription: in.TaskDescription,
	})
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("prompt build: %v", err))
		return
	}

	// Step 7 (post-prompt): create AgentSession.
	sid, err := ids.ParseSessionID(s.d.IDGen.NewID())
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("generate session id: %v", err))
		return
	}
	promptHash := hashPrompt(prompt)
	sess, err := session.New(sid, c.ID(), p.ID(), roleFor(p.Type()), s.d.Dispatcher.Provider(),
		promptHash, "opencode run", s.d.Clock.Now())
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("new session: %v", err))
		return
	}
	if err := sess.MarkRunning(); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("mark session running: %v", err))
		return
	}
	if err := s.d.SessionRepo.Save(ctx, sess); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("save session: %v", err))
		return
	}
	s.publishEvent(ctx, p.ID(), contract.EventAgentDispatched, inbound.AgentDispatchedPayload{
		PhaseID:   p.ID().String(),
		SessionID: sid.String(),
		Role:      string(roleFor(p.Type())),
		Provider:  string(s.d.Dispatcher.Provider()),
	})

	// Step 9-10: spawn governor acquire + dispatch.
	if err := s.d.SpawnGov.Acquire(ctx); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("spawn governor: %v", err))
		return
	}
	result, dispatchErr := s.d.Dispatcher.Dispatch(ctx, outbound.DispatchRequest{
		Prompt:       prompt,
		WorktreePath: ".",
		TimeoutMS:    s.d.Config.DispatchTimeoutMS,
		EnvelopeOut:  "stdout-fenced-json",
		PhaseType:    string(p.Type()),
	})
	_ = s.d.SpawnGov.Release(ctx)
	if dispatchErr != nil {
		_ = sess.RecordOutcome(nil, -1, s.d.Clock.Now())
		_ = s.d.SessionRepo.Save(ctx, sess)
		s.failPhase(ctx, p, fmt.Sprintf("dispatch: %v", dispatchErr))
		return
	}

	// Step 11: parse + envelope fallback.
	envRaw := result.EnvelopeRaw
	if len(envRaw) == 0 {
		envRaw = s.fallbackToMemory(ctx, c, p)
	}

	// Step 12: validate envelope.
	env, err := s.d.Validator.Validate(envRaw, p.Type())
	if err != nil {
		_ = sess.RecordOutcome(nil, result.ExitCode, s.d.Clock.Now())
		_ = s.d.SessionRepo.Save(ctx, sess)
		s.failPhase(ctx, p, fmt.Sprintf("envelope validation: %v", err))
		return
	}
	_ = sess.RecordOutcome(env, result.ExitCode, s.d.Clock.Now())
	_ = s.d.SessionRepo.Save(ctx, sess)
	s.publishEvent(ctx, p.ID(), inbound.EventAgentEnvelopeReceived, inbound.AgentEnvelopeReceivedPayload{
		Status:     string(env.Status),
		Confidence: env.Confidence,
	})

	// Step 13: complete phase + persist (Iron Law #1: persisted-before-return).
	if err := p.Complete(env, s.d.Clock.Now()); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("phase complete: %v", err))
		return
	}
	if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("save phase: %v", err))
		return
	}
	s.recordPhaseTerminal(p, env)
	s.recordPhaseEnded(p)

	// Step 14: advance Change.CurrentPhase if DONE.
	if p.Status() == phase.PhaseStatusDone {
		s.advanceChange(ctx, c, p.Type())
	}

	// Step 15-16: audit + emit terminal lifecycle event per
	// sophia-wire-v1 §5.3. Payload carries phase_id + phase_type +
	// ended_at + confidence per the spec; envelope_status is
	// retained as a Forward-compat extra field (clients ignore unknown
	// fields per §10).
	cidLocal := c.ID()
	pidLocal := p.ID()
	eventType := eventTypeForStatus(p.Status())
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventType, env)
	payload := inbound.PhaseCompletedPayload{
		PhaseID:            p.ID().String(),
		PhaseType:          string(p.Type()),
		EndedAt:            s.d.Clock.Now().UTC(),
		Confidence:         env.Confidence,
		EnvelopeStatus:     string(env.Status),
		EnvelopeConfidence: env.Confidence,
	}
	s.publishEvent(ctx, p.ID(), eventType, payload)
}

// runApplyPhase delegates apply-phase coordination to the injected
// ApplyExecutor (apply.RunService). Iron Law #1 (persisted-before-return),
// Change.CurrentPhase advance, audit, and SSE event emission all stay in
// phase.Service so the contract is uniform across phase types.
func (s *Service) runApplyPhase(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) {
	env, err := s.d.ApplyExecutor.Execute(ctx, c, p, in)
	if err != nil && env == nil {
		s.failPhase(ctx, p, fmt.Sprintf("apply executor: %v", err))
		return
	}
	if env == nil {
		s.failPhase(ctx, p, "apply executor returned nil envelope")
		return
	}

	if err := p.Complete(env, s.d.Clock.Now()); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("phase complete: %v", err))
		return
	}
	if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("save phase: %v", err))
		return
	}
	s.recordPhaseTerminal(p, env)
	s.recordPhaseEnded(p)

	if p.Status() == phase.PhaseStatusDone {
		s.advanceChange(ctx, c, p.Type())
	}

	cidLocal := c.ID()
	pidLocal := p.ID()
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventTypeForStatus(p.Status()), env)
	s.publishEvent(ctx, p.ID(), eventTypeForStatus(p.Status()), inbound.PhaseCompletedFromApplyPayload{
		EnvelopeStatus:     string(env.Status),
		EnvelopeConfidence: env.Confidence,
	})
}

// Resume re-launches an interrupted phase. V1: validates the phase is in
// running or interrupted status and reschedules runAsync. The retry budget
// is preserved.
func (s *Service) Resume(ctx context.Context, id ids.PhaseID) (*inbound.RunPhaseOutput, error) {
	p, err := s.d.PhaseRepo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find phase: %w", err)
	}
	if p.Status().IsTerminal() {
		return nil, fmt.Errorf("%w: %s", ErrAlreadyTerminal, p.Status())
	}
	c, err := s.d.ChangeRepo.FindByID(ctx, p.ChangeID())
	if err != nil {
		return nil, fmt.Errorf("find change: %w", err)
	}

	// Mark interrupted phases as running again to enable Complete.
	if p.Status() == phase.PhaseStatusInterrupted {
		if err := p.Start(s.d.Clock.Now()); err != nil {
			return nil, err //nolint:wrapcheck
		}
		if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
			return nil, fmt.Errorf("save phase: %w", err)
		}
	}

	output := &inbound.RunPhaseOutput{
		PhaseID:   p.ID(),
		Status:    p.Status(),
		EventsURL: fmt.Sprintf(s.d.Config.EventsURLTemplate, p.ID()),
		StartedAt: s.d.Clock.Now().Format(time.RFC3339),
	}
	bgCtx := traceBackground(ctx)
	s.d.Scheduler(func() {
		s.runAsync(bgCtx, c, p, inbound.RunPhaseInput{
			ChangeID:    c.ID(),
			PhaseType:   p.Type(),
			RetryBudget: p.RetryBudget(),
		})
	})
	return output, nil
}

// Approve records an approval and emits a phase.approved event. V1 does
// NOT auto-resume the dispatch — the caller must invoke Resume separately.
// V1.1 may collapse Approve+Resume into one step.
func (s *Service) Approve(ctx context.Context, id ids.PhaseID, approver, reason string) error {
	if approver == "" {
		return ErrApproverRequired
	}
	p, err := s.d.PhaseRepo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("find phase: %w", err)
	}
	if p.Status().IsTerminal() {
		return fmt.Errorf("%w: %s", ErrAlreadyTerminal, p.Status())
	}
	if err := s.checkGateState(ctx, id); err != nil {
		return err
	}
	// sophia-wire-v1 §5.3 + §8: audit + SSE event share the
	// approval.resolved name so the audit log is the single source of
	// truth for gate-state checks (gate_already_decided).
	s.appendAudit(ctx, nil, &id, nil, contract.EventApprovalResolved, map[string]any{
		"decision": contract.DecisionApproved,
		"approver": approver,
		"reason":   reason,
	})
	s.publishEvent(ctx, id, contract.EventApprovalResolved, inbound.ApprovalResolvedPayload{
		PhaseID:   id.String(),
		Decision:  contract.DecisionApproved,
		Approver:  approver,
		Reason:    reason,
		DecidedAt: s.d.Clock.Now().UTC(),
	})
	return nil
}

// Reject marks a phase as BLOCKED via a synthetic envelope, persists it,
// and emits phase.rejected.
func (s *Service) Reject(ctx context.Context, id ids.PhaseID, approver, reason string) error {
	if approver == "" {
		return ErrApproverRequired
	}
	p, err := s.d.PhaseRepo.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("find phase: %w", err)
	}
	if p.Status().IsTerminal() {
		return fmt.Errorf("%w: %s", ErrAlreadyTerminal, p.Status())
	}
	if err := s.checkGateState(ctx, id); err != nil {
		return err
	}
	c, err := s.d.ChangeRepo.FindByID(ctx, p.ChangeID())
	if err != nil {
		return fmt.Errorf("find change: %w", err)
	}
	syntheticEnv := &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(p.Type()),
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           envelope.StatusBlocked,
		Confidence:       0,
		ExecutiveSummary: "Rejected by " + approver + ": " + reason,
	}
	if p.Status() != phase.PhaseStatusRunning {
		// Need to be in running status to Complete. Rare but possible.
		if err := p.Start(s.d.Clock.Now()); err != nil {
			return err //nolint:wrapcheck
		}
	}
	if err := p.Complete(syntheticEnv, s.d.Clock.Now()); err != nil {
		return err //nolint:wrapcheck
	}
	if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
		return fmt.Errorf("save phase: %w", err)
	}
	s.appendAudit(ctx, nil, &id, nil, contract.EventApprovalResolved, map[string]any{
		"decision": contract.DecisionRejected,
		"approver": approver,
		"reason":   reason,
	})
	// Symmetric to Approve: single `approval.resolved` event with
	// decision="rejected".
	s.publishEvent(ctx, id, contract.EventApprovalResolved, inbound.ApprovalResolvedPayload{
		PhaseID:   id.String(),
		Decision:  contract.DecisionRejected,
		Approver:  approver,
		Reason:    reason,
		DecidedAt: s.d.Clock.Now().UTC(),
	})
	return nil
}

// --- helpers ---

// failPhase persists a synthetic BLOCKED envelope and emits phase.failed.
func (s *Service) failPhase(ctx context.Context, p *phase.Phase, reason string) {
	c, err := s.d.ChangeRepo.FindByID(ctx, p.ChangeID())
	if err != nil {
		return
	}
	env := &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            string(p.Type()),
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           envelope.StatusBlocked,
		Confidence:       0,
		ExecutiveSummary: reason,
	}
	if p.Status() != phase.PhaseStatusRunning {
		_ = p.Start(s.d.Clock.Now())
	}
	if err := p.Complete(env, s.d.Clock.Now()); err == nil {
		_ = s.d.PhaseRepo.Save(ctx, p)
	}
	s.recordPhaseTerminal(p, env)
	s.recordPhaseEnded(p)
	if strings.Contains(reason, "iron law") || strings.Contains(reason, "Iron Law") {
		s.recordIronLawViolation("IL_DETECTED")
	}
	pidLocal := p.ID()
	cidLocal := c.ID()
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, contract.EventPhaseFailed, map[string]any{"reason": reason})
	// sophia-wire-v1 §5.3: phase.failed payload carries phase_id +
	// phase_type + ended_at + error.
	s.publishEvent(ctx, p.ID(), contract.EventPhaseFailed, inbound.PhaseFailedPayload{
		PhaseID:   p.ID().String(),
		PhaseType: string(p.Type()),
		EndedAt:   s.d.Clock.Now().UTC(),
		Error:     reason,
	})
}

func (s *Service) loadPriorPhases(ctx context.Context, changeID ids.ChangeID) (map[phase.PhaseType]discipline.PhasePredicate, error) {
	out := map[phase.PhaseType]discipline.PhasePredicate{}
	for _, pt := range phase.AllPhaseTypes() {
		p, err := s.d.PhaseRepo.FindByChangeAndType(ctx, changeID, pt)
		if err != nil {
			if errors.Is(err, outbound.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out[pt] = discipline.PhasePredicate{
			Status:     p.Status(),
			Confidence: p.Confidence(),
		}
	}
	return out, nil
}

func (s *Service) buildPriorContext(ctx context.Context, c *change.Change) string {
	bundle, err := s.d.Memory.BuildContext(ctx, outbound.ContextRequest{
		Scope: outbound.MemoryScope{
			ProjectID: c.Project(),
			AgentID:   "sophia-orchestator",
			SessionID: c.ID().String(),
		},
		MaxTokens: 4000,
	})
	if err != nil || bundle == nil {
		return ""
	}
	var sb strings.Builder
	for _, sec := range bundle.Sections {
		for _, rec := range sec.Records {
			sb.WriteString(rec.Content)
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

func (s *Service) fallbackToMemory(ctx context.Context, c *change.Change, p *phase.Phase) []byte {
	topic := fmt.Sprintf("sdd/%s/%s", c.Name(), p.Type())
	rec, err := s.d.Memory.Get(ctx, topic)
	if err != nil || rec == nil {
		return nil
	}
	// Memory record content is opaque; if it happens to be the envelope
	// JSON, the validator will accept it. Otherwise validation fails and
	// the phase fails downstream.
	return []byte("")
}

func (s *Service) advanceChange(ctx context.Context, c *change.Change, completed phase.PhaseType) {
	// Advance CurrentPhase pointer to the just-completed phase. The next
	// orchestrator call validates that any new phase is in
	// completed.NextValid().
	if err := c.AdvancePhase(completed, s.d.Clock.Now()); err == nil {
		_ = s.d.ChangeRepo.Save(ctx, c)
	}
	// Archive is terminal — once it completes, mark the Change Completed.
	if completed == phase.PhaseArchive {
		if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
			_ = s.d.ChangeRepo.Save(ctx, c)
		}
	}
}

// checkGateState enforces sophia-wire-v1 §9.2 codes phase_not_gated /
// gate_already_decided. The audit log is the source of truth: a phase is
// "gated" iff at least one approval.required event has been recorded for
// it; the gate is "already decided" iff at least one approval.resolved
// event has been recorded. Audit-log query failures are NOT silently
// swallowed — they surface as errors so the handler returns 500 rather
// than a misleading 409.
func (s *Service) checkGateState(ctx context.Context, id ids.PhaseID) error {
	gated, err := s.d.Audit.HasEventForPhase(ctx, id, contract.EventApprovalRequired)
	if err != nil {
		return fmt.Errorf("audit lookup approval.required: %w", err)
	}
	if !gated {
		return ErrPhaseNotGated
	}
	decided, err := s.d.Audit.HasEventForPhase(ctx, id, contract.EventApprovalResolved)
	if err != nil {
		return fmt.Errorf("audit lookup approval.resolved: %w", err)
	}
	if decided {
		return ErrGateAlreadyDecided
	}
	return nil
}

func (s *Service) appendAudit(ctx context.Context, cid *ids.ChangeID, pid *ids.PhaseID, sid *ids.SessionID, eventType string, payload any) {
	var raw []byte
	if payload != nil {
		raw, _ = jsonMarshal(payload)
	}
	_ = s.d.Audit.Append(ctx, outbound.AuditEvent{
		ChangeID:   cid,
		PhaseID:    pid,
		SessionID:  sid,
		EventType:  eventType,
		Payload:    raw,
		OccurredAt: s.d.Clock.Now(),
	})
}

// publishEvent emits an SSE event with the given typed payload.
// payload should be one of the typed structs from
// internal/ports/inbound/event_payloads.go (e.g. PhaseStartedPayload)
// so the producer gets compile-time validation of field names.
// map[string]any is still accepted for tests and gradual migration.
//
// ctx must carry the request's Trace (via trace.NewContext) so the persisted
// Event row keeps trace_id correlation with the originating HTTP request.
// The async goroutine path propagates the parent Trace via traceBackground.
func (s *Service) publishEvent(ctx context.Context, pid ids.PhaseID, eventType string, payload any) {
	var traceID string
	if t, ok := trace.FromContext(ctx); ok {
		traceID = t.TraceID
	}
	_ = s.d.Events.Publish(ctx, pid, inbound.Event{
		Type:      eventType,
		Timestamp: s.d.Clock.Now(),
		Payload:   payload,
		TraceID:   traceID,
	})
}

// traceBackground returns a fresh background context that carries the
// Trace from src (if any). Used when detaching long-running work from
// the request context: cancellation no longer propagates, but the
// trace_id stays correlated for logs and event persistence.
func traceBackground(src context.Context) context.Context {
	bg := context.Background()
	if t, ok := trace.FromContext(src); ok {
		return trace.NewContext(bg, t)
	}
	return bg
}

func (s *Service) approvalURL(d *outbound.GovernanceDecision) string {
	if d.Approval != nil {
		return d.Approval.URL
	}
	return ""
}

func (s *Service) isNextValidTransition(current, next phase.PhaseType) bool {
	if current == next {
		return true // re-running an in-progress phase type is allowed (idempotent retry)
	}
	return slices.Contains(current.NextValid(), next)
}

func roleFor(pt phase.PhaseType) session.AgentRole {
	switch pt {
	case phase.PhaseInit:
		return session.RoleSDDInit
	case phase.PhaseExplore:
		return session.RoleSDDExplore
	case phase.PhaseProposal:
		return session.RoleSDDProposal
	case phase.PhaseSpec:
		return session.RoleSDDSpec
	case phase.PhaseDesign:
		return session.RoleSDDDesign
	case phase.PhaseTasks:
		return session.RoleSDDTasks
	case phase.PhaseVerify:
		return session.RoleSDDVerify
	case phase.PhaseArchive:
		return session.RoleSDDArchive
	case phase.PhaseApply:
		return session.RoleTeamLead // apply phase uses team-lead for the orchestrating agent
	default:
		return session.RoleSDDExplore
	}
}

func actionForPhase(pt phase.PhaseType) discipline.Action {
	switch pt {
	case phase.PhaseApply:
		return discipline.ActionRunApply
	case phase.PhaseArchive:
		return discipline.ActionRunArchive
	default:
		return discipline.ActionStartPhase
	}
}

func eventTypeForStatus(s phase.PhaseStatus) string {
	switch s {
	case phase.PhaseStatusDone:
		return contract.EventPhaseCompleted
	case phase.PhaseStatusDoneWithConcerns:
		return contract.EventPhaseCompletedWithConcerns
	case phase.PhaseStatusBlocked:
		return contract.EventPhaseFailed
	case phase.PhaseStatusNeedsContext:
		return contract.EventPhaseNeedsContext
	default:
		return contract.EventPhaseCompleted
	}
}

func hashPrompt(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

