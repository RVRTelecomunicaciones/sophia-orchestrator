package phase_test

// Tests for WU-5 (Clusters 2+3 — discarded errors in phase/service.go).
//
// TDD RED: assertions fail because the sites still use _ = (no log+audit).
// TDD GREEN: implemented in service.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// countDiscardedPhase returns the number of "phase.apply.error.discarded"
// events in the audit.
func countDiscardedPhase(a *fakeAudit) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.events {
		if e.EventType == "phase.apply.error.discarded" {
			n++
		}
	}
	return n
}

// hasDiscardedPhaseEventForOp returns true if any "phase.apply.error.discarded"
// event's payload contains the given op string.
func hasDiscardedPhaseEventForOp(a *fakeAudit, op string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.events {
		if e.EventType == "phase.apply.error.discarded" && e.Payload != nil {
			if findSubstrPhase(string(e.Payload), op) {
				return true
			}
		}
	}
	return false
}

func findSubstrPhase(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestPhaseService_SpawnGovRelease_LogsAndAudits verifies that when
// SpawnGov.Release returns an error after a successful dispatch, the error
// is logged and an "phase.apply.error.discarded" audit event is emitted.
//
// RED: site uses _ = s.d.SpawnGov.Release so no audit is emitted.
// GREEN: after implementing the if-err block at the SpawnGov.Release site.
func TestPhaseService_SpawnGovRelease_LogsAndAudits(t *testing.T) {
	h := newHarness(t)
	h.spawn.releaseErr = errors.New("spawn gov release fail")

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	out, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:    cid,
		PhaseType:   phase.PhaseSpec,
		RetryBudget: 3,
	})
	// Phase must complete even when Release fails (soft error).
	require.NoError(t, err)
	require.NotNil(t, out)

	// After GREEN: a "phase.apply.error.discarded" audit event must be emitted.
	assert.True(t, hasDiscardedPhaseEventForOp(h.audit, "SpawnGov.Release"),
		"expected audit payload to contain op=SpawnGov.Release (currently RED: site uses _ =)")
}

// TestPhaseService_SuccessPath_NoSpuriousLogOrAudit verifies that on the
// success path (all deps succeed), no "phase.apply.error.discarded" events
// are emitted.
func TestPhaseService_SuccessPath_NoSpuriousLogOrAudit(t *testing.T) {
	h := newHarness(t)

	cid, _ := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	_, err := h.svc.Run(context.Background(), inbound.RunPhaseInput{
		ChangeID:    cid,
		PhaseType:   phase.PhaseSpec,
		RetryBudget: 3,
	})
	require.NoError(t, err)

	assert.Equal(t, 0, countDiscardedPhase(h.audit),
		"success path must not emit any phase.apply.error.discarded events")
}
