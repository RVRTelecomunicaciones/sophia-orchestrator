package apply_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/session"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// ----- TruncateStderr tests -------------------------------------------

func TestTruncateStderr_ShortInput_NotTruncated(t *testing.T) {
	input := strings.Repeat("x", 100)
	out, truncated := apply.TruncateStderr([]byte(input))
	require.False(t, truncated)
	require.Equal(t, input, out)
}

func TestTruncateStderr_ExactBudget_NotTruncated(t *testing.T) {
	// stderrBudgetBytes == 4096; exactly at budget → not truncated.
	input := strings.Repeat("y", 4096)
	out, truncated := apply.TruncateStderr([]byte(input))
	require.False(t, truncated)
	require.Equal(t, input, out)
}

func TestTruncateStderr_OverBudget_TruncatesWithIndicator(t *testing.T) {
	// First 2048 bytes: 'a'. Next 2048+100 bytes: 'b'.
	// After truncation: head = first 2048 'a', tail = last 2048 'b',
	// indicator in between.
	head := strings.Repeat("a", 2048)
	tail := strings.Repeat("b", 2048+100)
	input := head + tail

	out, truncated := apply.TruncateStderr([]byte(input))
	require.True(t, truncated, "must be marked truncated")
	require.Contains(t, out, "truncated", "truncation indicator must appear in output")
	require.True(t, strings.HasPrefix(out, strings.Repeat("a", 2048)),
		"head must be the first 2048 'a' bytes")
	require.True(t, strings.HasSuffix(out, strings.Repeat("b", 2048)),
		"tail must be the last 2048 'b' bytes")
	// Total must not blow past budget + indicator.
	require.LessOrEqual(t, len(out), 4096+200)
}

func TestTruncateStderr_EmptyInput_NotTruncated(t *testing.T) {
	out, truncated := apply.TruncateStderr(nil)
	require.False(t, truncated)
	require.Empty(t, out)
}

// ----- wildcardGoModRuntime ------------------------------------------
// Answers "test -f …/go.mod" as true for ANY path so tests do not need
// to know the exact worktree path the RunService will compute.

type wildcardGoModRuntime struct {
	mu           sync.Mutex
	buildResults []int
	buildIdx     int
}

func (r *wildcardGoModRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	argsRaw, _ := m["args"].([]any)
	args := make([]string, len(argsRaw))
	for i, a := range argsRaw {
		args[i], _ = a.(string)
	}

	switch cmd {
	case "test":
		// Accept "test -f …/go.mod" regardless of base path.
		if len(args) >= 2 && args[0] == "-f" && strings.HasSuffix(args[1], "/go.mod") {
			return &outbound.ExecutionReceipt{ExitCode: 0}, nil
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil

	case "mkdir", "cp":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil

	default:
		// Treat as a build command.
		r.mu.Lock()
		idx := r.buildIdx
		if idx < len(r.buildResults) {
			r.buildIdx++
		}
		results := r.buildResults
		r.mu.Unlock()

		exitCode := 0
		if idx < len(results) {
			exitCode = results[idx]
		}
		var stderrBytes []byte
		if exitCode != 0 {
			stderrBytes = []byte("./main.go:5:1: syntax error: unexpected EOF")
		}
		return &outbound.ExecutionReceipt{
			ExitCode:   exitCode,
			Stderr:     stderrBytes,
			DurationMS: 200,
		}, nil
	}
}

// newRunServiceWildcard creates a RunService whose runtime uses
// wildcardGoModRuntime (go.mod present everywhere, build exit codes
// driven by buildResults).
func newRunServiceWildcard(t *testing.T, buildResults []int) (*apply.RunService, *fakeEvents) {
	t.Helper()
	wrt := &wildcardGoModRuntime{buildResults: buildResults}

	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     wrt,
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
		},
	}
	return apply.NewRun(deps), events
}

// ----- Build-gate integration tests via Execute ----------------------

// TestBuildGate_NoManifest_GroupCompletesImmediately verifies backward-compat:
// no go.mod → build gate skipped → group completes on task self-report alone.
// Uses noManifestRuntime which returns exit 1 for all "test -f" probes.
func TestBuildGate_NoManifest_GroupCompletesImmediately(t *testing.T) {
	rt := &noManifestRuntime{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"no manifest → no build start event")
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_GoManifest_PassOnFirstAttempt verifies: go.mod detected,
// build exits 0 → apply.build.passed + group.completed.
func TestBuildGate_GoManifest_PassOnFirstAttempt(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{0})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildStarted)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
	require.NotContains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_BuildFailThenPassAfterRepair verifies the feedback loop:
