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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/trace"
	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	initdetector "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	skdomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
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

	// MemoryTenantID is the tenant_id stamped on artifacts persisted to
	// memory-engine via persistArtifactsToMemory. Empty means "no
	// tenant" and is fine for single-tenant deployments where the
	// memory-engine API key is also tenantless. In multi-tenant
	// deployments the operator MUST set this so the auth scope on the
	// API key matches what the orch sends — otherwise memory-engine
	// returns HTTP 403 forbidden (cross-tenant write attempt).
	// Wired from SOPHIA_MEMORY_TENANT_ID at boot.
	MemoryTenantID string
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

	// Skills is the optional SkillProvider used to hydrate phase prompts
	// with runtime skill-guidance units. nil or a no-op provider is
	// safe: the prompt is left unchanged (byte-identical to pre-change
	// baseline). When SOPHIA_SKILLS_ENABLED=false or the provider errors,
	// the service passes nil Skills to PromptBuilder (fail-soft).
	Skills discipline.SkillProvider

	// SkillUsageRepo is the optional repository for recording skill injection
	// events (migration 011). nil means "no tracking" and is safe: every
	// callsite is nil-tolerant. Wired by bootstrap when skills are enabled.
	SkillUsageRepo outbound.SkillUsageRepository

	// Metrics is the optional Prometheus instrument set. When nil, all
	// metric record calls are no-ops.
	Metrics *obs.Metrics

	// Init is the InitService that handles PhaseInit execution. When non-nil
	// and Phase.Type == init, the Service invokes runInitPhase instead of the
	// standard governance/IronLaw/dispatch flow. nil ⇒ PhaseInit falls through
	// to the standard single-agent flow (should not happen in production).
	// Design: D-INIT-3 (branch at TOP of runAsync).
	Init InitService
}

// ApplyExecutor is the contract phase.Service uses to delegate apply-phase
// coordination. internal/application/apply.RunService implements it.
type ApplyExecutor interface {
	Execute(ctx context.Context, c *change.Change, p *phase.Phase, in inbound.RunPhaseInput) (*envelope.Envelope, error)
}

// InitRunInput carries the data InitService.Run needs from phase.Service.
// Wrapping *change.Change avoids a circular import; the concrete InitService
// implementation in internal/application/init/ can accept this directly.
type InitRunInput = *change.Change

