// Package skill provides the application service for skill lifecycle mutations.
// It implements the inbound.SkillService port, enforcing domain invariants and
// delegating persistence to outbound repositories.
package skill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ErrForbiddenStatusTransition is returned when a caller requests a status
// transition that violates domain invariants (e.g., candidate→archived direct skip).
var ErrForbiddenStatusTransition = errors.New("skill: forbidden status transition")

// allowedTransitions defines the only valid direct transitions per V4.1 §5.2.
// Any (from, to) pair not present is forbidden. Key = current status; value = allowed targets.
var allowedTransitions = map[skill.Status]map[skill.Status]bool{
	skill.StatusCandidate:  {skill.StatusValidated: true, skill.StatusBlocked: true},
	skill.StatusValidated:  {skill.StatusActive: true, skill.StatusBlocked: true},
	skill.StatusActive:     {skill.StatusDeprecated: true, skill.StatusBlocked: true},
	skill.StatusDeprecated: {skill.StatusBlocked: true},
	skill.StatusBlocked:    {skill.StatusCandidate: true},
	skill.StatusArchived:   {},
}

// Service implements inbound.SkillService.
type Service struct {
	skillRepo      outbound.SkillRepository
	skillUsageRepo outbound.SkillUsageRepository
	clock          shared.Clock
}

// New constructs a Service.
func New(skillRepo outbound.SkillRepository, skillUsageRepo outbound.SkillUsageRepository, clock shared.Clock) *Service {
	return &Service{
		skillRepo:      skillRepo,
		skillUsageRepo: skillUsageRepo,
		clock:          clock,
	}
}

// PatchMetrics applies additive deltas to a skill's metrics and updates last_used_at.
// Returns outbound.ErrNotFound when the skill does not exist.
func (s *Service) PatchMetrics(ctx context.Context, skillID string, delta inbound.MetricsDelta) error {
	id, err := ids.ParseSkillID(skillID)
	if err != nil {
		return fmt.Errorf("skill.PatchMetrics: %w", outbound.ErrNotFound)
	}

	m := skill.Metrics{
		SuccessCount:      delta.SuccessDelta,
		FailureCount:      delta.FailureDelta,
		TestsPassedCount:  delta.TestsPassedDelta,
		RollbackCount:     delta.RollbackDelta,
		DeprecatedAPIHits: delta.DeprecatedAPIHits,
		UsageCount:        delta.UsageDelta,
		AvgRetryReduction: delta.AvgRetryReduction,
	}

	return s.skillRepo.PatchMetrics(ctx, id, m, s.clock.Now())
}

// PatchStatus transitions the skill's lifecycle status.
// Validates the enum and enforces allowed transitions.
// Returns outbound.ErrNotFound when the skill does not exist.
// Returns ErrForbiddenStatusTransition when the transition violates domain invariants.
func (s *Service) PatchStatus(ctx context.Context, skillID string, status, _ string) error {
	id, err := ids.ParseSkillID(skillID)
	if err != nil {
		return fmt.Errorf("skill.PatchStatus: %w", outbound.ErrNotFound)
	}

	newStatus := skill.Status(status)
	if !newStatus.IsValid() {
		return fmt.Errorf("skill.PatchStatus: invalid status %q", status)
	}

	// Load current skill to check transition.
	current, err := s.skillRepo.FindByID(ctx, id)
	if err != nil {
		return err
	}

	allowed, ok := allowedTransitions[current.Status()]
	if !ok || !allowed[newStatus] {
		return fmt.Errorf("%w: %s→%s", ErrForbiddenStatusTransition, current.Status(), newStatus)
	}

	return s.skillRepo.PatchStatus(ctx, id, newStatus, s.clock.Now())
}

