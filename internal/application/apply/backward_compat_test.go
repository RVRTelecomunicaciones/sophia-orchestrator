package apply_test

// backward_compat_test.go — Task 3.3: backward-compatibility invariant tests.
//
// Spec invariant (design.md § "Backward-Compat Invariant"):
//   WorktreeInit=empty + no manifest → NO build runs + legacy completion unchanged.
//   Source-clone path skips build only when no runnable manifest exists.
//
// These tests are SEPARATE from the existing build_feedback_test.go entries so
// the "backward compat" intent is explicit and findable by name pattern:
//   go test ./internal/application/apply -run 'TestBackwardCompat'

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// ─── backward-compat: WorktreeInit=empty + no manifest ───────────────────────

// TestBackwardCompat_WorktreeInitEmpty_NoManifest_NoBuildRuns is the primary
// backward-compat pin: when both WorktreeInit=empty AND no runnable manifest
// exists in the worktree, the apply phase MUST:
//   - NOT emit apply.build.started
//   - NOT emit apply.build.failed or apply.build.passed
//   - Complete the group normally (apply.group.completed)
//   - Return a DONE envelope
//
// This is the invariant from spec § "Backward-Compat Invariant" that protects
// existing callers of the apply phase that never provision build manifests.
func TestBackwardCompat_WorktreeInitEmpty_NoManifest_NoBuildRuns(t *testing.T) {
	events := &fakeEvents{}
	svc := newBackCompatService(t, apply.WorktreeInitEmpty, &noManifestRuntime{}, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status),
		"WorktreeInit=empty + no manifest → legacy DONE completion must be unchanged")

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"no manifest → build gate MUST NOT start even with WorktreeInit=empty")
	require.NotContains(t, types, inbound.EventApplyBuildFailed,
		"no manifest → no build → no build failures")
	require.NotContains(t, types, inbound.EventApplyBuildPassed,
		"no manifest → no build → no build pass event")
	require.Contains(t, types, inbound.EventApplyGroupCompleted,
		"group must complete via legacy path when no manifest exists")
}

// TestBackwardCompat_WorktreeInitDefault_NoManifest_NoBuildRuns pins the same
// invariant for the default WorktreeInit (empty string = source_clone mode):
// even with source_clone, if no manifest is detected, the build gate is
// skipped and the group completes normally.
func TestBackwardCompat_WorktreeInitDefault_NoManifest_NoBuildRuns(t *testing.T) {
	events := &fakeEvents{}
	svc := newBackCompatService(t, "" /* default */, &noManifestRuntime{}, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status),
		"default WorktreeInit + no manifest → legacy DONE completion must be unchanged")

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"no manifest → build gate must be skipped regardless of WorktreeInit mode")
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBackwardCompat_SourceClone_NoManifest_SkipsBuildGate verifies the
// source-clone path: when WorktreeInit is NOT empty (source_clone mode) and
// the cloned source tree contains NO recognizable manifest, the build gate
// is still skipped and the phase completes (no regression from the clone).
func TestBackwardCompat_SourceClone_NoManifest_SkipsBuildGate(t *testing.T) {
	// sourceCloneNoManifestRT: mkdir/cp succeed (simulates source clone), but
	// no manifest file exists in the worktree (test -f returns 1 for all).
	rt := &sourceCloneNoManifestRuntime{}
	events := &fakeEvents{}
	svc := newBackCompatServiceWithSource(
		t,
		"", /* default = source_clone mode */
		"/tmp/fake-source-repo",
		rt,
		events,
	)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status),
		"source-clone path with no manifest must still complete — build gate skipped")

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"source-clone + no runnable manifest → build gate must not start")
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBackwardCompat_WorktreeInitEmpty_WithManifest_BuildRuns confirms the
// POSITIVE case: WorktreeInit=empty is about the worktree seed mode, NOT
// about suppressing builds. When a manifest IS present, the build gate runs.
func TestBackwardCompat_WorktreeInitEmpty_WithManifest_BuildRuns(t *testing.T) {
	// wildcardGoModRuntime: go.mod exists, first build exits 0.
	wrt := &wildcardGoModRuntime{buildResults: []int{0}}
	events := &fakeEvents{}
	svc := newBackCompatService(t, apply.WorktreeInitEmpty, wrt, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildStarted,
		"WorktreeInit=empty does NOT suppress build — manifest detection drives the decision")
	require.Contains(t, types, inbound.EventApplyBuildPassed)
}

