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
//
// M1 adds 9 lifecycle fields (V4.1 §5.2 §7): status, version, scope,
// appliesWhen, riskLevel, activationSource, metrics, lastUsedAt,
// lastValidatedAt. All fields are unexported with public getters.
type Skill struct {
	id         ids.SkillID
	name       string
	phases     []phase.PhaseType
	content    string
	techniques []Technique

	// M1 lifecycle (V4.1 §5.2 §7)
	status           Status
	version          string
	scope            Scope
	appliesWhen      AppliesWhen
	riskLevel        RiskLevel
	activationSource ActivationSource
	metrics          Metrics
	lastUsedAt       *time.Time
	lastValidatedAt  *time.Time

	createdAt time.Time
	updatedAt time.Time
}

// New constructs a validated Skill. It enforces all invariants:
//   - non-empty name and content
//   - at least one valid phase (duplicates are deduped and canonically ordered)
//   - at least one valid technique tag
//   - valid lifecycle enums (status, riskLevel, activationSource)
//   - non-empty version
//
// Zero-value LifecycleInput fields fall back to V4.1 §7 defaults:
//
//	Status=candidate, Version=v1, RiskLevel=medium, ActivationSource=manual.
func New(
	id ids.SkillID,
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	lifecycle LifecycleInput,
	now time.Time,
) (*Skill, error) {
	canonicalPhases, err := canonicalizePhases(phases)
	if err != nil {
		return nil, err
	}
	if err := validateCore(name, content, techniques); err != nil {
		return nil, err
	}
	lc, err := applyLifecycleDefaults(lifecycle)
	if err != nil {
		return nil, err
	}
	dedupedTechniques := dedupeTechniques(techniques)
	return &Skill{
		id:               id,
		name:             name,
		phases:           canonicalPhases,
		content:          content,
		techniques:       dedupedTechniques,
		status:           lc.Status,
		version:          lc.Version,
		scope:            lc.Scope,
		appliesWhen:      lc.AppliesWhen,
		riskLevel:        lc.RiskLevel,
		activationSource: lc.ActivationSource,
		metrics:          lc.Metrics,
		lastUsedAt:       lc.LastUsedAt,
		lastValidatedAt:  lc.LastValidatedAt,
		createdAt:        now,
		updatedAt:        now,
	}, nil
}

// NewLegacy is a convenience constructor for the boot seeder. Equivalent to
// New() with lifecycle = {Status: StatusActive, Version: "v1",
// ActivationSource: SourceLegacySeed, RiskLevel: RiskMedium,
// Scope: {ProjectID: "*", RepoID: "*", Phases: [phases as strings]}}.
// This satisfies D-M1-4 (V4.1 §7 legacy payload).
func NewLegacy(
	id ids.SkillID,
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	now time.Time,
) (*Skill, error) {
	phaseStrs := make([]string, len(phases))
	for i, p := range phases {
		phaseStrs[i] = string(p)
	}
	lc := LifecycleInput{
		Status:           StatusActive,
		Version:          "v1",
		ActivationSource: SourceLegacySeed,
		RiskLevel:        RiskMedium,
		Scope: Scope{
			ProjectID: "*",
			RepoID:    "*",
			Phases:    phaseStrs,
		},
	}
	return New(id, name, phases, content, techniques, lc, now)
}

// Hydrate reconstructs a Skill from persisted storage without re-running full
// validation. The persistence layer is trusted to have stored only valid data;
// lifecycle fields are accepted verbatim without enum re-validation.
func Hydrate(
	id ids.SkillID,
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	status Status,
	version string,
	scope Scope,
	appliesWhen AppliesWhen,
	riskLevel RiskLevel,
	activationSource ActivationSource,
	metrics Metrics,
	lastUsedAt, lastValidatedAt *time.Time,
	createdAt, updatedAt time.Time,
) *Skill {
	return &Skill{
		id:               id,
		name:             name,
		phases:           phases,
		content:          content,
		techniques:       techniques,
		status:           status,
		version:          version,
		scope:            scope,
		appliesWhen:      appliesWhen,
		riskLevel:        riskLevel,
		activationSource: activationSource,
		metrics:          metrics,
		lastUsedAt:       lastUsedAt,
		lastValidatedAt:  lastValidatedAt,
		createdAt:        createdAt,
		updatedAt:        updatedAt,
	}
}

