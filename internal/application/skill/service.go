// Package skill provides the application service for skill lifecycle mutations.
// It implements the inbound.SkillService port, enforcing domain invariants and
// delegating persistence to outbound repositories.
package skill

import (
	"context"
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

// GetUsage returns all skill_usage rows for the given change_id with apply_attempts.
// Currently ApplyAttempts is set to 0; the M4+ pass will enrich from the phase envelope.
func (s *Service) GetUsage(ctx context.Context, changeID string) ([]inbound.SkillUsageRow, error) {
	cid, err := ids.ParseChangeID(changeID)
	if err != nil {
		return nil, fmt.Errorf("skill.GetUsage: invalid change_id: %w", err)
	}

	rows, err := s.skillUsageRepo.FindByChange(ctx, cid)
	if err != nil {
		return nil, err
	}

	out := make([]inbound.SkillUsageRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, inbound.SkillUsageRow{
			SkillUsage:    r,
			ApplyAttempts: 0, // M4+: enrich from phase envelope
		})
	}
	return out, nil
}

// Verify Service satisfies the SkillService port at compile time.
var _ inbound.SkillService = (*Service)(nil)