// first build fails → repair dispatched → second build passes → group completes.
func TestBuildGate_BuildFailThenPassAfterRepair(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 0}) // fail, pass
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildStarted)
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_BudgetExhausted_GroupFailed verifies: 3 build failures
// (== MaxAttempts) → ErrBuildBudgetExhausted → group.failed.
func TestBuildGate_BudgetExhausted_GroupFailed(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 1, 1})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", string(env.Status))

	types := events.types()
	buildFailedCount := 0
	for _, et := range types {
		if et == inbound.EventApplyBuildFailed {
			buildFailedCount++
		}
	}
	require.GreaterOrEqual(t, buildFailedCount, 3,
		"must emit apply.build.failed for every exhausted attempt")
	require.Contains(t, types, inbound.EventApplyGroupFailed)
	require.NotContains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_BuildFailedPayloadContainsStderr verifies spec requirement:
// apply.build.failed payload MUST include the captured stderr.
func TestBuildGate_BuildFailedPayloadContainsStderr(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 1, 1})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)

	events.mu.Lock()
	defer events.mu.Unlock()
	var foundFailedWithStderr bool
	for _, ev := range events.events {
		if ev.Type != inbound.EventApplyBuildFailed {
			continue
		}
		// Payload is typed ApplyBuildFailedPayload; marshal+unmarshal to check.
		raw, marshalErr := json.Marshal(ev.Payload)
		require.NoError(t, marshalErr)
		var p inbound.ApplyBuildFailedPayload
		require.NoError(t, json.Unmarshal(raw, &p))
		if p.Stderr != "" {
			foundFailedWithStderr = true
		}
	}
	require.True(t, foundFailedWithStderr,
		"at least one apply.build.failed event must carry non-empty Stderr")
}

// TestBuildGate_BuildPassedEmitsPayloadWithDuration verifies that
// apply.build.passed carries the expected fields including duration.
func TestBuildGate_BuildPassedEmitsPayloadWithDuration(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{0})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)

	events.mu.Lock()
	defer events.mu.Unlock()
	var foundPassed bool
	for _, ev := range events.events {
		if ev.Type != inbound.EventApplyBuildPassed {
			continue
		}
		raw, marshalErr := json.Marshal(ev.Payload)
		require.NoError(t, marshalErr)
		var payload inbound.ApplyBuildPassedPayload
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.NotEmpty(t, payload.GroupID)
		require.Equal(t, "go.mod", payload.Manifest)
		require.Equal(t, "go", payload.Command)
		foundPassed = true
	}
	require.True(t, foundPassed, "apply.build.passed event must be emitted")
}

// TestBuildGate_SSEEventsAreKnownTypes verifies that the three new build
// events are in the IsKnownEventType registry (compile-time constant test
// mirror at the integration layer).
func TestBuildGate_SSEEventsAreKnownTypes(t *testing.T) {
	for _, name := range []string{
		inbound.EventApplyBuildStarted,
		inbound.EventApplyBuildPassed,
		inbound.EventApplyBuildFailed,
	} {
		require.True(t, inbound.IsKnownEventType(name),
			"build event %q must be registered in knownEventTypes", name)
	}
}

// TestBuildGate_BuildStartedEmitsGroupAndManifest verifies the started payload.
func TestBuildGate_BuildStartedEmitsGroupAndManifest(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{0})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)

	events.mu.Lock()
	defer events.mu.Unlock()
	var found bool
	for _, ev := range events.events {
		if ev.Type != inbound.EventApplyBuildStarted {
			continue
		}
		raw, _ := json.Marshal(ev.Payload)
		var payload inbound.ApplyBuildStartedPayload
		require.NoError(t, json.Unmarshal(raw, &payload))
		require.NotEmpty(t, payload.GroupID)
		require.Equal(t, "go.mod", payload.Manifest)
		require.Equal(t, "go", payload.Command)
		require.Equal(t, 1, payload.Attempt, "first attempt must be 1")
		found = true
	}
	require.True(t, found, "apply.build.started must be emitted")
}

// TestBuildGate_RepairAttemptIncrements verifies that build attempt number
// increments across failures (attempt 1 → 2 → 3 before budget exhaustion).
func TestBuildGate_RepairAttemptIncrements(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 1, 1})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	_, _ = svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})

	events.mu.Lock()
	defer events.mu.Unlock()

	var attempts []int
	for _, ev := range events.events {
		if ev.Type != inbound.EventApplyBuildStarted {
			continue
		}
		raw, _ := json.Marshal(ev.Payload)
		var payload inbound.ApplyBuildStartedPayload
		if err := json.Unmarshal(raw, &payload); err == nil {
			attempts = append(attempts, payload.Attempt)
		}
	}
	require.Equal(t, []int{1, 2, 3}, attempts,
		"build attempt counter must increment 1→2→3 across repair iterations")
}

