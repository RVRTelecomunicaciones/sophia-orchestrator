package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ── GET /api/v1/skills/{skill_id} DTOs ──────────────────────────────────────

// getSkillResp is the JSON shape for GET /api/v1/skills/{skill_id}.
// The narrow fields (skill_id, status, risk_level, version, metrics) mirror the
// ME worker's SkillSnapshot contract verbatim (D-M3-1).
// The additive fields (skill_name, scope, applies_when) extend the response so
// the ME proposer can consume them without a second endpoint (D-M3-2). These
// fields use omitempty so a zero-value struct produces no extra keys.
// Go's default json.Unmarshal ignores unknown keys, so the worker's existing
// GetSkill deserialization is unaffected.
type getSkillResp struct {
	SkillID     string          `json:"skill_id"`
	Status      string          `json:"status"`
	RiskLevel   string          `json:"risk_level"`
	Version     string          `json:"version"`
	Metrics     getSkillMetrics `json:"metrics"`
	// Additive richer fields — NOT in worker SkillSnapshot; consumed by the ME proposer.
	Name        string         `json:"skill_name,omitempty"`
	Scope       map[string]any `json:"scope,omitempty"`
	AppliesWhen map[string]any `json:"applies_when,omitempty"`
}

// getSkillMetrics mirrors the ME worker's SkillMetrics (outbound.SkillMetrics)
// field-for-field. JSON tags must not drift.
type getSkillMetrics struct {
	UsageCount        int     `json:"usage_count"`
	SuccessCount      int     `json:"success_count"`
	FailureCount      int     `json:"failure_count"`
	TestsPassedCount  int     `json:"tests_passed_count"`
	DeprecatedAPIHits int     `json:"deprecated_api_hits"`
	RollbackCount     int     `json:"rollback_count"`
	AvgRetryReduction float64 `json:"avg_retry_reduction"`
}

// toGetSkillResp maps a GetSkillResult into the wire shape.
func toGetSkillResp(r *inbound.GetSkillResult) getSkillResp {
	return getSkillResp{
		SkillID:   r.SkillID,
		Status:    r.Status,
		RiskLevel: r.RiskLevel,
		Version:   r.Version,
		Name:      r.Name,
		Scope:     r.Scope,
		AppliesWhen: r.AppliesWhen,
		Metrics: getSkillMetrics{
			UsageCount:        r.Metrics.UsageCount,
			SuccessCount:      r.Metrics.SuccessCount,
			FailureCount:      r.Metrics.FailureCount,
			TestsPassedCount:  r.Metrics.TestsPassedCount,
			DeprecatedAPIHits: r.Metrics.DeprecatedAPIHits,
			RollbackCount:     r.Metrics.RollbackCount,
			AvgRetryReduction: r.Metrics.AvgRetryReduction,
		},
	}
}

// SkillsHandler exposes the skills write API:
//   - PATCH /api/v1/skills/{id}/metrics
//   - PATCH /api/v1/skills/{id}/status
//   - GET  /api/v1/skills/usage
type SkillsHandler struct {
	svc       inbound.SkillService
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
}

// NewSkillsHandler constructs a SkillsHandler.
func NewSkillsHandler(svc inbound.SkillService, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any)) *SkillsHandler {
	return &SkillsHandler{svc: svc, writeErr: writeErr, writeJSON: writeJSON}
}

// patchMetricsReq is the JSON body for PATCH /api/v1/skills/{id}/metrics.
type patchMetricsReq struct {
	SuccessDelta         int     `json:"success_delta"`
	FailureDelta         int     `json:"failure_delta"`
	TestsPassedDelta     int     `json:"tests_passed_delta"`
	RollbackDelta        int     `json:"rollback_delta"`
	DeprecatedAPIHitsDelta int   `json:"deprecated_api_hits_delta"`
	UsageDelta           int     `json:"usage_delta"`
	AvgRetryReduction    float64 `json:"avg_retry_reduction"`
}

