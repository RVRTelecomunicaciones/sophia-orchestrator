package apply_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

// ----- programmable fake runtime for registry tests -------------------

// registryFakeRuntime answers test-existence and cat commands from a
// virtual filesystem seeded per test case.
type registryFakeRuntime struct {
	mu    sync.Mutex
	files map[string][]byte // path → content; path absent ⇒ file does not exist
}

func newRegistryRuntime(files map[string][]byte) *registryFakeRuntime {
	if files == nil {
		files = map[string][]byte{}
	}
	return &registryFakeRuntime{files: files}
}

func (r *registryFakeRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
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

	r.mu.Lock()
	defer r.mu.Unlock()

	switch cmd {
	case "test":
		// test -f <path>
		if len(args) >= 2 && args[0] == "-f" {
			if _, ok := r.files[args[1]]; ok {
				return &outbound.ExecutionReceipt{ExitCode: 0}, nil
			}
			return &outbound.ExecutionReceipt{ExitCode: 1}, nil
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil

	case "cat":
		if len(args) >= 1 {
			if content, ok := r.files[args[0]]; ok {
				return &outbound.ExecutionReceipt{ExitCode: 0, Stdout: content}, nil
			}
			return &outbound.ExecutionReceipt{ExitCode: 1}, nil
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}

	return &outbound.ExecutionReceipt{ExitCode: 0}, nil
}

// ----- DetectBuildPlan tests -----------------------------------------

func TestDetectBuildPlan_GoMod_ReturnsGoBuild(t *testing.T) {
	cwd := "/wt/go-project"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/go.mod": []byte("module example.com/foo\ngo 1.21\n"),
	})

	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, plan)
	require.Equal(t, "go", plan.Command)
	require.Equal(t, []string{"build", "./..."}, plan.Args)
	require.Equal(t, "go.mod", plan.Manifest)
	require.Greater(t, plan.TimeoutMS, 0)
}

func TestDetectBuildPlan_PackageJSON_WithBuildScript_ReturnsNpmRun(t *testing.T) {
	cwd := "/wt/node-project"
	pkg, _ := json.Marshal(map[string]any{
		"name":    "my-app",
		"scripts": map[string]string{"build": "tsc && vite build"},
	})
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/package.json": pkg,
	})

	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, plan)
	require.Equal(t, "npm", plan.Command)
	require.Equal(t, []string{"run", "build"}, plan.Args)
	require.Equal(t, "package.json", plan.Manifest)
}

func TestDetectBuildPlan_PackageJSON_WithoutBuildScript_ReturnsSkip(t *testing.T) {
	cwd := "/wt/node-no-build"
	pkg, _ := json.Marshal(map[string]any{
		"name":    "my-app",
		"scripts": map[string]string{"test": "jest"},
	})
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/package.json": pkg,
	})

	_, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.False(t, found, "no scripts.build → rule must not match")
}

func TestDetectBuildPlan_PackageJSON_MalformedJSON_ReturnsSkip(t *testing.T) {
	cwd := "/wt/node-bad"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/package.json": []byte("{invalid json"),
	})

	_, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.False(t, found, "malformed package.json must be treated as no build script")
}

func TestDetectBuildPlan_PubspecYAML_FlutterMarker_ReturnsFlutterBuild(t *testing.T) {
	cwd := "/wt/flutter-project"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/pubspec.yaml":     []byte("name: my_app\ndependencies:\n  flutter:\n    sdk: flutter\n"),
		cwd + "/.flutter-plugins": []byte(""),
	})

	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, plan)
	require.Equal(t, "flutter", plan.Command)
	require.Equal(t, "pubspec.yaml", plan.Manifest)
}

func TestDetectBuildPlan_PubspecYAML_DartEntrypoint_ReturnsDartBuild(t *testing.T) {
	cwd := "/wt/dart-project"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/pubspec.yaml":  []byte("name: my_cli\nenvironment:\n  sdk: '>=3.0.0'\n"),
		cwd + "/lib/main.dart": []byte("void main() {}"),
	})

	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, plan)
	require.Equal(t, "dart", plan.Command)
	require.Equal(t, "pubspec.yaml", plan.Manifest)
}

func TestDetectBuildPlan_PubspecYAML_NoEntrypoint_ReturnsSkip(t *testing.T) {
	cwd := "/wt/dart-no-main"
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/pubspec.yaml": []byte("name: my_lib\n"),
		// No .flutter-plugins, no sdk: flutter, no lib/main.dart
	})

	_, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.False(t, found, "pubspec.yaml with no entrypoint and no flutter markers → skip")
}

func TestDetectBuildPlan_NoManifest_ReturnsSkip(t *testing.T) {
	cwd := "/wt/empty"
	rt := newRegistryRuntime(nil) // empty virtual fs

	_, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.False(t, found)
}