// TestBuildGate_BackwardCompat_WorktreeInitEmpty pins the no-manifest +
// empty worktree backward compat contract from spec apply-orchestration §
// "Backward-Compat Invariant": no build runs, group completes on self-report.
func TestBuildGate_BackwardCompat_WorktreeInitEmpty(t *testing.T) {
	// The default fakeRuntime from run_test.go does NOT expose go.mod:
	// "test -f …/go.mod" returns exit 0 but only because fakeRuntime
	// always returns exit 0. We need to differentiate — use an explicit
	// no-manifest runtime.
	noManifestRT := &noManifestRuntime{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     noManifestRT,
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
			WorktreeInit:                  apply.WorktreeInitEmpty,
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"WorktreeInit=empty + no manifest → no build must run")
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// noManifestRuntime always returns exit 1 for "test -f" probes, so
// DetectBuildPlan finds no manifest. mkdir/cp succeed for worktree setup.
type noManifestRuntime struct{}

func (r *noManifestRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	switch cmd {
	case "test":
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil // file does not exist
	case "mkdir", "cp":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	default:
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	}
}

// TestPhaseApply_WildcardRuntimeAllCalls_NoStray verifies that no
// unexpected runtime calls appear when build gate is skipped (no-manifest).
func TestPhaseApply_NoManifestRuntime_NoStray(t *testing.T) {
	rt := &noManifestRuntime{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.NotContains(t, types, inbound.EventApplyBuildStarted)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// Verify all three build events are in IsKnownEventType.
func TestEventTypes_BuildEventsRegistered(t *testing.T) {
	for _, name := range []string{
		inbound.EventApplyBuildStarted,
		inbound.EventApplyBuildPassed,
		inbound.EventApplyBuildFailed,
	} {
		t.Run(name, func(t *testing.T) {
			require.True(t, inbound.IsKnownEventType(name))
		})
	}
}

// phase type helper — tests use inbound.RunPhaseInput with a Phase
// produced by mkPhase which already sets PhaseApply. The event system
// needs the type string to match inbound constants; verify here.
func TestPhaseApplyTypeString(t *testing.T) {
	require.Equal(t, "apply", string(phase.PhaseApply))
}

// ----- Aider dispatcher coverage for dispatchBuildRepair ----------------

// aiderDispatcher mimics an aider-style dispatcher: returns nil EnvelopeRaw
// and AdapterID="aider" for repair dispatches so the synthesize-from-git
// branch in dispatchBuildRepair is exercised.
type aiderDispatcher struct {
	taskDisp *fakeDispatcher // handles implement dispatches
}

func (d *aiderDispatcher) Provider() session.Provider          { return session.ProviderOpenCode }
func (d *aiderDispatcher) SuggestedMaxConcurrent() int         { return 4 }
func (d *aiderDispatcher) HealthCheck(_ context.Context) error { return nil }

func (d *aiderDispatcher) Dispatch(_ context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	// Implement-agent prompts contain "Worktree:"; repair prompts contain "Build Repair".
	if strings.Contains(req.Prompt, "Build Repair") {
		// Repair dispatch → aider path (no envelope).
		return &outbound.DispatchResult{AdapterID: "aider"}, nil
	}
	return d.taskDisp.Dispatch(context.Background(), req)
}

func newRunServiceWildcardWithAiderRepair(t *testing.T, buildResults []int) (*apply.RunService, *fakeEvents) {
	t.Helper()
	wrt := &wildcardGoModRuntime{buildResults: buildResults}

	board := newFakeBoardRepo()
	taskDisp := &fakeDispatcher{}
	disp := &aiderDispatcher{taskDisp: taskDisp}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     wrt,
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
		},
	}
	return apply.NewRun(deps), events
}

// TestBuildGate_AiderRepairDispatch_HitsAiderBranch exercises the
// dispatchBuildRepair aider path: repair dispatch returns AdapterID=aider,
// the synthesize-from-git branch is entered. The build then passes on the
// next attempt so the group completes.
func TestBuildGate_AiderRepairDispatch_HitsAiderBranch(t *testing.T) {
	svc, events := newRunServiceWildcardWithAiderRepair(t, []int{1, 0}) // fail, pass
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
}

// ----- erroring runtime (executeBuild error path) -----------------------

// errorOnBuildRuntime returns an error from Execute when the command is
// a build executable (not test/mkdir/cp), simulating a runtime transport
// failure during build execution.
type errorOnBuildRuntime struct {
	mu       sync.Mutex
	hitCount int
}

func (r *errorOnBuildRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	switch cmd {
	case "test":
		// go.mod exists
		argsRaw, _ := m["args"].([]any)
		args := make([]string, len(argsRaw))
		for i, a := range argsRaw {
			args[i], _ = a.(string)
		}
		if len(args) >= 2 && args[0] == "-f" && strings.HasSuffix(args[1], "/go.mod") {
			return &outbound.ExecutionReceipt{ExitCode: 0}, nil
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	case "mkdir", "cp":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	default:
		// Build execution → runtime error.
		r.mu.Lock()
		r.hitCount++
		r.mu.Unlock()
		return nil, errors.New("runtime transport failure: connection refused")
	}
}

// ----- dispatchBuildRepair error path --------------------------------

// repairFailDispatcher fails the repair dispatch (returns an error for
// repair prompts) to exercise the error-path inside dispatchBuildRepair.
type repairFailDispatcher struct {
	taskDisp *fakeDispatcher
}

func (d *repairFailDispatcher) Provider() session.Provider          { return session.ProviderOpenCode }
func (d *repairFailDispatcher) SuggestedMaxConcurrent() int         { return 4 }
func (d *repairFailDispatcher) HealthCheck(_ context.Context) error { return nil }

func (d *repairFailDispatcher) Dispatch(_ context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error) {
	if strings.Contains(req.Prompt, "Build Repair") {
		return nil, errors.New("simulated dispatch failure")
	}
	return d.taskDisp.Dispatch(context.Background(), req)
}

// TestBuildGate_RepairDispatchError_ContinuesLoop verifies that a dispatch
// failure during repair does NOT abort the build loop — the next build
// iteration runs and (if it still fails) drains the budget.
func TestBuildGate_RepairDispatchError_ContinuesLoop(t *testing.T) {
	wrt := &wildcardGoModRuntime{buildResults: []int{1, 1, 1}} // all fail
	board := newFakeBoardRepo()
	taskDisp := &fakeDispatcher{}
	disp := &repairFailDispatcher{taskDisp: taskDisp}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     wrt,
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	// Build budget exhausted → BLOCKED.
	require.Equal(t, "BLOCKED", string(env.Status))

	types := events.types()
	// The repair dispatch error must surface as apply.dispatch.error.
	require.Contains(t, types, inbound.EventApplyDispatchError,
		"repair dispatch failure must emit apply.dispatch.error")
	require.Contains(t, types, inbound.EventApplyGroupFailed)
}

// TestBuildGate_RuntimeTransportError_CountsAsFail exercises the
// executeBuild error path: when runtime.Execute returns an error, the
// build counts as a failed attempt (exit -1).
func TestBuildGate_RuntimeTransportError_CountsAsFail(t *testing.T) {
	errRT := &errorOnBuildRuntime{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     errRT,
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	// Runtime errors count as failed attempts; after MaxAttempts the group fails.
	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyGroupFailed)
}

// ----- DetectBuildPlan error path in feedback loop --------------------

// probeErrorRuntime makes the go.mod probe fail (runtime error) to exercise
// the detection-error → SkipBuild path in runGroupBuildFeedbackLoop.
type probeErrorRuntime struct {
	mu       sync.Mutex
	probeHit bool
}

func (r *probeErrorRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	switch cmd {
	case "test":
		r.mu.Lock()
		r.probeHit = true
		r.mu.Unlock()
		return nil, errors.New("runtime connection refused")
	case "mkdir", "cp":
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	}
	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

// TestBuildGate_DetectionError_SkipsBuildAndCompletes verifies that when
// manifest detection fails (runtime error), the loop falls back to SkipBuild
// and the group completes normally (backward compat preserved).
func TestBuildGate_DetectionError_SkipsBuildAndCompletes(t *testing.T) {
	rt := &probeErrorRuntime{}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyWorktreeError,
		"detection error must emit apply.worktree.error")
	require.NotContains(t, types, inbound.EventApplyBuildStarted,
		"detection error must skip the build gate")
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_BuildRepairPromptNoPriorContext exercises assembleBuildRepairPrompt
// with empty priorContext (the "no prior context" branch).
func TestBuildGate_BuildRepairPromptNoPriorContext(t *testing.T) {
	// fail then pass → repair fired with no spec/design in memory → priorContext="".
	wrt := &wildcardGoModRuntime{buildResults: []int{1, 0}}
	board := newFakeBoardRepo()
	disp := &fakeDispatcher{}
	spawn := &fakeSpawnGov{}
	events := &fakeEvents{}
	mem := newFakeMemory()
	mem.putTasksList("feat-x", defaultTasksListJSON())
	// No spec/design records → priorContext="" in assembleBuildRepairPrompt.

	clock := shared.FixedClock(time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC))
	idGen := shared.NewSystemIDGenerator(clock)

	deps := apply.RunDeps{
		BoardRepo:   board,
		SessionRepo: newFakeSessionRepo(),
		Runtime:     wrt,
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
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
}