// InitService is the contract phase.Service uses to delegate INIT phase
// execution. internal/application/init.Service implements it.
// The INIT branch fires at the TOP of runAsync BEFORE governance/IronLaw/dispatch
// (design D-INIT-3). persistArtifactsToMemory is skipped for INIT because
// InitService.Run persists internally (design D-INIT-3).
type InitService interface {
	// Run executes the INIT phase for change c. Returns the StructuralContext,
	// the phase envelope, and any hard error. Spawner degraded-mode is NOT a
	// hard error; it is absorbed into sc.DegradedReason.
	Run(ctx context.Context, c InitRunInput) (initdetector.StructuralContext, *envelope.Envelope, error)
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
	//
	// Spec #49: for retry idempotency, look up the most-recent prior phase for
	// this (change_id, phase_type). When one exists and is terminal, start the
	// new attempt at prior.Attempts() so Start() makes it N+1. On a first run
	// FindByChangeAndType returns ErrNotFound, and phase.New starts at 0 → Start
	// makes it 1.
	pid, err := ids.ParsePhaseID(s.d.IDGen.NewID())
	if err != nil {
		return nil, fmt.Errorf("generate phase id: %w", err)
	}
	budget := in.RetryBudget
	if budget <= 0 {
		budget = 3
	}
	priorAttempts := 0
	// BUG-24: include PhaseStatusNeedsContext in the "retry bumps
	// attempts" set. The domain contract (internal/domain/phase/status.go)
	// documents NeedsContext as non-terminal-but-retryable-within-budget;
	// when the operator re-POSTs Run on a needs_context row we MUST
	// land on a new (change_id, phase_type, attempts) tuple so the
	// upsert lands on a fresh row instead of clobbering the prior one
	// in place (which would leave the API-returned phase_id unwritten
	// and surface as phase_not_found on the very next poll).
	if prior, priorErr := s.d.PhaseRepo.FindByChangeAndType(ctx, in.ChangeID, in.PhaseType); priorErr == nil &&
		(prior.Status().IsTerminal() || prior.Status() == phase.PhaseStatusNeedsContext) {
		priorAttempts = prior.Attempts()
	}
	// Hydrate with priorAttempts so Start() increments to priorAttempts+1.
	// This makes retry N produce attempts=N+1 in the upsert key.
	p := phase.Hydrate(pid, in.ChangeID, in.PhaseType,
		phase.PhaseStatusPending, nil, 0, budget, priorAttempts, nil, nil)
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
	// INIT branch: short-circuits BEFORE governance/IronLaw/prompt/dispatch.
	// Design D-INIT-3: PhaseInit uses deterministic FS detection instead of
	// an LLM agent; skips governance, IronLaw, session, and dispatch entirely.
	// persistArtifactsToMemory is also skipped (InitService.Run persists
	// internally — design D-INIT-3).
	if p.Type() == phase.PhaseInit && s.d.Init != nil {
		s.runInitPhase(ctx, c, p)
		return
	}

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
		// Spec #67 (BUG-21): the approval gate check in Approve/Reject
		// reads `approval.required` from the AUDIT log
		// (checkGateState → s.d.Audit.HasEventForPhase). publishEvent
		// only writes to the SSE phase_events table, so without an
		// explicit audit append the gate is invisible to the resolver
		// and Approve fails with ErrPhaseNotGated even though SSE
		// clients saw the event. Append BEFORE publish so the audit
		// row is durable before any caller can race to Approve.
		cidLocal := c.ID()
		pidLocal := p.ID()
		s.appendAudit(ctx, &cidLocal, &pidLocal, nil, contract.EventApprovalRequired, approvalPayload)
		s.publishEvent(ctx, p.ID(), contract.EventApprovalRequired, approvalPayload)
		return
	}

	// Apply phase delegates to the parallel coordination ApplyExecutor.
	// Iron Laws + envelope persistence still happen here so the contract
	// stays uniform across phase types. Spec #51: pre-load the prior-
	// phase status snapshot and stuff it into the input so the apply
	// executor can pass it down to each implement-agent prompt.
	if p.Type() == phase.PhaseApply && s.d.ApplyExecutor != nil {
		if prior, err := s.loadPriorPhases(ctx, c.ID()); err == nil {
			in.PriorPhasesStatus = phasesPredicateToStatusMap(prior)
		}
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

	// Step 8: build prompt. Parse tests_required from ContextOverrides so
	// the apply TDD hard-gate (Spec #46) is conditional on the change's scope.
	// Spec #51: pass the orchestrator-verified prior-phase status map so
	// the LLM sees factual evidence instead of having to search for it.
	testsRequired := parseScopeTestsRequired(in.ContextOverrides)
	priorCtx := s.buildPriorContext(ctx, c)

	// Hydrate skills fail-soft: if provider is nil, flag off, returns empty,
	// or errors → pass nil Skills so the prompt is unchanged (byte-identical).
	var phaseSkills []*skdomain.Skill
	if s.d.Skills != nil {
		if sk, skErr := s.d.Skills.SkillsForPhase(ctx, p.Type()); skErr == nil && len(sk) > 0 {
			phaseSkills = sk
		}
		// skErr != nil or empty slice → phaseSkills stays nil (fail-soft)
	}

	// Record skill_usage rows at injection time (D-M2-2). Fail-soft: repo
	// errors are logged and swallowed so the phase is never blocked by
	// tracking infra failures.
	var skillUsageIDs []ids.SkillUsageID
	if s.d.SkillUsageRepo != nil && len(phaseSkills) > 0 {
		now := s.d.Clock.Now()
		for _, sk := range phaseSkills {
			usageIDStr := s.d.IDGen.NewID()
			usageID, parseErr := ids.ParseSkillUsageID(usageIDStr)
			if parseErr != nil {
				continue
			}
			su := newSkillUsage(usageID, c.ID(), p.Type(), sk.ID(), sk.Version(), now)
			if insertErr := s.d.SkillUsageRepo.Insert(ctx, su); insertErr != nil {
				slog.Default().WarnContext(ctx, "skill_usage insert failed; continuing",
					"skill_id", sk.ID().String(), "error", insertErr)
				continue
			}
			skillUsageIDs = append(skillUsageIDs, usageID)
		}
	}

	prompt, err := s.d.Prompts.Build(discipline.PromptInput{
		Phase:             p.Type(),
		ChangeName:        c.Name(),
		Project:           c.Project(),
		PriorContext:      priorCtx,
		TaskDescription:   in.TaskDescription,
		TestsRequired:     testsRequired,
		PriorPhasesStatus: phasesPredicateToStatusMap(prior),
		Skills:            phaseSkills,
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

	// Step 12a: Spec #45 — for PhaseTasks, reject envelopes that carry a
	// flat top-level "tasks" array instead of the required "data.groups[]"
	// shape. This catches schema drift early and returns a named rule id
	// ("schema_mismatch") that clients can act on programmatically.
	if p.Type() == phase.PhaseTasks {
		if mismatch := detectTasksSchemaMismatch(env.Data); mismatch != "" {
			_ = sess.RecordOutcome(env, result.ExitCode, s.d.Clock.Now())
			_ = s.d.SessionRepo.Save(ctx, sess)
			s.failPhaseWithReason(ctx, p, sess, "schema_mismatch", mismatch)
			return
		}
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

	// Update skill_usage outcomes now that the phase has a terminal status.
	s.updateSkillUsageOutcomes(ctx, skillUsageIDs, env.Status)

	// Step 13b: persist envelope.ArtifactsSaved entries to memory-engine
	// so downstream phases can read them via MemoryClient.GetByTopicKey
	// or BuildContext. The LLM declares the artifacts; the orch carries
	// out the actual write — opencode/ollama/aider don't run an MCP
	// memory tool today, so this bridge closes the orch ↔ memory loop.
	//
	// Fail-soft: a memory-engine failure emits memory.artifact_persist_
	// failed but does NOT fail the phase — the envelope is already
	// persisted on the orch side (Iron Law #1).
	s.persistArtifactsToMemory(ctx, c, p, env)

	// Step 14: advance Change.CurrentPhase when finishing in a status
	// that allows progression. DONE and DONE_WITH_CONCERNS both qualify
	// (see PhaseStatus.AdvanceAllowed doc). BLOCKED never advances.
	if p.Status().AdvanceAllowed() {
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

	// Apply-phase advance: same rule as the single-agent flow above —
	// DONE and DONE_WITH_CONCERNS both progress the change.
	if p.Status().AdvanceAllowed() {
		s.advanceChange(ctx, c, p.Type())
	}

	cidLocal := c.ID()
	pidLocal := p.ID()
	eventType := eventTypeForStatus(p.Status())
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventType, env)

	// Spec #51 — when apply terminates BLOCKED emit the enriched
	// PhaseFailedPayload (same shape as the single-agent failure path)
	// so operators see the actual reason via SSE instead of the slim
	// {envelope_status, envelope_confidence} pair. Non-blocked
	// terminations keep the slim payload to preserve the existing
	// contract for clients that don't need the extra fields.
	if env.Status == envelope.StatusBlocked {
		s.publishEvent(ctx, p.ID(), eventType,
			buildApplyFailedPayload(p.ID(), p.Type(), s.d.Clock.Now().UTC(), env))
		return
	}
	s.publishEvent(ctx, p.ID(), eventType, inbound.PhaseCompletedFromApplyPayload{
		EnvelopeStatus:     string(env.Status),
		EnvelopeConfidence: env.Confidence,
	})
}

// runInitPhase handles the INIT phase execution path. It mirrors Steps 13-16
// of runAsync (envelope persist + advance + audit + event) but SKIPS governance,
// IronLaw, prompt, session, dispatch, and persistArtifactsToMemory.
//
// Iron Law D1.2 ordering:
//  1. InitService.Run → (sc, env, err)  [compute + artifact persist inside]
//  2. Validator.Validate(envBytes)        [schema check]
//  3. p.Complete(env, clock.Now())        [in-memory]
//  4. PhaseRepo.Save                      [PHASE durable — after artifact]
//  5. advanceChange                       [CHANGE durable]
//  6. appendAudit + publishEvent          [SSE]
func (s *Service) runInitPhase(ctx context.Context, c *change.Change, p *phase.Phase) {
	// Step 1: run InitService (structural detection + dual persist inside).
	_, env, err := s.d.Init.Run(ctx, c)
	if err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("init service: %v", err))
		return
	}
	if env == nil {
		s.failPhase(ctx, p, "init service returned nil envelope")
		return
	}

	// Step 2: validate envelope (uniformity check — same as runAsync).
	envBytes, marshalErr := jsonMarshal(env)
	if marshalErr != nil {
		s.failPhase(ctx, p, fmt.Sprintf("init: marshal envelope: %v", marshalErr))
		return
	}
	validatedEnv, valErr := s.d.Validator.Validate(envBytes, p.Type())
	if valErr != nil {
		s.failPhase(ctx, p, fmt.Sprintf("init: envelope validation: %v", valErr))
		return
	}

	// Steps 3-4: complete phase + persist (Iron Law D1.2).
	if err := p.Complete(validatedEnv, s.d.Clock.Now()); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("init: phase complete: %v", err))
		return
	}
	if err := s.d.PhaseRepo.Save(ctx, p); err != nil {
		s.failPhase(ctx, p, fmt.Sprintf("init: save phase: %v", err))
		return
	}
	s.recordPhaseTerminal(p, validatedEnv)
	s.recordPhaseEnded(p)

	// Step 5: advance Change.CurrentPhase.
	if p.Status().AdvanceAllowed() {
		s.advanceChange(ctx, c, p.Type())
	}

	// Step 6: audit + SSE event.
	cidLocal := c.ID()
	pidLocal := p.ID()
	eventType := eventTypeForStatus(p.Status())
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, eventType, validatedEnv)
	payload := inbound.PhaseCompletedPayload{
		PhaseID:            p.ID().String(),
		PhaseType:          string(p.Type()),
		EndedAt:            s.d.Clock.Now().UTC(),
		Confidence:         validatedEnv.Confidence,
		EnvelopeStatus:     string(validatedEnv.Status),
		EnvelopeConfidence: validatedEnv.Confidence,
	}
	s.publishEvent(ctx, p.ID(), eventType, payload)
}

