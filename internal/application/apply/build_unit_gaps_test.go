package apply_test

// build_unit_gaps_test.go — Task 3.1: targeted unit tests for branches in
// build_registry.go, build_feedback.go, and run.go that were below the 85%
// gate before Slice 3 or were not explicitly named after the spec scenario.
//
// Scope (task 3.1): manifest detection for each type + none, stderr
// truncation edge-cases, build pass / fail→repair→pass /
// fail→budget-exhausted flows are covered BY NAME so the test suite reads
// as a spec mirror.  We ADD tests for uncovered internal helpers and probe
// error paths; we do NOT duplicate tests that already exist in
// build_feedback_test.go or build_registry_test.go.

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
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// ─── TruncateStderr edge cases ───────────────────────────────────────────────

// TestTruncateStderr_OneBytePastBudget exercises the boundary exactly one byte
// above the 4096-byte budget so the truncation branch is taken with the
// shortest possible tail.
func TestTruncateStderr_OneBytePastBudget(t *testing.T) {
	input := strings.Repeat("z", 4097)
	out, truncated := apply.TruncateStderr([]byte(input))
	require.True(t, truncated)
	require.Contains(t, out, "truncated")
	// head must be exactly 2048 'z'; tail must be exactly 2048 'z'.
	require.True(t, strings.HasPrefix(out, strings.Repeat("z", 2048)))
	require.True(t, strings.HasSuffix(out, strings.Repeat("z", 2048)))
}

// TestTruncateStderr_LargeInput confirms that very large stderr (e.g. a
// 100 KB compiler dump) is always capped to at most 4096 + indicator bytes.
func TestTruncateStderr_LargeInput_BudgetCapped(t *testing.T) {
	input := strings.Repeat("e", 100_000)
	out, truncated := apply.TruncateStderr([]byte(input))
	require.True(t, truncated)
	// stderrBudgetBytes == 4096; indicator is < 200 bytes.
	require.LessOrEqual(t, len(out), 4096+200)
}

// ─── DetectBuildPlan — explicit per-type named scenarios ─────────────────────

// TestDetectBuildPlan_GoMod_Named pins the go.mod → "go build ./..." scenario
// by name so the test output reads as a spec mirror.
func TestDetectBuildPlan_GoMod_Named(t *testing.T) {
	cwd := "/wt/go"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/go.mod": []byte("module example.com/x\ngo 1.26\n"),
	})
	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found, "go.mod present → rule must match")
	require.Equal(t, "go", plan.Command)
	require.Equal(t, []string{"build", "./..."}, plan.Args)
	require.Equal(t, "go.mod", plan.Manifest)
}

// TestDetectBuildPlan_PackageJSON_WithBuildScript_Named pins the package.json
// + scripts.build → "npm run build" scenario by name.
func TestDetectBuildPlan_PackageJSON_WithBuildScript_Named(t *testing.T) {
	cwd := "/wt/node"
	pkg, _ := json.Marshal(map[string]any{"scripts": map[string]string{"build": "tsc"}})
	rt := newRegistryRuntime(map[string][]byte{cwd + "/package.json": pkg})
	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "npm", plan.Command)
	require.Equal(t, []string{"run", "build"}, plan.Args)
}

// TestDetectBuildPlan_PackageJSON_NoBuildScript_SkipsNamed verifies
// package.json without scripts.build does not match — explicit named check.
func TestDetectBuildPlan_PackageJSON_NoBuildScript_SkipsNamed(t *testing.T) {
	cwd := "/wt/node-noscript"
	pkg, _ := json.Marshal(map[string]any{"scripts": map[string]string{"start": "node server.js"}})
	rt := newRegistryRuntime(map[string][]byte{cwd + "/package.json": pkg})
	_, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.False(t, found, "no scripts.build → skip")
}

// TestDetectBuildPlan_PubspecYAML_Dart_Named pins the pubspec.yaml + Dart
// entrypoint scenario (dart compile exe lib/main.dart) by name.
func TestDetectBuildPlan_PubspecYAML_Dart_Named(t *testing.T) {
	cwd := "/wt/dart"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/pubspec.yaml":  []byte("name: cli_tool\n"),
		cwd + "/lib/main.dart": []byte("void main() {}"),
	})
	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "dart", plan.Command)
}

// TestDetectBuildPlan_NoManifest_Named pins the "no recognised manifest →
// skip" scenario by name (the backward-compat invariant).
func TestDetectBuildPlan_NoManifest_Named(t *testing.T) {
	rt := newRegistryRuntime(nil)
	_, found, err := apply.DetectBuildPlan(context.Background(), &emptyFSRuntime{}, "/wt/empty")
	require.NoError(t, err)
	require.False(t, found, "empty worktree → no manifest → skip")
	_ = rt // suppress unused warning
}

// emptyFSRuntime always returns exit 1 from "test -f"; used to confirm skip.
type emptyFSRuntime struct{}

