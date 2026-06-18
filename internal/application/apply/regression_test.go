package apply_test

// WU-6: Success-path regression guard for PR 1 (Clusters 2+3).
//
// These tests verify that on the normal happy path, no SPURIOUS
// "apply.error.discarded" events are emitted from Cluster 3 repo-save
// sites in teamlead.go, build_feedback.go, or phase/service.go.
//
// The structural teamLeadSess.RecordOutcome event (Cluster 2, pre-existing
// invariant) IS expected and is counted separately.

import (
	"context"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyLayer_SuccessPath_NoSpuriousLogOrAudit runs the full apply layer
// with all fakes returning nil from every domain-transition and repo-save
// call, then asserts that no Cluster 3 (repo-save) "apply.error.discarded"
// events are emitted. This is the WU-6 comprehensive regression guard.
func TestApplyLayer_SuccessPath_NoSpuriousLogOrAudit(t *testing.T) {
	audit := &captureAudit{}
	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	// Cluster 3: no repo-save error events on success path.
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"success path: no BoardRepo.SaveGroup error audit events expected")
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveTask"),
		"success path: no BoardRepo.SaveTask error audit events expected")
	assert.False(t, hasDiscardedEventForOp(audit, "SessionRepo.Save"),
		"success path: no SessionRepo.Save error audit events expected")
	assert.False(t, hasDiscardedEventForOp(audit, "SpawnGov.Release"),
		"success path: no SpawnGov.Release error audit events expected")
}
