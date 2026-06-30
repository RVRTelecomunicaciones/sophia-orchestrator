// Package convention declares the ConventionProfile aggregate: an evidence-based
// snapshot of structural conventions extracted from a target repository at INIT
// time. It is a distinct domain concept from Skill — different lifecycle
// (machine-refresh vs human-promote), different schema, and different storage.
package convention

import "errors"

// Domain sentinel errors for ConventionProfile invariants.
var (
	// ErrEmptyProjectID is returned when NewConventionProfile receives an empty
	// projectID string.
	ErrEmptyProjectID = errors.New("convention: projectID must not be empty")

	// ErrEmptyFramework is returned when NewConventionProfile receives an empty
	// framework string.
	ErrEmptyFramework = errors.New("convention: framework must not be empty")

	// ErrEmptyEvidence is returned when a PatternEntry has zero evidence file
	// paths. The never-invent invariant: a pattern without evidence MUST NOT be
	// emitted.
	ErrEmptyEvidence = errors.New("convention: pattern entry must have at least one evidence file path")

	// ErrInvalidConfidence is returned when a PatternEntry carries a Confidence
	// value outside the closed [0.0, 1.0] interval.
	ErrInvalidConfidence = errors.New("convention: confidence must be in [0.0, 1.0]")

	// ErrInvalidSource is returned when a PatternEntry carries a Source value
	// that is not one of the three closed enum values.
	ErrInvalidSource = errors.New("convention: source must be one of curated-skill, detected-from-code, baseline-framework-docs")

	// ErrEmptyPattern is returned when a PatternEntry has an empty or
	// whitespace-only Pattern key.
	ErrEmptyPattern = errors.New("convention: pattern key must not be empty")

	// ErrEmptyRule is returned when a PatternEntry has an empty or
	// whitespace-only Rule string.
	ErrEmptyRule = errors.New("convention: rule must not be empty")
)
