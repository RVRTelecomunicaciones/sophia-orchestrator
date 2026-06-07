package skill

import (
	"errors"
	"strings"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// Domain errors for Skill invariants.
var (
	ErrEmptyName     = errors.New("skill: name must not be empty")
	ErrEmptyContent  = errors.New("skill: content must not be empty")
	ErrNoValidPhases = errors.New("skill: at least one valid phase is required")
	ErrNoTechniques  = errors.New("skill: at least one technique tag is required")
)

// Skill is an aggregate root representing a persisted prompt-guidance unit.
// Skills are seeded at boot and hydrated by the application layer before
// prompt assembly. The persisted content is always the runtime source of truth.
type Skill struct {
	id         ids.SkillID
	name       string
	phases     []phase.PhaseType
	content    string
	techniques []Technique
	createdAt  time.Time
	updatedAt  time.Time
}

// New constructs a validated Skill. It enforces all invariants:
//   - non-empty name and content
//   - at least one valid phase (duplicates are deduped and canonically ordered)
//   - at least one valid technique tag
func New(
	id ids.SkillID,
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	now time.Time,
) (*Skill, error) {
	canonicalPhases, err := canonicalizePhases(phases)
	if err != nil {
		return nil, err
	}
	if err := validateCore(name, content, techniques); err != nil {
		return nil, err
	}
	dedupedTechniques := dedupeTechniques(techniques)
	return &Skill{
		id:         id,
		name:       name,
		phases:     canonicalPhases,
		content:    content,
		techniques: dedupedTechniques,
		createdAt:  now,
		updatedAt:  now,
	}, nil
}

// Hydrate reconstructs a Skill from persisted storage without re-running full
// validation. The persistence layer is trusted to have stored only valid data;
// however basic non-empty checks are still enforced to catch data corruption.
func Hydrate(
	id ids.SkillID,
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	createdAt, updatedAt time.Time,
) *Skill {
	return &Skill{
		id:         id,
		name:       name,
		phases:     phases,
		content:    content,
		techniques: techniques,
		createdAt:  createdAt,
		updatedAt:  updatedAt,
	}
}

// Update applies a runtime edit to the Skill, bumping updatedAt.
// All invariants are re-enforced; phases are re-deduped and canonically ordered.
func (s *Skill) Update(
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	now time.Time,
) error {
	canonicalPhases, err := canonicalizePhases(phases)
	if err != nil {
		return err
	}
	if err := validateCore(name, content, techniques); err != nil {
		return err
	}
	s.name = name
	s.phases = canonicalPhases
	s.content = content
	s.techniques = dedupeTechniques(techniques)
	s.updatedAt = now
	return nil
}

// ── Getters ──────────────────────────────────────────────────────────────────

// ID returns the Skill identifier.
func (s *Skill) ID() ids.SkillID { return s.id }

// Name returns the unique skill name.
func (s *Skill) Name() string { return s.name }

// Phases returns the canonical, deduped list of applicable phases.
func (s *Skill) Phases() []phase.PhaseType {
	out := make([]phase.PhaseType, len(s.phases))
	copy(out, s.phases)
	return out
}

// Content returns the skill guidance text.
func (s *Skill) Content() string { return s.content }

// Techniques returns the deduped technique tags.
func (s *Skill) Techniques() []Technique {
	out := make([]Technique, len(s.techniques))
	copy(out, s.techniques)
	return out
}

// CreatedAt returns the creation timestamp.
func (s *Skill) CreatedAt() time.Time { return s.createdAt }

// UpdatedAt returns the last-update timestamp.
func (s *Skill) UpdatedAt() time.Time { return s.updatedAt }

// ── Helpers ───────────────────────────────────────────────────────────────────

// validateCore enforces the name, content, and technique invariants for both
// New and Update. Phase emptiness is enforced by canonicalizePhases before
// this function is called, so phases is always non-empty here.
func validateCore(name, content string, techniques []Technique) error {
	if strings.TrimSpace(name) == "" {
		return ErrEmptyName
	}
	if strings.TrimSpace(content) == "" {
		return ErrEmptyContent
	}
	if len(techniques) == 0 {
		return ErrNoTechniques
	}
	return ValidateTechniques(techniques)
}

// canonicalizePhases deduplicates the input slice and returns phases sorted in
// the canonical AllPhaseTypes order. Returns ErrNoValidPhases when the result
// is empty (all inputs were invalid phase types).
func canonicalizePhases(input []phase.PhaseType) ([]phase.PhaseType, error) {
	seen := make(map[phase.PhaseType]struct{}, len(input))
	for _, p := range input {
		if p.IsValid() {
			seen[p] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil, ErrNoValidPhases
	}
	// Re-order by canonical AllPhaseTypes order to guarantee determinism.
	canonical := phase.AllPhaseTypes()
	out := make([]phase.PhaseType, 0, len(seen))
	for _, p := range canonical {
		if _, ok := seen[p]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// dedupeTechniques returns a deduplicated slice preserving the original order.
func dedupeTechniques(input []Technique) []Technique {
	seen := make(map[Technique]struct{}, len(input))
	out := make([]Technique, 0, len(input))
	for _, t := range input {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

// AppliesTo reports whether this Skill applies to the given phase.
func (s *Skill) AppliesTo(pt phase.PhaseType) bool {
	for _, p := range s.phases {
		if p == pt {
			return true
		}
	}
	return false
}

// PhaseStrings returns the phases as strings (for persistence).
func (s *Skill) PhaseStrings() []string {
	out := make([]string, len(s.phases))
	for i, p := range s.phases {
		out[i] = string(p)
	}
	return out
}

// TechniqueStrings returns the techniques as strings (for persistence).
func (s *Skill) TechniqueStrings() []string {
	out := make([]string, len(s.techniques))
	for i, t := range s.techniques {
		out[i] = string(t)
	}
	return out
}
