package http_test

// Group A — GetSkill endpoint (RED tests)
// Tests: A.1 contract test (200 + exact SkillSnapshot shape + additive fields),
//        A.2 unknown id 404, A.3 missing API key 401.
// Production code (handler + service + route) does NOT exist yet — these tests
// reference inbound.SkillService.GetSkill which does not exist, so they MUST fail.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// workerSkillSnapshot is a VENDORED copy of the ME worker's
// sophia-memory-engine/internal/ports/outbound.SkillSnapshot struct (lines 36-42).
// It pins the cross-repo JSON contract in an orch test without importing the ME module.
// Any field rename or type change in SkillSnapshot MUST also be reflected here.
type workerSkillSnapshot struct {
	SkillID   string              `json:"skill_id"`
	Status    string              `json:"status"`
	RiskLevel string              `json:"risk_level"`
	Version   string              `json:"version"`
	Metrics   workerSkillMetrics  `json:"metrics"`
}

type workerSkillMetrics struct {
	UsageCount        int     `json:"usage_count"`
	SuccessCount      int     `json:"success_count"`
	FailureCount      int     `json:"failure_count"`
	TestsPassedCount  int     `json:"tests_passed_count"`
	DeprecatedAPIHits int     `json:"deprecated_api_hits"`
	RollbackCount     int     `json:"rollback_count"`
	AvgRetryReduction float64 `json:"avg_retry_reduction"`
}

// getSkillSvc is a fake SkillService that supports GetSkill for Group A tests.
// It ALSO satisfies the existing methods so it can be used as inbound.SkillService.
type getSkillSvc struct {
	fakeSkillSvc                                // embeds Group D fake (PatchMetrics/PatchStatus/GetUsage)
	getSkillResult *inbound.GetSkillResult
	getSkillErr    error
}

func (f *getSkillSvc) GetSkill(_ context.Context, _ string) (*inbound.GetSkillResult, error) {
	return f.getSkillResult, f.getSkillErr
}

// A.1 — Contract test: GET /api/v1/skills/{id} → 200, response unmarshals into
// vendored workerSkillSnapshot field-for-field (5 top-level + 7 metrics fields).
func TestSkills_GetSkill_200_ContractShape(t *testing.T) {
	svc := &getSkillSvc{
		getSkillResult: &inbound.GetSkillResult{
			SkillID:   testSkillID,
			Status:    "active",
			RiskLevel: "low",
			Version:   "v2",
			Name:      "clean-arch",
			Metrics: inbound.SkillMetricsResult{
				UsageCount:        10,
				SuccessCount:      8,
				FailureCount:      2,
				TestsPassedCount:  5,
				DeprecatedAPIHits: 1,
				RollbackCount:     0,
				AvgRetryReduction: 0.3,
			},
		},
	}
	srv := skillSrv(t, svc, false)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/"+testSkillID, nil)
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// Contract: response must unmarshal into the worker's narrow SkillSnapshot struct.
	var snap workerSkillSnapshot
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&snap), "response must unmarshal into SkillSnapshot")

	// Assert all 5 top-level fields.
	require.Equal(t, testSkillID, snap.SkillID)
	require.Equal(t, "active", snap.Status)
	require.Equal(t, "low", snap.RiskLevel)
	require.Equal(t, "v2", snap.Version)

	// Assert all 7 metrics sub-fields.
	require.Equal(t, 10, snap.Metrics.UsageCount)
	require.Equal(t, 8, snap.Metrics.SuccessCount)
	require.Equal(t, 2, snap.Metrics.FailureCount)
	require.Equal(t, 5, snap.Metrics.TestsPassedCount)
	require.Equal(t, 1, snap.Metrics.DeprecatedAPIHits)
	require.Equal(t, 0, snap.Metrics.RollbackCount)
	require.InDelta(t, 0.3, snap.Metrics.AvgRetryReduction, 0.001)
}

// A.1 triangulation — additive fields (skill_name, scope, applies_when) are
// present in the raw JSON response so the ME proposer can consume them.
func TestSkills_GetSkill_200_AdditiveFields(t *testing.T) {
	svc := &getSkillSvc{
		getSkillResult: &inbound.GetSkillResult{
			SkillID:   testSkillID,
			Status:    "active",
			RiskLevel: "medium",
			Version:   "v1",
			Name:      "clean-arch",
			Scope:     map[string]any{"project_id": "proj-1"},
			AppliesWhen: map[string]any{"feature_type": []any{"backend"}},
			Metrics: inbound.SkillMetricsResult{UsageCount: 5},
		},
	}
	srv := skillSrv(t, svc, false)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/"+testSkillID, nil)
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	require.Equal(t, "clean-arch", body["skill_name"], "additive skill_name must be present")
	require.NotNil(t, body["scope"], "additive scope must be present")
	require.NotNil(t, body["applies_when"], "additive applies_when must be present")
}

// A.2 — Unknown skill id → 404 with error + code fields.
func TestSkills_GetSkill_NotFound_404(t *testing.T) {
	svc := &getSkillSvc{getSkillErr: outbound.ErrNotFound}
	srv := skillSrv(t, svc, false)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/does-not-exist", nil)
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.NotEmpty(t, body["error"], "404 response must have error field")
	require.NotEmpty(t, body["code"], "404 response must have code field")
}

// A.3 — No API key → 401.
func TestSkills_GetSkill_NoAuth_401(t *testing.T) {
	svc := &getSkillSvc{}
	srv := skillSrv(t, svc, true) // rejectAll=true

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/"+testSkillID, nil)
	// No API key header.

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