// GetUsage returns all skill_usage rows for the given change_id, enriching each
// with the real per-change apply_attempts (D-LH-2): SUM(tasks.attempts) for the
// change's apply tasks, applied identically to every row of that change. Tasks
// carry no skill_id, so per-change is the finest honest granularity.
func (s *Service) GetUsage(ctx context.Context, changeID string) ([]inbound.SkillUsageRow, error) {
	cid, err := ids.ParseChangeID(changeID)
	if err != nil {
		return nil, fmt.Errorf("skill.GetUsage: invalid change_id: %w", err)
	}

	rows, err := s.skillUsageRepo.FindByChange(ctx, cid)
	if err != nil {
		return nil, err
	}

	applyAttempts, err := s.skillUsageRepo.SumApplyAttemptsByChange(ctx, cid)
	if err != nil {
		return nil, err
	}

	out := make([]inbound.SkillUsageRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, inbound.SkillUsageRow{
			SkillUsage:    r,
			ApplyAttempts: applyAttempts,
		})
	}
	return out, nil
}

// GetUsageBySkill returns all skill_usage rows for the given skill_id, enriching
// each row with the real per-change apply_attempts (SUM(tasks.attempts)) for that
// row's own change_id. Because a skill spans multiple changes, the per-change sum
// is computed once per distinct change and reused for every row of that change.
// Mirrors GetUsage's enrichment semantics (D-LH-2) on the skill_id filter path
// (spec skill-usage-tracking "Filter by skill_id").
func (s *Service) GetUsageBySkill(ctx context.Context, skillID string) ([]inbound.SkillUsageRow, error) {
	sid, err := ids.ParseSkillID(skillID)
	if err != nil {
		return nil, fmt.Errorf("skill.GetUsageBySkill: invalid skill_id: %w", err)
	}

	rows, err := s.skillUsageRepo.FindBySkill(ctx, sid)
	if err != nil {
		return nil, err
	}

	attemptsByChange := make(map[string]int, len(rows))
	out := make([]inbound.SkillUsageRow, 0, len(rows))
	for _, r := range rows {
		cid := r.ChangeID()
		key := cid.String()
		attempts, ok := attemptsByChange[key]
		if !ok {
			attempts, err = s.skillUsageRepo.SumApplyAttemptsByChange(ctx, cid)
			if err != nil {
				return nil, err
			}
			attemptsByChange[key] = attempts
		}
		out = append(out, inbound.SkillUsageRow{
			SkillUsage:    r,
			ApplyAttempts: attempts,
		})
	}
	return out, nil
}

// GetSkill returns the current skill snapshot for GET /api/v1/skills/{id}.
// Returns outbound.ErrNotFound (wrapped) when the skill_id is unknown or
// cannot be parsed.
func (s *Service) GetSkill(ctx context.Context, skillID string) (*inbound.GetSkillResult, error) {
	id, err := ids.ParseSkillID(skillID)
	if err != nil {
		return nil, fmt.Errorf("skill.GetSkill: %w", outbound.ErrNotFound)
	}

	sk, err := s.skillRepo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}

	scopeMap, err := structToMap(sk.Scope())
	if err != nil {
		return nil, fmt.Errorf("skill.GetSkill: marshal scope: %w", err)
	}

	awMap, err := structToMap(sk.AppliesWhen())
	if err != nil {
		return nil, fmt.Errorf("skill.GetSkill: marshal applies_when: %w", err)
	}

	m := sk.Metrics()
	return &inbound.GetSkillResult{
		SkillID:     sk.ID().String(),
		Status:      sk.Status().String(),
		RiskLevel:   sk.RiskLevel().String(),
		Version:     sk.Version(),
		Name:        sk.Name(),
		Scope:       scopeMap,
		AppliesWhen: awMap,
		Metrics: inbound.SkillMetricsResult{
			UsageCount:        m.UsageCount,
			SuccessCount:      m.SuccessCount,
			FailureCount:      m.FailureCount,
			TestsPassedCount:  m.TestsPassedCount,
			DeprecatedAPIHits: m.DeprecatedAPIHits,
			RollbackCount:     m.RollbackCount,
			AvgRetryReduction: m.AvgRetryReduction,
		},
	}, nil
}

// structToMap JSON-marshals v and unmarshals into map[string]any, providing
// a clean any-typed representation of JSONB-tagged struct fields.
func structToMap(v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("structToMap: marshal: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("structToMap: unmarshal: %w", err)
	}
	return m, nil
}

// Verify Service satisfies the SkillService port at compile time.
var _ inbound.SkillService = (*Service)(nil)
