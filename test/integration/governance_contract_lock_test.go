//go:build integration

// governance_contract_lock_test.go — GAP A cross-repo contract-lock e2e test
// (spec: governance-integration-contract).
//
// This test drives orchestator's REAL outbound governance client
// (internal/adapters/outbound/governance) against agent-governance-core's REAL
// /governance/v1 decision facade, stood up in-process via the exported
// govhttptest seam. No mocks, no stubs, no PostgreSQL: the seam wires the
// production chi router + production govdecisions.DecisionsService over
// deterministic in-memory repositories.
//
// Purpose: LOCK the cross-repo JSON contract. The assertions below check
// CONCRETE decoded fields (decision, agent_role, reason, approval status), so
// the test FAILS if either side's wire shape drifts — that failure is a real
// contract incompatibility, not flakiness.
//
// CI CAVEAT: this test imports github.com/russellcxl/agent-governance-core via
// the public govhttptest seam. orch's production code never imports govcore.
// Resolution is provided ENTIRELY by the go.work workspace `use
// ./agent-governance-core` directive — there is intentionally NO require in
// orch's go.mod. govcore's module path has no public remote yet, so adding a
// go.mod require forces a failing VCS lookup of the pseudo-version; the
// workspace `use` target supplies the seam source directly and that require is
// unnecessary. Consequence: this test runs ONLY with the go.work workspace
// active. In CI it REQUIRES either go.work active OR (once published) a tagged
// govcore version containing govhttptest, at which point a go.mod require can be
// added. The standard unit build (`go test ./internal/...`) is unaffected
// because the test is gated behind the `integration` build tag and the seam
// import lives only here.
package integration_test

import (
	"context"
	"net/http/httptest"
	"testing"

	govhttptest "github.com/russellcxl/agent-governance-core/govhttptest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/governance"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Deterministic fixtures (valid ULIDs the client can parse). No time.Now /
// ulid.Make anywhere — the seam supplies the deterministic id/clock.
const (
	contractLockChangeID = "01ARZ3NDEKTSV4RRFFQ69G5C01"
	contractLockPhaseID  = "01ARZ3NDEKTSV4RRFFQ69G5P01"
)

// newRealFacadeClient boots the REAL govcore /governance/v1 facade in-process
// over an httptest.Server and points the REAL orch governance.Client at it.
// Returns the wired client plus the seam store so callers can inspect the
// recorded decision rows.
func newRealFacadeClient(t *testing.T) (*governance.Client, *govhttptest.Store) {
	t.Helper()

	store := govhttptest.NewInMemoryStore()
	srv := httptest.NewServer(govhttptest.NewInMemoryDecisionsHandlerFromStore(store))
	t.Cleanup(srv.Close)

	cfg := governance.DefaultConfig(srv.URL, "contract-lock-key")
	client, err := governance.New(cfg)
	require.NoError(t, err, "real governance client must construct against the real facade")

	return client, store
}

// TestContractLock_PhaseDecision_EndToEnd locks the phase-decision path:
// orch's EvaluatePhase POSTs to /governance/v1/decisions/phase, the real facade
// applies M-E0 default-allow, and the client decodes the production DTO. The
// concrete field assertions are the drift lock — if either side's JSON shape
// changes, the decode mismatches and this fails.
func TestContractLock_PhaseDecision_EndToEnd(t *testing.T) {
	client, store := newRealFacadeClient(t)

	cid, err := ids.ParseChangeID(contractLockChangeID)
	require.NoError(t, err)

	decision, err := client.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID:        cid,
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "contract-lock phase decision",
		Sensitive:       false,
	})
	require.NoError(t, err, "phase decision must round-trip without 404 or decode error")
	require.NotNil(t, decision)

	// Drift lock: concrete production fields the facade emits today.
	assert.Equal(t, outbound.DecisionAllow, decision.Decision,
		"phase decision must deserialize to default-allow")
	assert.Equal(t, "team-lead", decision.AgentRole,
		"agent_role field must survive the cross-repo round-trip")
	assert.Equal(t, "M-E0 default-allow policy", decision.Reason,
		"reason field must survive the cross-repo round-trip")

	// The real service recorded the decision row through the in-memory repo,
	// proving the request reached the production handler (not a stub).
	assert.Len(t, store.Decisions.Saved(), 1,
		"real facade must record exactly one decision row for the phase call")
}

// TestContractLock_SensitiveDecision_EndToEnd locks the sensitive-action path:
// orch's EvaluateSensitiveAction POSTs to /governance/v1/decisions/sensitive
// and decodes the production DTO emitted by the real facade.
func TestContractLock_SensitiveDecision_EndToEnd(t *testing.T) {
	client, store := newRealFacadeClient(t)

	cid, err := ids.ParseChangeID(contractLockChangeID)
	require.NoError(t, err)

	decision, err := client.EvaluateSensitiveAction(
		context.Background(), cid, "git.push@v1", []byte(`{"branch":"main"}`),
	)
	require.NoError(t, err, "sensitive decision must round-trip without 404 or decode error")
	require.NotNil(t, decision)

	// Drift lock: concrete production fields for the sensitive path.
	assert.Equal(t, outbound.DecisionAllow, decision.Decision,
		"sensitive decision must deserialize to default-allow")
	assert.Equal(t, "M-E0 default-allow sensitive policy", decision.Reason,
		"sensitive reason field must survive the cross-repo round-trip")

	assert.Len(t, store.Decisions.Saved(), 1,
		"real facade must record exactly one decision row for the sensitive call")
}

// TestContractLock_ApprovalStatus_EndToEnd locks the approval-status GET path:
// orch's AwaitApproval polls
// /governance/v1/approvals/{change_id}/{phase_id}/status. The real facade
// reports "granted" by default (no seeded gate), so AwaitApproval returns nil
// on the first poll — proving the path and the `status` field decode end to
// end without sleeps.
func TestContractLock_ApprovalStatus_EndToEnd(t *testing.T) {
	client, _ := newRealFacadeClient(t)

	cid, err := ids.ParseChangeID(contractLockChangeID)
	require.NoError(t, err)
	pid, err := ids.ParsePhaseID(contractLockPhaseID)
	require.NoError(t, err)

	// Default approval gate is "granted": AwaitApproval returns immediately.
	// A nil return is the drift lock for the {change_id}/{phase_id}/status route
	// AND the `status` field shape — any path or field drift surfaces as a
	// non-nil error (404 / decode failure / unrecognized status).
	err = client.AwaitApproval(context.Background(), cid, pid)
	require.NoError(t, err,
		"approval-status GET must decode a recognized status and resolve as granted")
}