// Resume re-launches an interrupted phase. V1: validates the phase is in
// running or interrupted status and reschedules runAsync. The retry budget
// is preserved.
//
// BUG-28 extension: blocked apply phases ARE resumable. Apply is the only
// terminal status that earns this exception because the underlying board
// + worktrees + per-task statuses are all preserved across retries — the
// re-Execute reuses the existing board, skips done groups/tasks, and only
// reattempts the ones that previously failed. Other terminal statuses
// (done / done_with_concerns) stay non-resumable: those phases produced
// an accepted envelope and replaying them is semantically wrong. Blocked
// non-apply phases also stay non-resumable today — the retry semantics
// for spec/proposal/etc. live in the existing Service.Run path (BUG-24).
func (s *Service) Resume(ctx context.Context, id ids.PhaseID) (*inbound.RunPhaseOutput, error) {
	p, err := s.d.PhaseRepo.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("find phase: %w", err)
	}
	if p.Status().IsTerminal() {
		// BUG-28: blocked apply phases are the only resumable terminal.
		if p.Status() != phase.PhaseStatusBlocked || p.Type() != phase.PhaseApply {
			return nil, fmt.Errorf("%w: %s", ErrAlreadyTerminal, p.Status())
		}
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

	// BUG-28: blocked apply transitions back to running via Restart, which
	// consumes the retry budget. Apply.Execute then reuses the existing
	// board for this phase_id (no board re-creation) and the run loops
	// skip groups/tasks already at Completed/Done.
	if p.Status() == phase.PhaseStatusBlocked && p.Type() == phase.PhaseApply {
		if err := p.Restart(s.d.Clock.Now()); err != nil {
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
// Spec #48: if the phase has a recorded session (sess != nil), extract
// failure_reason and failure_detail from its envelope before emitting.
func (s *Service) failPhase(ctx context.Context, p *phase.Phase, reason string) {
	s.failPhaseWithReason(ctx, p, nil, "", reason)
}

// failPhaseWithReason is the enriched variant of failPhase. ruleID and detail
// are forwarded into PhaseFailedPayload.FailureReason / .FailureDetail (Spec #48).
// When ruleID is empty the method looks up the latest session envelope for the
// phase and extracts rule_id / message from it; if nothing is found, FailureReason
// defaults to "unknown".
func (s *Service) failPhaseWithReason(ctx context.Context, p *phase.Phase, _ any /* reserved */, ruleID, reason string) {
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
	s.appendAudit(ctx, &cidLocal, &pidLocal, nil, contract.EventPhaseFailed, map[string]any{
		"reason":         reason,
		"failure_reason": ruleID,
	})

	// Spec #48: populate failure_reason / failure_detail from the session
	// envelope when a ruleID was not supplied by the caller.
	failureReason, failureDetail := ruleID, ""
	if failureReason == "" {
		failureReason, failureDetail = s.extractFailureReasonFromSession(ctx, p.ID(), reason)
	} else {
		failureDetail = reason
	}

	// sophia-wire-v1 §5.3: phase.failed payload carries phase_id +
	// phase_type + ended_at + error + failure_reason + failure_detail.
	s.publishEvent(ctx, p.ID(), contract.EventPhaseFailed, inbound.PhaseFailedPayload{
		PhaseID:       p.ID().String(),
		PhaseType:     string(p.Type()),
		EndedAt:       s.d.Clock.Now().UTC(),
		Error:         reason,
		FailureReason: failureReason,
		FailureDetail: failureDetail,
	})
}

// extractFailureReasonFromSession inspects the latest session for the given
// phase and returns (rule_id, message) extracted from the agent envelope's
// data block. Falls back to ("unknown", executiveSummary) when nothing
// can be extracted (Spec #48 fallback contract).
func (s *Service) extractFailureReasonFromSession(ctx context.Context, phaseID ids.PhaseID, fallbackReason string) (ruleID, detail string) {
	sessions, err := s.d.SessionRepo.FindByPhaseID(ctx, phaseID)
	if err != nil || len(sessions) == 0 {
		return "unknown", fallbackReason
	}
	// Use the last session — it represents the most recent dispatch attempt.
	latest := sessions[len(sessions)-1]
	env := latest.Envelope()
	if env == nil {
		return "unknown", fallbackReason
	}

	// Try to extract rule_id + message from envelope.data.
	if len(env.Data) > 0 {
		var data struct {
			RuleID  string `json:"rule_id"`
			Message string `json:"message"`
		}
		if jsonErr := json.Unmarshal(env.Data, &data); jsonErr == nil {
			if data.RuleID != "" {
				msg := data.Message
				if msg == "" {
					msg = env.ExecutiveSummary
				}
				return data.RuleID, msg
			}
		}
	}

	// Fall back to executive_summary as the detail.
	detail = env.ExecutiveSummary
	if detail == "" {
		detail = fallbackReason
	}
	return "unknown", detail
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
			// Tenant binding mirrors persistArtifactsToMemory — without
			// it the auth scope check filters out everything the API
			// key actually owns.
			TenantID: s.d.Config.MemoryTenantID,
			// AgentID + SessionID are intentionally omitted from the
			// BuildContext scope: heuristics and decisions are
			// project-wide, not session-bound. memory-engine's
			// retrieval filter narrows on whatever scope fields are
			// present, so passing session_id excludes any record not
			// tagged with that exact session — which is true for ALL
			// heuristics and decisions (they're created out-of-band
			// of any single change). Pass only the project (+ tenant)
			// so the BuildContext returns the project-wide knowledge
			// the LLM should reason from.
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
	pc := discipline.PriorContext{RawMemoryBlob: sb.String()}
	return pc.Render(discipline.RenderOpts{})
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
	// Archive is terminal — once it completes, mark the Change Completed and
	// emit the phase.archived event (Iron Law D1.2: envelope already persisted
	// upstream by runPhaseCompletion; terminal state durable after Save below).
	if completed == phase.PhaseArchive {
		if err := c.MarkCompleted(s.d.Clock.Now()); err == nil {
			if saveErr := s.d.ChangeRepo.Save(ctx, c); saveErr == nil {
				// Resolve the archive phase ID via the phase repo so we can
				// correlate the SSE event with the phase row. Failure to look
				// up is non-fatal — emit with a zero PhaseID rather than
				// dropping the event entirely.
				var archivePhaseID ids.PhaseID
				if p, lookupErr := s.d.PhaseRepo.FindByChangeAndType(ctx, c.ID(), phase.PhaseArchive); lookupErr == nil {
					archivePhaseID = p.ID()
				}
				s.publishEvent(ctx, archivePhaseID, inbound.EventPhaseArchived, inbound.PhaseArchivedPayload{
					ChangeID:   c.ID().String(),
					ChangeName: c.Name(),
					PhaseType:  string(phase.PhaseArchive),
					ArchivedAt: s.d.Clock.Now(),
				})
			}
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

// phasesPredicateToStatusMap projects a loadPriorPhases result into the
// stringified status map consumed by discipline.PromptInput.PriorPhasesStatus.
// Returns nil when no prior phases exist (init / explore) so the prompt
// builder skips rendering the snapshot block entirely. Spec #51.
func phasesPredicateToStatusMap(prior map[phase.PhaseType]discipline.PhasePredicate) map[phase.PhaseType]string {
	if len(prior) == 0 {
		return nil
	}
	out := make(map[phase.PhaseType]string, len(prior))
	for pt, pp := range prior {
		out[pt] = string(pp.Status)
	}
	return out
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

// parseScopeTestsRequired reads ContextOverrides["scope"]["tests_required"]
// and returns the boolean value. Any missing key or type mismatch defaults
// to false so non-TDD changes are never inadvertently gated (Spec #46).
func parseScopeTestsRequired(overrides map[string]any) bool {
	if overrides == nil {
		return false
	}
	scopeRaw, ok := overrides["scope"]
	if !ok {
		return false
	}
	scope, ok := scopeRaw.(map[string]any)
	if !ok {
		return false
	}
	v, ok := scope["tests_required"]
	if !ok {
		return false
	}
	b, ok := v.(bool)
	return ok && b
}

// detectTasksSchemaMismatch inspects the raw JSON of an envelope's data block
// and returns a non-empty error string if the payload uses a flat "tasks" array
// instead of the required "groups" shape (Spec #45 / #44). Returns "" when the
// shape is acceptable (including empty data or already correct grouped shape).
func detectTasksSchemaMismatch(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var d map[string]json.RawMessage
	if err := json.Unmarshal(data, &d); err != nil {
		// Can't parse data — not a shape mismatch, let downstream decide.
		return ""
	}
	_, hasGroups := d["groups"]
	_, hasTasks := d["tasks"]
	if hasTasks && !hasGroups {
		return "tasks output must use data.groups[] not a flat tasks array (schema_mismatch)"
	}
	return ""
}

// ── Skill usage helpers ───────────────────────────────────────────────────────

// newSkillUsage constructs a SkillUsage entity for injection tracking (D-M2-2).
func newSkillUsage(
	id ids.SkillUsageID,
	changeID ids.ChangeID,
	phaseType phase.PhaseType,
	skillID ids.SkillID,
	skillVersion string,
	now time.Time,
) *skillusage.SkillUsage {
	return skillusage.New(id, changeID, string(phaseType), skillID, skillVersion, now)
}

// updateSkillUsageOutcomes updates every skill_usage row that was written
// at injection time with the final outcome derived from the envelope status.
// Fail-soft: errors are logged at WARN level and do not surface to callers.
func (s *Service) updateSkillUsageOutcomes(ctx context.Context, ids []ids.SkillUsageID, envStatus envelope.Status) {
	if s.d.SkillUsageRepo == nil || len(ids) == 0 {
		return
	}
	outcome := skillUsageOutcomeFor(envStatus)
	for _, id := range ids {
		if err := s.d.SkillUsageRepo.UpdateOutcome(ctx, id, outcome); err != nil {
			slog.Default().WarnContext(ctx, "skill_usage outcome update failed",
				"skill_usage_id", id.String(), "error", err)
		}
	}
}

// skillUsageOutcomeFor maps an envelope.Status to a skillusage.Outcome.
// done → success; blocked → failure; anything else → failure.
func skillUsageOutcomeFor(s envelope.Status) skillusage.Outcome {
	switch s {
	case envelope.StatusDone, envelope.StatusDoneWithConcerns:
		return skillusage.OutcomeSuccess
	case envelope.StatusBlocked:
		return skillusage.OutcomeBlocked
	default:
		return skillusage.OutcomeFailure
	}
}
