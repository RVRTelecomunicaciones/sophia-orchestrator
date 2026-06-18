package apply_test

// Tests for WU-4 (Cluster 3 — repo Save errors in build_feedback.go).
//
// TDD RED: assertions below fail because the 3 SaveGroup sites in
// build_feedback.go still use _ = (no log+audit).
// TDD GREEN: implemented in build_feedback.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildFeedback_SaveGroupErr_NoManifest_LogsAndAudits verifies that when
// SaveGroup fails on the no-manifest SkipBuild path (build_feedback.go:153),
// an "apply.error.discarded" audit event is captured.
//
// The no-manifest path is exercised by the default newRunService (which uses
// fakeRuntime returning exit=1 for all "test -f" probes = no go.mod found).
//
// RED: site uses _ = so no audit event is emitted.
// GREEN: after implementing the if-err block at line 153.
func TestBuildFeedback_SaveGroupErr_NoManifest_LogsAndAudits(t *testing.T) {
	audit := &captureAudit{}
	board := newFakeBoardRepo()
	board.saveGroupErr = errors.New("save group db error")
	// Skip the guarded SaveGroup calls before reaching the SkipBuild site:
	// 1. buildBoard:963 SaveGroup (guarded)
	// 2. assignWorktrees:1040 SaveGroup (guarded)
	// 3. runTeamLead:99 SaveGroup (guarded)
	// The 4th call is build_feedback.go:153 (silently discarded).
	board.saveGroupErrAfterN = 3

	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
		d.BoardRepo = board
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	// Execute must complete; SaveGroup failure is soft.
	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	// After GREEN: the SaveGroup error in build_feedback.go:153 must be audited.
	assert.True(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"expected audit payload to contain operation=BoardRepo.SaveGroup from build_feedback.go")
}

// TestBuildFeedback_SaveGroupErr_BuildPass_LogsAndAudits verifies that when
// SaveGroup fails on the build-passed path (build_feedback.go:179), an
// "apply.error.discarded" audit event is captured.
//
// Uses wildcardGoModRuntime (go.mod present) with build result [0] (pass).
func TestBuildFeedback_SaveGroupErr_BuildPass_LogsAndAudits(t *testing.T) {
	audit := &captureAudit{}

	// Use wildcardGoModRuntime with build result 0 (pass) so the build-passed
	// path (line 179 SaveGroup) is exercised.
	wrt := &wildcardGoModRuntime{buildResults: []int{0}}
	board := newFakeBoardRepo()
	board.saveGroupErr = errors.New("save group db error after build pass")
	// Skip 3 guarded calls (same as above); 4th call is build_feedback.go:179.
	board.saveGroupErrAfterN = 3

	svc, _, _, _, _, _ := newRunService(t, func(d *apply.RunDeps) {
		d.Audit = audit
		d.BoardRepo = board
		d.Runtime = wrt
	})

	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID:  c.ID(),
		PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	assert.True(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"expected audit payload to contain operation=BoardRepo.SaveGroup from build_feedback.go build-passed path")
}

// TestBuildFeedback_SuccessPath_NoSpuriousAuditWhenSavesSucceed verifies that
// when all repo saves succeed on the build-gate path, no spurious
// "apply.error.discarded" events are emitted for SaveGroup.
func TestBuildFeedback_SuccessPath_NoSpuriousAuditWhenSavesSucceed(t *testing.T) {
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

	// No BoardRepo.SaveGroup error events on success path.
	assert.False(t, hasDiscardedEventForOp(audit, "BoardRepo.SaveGroup"),
		"success path must not emit BoardRepo.SaveGroup error audit events")
}
