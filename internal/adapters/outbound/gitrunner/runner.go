// Package gitrunner provides a GitRunner implementation using os/exec.
// Used only in bootstrap/wire.go (production). Tests inject fakes.
package gitrunner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
)

// ExecGitRunner implements initphase.GitRunner using os/exec.
type ExecGitRunner struct{}

// NewExecRunner constructs an ExecGitRunner.
func NewExecRunner() *ExecGitRunner { return &ExecGitRunner{} }

// RevParseHead returns the HEAD commit hash for the repo at repoRoot.
func (r *ExecGitRunner) RevParseHead(ctx context.Context, repoRoot string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD") //nolint:forbidigo
	cmd.Dir = repoRoot
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// StatusPorcelain returns `git status --porcelain` output.
func (r *ExecGitRunner) StatusPorcelain(ctx context.Context, repoRoot string) ([]byte, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain") //nolint:forbidigo
	cmd.Dir = repoRoot
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git status --porcelain: %w", err)
	}
	return buf.Bytes(), nil
}

// Ensure ExecGitRunner satisfies the interface.
var _ initphase.GitRunner = &ExecGitRunner{}
