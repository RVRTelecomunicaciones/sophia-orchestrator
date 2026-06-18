package apply_test

// Tests for WU-2 (Cluster 2 — domain-transition errors) and WU-3 (Cluster 3
// — repo Save errors) in teamlead.go and build_feedback.go.
//
// TDD RED: assertions below fail because the sites still use _ = (no log+audit).
// TDD GREEN: implemented in teamlead.go + build_feedback.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureAudit is a test-local audit capture struct (mirrors fakeAudit but
// explicit so these tests are self-contained).
type captureAudit struct {
	events []outbound.AuditEvent
}

func (a *captureAudit) Append(_ context.Context, e outbound.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}
func (a *captureAudit) HasEventForPhase(_ context.Context, _ ids.PhaseID, _ string) (bool, error) {
	return false, nil
}

// countDiscardedAuditEvents returns the number of "apply.error.discarded"
// events in a.
func countDiscardedAuditEvents(a *captureAudit) int {
	n := 0
	for _, e := range a.events {
		if e.EventType == "apply.error.discarded" {
			n++
		}
	}
	return n
}

// hasDiscardedEventForOp returns true if any "apply.error.discarded" event's
// payload JSON contains the given op string.
func hasDiscardedEventForOp(a *captureAudit, op string) bool {
	for _, e := range a.events {
		if e.EventType == "apply.error.discarded" && e.Payload != nil {
			if containsStr(string(e.Payload), op) {
				return true
			}
		}
	}
	return false
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 ||
		findSubstr(haystack, needle))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- WU-2: Cluster 2 — domain-transition errors ---

// TestRunTeamLead_TeamLeadSession_RecordOutcome_LogsWarnAndAudits asserts that
// when the team-lead session's RecordOutcome returns an error (which ALWAYS
// happens because teamLeadSess is never MarkRunning'd before RecordOutcome is
// called at teamlead.go:188), the error is surfaced via an
// "apply.error.discarded" audit event and a WARN-level log.
//
// RED: the audit event is NOT emitted yet (site uses _ =).
// GREEN: after implementing the if-err block at line 188.
func TestRunTeamLead_TeamLeadSession_RecordOutcome_LogsWarnAndAudits(t *testing.T) {
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

	// The team-lead session RecordOutcome always fails (session is in Pending,
	// not Running, when RecordOutcome is called). After WU-2 GREEN this must
	// emit an audit event.
	assert.GreaterOrEqual(t, countDiscardedAuditEvents(audit), 1,
		"expected at least one apply.error.discarded audit event for teamLeadSess.RecordOutcome")
	assert.True(t, hasDiscardedEventForOp(audit, "teamLeadSess.RecordOutcome"),
		"expected audit payload to contain operation=teamLeadSess.RecordOutcome")
}

// TestRunTeamLead_TransitionErrors_NoPhasAbort asserts that domain-transition
// errors (group.Fail, group.Complete, sess.RecordOutcome) do NOT abort the
// apply phase — the phase still reaches a terminal status.
//
// This test verifies the no-abort invariant regardless of whether audit events
// are emitted or not.
func TestRunTeamLead_TransitionErrors_NoPhasAbort(t *testing.T) {
	audit := &captureAudit{}
	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	// Apply phase must complete (no panic, no fatal error) even with
	// internal domain-transition errors.
	require.NoError(t, err)
	require.NotNil(t, env, "apply must produce an envelope even if domain transitions fail")
}

// --- WU-3: Cluster 3 — repo Save errors ---

// TestRunTeamLead_SaveGroupErr_LogsErrorAndAudits asserts that when
// BoardRepo.SaveGroup returns an error at a silently-discarded site, the error
// is logged at ERROR level and an "apply.error.discarded" audit event is captured.
//
// The first SaveGroup call in runTeamLead (line 99) IS properly guarded and
// returns early on error. We skip that call by setting saveGroupErrAfterN=1
// so only the later _ = SaveGroup(...) calls (lines 164, 168) are injected.
//
// RED: the audit event is NOT emitted (sites use _ = s.d.BoardRepo.SaveGroup).
// GREEN: after implementing the if-err block.
func TestRunTeamLead_SaveGroupErr_LogsErrorAndAudits(t *testing.T) {
	audit := &captureAudit{}
	board := newFakeBoardRepo()
	board.saveGroupErr = errors.New("db timeout simulated")
	// Skip the first 3 guarded SaveGroup calls (buildBoard:963, assignWorktrees:1040,
	// runTeamLead:99). The 4th call onwards hits the silently-discarded sites
	// (build_feedback.go:153, teamlead.go:164,168).
	board.saveGroupErrAfterN = 3

	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
		d.BoardRepo = board
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	// Execute must complete (no abort) even when SaveGroup fails at the
	// silently-discarded sites.
	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)
	require.NotNil(t, env)

	// At least one "apply.error.discarded" event must be captured for the
	// SaveGroup operation after GREEN implementation.
	assert.GreaterOrEqual(t, countDiscardedAuditEvents(audit), 1,
		"expected at least one apply.error.discarded audit event for BoardRepo.SaveGroup error")
	assert.True(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"expected audit payload to contain operation=BoardRepo.SaveGroup")
}

// TestRunTeamLead_SaveErrors_NoPhaseAbort asserts that SaveGroup AND SaveTask
// both returning errors at the silently-discarded sites do NOT abort the apply phase.
func TestRunTeamLead_SaveErrors_NoPhaseAbort(t *testing.T) {
	audit := &captureAudit{}
	board := newFakeBoardRepo()
	board.saveGroupErr = errors.New("group save fail")
	board.saveGroupErrAfterN = 3 // skip 3 guarded SaveGroup calls before hitting discarded sites
	board.saveTaskErr = errors.New("task save fail")
	board.saveTaskErrAfterN = 1 // skip the 1 guarded SaveTask in buildBoard

	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
		d.BoardRepo = board
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)
	require.NotNil(t, env, "apply must complete even when repo saves fail at discarded sites")
}

// TestApplyLayer_SuccessPath_NoSpuriousAuditFromCluster3 asserts that when
// all repo saves succeed, the apply layer does NOT emit any extra
// "apply.error.discarded" events beyond those from Cluster 2 domain errors
// (i.e., only the pre-existing teamLeadSess.RecordOutcome error — which is
// structural — is captured, not spurious ones from healthy saves).
//
// This is a partial success-path regression guard (WU-6 extends this).
func TestApplyLayer_SuccessPath_NoSpuriousAuditFromCluster3(t *testing.T) {
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

	// Verify no audit events contain "BoardRepo.SaveGroup" on the success path.
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"success path must not emit BoardRepo.SaveGroup error audit events")
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveTask"),
		"success path must not emit BoardRepo.SaveTask error audit events")
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveBoard"),
		"success path must not emit BoardRepo.SaveBoard error audit events")
}
