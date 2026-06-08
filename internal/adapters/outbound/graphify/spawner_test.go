package graphify_test

// spawner_test.go — D.6, D.7, D.8 (Strict TDD: RED tests first)
//
// Tests for GraphifySpawner.Build using a FakeExecRunner.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/graphify"
	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/stretchr/testify/require"
)

// fakeExecRunner implements initphase.ExecRunner for tests.
type fakeExecRunner struct {
	// key is the binary name; first match wins.
	results map[string]execResult
}

type execResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
	err      error
}

func (f *fakeExecRunner) Run(_ context.Context, name string, _ []string, _ initphase.ExecOpts) ([]byte, []byte, int, error) {
	if r, ok := f.results[name]; ok {
		return r.stdout, r.stderr, r.exitCode, r.err
	}
	return nil, nil, 127, errors.New("command not found")
}

// validGraphJSON is sample graph.json content.
const validGraphJSON = `{
	"nodes": [
		{"id": "a", "out_degree": 10},
		{"id": "b", "out_degree": 5},
		{"id": "c", "out_degree": 2}
	],
	"edges": [
		{"from": "a", "to": "b"},
		{"from": "b", "to": "c"}
	],
	"communities": [{"id": 1}, {"id": 2}]
}`

// D.6: GraphifySpawner.Build returns *GraphSummary with TotalNodes/TotalEdges/GodNodes
// when FakeExecRunner returns valid graph.json.
func TestGraphifySpawner_Build_Success(t *testing.T) {
	dir := t.TempDir()
	graphifyOutDir := filepath.Join(dir, "graphify-out")
	require.NoError(t, os.MkdirAll(graphifyOutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(graphifyOutDir, "graph.json"), []byte(validGraphJSON), 0o644))

	runner := &fakeExecRunner{
		results: map[string]execResult{
			"graphify": {stdout: []byte("graphify 0.8.35"), exitCode: 0},
		},
	}
	spawner := graphify.NewSpawner(runner, nil, 30_000)

	summary, version, err := spawner.Build(context.Background(), dir)
	require.NoError(t, err)
	require.NotNil(t, summary)
	require.Equal(t, "graphify 0.8.35", version)
	require.Equal(t, 3, summary.TotalNodes)
	require.Equal(t, 2, summary.TotalEdges)
	require.Equal(t, 2, summary.CommunityCount)
	require.NotEmpty(t, summary.GodNodes)
	require.Equal(t, "a", summary.GodNodes[0], "GodNodes must be sorted by out_degree desc")
}

// D.7: GraphifySpawner.Build returns (nil, "", ErrGraphifyDegraded) when graphify
// exits 127 (not found).
func TestGraphifySpawner_Build_MissingGraphify(t *testing.T) {
	runner := &fakeExecRunner{
		results: map[string]execResult{
			"graphify": {exitCode: 127, err: errors.New("graphify: command not found")},
		},
	}
	spawner := graphify.NewSpawner(runner, nil, 30_000)

	summary, version, err := spawner.Build(context.Background(), t.TempDir())
	require.Error(t, err)
	require.True(t, errors.Is(err, initphase.ErrGraphifyDegraded),
		"expected ErrGraphifyDegraded but got: %v", err)
	require.Nil(t, summary)
	require.Empty(t, version)
}

// D.8: GraphifySpawner.Build returns (nil, version, ErrGraphifyDegraded) when
// graph.json is malformed; version is still captured from --version step.
func TestGraphifySpawner_Build_MalformedGraphJSON(t *testing.T) {
	dir := t.TempDir()
	graphifyOutDir := filepath.Join(dir, "graphify-out")
	require.NoError(t, os.MkdirAll(graphifyOutDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(graphifyOutDir, "graph.json"), []byte(`{not valid json}`), 0o644))

	runner := &fakeExecRunner{
		results: map[string]execResult{
			"graphify": {stdout: []byte("graphify 0.8.35"), exitCode: 0},
		},
	}
	spawner := graphify.NewSpawner(runner, nil, 30_000)

	summary, version, err := spawner.Build(context.Background(), dir)
	require.Error(t, err)
	require.True(t, errors.Is(err, initphase.ErrGraphifyDegraded),
		"expected ErrGraphifyDegraded but got: %v", err)
	require.Nil(t, summary)
	require.NotEmpty(t, version, "version should be captured even when graph.json is malformed")
}