func (r *emptyFSRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	if cmd == "test" {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

// ─── readFileInWorktree — error path ─────────────────────────────────────────

// TestDetectBuildPlan_ReadFileError_Propagates exercises the readFileInWorktree
// non-zero-exit path via a runtime that makes cat fail. packageJSONRule hits
// this path first in the ordered resolver chain.
type catNonZeroRuntime struct{}

func (r *catNonZeroRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
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
		// go.mod absent, package.json present.
		if len(args) >= 2 {
			if hasSuffix(args[1], "/go.mod") {
				return &outbound.ExecutionReceipt{ExitCode: 1}, nil
			}
			if hasSuffix(args[1], "/package.json") {
				return &outbound.ExecutionReceipt{ExitCode: 0}, nil
			}
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	case "cat":
		// cat exits non-zero — readFileInWorktree must surface the error.
		return &outbound.ExecutionReceipt{ExitCode: 2, Stderr: []byte("permission denied")}, nil
	}
	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

// TestDetectBuildPlan_PackageJSON_CatNonZeroExit_ReturnsError exercises the
// readFileInWorktree non-zero-exit branch (distinct from the error-return
// branch already covered by catErrorRuntime in build_registry_test.go).
func TestDetectBuildPlan_PackageJSON_CatNonZeroExit_ReturnsError(t *testing.T) {
	_, _, err := apply.DetectBuildPlan(context.Background(), &catNonZeroRuntime{}, "/wt/node-no-perm")
	require.Error(t, err, "cat non-zero exit must propagate as an error")
}

// ─── Build pass / fail→repair→pass / fail→exhausted (explicit names) ─────────

// TestBuildGate_BuildPass_Named pins the "go.mod detected, build exits 0"
// scenario by spec scenario name (mirrors spec § build-pass invariant).
func TestBuildGate_BuildPass_Named(t *testing.T) {
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
}

// TestBuildGate_FailRepairPass_Named pins the "first build fails, repair
// dispatched, second build passes" scenario by name (spec § repair-pass).
func TestBuildGate_FailRepairPass_Named(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 0})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))
	types := events.types()
	require.Contains(t, types, inbound.EventApplyBuildFailed)
	require.Contains(t, types, inbound.EventApplyBuildPassed)
	require.Contains(t, types, inbound.EventApplyGroupCompleted)
}

// TestBuildGate_BudgetExhausted_Named pins the "all MaxAttempts fail →
// budget exhausted → group failed" scenario by name (spec § budget-exhausted).
func TestBuildGate_BudgetExhausted_Named(t *testing.T) {
	svc, events := newRunServiceWildcard(t, []int{1, 1, 1})
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", string(env.Status))
	types := events.types()
	require.Contains(t, types, inbound.EventApplyGroupFailed)
	require.NotContains(t, types, inbound.EventApplyGroupCompleted)
}

// ─── materializeWorktrees — error path ───────────────────────────────────────

// TestMaterializeWorktrees_MkdirError_EmitsErrorEvent exercises the
// materialize mkdir-transport-error branch: when Runtime.Execute returns an
// error (not just non-zero exit) for the materialize mkdir, apply.materialize.error
// is emitted for that group but the overall phase still completes (non-fatal).
//
// NOTE: materializeWorktrees only triggers the error event on transport-level
// errors from Execute (not non-zero exit codes), because the runtime layer
// treats non-zero exit as a normal receipt.
type mkdirErrorRuntime struct {
	mu         sync.Mutex
	mkdirCount int
}

func (r *mkdirErrorRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 0}, nil
	}
	cmd, _ := m["command"].(string)
	switch cmd {
	case "mkdir":
		r.mu.Lock()
		r.mkdirCount++
		count := r.mkdirCount
		r.mu.Unlock()
		if count == 1 {
			// First mkdir: worktree setup — succeeds.
			return &outbound.ExecutionReceipt{ExitCode: 0}, nil
		}
		// Second mkdir: materialize target — transport-level error.
		return nil, errors.New("mkdir: transport error: connection refused")
	case "test":
		// No manifest → skip build gate (noManifest behaviour).
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

func TestMaterializeWorktrees_MkdirError_EmitsErrorEvent(t *testing.T) {
	rt := &mkdirErrorRuntime{}
	target := t.TempDir()

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
			TargetPath:                    target,
			// No SourceRepoPath → only one mkdir in createWorktrees per group.
		},
	}
	svc := apply.NewRun(deps)
	c := mkChange(t, "feat-x")
	p := mkPhase(t, c)

	// Phase still completes even though materialize mkdir transport-errors.
	env, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{ChangeID: c.ID()})
	require.NoError(t, err)
	require.Equal(t, "DONE", string(env.Status))

	types := events.types()
	require.Contains(t, types, inbound.EventApplyMaterializeStarted)
	require.Contains(t, types, inbound.EventApplyMaterializeError,
		"mkdir transport error during materialize must emit apply.materialize.error")
}
