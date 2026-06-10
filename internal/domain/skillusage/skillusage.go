// Package skillusage provides the SkillUsage domain entity that records every
// skill injection into an orchestration phase. It persists into the skill_usage
// table (migration 011) and drives M2 metrics aggregation.
package skillusage

import (
	"errors"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// ErrInvalidOutcome is returned when an unknown outcome value is supplied to
// SetOutcome.
var ErrInvalidOutcome = errors.New("skillusage: invalid outcome")

// Outcome is the closed enum for skill injection outcome per migration 011
// CHECK constraint: 'pending', 'success', 'failure', 'blocked'.
type Outcome string

// Outcome values — must match the SQL CHECK constraint in 011_skill_usage.up.sql.
const (
	OutcomePending Outcome = "pending"
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeBlocked Outcome = "blocked"
)

// IsValid reports whether o is one of the four closed enum values.
func (o Outcome) IsValid() bool {
	switch o {
	case OutcomePending, OutcomeSuccess, OutcomeFailure, OutcomeBlocked:
		return true
	}
	return false
}

// String returns the underlying string value.
func (o Outcome) String() string { return string(o) }

// SkillUsage records a single injection of a skill into a phase of a change.
// All fields are unexported with public getters per project convention.
type SkillUsage struct {
	id           ids.SkillUsageID
	changeID     ids.ChangeID
	phaseType    string
	skillID      ids.SkillID
	skillVersion string
	injectedAt   time.Time
	outcome      Outcome
}

// New constructs a SkillUsage with outcome=pending. Called at injection time.
func New(
	id ids.SkillUsageID,
	changeID ids.ChangeID,
	phaseType string,
	skillID ids.SkillID,
	skillVersion string,
	injectedAt time.Time,
) *SkillUsage {
	return &SkillUsage{
		id:           id,
		changeID:     changeID,
		phaseType:    phaseType,
		skillID:      skillID,
		skillVersion: skillVersion,
		injectedAt:   injectedAt,
		outcome:      OutcomePending,
	}
}

// Hydrate reconstructs a SkillUsage from persisted storage without re-running
// validation. The persistence layer is trusted to have stored only valid data.
func Hydrate(
	id ids.SkillUsageID,
	changeID ids.ChangeID,
	phaseType string,
	skillID ids.SkillID,
	skillVersion string,
	injectedAt time.Time,
	outcome Outcome,
) *SkillUsage {
	return &SkillUsage{
		id:           id,
		changeID:     changeID,
		phaseType:    phaseType,
		skillID:      skillID,
		skillVersion: skillVersion,
		injectedAt:   injectedAt,
		outcome:      outcome,
	}
}

// SetOutcome updates the outcome. Returns ErrInvalidOutcome for unknown values.
func (s *SkillUsage) SetOutcome(o Outcome) error {
	if !o.IsValid() {
		return ErrInvalidOutcome
	}
	s.outcome = o
	return nil
}

// ── Getters ──────────────────────────────────────────────────────────────────

// ID returns the SkillUsage identifier.
func (s *SkillUsage) ID() ids.SkillUsageID { return s.id }

// ChangeID returns the associated change identifier.
func (s *SkillUsage) ChangeID() ids.ChangeID { return s.changeID }

// PhaseType returns the phase type string (e.g. "apply", "verify").
func (s *SkillUsage) PhaseType() string { return s.phaseType }

// SkillID returns the associated skill identifier.
func (s *SkillUsage) SkillID() ids.SkillID { return s.skillID }

// SkillVersion returns the skill version string (e.g. "v1").
func (s *SkillUsage) SkillVersion() string { return s.skillVersion }

// InjectedAt returns the injection timestamp (UTC).
func (s *SkillUsage) InjectedAt() time.Time { return s.injectedAt }

// Outcome returns the current outcome.
func (s *SkillUsage) Outcome() Outcome { return s.outcome }