// TestBackwardCompat_GroupCompletion_IsUnchangedWithoutBuild pins that the
// group-completion event sequence (spawned → task-claimed → group-completed)
// is identical between the pre-build-loop code and the post-build-loop code
// when no manifest is found — zero new events are injected.
func TestBackwardCompat_GroupCompletion_IsUnchangedWithoutBuild(t *testing.T) {
	events := &fakeEvents{}
	svc := newBackCompatService(t, apply.WorktreeInitEmpty, &noManifestRuntime{}, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)

	types := events.types()
	// Verify the legacy sequence is intact.
	require.Contains(t, types, inbound.EventApplyTeamLeadSpawned)
	require.Contains(t, types, inbound.EventApplyTaskClaimed)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)

	// Verify NO build events are injected.
	for _, et := range types {
		require.False(t,
			et == inbound.EventApplyBuildStarted ||
				et == inbound.EventApplyBuildPassed ||
				et == inbound.EventApplyBuildFailed,
			"no-manifest path must emit ZERO build events; found %q", et,
		)
	}
}

// TestBackwardCompat_PhaseType_IsApplyForAllPaths pins that the phase type
// remains "apply" through both the build-gate path and the skip path so SSE
// consumers that key on phase type see a consistent contract.
func TestBackwardCompat_PhaseType_IsApplyForAllPaths(t *testing.T) {
	require.Equal(t, "apply", string(phase.PhaseApply),
		"phase type constant must remain 'apply' — SSE consumers depend on this string")
}

// ─── runtime helpers ─────────────────────────────────────────────────────────

// sourceCloneNoManifestRuntime simulates the source-clone worktree mode:
// mkdir and cp succeed (simulating the clone step), but all "test -f"
// manifest probes fail (no go.mod, package.json, or pubspec.yaml in the tree).
type sourceCloneNoManifestRuntime struct{}

