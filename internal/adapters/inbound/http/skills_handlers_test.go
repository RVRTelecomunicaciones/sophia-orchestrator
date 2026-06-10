package http_test

// Group D — Skills write API (RED tests)
// Tests: D.1 valid delta PATCH /metrics, D.2 negative delta 422, D.3 missing auth 401,
//        D.4 unknown skill 404, D.5 valid status transition 200, D.6 invalid enum 422 +
//        forbidden skip 422, D.7 GET /skills/usage filtered rows + missing auth 401.
// Production code (handlers/skills.go + router wiring) does NOT exist yet —
// these tests reference the inbound.SkillService interface and httpinbound.Deps.Skills
// field that will be created in the GREEN step.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skillusage"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// errForbiddenTransition is the real sentinel from the application/skill package,
// used by fakeSkillSvc.PatchStatus to simulate a domain-invariant violation.
var errForbiddenTransition = skillapp.ErrForbiddenStatusTransition

// ── Fakes for Group D ─────────────────────────────────────────────────────────

type fakeSkillSvc struct {
	// PatchMetrics control
	patchMetricsErr    error
	patchMetricsCalled bool
	patchMetricsID     string
	patchMetricsDelta  inbound.MetricsDelta

	// PatchStatus control
	patchStatusErr    error
	patchStatusCalled bool
	patchStatusStatus string

	// GetUsage control
	getUsageRows []inbound.SkillUsageRow
	getUsageErr  error
}

func (f *fakeSkillSvc) PatchMetrics(_ context.Context, skillID string, delta inbound.MetricsDelta) error {
	f.patchMetricsCalled = true
	f.patchMetricsID = skillID
	f.patchMetricsDelta = delta
	return f.patchMetricsErr
}

func (f *fakeSkillSvc) PatchStatus(_ context.Context, skillID string, status, _ string) error {
	f.patchStatusCalled = true
	_ = skillID
	f.patchStatusStatus = status
	return f.patchStatusErr
}

func (f *fakeSkillSvc) GetUsage(_ context.Context, _ string) ([]inbound.SkillUsageRow, error) {
	return f.getUsageRows, f.getUsageErr
}

// GetSkill — not exercised by Group D tests; returns nil to satisfy SkillService.
func (f *fakeSkillSvc) GetSkill(_ context.Context, _ string) (*inbound.GetSkillResult, error) {
	return nil, nil
}

func skillSrv(t *testing.T, svc inbound.SkillService, rejectAuth bool) *httptest.Server {
	t.Helper()
	d := defaultDeps()
	d.Skills = svc
	if rejectAuth {
		d.Auth = &fakeAuthn{rejectAll: true}
	}
	return newSrv(t, d)
}

const testSkillID = "01ARZ3NDEKTSV4RRFFQ69G5SK1"

// D.1 — Valid delta applied atomically → 200 + service called with correct delta.
func TestSkills_PatchMetrics_ValidDelta_200(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, false)

	body := `{"success_delta":1,"failure_delta":0,"tests_passed_delta":1,"rollback_delta":0,"deprecated_api_hits_delta":0,"avg_retry_reduction":0.25}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/metrics", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, svc.patchMetricsCalled, "PatchMetrics must be called")
	require.Equal(t, testSkillID, svc.patchMetricsID)
	require.Equal(t, 1, svc.patchMetricsDelta.SuccessDelta)
	require.Equal(t, 1, svc.patchMetricsDelta.TestsPassedDelta)
	require.InDelta(t, 0.25, svc.patchMetricsDelta.AvgRetryReduction, 0.001)
}

// D.2 — Negative delta rejected → 422 + no mutation.
func TestSkills_PatchMetrics_NegativeDelta_422(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, false)

	body := `{"failure_delta":-1}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/metrics", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.False(t, svc.patchMetricsCalled, "PatchMetrics must NOT be called when delta is negative")
}

// D.3 — Missing API key → 401.
func TestSkills_PatchMetrics_NoAuth_401(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, true) // rejectAll=true

	body := `{"success_delta":1}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/metrics", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	// No API key header.

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// D.4 — Unknown skill ID → 404.
func TestSkills_PatchMetrics_UnknownSkill_404(t *testing.T) {
	svc := &fakeSkillSvc{patchMetricsErr: outbound.ErrNotFound}
	srv := skillSrv(t, svc, false)

	body := `{"success_delta":1}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/metrics", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// D.5 — Valid status transition → 200 + PatchStatus called with "validated".
func TestSkills_PatchStatus_ValidTransition_200(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, false)

	body := `{"status":"validated","reason":"thresholds met"}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/status", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.True(t, svc.patchStatusCalled, "PatchStatus must be called")
	require.Equal(t, "validated", svc.patchStatusStatus)
}

// D.6a — Invalid status enum → 422, service not called.
func TestSkills_PatchStatus_InvalidEnum_422(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, false)

	body := `{"status":"unknown_value"}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/status", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	require.False(t, svc.patchStatusCalled, "PatchStatus must NOT be called with invalid enum")
}

// D.6b — Forbidden skip transition (service returns domain error) → 422.
func TestSkills_PatchStatus_ForbiddenTransition_422(t *testing.T) {
	svc := &fakeSkillSvc{patchStatusErr: errForbiddenTransition}
	srv := skillSrv(t, svc, false)

	body := `{"status":"archived","reason":"skip"}`
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/skills/"+testSkillID+"/status", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// D.7a — GET /skills/usage?change_id=X returns rows with apply_attempts.
func TestSkills_GetUsage_FilteredRows(t *testing.T) {
	changeIDStr := "01ARZ3NDEKTSV4RRFFQ69G5CH1"
	skillIDParsed, _ := ids.ParseSkillID(testSkillID)
	cid, _ := ids.ParseChangeID(changeIDStr)
	now := time.Now().UTC().Truncate(time.Second)
	suID, _ := ids.ParseSkillUsageID("01ARZ3NDEKTSV4RRFFQ69G5SU1")

	su := skillusage.Hydrate(suID, cid, "apply", skillIDParsed, "v1", now, skillusage.OutcomeSuccess)

	svc := &fakeSkillSvc{
		getUsageRows: []inbound.SkillUsageRow{
			{SkillUsage: su, ApplyAttempts: 2},
		},
	}
	srv := skillSrv(t, svc, false)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/usage?change_id="+changeIDStr, nil)
	req.Header.Set("X-Sophia-API-Key", "valid-key")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	items, ok := result["items"].([]any)
	require.True(t, ok, "response must have items array")
	require.Len(t, items, 1)

	item := items[0].(map[string]any)
	require.Equal(t, changeIDStr, item["change_id"])
	require.Equal(t, float64(2), item["apply_attempts"])
}

// D.7b — GET /skills/usage without valid auth → 401.
func TestSkills_GetUsage_NoAuth_401(t *testing.T) {
	svc := &fakeSkillSvc{}
	srv := skillSrv(t, svc, true) // rejectAll=true

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/skills/usage?change_id=somechange", nil)
	// No API key header.

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
