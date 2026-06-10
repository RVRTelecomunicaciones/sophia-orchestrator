package inbound

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
)

// MetricsDelta carries the additive delta values for PATCH /api/v1/skills/{id}/metrics.
// All integer fields are additive counters. AvgRetryReduction is a float that
// overwrites the stored value when non-zero. Negative integer values are rejected
// at the handler boundary (HTTP 422).
type MetricsDelta struct {
	SuccessDelta         int     `json:"success_delta"`
	FailureDelta         int     `json:"failure_delta"`
	TestsPassedDelta     int     `json:"tests_passed_delta"`
	RollbackDelta        int     `json:"rollback_delta"`
	DeprecatedAPIHits    int     `json:"deprecated_api_hits_delta"`
	UsageDelta           int     `json:"usage_delta"`
	AvgRetryReduction    float64 `json:"avg_retry_reduction"`
}

// SkillUsageRow extends a SkillUsage domain entity with apply-phase envelope data
// for the GET /api/v1/skills/usage response. ApplyAttempts is sourced from the
// apply phase envelope for the same change_id.
type SkillUsageRow struct {
	*skillusage.SkillUsage
	ApplyAttempts int
}

// SkillMetricsResult carries the metrics snapshot for the GET /api/v1/skills/{id} response.
type SkillMetricsResult struct {
	UsageCount        int
	SuccessCount      int
	FailureCount      int
	TestsPassedCount  int
	DeprecatedAPIHits int
	RollbackCount     int
	AvgRetryReduction float64
}

// GetSkillResult carries the full GET /api/v1/skills/{id} response data.
// The narrow fields (SkillID, Status, RiskLevel, Version, Metrics) mirror the
// ME worker's SkillSnapshot contract. The additive fields (Name, Scope,
// AppliesWhen) are consumed by the ME proposer reconcile (D-M3-2).
type GetSkillResult struct {
	SkillID     string
	Status      string
	RiskLevel   string
	Version     string
	Name        string
	Scope       map[string]any
	AppliesWhen map[string]any
	Metrics     SkillMetricsResult
}

// SkillService is the inbound port for the skills write API.
// Implementations apply mutations atomically and enforce domain invariants.
//
// PatchMetrics applies additive deltas to skill metrics. Returns outbound.ErrNotFound
// when the skill does not exist.
//
// PatchStatus transitions the skill lifecycle status. Returns outbound.ErrNotFound
// when the skill does not exist. Returns a domain error (ErrForbiddenStatusTransition)
// when the transition violates domain invariants (e.g., candidate→archived).
//
// GetUsage returns all skill_usage rows for the given change_id enriched with
// apply_attempts from the apply phase envelope.
//
// GetSkill returns the current skill snapshot for GET /api/v1/skills/{id}.
// Returns outbound.ErrNotFound when the skill does not exist.
type SkillService interface {
	PatchMetrics(ctx context.Context, skillID string, delta MetricsDelta) error
	PatchStatus(ctx context.Context, skillID string, status, reason string) error
	GetUsage(ctx context.Context, changeID string) ([]SkillUsageRow, error)
	GetSkill(ctx context.Context, skillID string) (*GetSkillResult, error)
}