// Update applies a runtime edit to the Skill, bumping updatedAt.
// All core and lifecycle invariants are re-enforced; phases are re-deduped
// and canonically ordered. Zero-value lifecycle fields preserve the aggregate's
// current values (no default override on Update — caller must supply explicit
// values to change a lifecycle field).
func (s *Skill) Update(
	name string,
	phases []phase.PhaseType,
	content string,
	techniques []Technique,
	lifecycle LifecycleInput,
	now time.Time,
) error {
	canonicalPhases, err := canonicalizePhases(phases)
	if err != nil {
		return err
	}
	if err := validateCore(name, content, techniques); err != nil {
		return err
	}
	// Merge lifecycle: zero values preserve existing fields.
	merged := mergeLifecycle(s, lifecycle)
	if err := validateLifecycle(merged); err != nil {
		return err
	}
	s.name = name
	s.phases = canonicalPhases
	s.content = content
	s.techniques = dedupeTechniques(techniques)
	s.status = merged.Status
	s.version = merged.Version
	s.scope = merged.Scope
	s.appliesWhen = merged.AppliesWhen
	s.riskLevel = merged.RiskLevel
	s.activationSource = merged.ActivationSource
	s.metrics = merged.Metrics
	s.lastUsedAt = merged.LastUsedAt
	s.lastValidatedAt = merged.LastValidatedAt
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

// ── M1 lifecycle getters ──────────────────────────────────────────────────────

// Status returns the lifecycle status (V4.1 §5.2).
func (s *Skill) Status() Status { return s.status }

// Version returns the skill version string (e.g. "v1").
func (s *Skill) Version() string { return s.version }

// Scope returns a copy of the scope struct (V4.1 §5.3).
func (s *Skill) Scope() Scope { return s.scope }

// AppliesWhen returns a copy of the applies_when struct (V4.1 §5.3).
func (s *Skill) AppliesWhen() AppliesWhen { return s.appliesWhen }

// RiskLevel returns the risk level (V4.1 §5.2).
func (s *Skill) RiskLevel() RiskLevel { return s.riskLevel }

// ActivationSource returns the activation source (V4.1 §5.2).
func (s *Skill) ActivationSource() ActivationSource { return s.activationSource }

// Metrics returns a copy of the metrics struct (V4.1 §5.4).
func (s *Skill) Metrics() Metrics { return s.metrics }

// LastUsedAt returns a copy of the last-used timestamp, or nil.
func (s *Skill) LastUsedAt() *time.Time {
	if s.lastUsedAt == nil {
		return nil
	}
	t := *s.lastUsedAt
	return &t
}

// LastValidatedAt returns a copy of the last-validated timestamp, or nil.
func (s *Skill) LastValidatedAt() *time.Time {
	if s.lastValidatedAt == nil {
		return nil
	}
	t := *s.lastValidatedAt
	return &t
}

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

// applyLifecycleDefaults applies V4.1 §7 defaults to zero-value LifecycleInput
// fields, then validates all fields.
func applyLifecycleDefaults(lc LifecycleInput) (LifecycleInput, error) {
	if lc.Status == "" {
		lc.Status = StatusCandidate
	}
	if lc.Version == "" {
		lc.Version = "v1"
	}
	if lc.RiskLevel == "" {
		lc.RiskLevel = RiskMedium
	}
	if lc.ActivationSource == "" {
		lc.ActivationSource = SourceManual
	}
	if err := validateLifecycle(lc); err != nil {
		return LifecycleInput{}, err
	}
	return lc, nil
}

// mergeLifecycle produces an updated LifecycleInput where zero-value fields
// in the incoming input are replaced with the aggregate's current values.
func mergeLifecycle(s *Skill, lc LifecycleInput) LifecycleInput {
	if lc.Status == "" {
		lc.Status = s.status
	}
	if lc.Version == "" {
		lc.Version = s.version
	}
	if lc.RiskLevel == "" {
		lc.RiskLevel = s.riskLevel
	}
	if lc.ActivationSource == "" {
		lc.ActivationSource = s.activationSource
	}
	// Struct fields: zero values are indistinguishable from "no change".
	// For M1, Update always carries the full lifecycle or partial updates.
	// Use the incoming value as-is (struct zero means "reset to empty" on
	// scope/applies_when/metrics — Update callers must supply current values
	// when they don't intend a reset). This is consistent with the aggregate
	// not tracking "dirty" state.
	return lc
}

// validateLifecycle validates all lifecycle enum fields. Returns errors wrapping
// the specific sentinel for each invalid field.
func validateLifecycle(lc LifecycleInput) error {
	if !lc.Status.IsValid() {
		return ErrInvalidStatus
	}
	if strings.TrimSpace(lc.Version) == "" {
		return ErrEmptyVersion
	}
	if !lc.RiskLevel.IsValid() {
		return ErrInvalidRiskLevel
	}
	if !lc.ActivationSource.IsValid() {
		return ErrInvalidActivationSource
	}
	return nil
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