// TestDetectBuildPlan_GoRuleTakesPrecedence verifies that when both go.mod
// and package.json exist, the Go rule fires first (ordered resolver chain).
func TestDetectBuildPlan_GoRuleTakesPrecedence(t *testing.T) {
	cwd := "/wt/mixed"
	pkg, _ := json.Marshal(map[string]any{
		"scripts": map[string]string{"build": "npm run compile"},
	})
	rt := newRegistryRuntime(map[string][]byte{
		cwd + "/go.mod":       []byte("module x"),
		cwd + "/package.json": pkg,
	})

	plan, found, err := apply.DetectBuildPlan(context.Background(), rt, cwd)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "go", plan.Command, "Go rule must fire before Node rule in priority order")
}

// ----- Runtime error paths --------------------------------------------

// alwaysErrRuntime returns an error from Execute for every call.
type alwaysErrRuntime struct{}

func (r *alwaysErrRuntime) Execute(_ context.Context, _ outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	return nil, errors.New("simulated runtime error")
}

// TestDetectBuildPlan_RuntimeError_GoProbe_ReturnsError verifies that when
// the runtime probe for go.mod fails, DetectBuildPlan surfaces the error.
func TestDetectBuildPlan_RuntimeError_GoProbe_ReturnsError(t *testing.T) {
	_, _, err := apply.DetectBuildPlan(context.Background(), &alwaysErrRuntime{}, "/any/cwd")
	require.Error(t, err, "runtime probe failure must be propagated")
}

// TestDetectBuildPlan_PackageJSON_RuntimeCatError_ReturnsSkip exercises the
// cat-error path inside packageJSONRule: file exists (test -f exits 0) but
// cat fails, so the rule returns skip (false, nil).
type catErrorRuntime struct {
	mu    sync.Mutex
	calls int
}

func (r *catErrorRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	}
	cmd, _ := m["command"].(string)
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	switch cmd {
	case "test":
		// Pretend package.json exists, go.mod does not.
		argsRaw, _ := m["args"].([]any)
		args := make([]string, len(argsRaw))
		for i, a := range argsRaw {
			args[i], _ = a.(string)
		}
		if len(args) >= 2 {
			if hasGoMod := hasSuffix(args[1], "/go.mod"); hasGoMod {
				return &outbound.ExecutionReceipt{ExitCode: 1}, nil
			}
			if hasPkg := hasSuffix(args[1], "/package.json"); hasPkg {
				return &outbound.ExecutionReceipt{ExitCode: 0}, nil
			}
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	case "cat":
		// Cat always fails.
		return nil, errors.New("cat: permission denied")
	}
	return &outbound.ExecutionReceipt{ExitCode: 1}, nil
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func TestDetectBuildPlan_PackageJSON_CatError_ReturnsError(t *testing.T) {
	// catErrorRuntime makes "test -f package.json" succeed but "cat package.json" fail.
	// The packageJSONRule should propagate the cat error.
	rt := &catErrorRuntime{}
	_, _, err := apply.DetectBuildPlan(context.Background(), rt, "/wt/node")
	require.Error(t, err, "cat failure must propagate as an error from the Node rule")
}

// TestDetectBuildPlan_PubspecYAML_CatReadError_ReturnsGraceful exercises
// the isFlutterProject pubspec read error path: pubspec.yaml exists but
// .flutter-plugins does not and reading pubspec.yaml returns an error.
// This is a best-effort path → DetectBuildPlan should still continue
// and check the entrypoint.
type pubspecCatErrorRuntime struct{}

func (r *pubspecCatErrorRuntime) Execute(_ context.Context, req outbound.ExecutionRequest) (*outbound.ExecutionReceipt, error) {
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
		if len(args) >= 2 {
			// go.mod absent, pubspec.yaml present, others absent
			if hasSuffix(args[1], "/pubspec.yaml") {
				return &outbound.ExecutionReceipt{ExitCode: 0}, nil
			}
		}
		return &outbound.ExecutionReceipt{ExitCode: 1}, nil
	case "cat":
		// Reading pubspec.yaml itself fails.
		return nil, errors.New("cat: permission denied")
	}
	return &outbound.ExecutionReceipt{ExitCode: 1}, nil
}

func TestDetectBuildPlan_PubspecYAML_CatError_NilEntrpoint_Skip(t *testing.T) {
	// pubspec.yaml exists, .flutter-plugins absent, cat fails → isFlutterProject
	// returns false via the best-effort nil-err branch. No lib/main.dart either.
	// Result: no plan, skip.
	rt := &pubspecCatErrorRuntime{}
	_, found, err := apply.DetectBuildPlan(context.Background(), rt, "/wt/dart-broken")
	require.NoError(t, err)
	require.False(t, found)
}