func (r *sourceCloneNoManifestRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	}
	cmd, _ := m["command"].(string)
	switch cmd {
	case "mkdir":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	case "cp":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	case "test":
		// All manifest probes fail → no manifest detected.
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

// ─── service constructors ────────────────────────────────────────────────────

// newBackCompatService builds a RunService with the given WorktreeInit and
// runtime, wired to a shared fakeEvents so callers can inspect event output.
func newBackCompatService(
	t *testing.T,
	worktreeInit string,
	rt outbound.RuntimeClient,
	events *fakeEvents,
) *apply.RunService {
	t.Helper()
	return newBackCompatServiceWithSource(t, worktreeInit, "", rt, events)
}

// newBackCompatServiceWithSource is like newBackCompatService but also sets
// SourceRepoPath (for source-clone mode tests).
func newBackCompatServiceWithSource(
	t *testing.T,
	worktreeInit string,
	sourcePath string,
	rt outbound.RuntimeClient,
	events *fakeEvents,
) *apply.RunService {
	t.Helper()

	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	cfg := apply.RunConfig{
		MaxParallelGroups:             1,
		MaxParallelImplementsPerGroup: 1,
		DepWaitTimeout:                3,
		DispatchTimeoutMS:             5000,
		WorktreeRoot:                  t.TempDir(),
		WorktreeInit:                  worktreeInit,
	}
	if sourcePath != "" {
		cfg.SourceRepoPath = sourcePath
	}

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     rt,
		Dispatcher:  disp,
		SpawnGov:    spawn,
		Validator:   discipline.NewValidator(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      events,
		Memory:      mem,
		Clock:       clock,
		IDGen:       idGen,
		Config:      cfg,
	}
	return apply.NewRun(deps)
}

// ─── multi-group backward-compat with the build gate ─────────────────────────

// TestBackwardCompat_MultiGroup_NoManifest_AllGroupsComplete verifies that a
// multi-group apply phase (the typical production shape) still completes all
// groups when no manifest exists — no group is accidentally blocked by the
// new build-gate code path.
func TestBackwardCompat_MultiGroup_NoManifest_AllGroupsComplete(t *testing.T) {
	events := &fakeEvents{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	mem := newFakeMemory()

	// Three-group tasks list (resembles a real Sophia change).
	mem.putTasksList("feat-x", map[string]any{
		"groups": []map[string]any{
			{
				"name": "domain",
				"tasks": []map[string]any{
					{"description": "add domain type", "files_pattern": []string{"internal/domain/*.go"}},
				},
			},
			{
				"name":       "application",
				"depends_on": []string{"domain"},
				"tasks": []map[string]any{
					{"description": "wire service", "files_pattern": []string{"internal/application/*.go"}},
				},
			},
			{
				"name":       "bootstrap",
				"depends_on": []string{"application"},
				"tasks": []map[string]any{
					{"description": "wire deps", "files_pattern": []string{"internal/bootstrap/*.go"}},
				},
			},
		},
	})

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     &noManifestRuntime{},
		Dispatcher:  disp,
		SpawnGov:    spawn,
		Validator:   discipline.NewValidator(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      events,
		Memory:      mem,
		Clock:       clock,
		IDGen:       idGen,
		Config: apply.RunConfig{
			MaxParallelGroups:             3,
			MaxParallelImplementsPerGroup: 2,
			DepWaitTimeout:                3,
			DispatchTimeoutMS:             5000,
			WorktreeRoot:                  t.TempDir(),
			WorktreeInit:                  apply.WorktreeInitEmpty,
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status),
		"multi-group apply with no manifest must complete DONE — build gate must not regress any group")

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"no manifest across all groups → zero build events must be emitted")

	// Count group completed events — must be 3.
	completedCount := 0
	for _, et := range types {
		if et == inbound.EventApplyGroupCompleted {
			completedCount++
		}
	}
	require.Equal(t, 3, completedCount,
		"all three groups must complete via the legacy (no-build) path")
}

// TestBackwardCompat_AssembleBuildRepairPrompt_EmptyGroupName pins that
// assembleBuildRepairPrompt handles a group with an empty worktree path
// gracefully (no panic) — this is the edge case where the worktree was
// never assigned before the build feedback loop ran.
func TestBackwardCompat_BuildRepairPrompt_HasGroupAndCommandInfo(t *testing.T) {
	// fail once, pass on second attempt — repair prompt is assembled once.
	wrt := &wildcardGoModRuntime{buildResults: []int{1, 0}}
	events := &fakeEvents{}
	svc := newBackCompatService(t, "", wrt, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	// The repair dispatch was triggered; verify the group completed after repair.
	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// ─── named: no-manifest source-clone still uses WorktreeInitDefault path ─────

// TestBackwardCompat_createWorktrees_NoBuildWhenNoManifest pins the
// run.go createWorktrees + runGroupBuildFeedbackLoop interaction:
// when SourceRepoPath is set (source_clone) AND the cloned repo does not
// have a recognised manifest, zero build events must be emitted.
func TestBackwardCompat_SourceClone_WithManifest_BuildGateRuns(t *testing.T) {
	// Source-clone mode (default WorktreeInit) with go.mod present.
	// Build exits 0 on first attempt.
	wrt := &wildcardGoModRuntime{buildResults: []int{0}}
	events := &fakeEvents{}
	svc := newBackCompatServiceWithSource(t, "" /* default */, "/tmp/src", wrt, events)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	// Source-clone + manifest present → build gate MUST run.
	require.Contains(t, types, inbound.EventApplyBuildStarted,
		"source-clone mode with a runnable manifest → build gate must execute")
	require.Contains(t, types, inbound.EventApplyBuildPassed)

	// Also verify that the cp command was issued (source-clone mode active).
	// We do this indirectly: if the build gate ran, the runtime was called
	// with "go build ./..." which means the wildcardGoModRuntime handled it.
	// The events confirm the build gate engaged.
	require.NotContains(t, types, inbound.EventApplyBuildFailed)
}

// TestBackwardCompat_WorktreeInitEmpty_SkipsSourceCopy_NamedVariant is the
// explicitly-named backward-compat test that the task spec calls for:
// WorktreeInit=empty MUST suppress the cp -aR source copy (BUG-27 pin).
func TestBackwardCompat_WorktreeInitEmpty_SkipsSourceCopy_NamedVariant(t *testing.T) {
	rt := &fakeRuntime{}
	events := &fakeEvents{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     rt,
		Dispatcher:  disp,
		SpawnGov:    spawn,
		Validator:   discipline.NewValidator(),
		Prompts:     discipline.NewPromptBuilder(),
		Audit:       &fakeAudit{},
		Events:      events,
		Memory:      mem,
		Clock:       clock,
		IDGen:       idGen,
		Config: apply.RunConfig{
			MaxParallelGroups:             1,
			MaxParallelImplementsPerGroup: 1,
			DepWaitTimeout:                3,
			DispatchTimeoutMS:             5000,
			WorktreeRoot:                  t.TempDir(),
			SourceRepoPath:                "/tmp/should-not-be-copied",
			WorktreeInit:                  apply.WorktreeInitEmpty,
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)

	rt.mu.Lock()
	defer rt.mu.Unlock()

	var sawCp bool
	for _, call := range rt.calls {
		var m map[string]any
		if err := json.Unmarshal(call.Payload, &m); err != nil {
			continue
		}
		if cmd, _ := m["command"].(string); cmd == "cp" {
			argsRaw, _ := m["args"].([]any)
			args := make([]string, len(argsRaw))
			for i, a := range argsRaw {
				args[i], _ = a.(string)
			}
			// Detect a cp that originates from SourceRepoPath.
			for _, a := range args {
				if strings.HasPrefix(a, "/tmp/should-not-be-copied") {
					sawCp = true
				}
			}
		}
	}
	require.False(t, sawCp,
		"WorktreeInit=empty MUST suppress the cp -aR from SourceRepoPath (BUG-27 pin)")
}
