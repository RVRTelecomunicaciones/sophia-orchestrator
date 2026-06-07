// Package skill contains the Skill aggregate root and its invariants.
// Skills are persisted prompt-guidance units injected into SDD phase prompts.
package skill

import "errors"

// ErrInvalidTechnique is returned when a technique tag is not in the allowed set.
var ErrInvalidTechnique = errors.New("skill: invalid technique tag")

// Technique is a closed set of cognitive technique tags allowed on a Skill.
type Technique string

// Allowed technique tags. Tags are authored as string constants so callers
// can reference them without magic strings, while the validation set below
// remains the single source of truth for the closed enum.
const (
	TechniqueConstitutionalSelfCritique Technique = "constitutional-self-critique"
	TechniqueChainOfVerification        Technique = "chain-of-verification"
	TechniqueExtendedThinking           Technique = "extended-thinking"
	TechniqueSkeletonOfThought          Technique = "skeleton-of-thought"
	TechniqueReAct                      Technique = "react"
	TechniqueStepBack                   Technique = "step-back"
	TechniqueInlineWhy                  Technique = "inline-why"
)

// allowedTechniques is the closed set of valid Technique values.
var allowedTechniques = map[Technique]struct{}{
	TechniqueConstitutionalSelfCritique: {},
	TechniqueChainOfVerification:        {},
	TechniqueExtendedThinking:           {},
	TechniqueSkeletonOfThought:          {},
	TechniqueReAct:                      {},
	TechniqueStepBack:                   {},
	TechniqueInlineWhy:                  {},
}

// IsValid reports whether t is a known Technique tag.
func (t Technique) IsValid() bool {
	_, ok := allowedTechniques[t]
	return ok
}

// ValidateTechniques validates a slice of Technique tags and returns the first
// invalid tag as an error. An empty slice is allowed here; the Skill aggregate
// enforces the non-empty constraint separately.
func ValidateTechniques(tags []Technique) error {
	for _, t := range tags {
		if !t.IsValid() {
			return ErrInvalidTechnique
		}
	}
	return nil
}