// PatchMetrics handles PATCH /api/v1/skills/{id}/metrics.
func (h *SkillsHandler) PatchMetrics(w http.ResponseWriter, r *http.Request) {
	skillID := chi.URLParam(r, "skill_id")

	var req patchMetricsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	// Negative delta rejection per spec: HTTP 422.
	if req.SuccessDelta < 0 || req.FailureDelta < 0 || req.TestsPassedDelta < 0 ||
		req.RollbackDelta < 0 || req.DeprecatedAPIHitsDelta < 0 || req.UsageDelta < 0 {
		h.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "negative delta values are not allowed",
			"code":  "negative_delta",
		})
		return
	}

	delta := inbound.MetricsDelta{
		SuccessDelta:      req.SuccessDelta,
		FailureDelta:      req.FailureDelta,
		TestsPassedDelta:  req.TestsPassedDelta,
		RollbackDelta:     req.RollbackDelta,
		DeprecatedAPIHits: req.DeprecatedAPIHitsDelta,
		UsageDelta:        req.UsageDelta,
		AvgRetryReduction: req.AvgRetryReduction,
	}

	if err := h.svc.PatchMetrics(r.Context(), skillID, delta); err != nil {
		if errors.Is(err, outbound.ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "skill not found",
				"code":  "skill_not_found",
			})
			return
		}
		h.writeErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// patchStatusReq is the JSON body for PATCH /api/v1/skills/{id}/status.
type patchStatusReq struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// validStatusValues is the closed set of V4.1 §5.2 status values.
var validStatusValues = map[string]bool{
	"candidate":  true,
	"validated":  true,
	"active":     true,
	"deprecated": true,
	"blocked":    true,
	"archived":   true,
}

// PatchStatus handles PATCH /api/v1/skills/{id}/status.
func (h *SkillsHandler) PatchStatus(w http.ResponseWriter, r *http.Request) {
	skillID := chi.URLParam(r, "skill_id")

	var req patchStatusReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	// Enum validation at handler boundary — reject unknown values before service call.
	if !validStatusValues[req.Status] {
		h.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
			"error": "invalid status value",
			"code":  "invalid_status",
		})
		return
	}

	if err := h.svc.PatchStatus(r.Context(), skillID, req.Status, req.Reason); err != nil {
		if errors.Is(err, outbound.ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "skill not found",
				"code":  "skill_not_found",
			})
			return
		}
		if errors.Is(err, skillapp.ErrForbiddenStatusTransition) {
			h.writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": err.Error(),
				"code":  "forbidden_transition",
			})
			return
		}
		h.writeErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// GetSkill handles GET /api/v1/skills/{skill_id}.
// Returns 200 + getSkillResp on success, 404 when the skill is not found.
func (h *SkillsHandler) GetSkill(w http.ResponseWriter, r *http.Request) {
	skillID := chi.URLParam(r, "skill_id")

	result, err := h.svc.GetSkill(r.Context(), skillID)
	if err != nil {
		if errors.Is(err, outbound.ErrNotFound) {
			h.writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "skill not found",
				"code":  "skill_not_found",
			})
			return
		}
		h.writeErr(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, toGetSkillResp(result))
}

// skillUsageRowDTO is the JSON shape for each row in GET /api/v1/skills/usage.
type skillUsageRowDTO struct {
	SkillUsageID string `json:"skill_usage_id"`
	ChangeID     string `json:"change_id"`
	PhaseType    string `json:"phase_type"`
	SkillID      string `json:"skill_id"`
	SkillVersion string `json:"skill_version"`
	Outcome      string `json:"outcome"`
	ApplyAttempts int   `json:"apply_attempts"`
}

// GetUsage handles GET /api/v1/skills/usage?change_id=...
func (h *SkillsHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	changeID := r.URL.Query().Get("change_id")
	if changeID == "" {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "change_id query parameter is required",
			"code":  "missing_change_id",
		})
		return
	}

	rows, err := h.svc.GetUsage(r.Context(), changeID)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	dtos := make([]skillUsageRowDTO, 0, len(rows))
	for _, row := range rows {
		dtos = append(dtos, skillUsageRowDTO{
			SkillUsageID:  row.ID().String(),
			ChangeID:      row.ChangeID().String(),
			PhaseType:     row.PhaseType(),
			SkillID:       row.SkillID().String(),
			SkillVersion:  row.SkillVersion(),
			Outcome:       row.Outcome().String(),
			ApplyAttempts: row.ApplyAttempts,
		})
	}

	h.writeJSON(w, http.StatusOK, map[string]any{"items": dtos})
}
