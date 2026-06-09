package skill

// lifecycle.go declares the V4.1 §5.2 lifecycle types for the Skill aggregate:
//   - Status (6 values)
//   - ActivationSource (5 values)
//   - RiskLevel (4 values)
//   - Scope, AppliesWhen, Metrics value structs with JSON tags
//   - LifecycleInput optional construction payload

import (
	"errors"
	"time"
)

// Domain errors for lifecycle invariants.
var (
	ErrInvalidStatus           = errors.New("skill: invalid status")
	ErrEmptyVersion            = errors.New("skill: version must not be empty")
	ErrInvalidRiskLevel        = errors.New("skill: invalid risk level")
	ErrInvalidActivationSource = errors.New("skill: invalid activation source")
)

// ── Status ────────────────────────────────────────────────────────────────────

// Status is a closed lifecycle enum per V4.1 §5.2 (CORRECTED — 6 values).
type Status string

// Status lifecycle enum values per V4.1 §5.2.
const (
	StatusCandidate  Status = "candidate"
	StatusValidated  Status = "validated"
	StatusActive     Status = "active"
	StatusDeprecated Status = "deprecated"
	StatusBlocked    Status = "blocked"
	StatusArchived   Status = "archived"
)

// IsValid reports whether s is one of the 6 closed enum values.
func (s Status) IsValid() bool {
	switch s {
	case StatusCandidate, StatusValidated, StatusActive,
		StatusDeprecated, StatusBlocked, StatusArchived:
		return true
	}
	return false
}

// String returns the underlying string value.
func (s Status) String() string { return string(s) }

// ── RiskLevel ─────────────────────────────────────────────────────────────────

// RiskLevel is a closed enum per V4.1 §5.2 (4 values).
type RiskLevel string

// RiskLevel enum values per V4.1 §5.2.
const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

// IsValid reports whether r is one of the 4 closed enum values.
func (r RiskLevel) IsValid() bool {
	switch r {
	case RiskLow, RiskMedium, RiskHigh, RiskCritical:
		return true
	}
	return false
}

// String returns the underlying string value.
func (r RiskLevel) String() string { return string(r) }

// ── ActivationSource ──────────────────────────────────────────────────────────

// ActivationSource is a closed enum per V4.1 §5.2 (5 values).
type ActivationSource string

// ActivationSource enum values per V4.1 §5.2.
const (
	SourceManual        ActivationSource = "manual"
	SourceLegacySeed    ActivationSource = "legacy_seed"
	SourceArchiveWorker ActivationSource = "archive_worker"
	SourceLLMProposal   ActivationSource = "llm_proposal"
	SourceImported      ActivationSource = "imported"
)

// IsValid reports whether a is one of the 5 closed enum values.
func (a ActivationSource) IsValid() bool {
	switch a {
	case SourceManual, SourceLegacySeed, SourceArchiveWorker,
		SourceLLMProposal, SourceImported:
		return true
	}
	return false
}

// String returns the underlying string value.
func (a ActivationSource) String() string { return string(a) }

// ── Scope ─────────────────────────────────────────────────────────────────────

// Scope mirrors the JSONB scope column. JSON tags use snake_case for JSONB
// round-trip. M1 uses ProjectID + RepoID + Phases; M3 adds TenantID etc.
type Scope struct {
	ProjectID string   `json:"project_id"`
	RepoID    string   `json:"repo_id"`
	Phases    []string `json:"phases"`
}

// ── AppliesWhen ───────────────────────────────────────────────────────────────

// AppliesWhen mirrors the JSONB applies_when column. M1 uses FeatureType +
// TouchedPaths + ExcludePaths; M3 adds Framework + StateModel.
type AppliesWhen struct {
	FeatureType  []string `json:"feature_type,omitempty"`
	TouchedPaths []string `json:"touched_paths,omitempty"`
	ExcludePaths []string `json:"exclude_paths,omitempty"`
}

// ── Metrics ───────────────────────────────────────────────────────────────────

// Metrics holds promotion-relevant counters per V4.1 §5.4.
type Metrics struct {
	UsageCount        int     `json:"usage_count"`
	SuccessCount      int     `json:"success_count"`
	FailureCount      int     `json:"failure_count"`
	TestsPassedCount  int     `json:"tests_passed_count"`
	DeprecatedAPIHits int     `json:"deprecated_api_hits"`
	RollbackCount     int     `json:"rollback_count"`
	AvgRetryReduction float64 `json:"avg_retry_reduction"`
	LastStackVersion  *string `json:"last_stack_version,omitempty"`
}

// ── LifecycleInput ────────────────────────────────────────────────────────────

// LifecycleInput is the optional lifecycle construction payload for skill.New.
// Zero-value fields fall back to V4.1 §7 defaults:
//
//	Status=candidate, Version=v1, RiskLevel=medium, ActivationSource=manual,
//	Scope/AppliesWhen/Metrics zero-values, LastUsedAt/LastValidatedAt=nil.
type LifecycleInput struct {
	Status           Status
	Version          string
	Scope            Scope
	AppliesWhen      AppliesWhen
	RiskLevel        RiskLevel
	ActivationSource ActivationSource
	Metrics          Metrics
	LastUsedAt       *time.Time
	LastValidatedAt  *time.Time
}
