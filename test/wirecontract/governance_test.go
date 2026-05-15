//go:build wirecontract

package wirecontract_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/governance"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// Matrix row #1: POST /governance/v1/decisions/phase
func TestGovernance_EvaluatePhase_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"decision":"allow"}`)

	cfg := governance.DefaultConfig(srv.URL, "test-key")
	client, err := governance.New(cfg)
	require.NoError(t, err)

	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	_, err = client.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "wire-contract test",
		Sensitive:       false,
	})
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/governance/v1/decisions/phase", 1)
}

// Matrix row #2: POST /governance/v1/decisions/sensitive
func TestGovernance_EvaluateSensitiveAction_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"decision":"allow"}`)

	cfg := governance.DefaultConfig(srv.URL, "test-key")
	client, err := governance.New(cfg)
	require.NoError(t, err)

	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	_, err = client.EvaluateSensitiveAction(context.Background(), cid, "git.push@v1", []byte(`{}`))
	require.NoError(t, err)

	assertRoute(t, capt, "POST", "/governance/v1/decisions/sensitive", 2)
}

// Matrix row #3: GET /governance/v1/approvals/{change_id}/{phase_id}/status
//
// AwaitApproval polls until terminal; we reply "granted" on the first hit so
// it returns immediately and the test stays fast.
func TestGovernance_AwaitApproval_PathContract(t *testing.T) {
	srv, capt := newCapturer(t, http.StatusOK, `{"status":"granted"}`)

	cfg := governance.DefaultConfig(srv.URL, "test-key")
	client, err := governance.New(cfg)
	require.NoError(t, err)

	cid, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	pid, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)

	err = client.AwaitApproval(context.Background(), cid, pid)
	require.NoError(t, err)

	assertRoute(t, capt, "GET",
		"/governance/v1/approvals/01ARZ3NDEKTSV4RRFFQ69G5C01/01ARZ3NDEKTSV4RRFFQ69G5P01/status", 3)
}
