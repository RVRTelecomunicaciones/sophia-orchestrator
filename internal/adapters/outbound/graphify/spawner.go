// Package graphify provides the GraphifySpawner adapter (Pattern B: CLI per-query).
// It runs `graphify update <repoRoot>` and parses graphify-out/graph.json.
// All subprocess calls go through the injected ExecRunner interface so unit
// tests never spawn real processes.
package graphify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
)

// Spawner implements initphase.GraphifySpawner using exec.Command (via ExecRunner).
type Spawner struct {
	runner    initphase.ExecRunner
	logger    *slog.Logger
	timeoutMS int
}

// NewSpawner constructs a Spawner.
//
//   - runner:    ExecRunner used for all subprocess calls (inject a fake in tests).
//   - logger:    optional slog logger (nil = discard).
//   - timeoutMS: per-command timeout in milliseconds; 0 uses 30_000 default.
func NewSpawner(runner initphase.ExecRunner, logger *slog.Logger, timeoutMS int) *Spawner {
	if timeoutMS <= 0 {
		timeoutMS = 30_000
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(nilWriter{}, nil))
	}
	return &Spawner{runner: runner, logger: logger, timeoutMS: timeoutMS}
}

// Build runs graphify update <repoRoot> and parses graphify-out/graph.json.
// All errors wrap ErrGraphifyDegraded so callers can errors.Is-check for
// degraded mode.
//
// Steps:
//  1. `graphify --version` — captures version; failure wraps ErrGraphifyDegraded.
//  2. `graphify update <repoRoot>` with timeoutMS deadline.
//  3. Read <repoRoot>/graphify-out/graph.json.
//  4. Parse: GodNodes = top-10 by out_degree.
func (s *Spawner) Build(ctx context.Context, repoRoot string) (*detector.GraphSummary, string, error) {
	// Step 1: graphify --version.
	stdout, stderr, code, err := s.runner.Run(ctx, "graphify", []string{"--version"}, initphase.ExecOpts{
		TimeoutMS: s.timeoutMS,
	})
	if err != nil || code != 0 {
		msg := ""
		if len(stderr) > 0 {
			msg = ": " + strings.TrimSpace(string(stderr))
		} else if err != nil {
			msg = ": " + err.Error()
		}
		return nil, "", fmt.Errorf("%w: graphify --version exit %d%s", initphase.ErrGraphifyDegraded, code, msg)
	}
	version := strings.TrimSpace(string(stdout))

	// Step 2: graphify update <repoRoot>.
	_, stderr, code, err = s.runner.Run(ctx, "graphify", []string{"update", repoRoot}, initphase.ExecOpts{
		Dir:       repoRoot,
		TimeoutMS: s.timeoutMS,
	})
	if err != nil || code != 0 {
		msg := ""
		if len(stderr) > 0 {
			msg = ": " + strings.TrimSpace(string(stderr))
		} else if err != nil {
			msg = ": " + err.Error()
		}
		s.logger.Warn("graphify update failed; degraded mode", "code", code, "detail", msg)
		return nil, version, fmt.Errorf("%w: graphify update exit %d%s", initphase.ErrGraphifyDegraded, code, msg)
	}

	// Step 3: read graph.json.
	graphPath := filepath.Join(repoRoot, "graphify-out", "graph.json")
	data, err := os.ReadFile(graphPath) // #nosec G304 -- path is under the caller-provided repoRoot
	if err != nil {
		return nil, version, fmt.Errorf("%w: read graph.json: %w", initphase.ErrGraphifyDegraded, err)
	}

	// Step 4: parse graph.json.
	summary, err := parseGraphJSON(data)
	if err != nil {
		return nil, version, fmt.Errorf("%w: parse graph.json: %w", initphase.ErrGraphifyDegraded, err)
	}
	return summary, version, nil
}

// graphNode is used internally when parsing graph.json.
type graphNode struct {
	ID        string `json:"id"`
	OutDegree int    `json:"out_degree"`
}

// graphEdge is used internally when parsing graph.json.
type graphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// graphCommunity is used internally when parsing graph.json.
type graphCommunity struct {
	ID int `json:"id"`
}

// graphJSON is the minimal shape of graphify-out/graph.json.
type graphJSON struct {
	Nodes       []graphNode      `json:"nodes"`
	Edges       []graphEdge      `json:"edges"`
	Communities []graphCommunity `json:"communities"`
}

// parseGraphJSON parses graph.json and produces a GraphSummary.
// GodNodes = top-10 by out_degree.
func parseGraphJSON(data []byte) (*detector.GraphSummary, error) {
	var g graphJSON
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("unmarshal graph.json: %w", err)
	}

	// Sort nodes by out_degree descending for GodNodes.
	nodes := make([]graphNode, len(g.Nodes))
	copy(nodes, g.Nodes)
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].OutDegree > nodes[j].OutDegree
	})

	maxGodNodes := 10
	if len(nodes) < maxGodNodes {
		maxGodNodes = len(nodes)
	}
	godNodes := make([]string, maxGodNodes)
	for i := 0; i < maxGodNodes; i++ {
		godNodes[i] = nodes[i].ID
	}

	return &detector.GraphSummary{
		TotalNodes:     len(g.Nodes),
		TotalEdges:     len(g.Edges),
		CommunityCount: len(g.Communities),
		GodNodes:       godNodes,
	}, nil
}

// nilWriter discards log output.
type nilWriter struct{}

func (nilWriter) Write(p []byte) (n int, err error) { return len(p), nil }

// Ensure ErrGraphifyDegraded is not wrapped away when using errors.Is.
var _ = errors.Is
