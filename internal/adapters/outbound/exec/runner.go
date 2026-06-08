// Package exec provides a real ExecRunner that uses os/exec.Command.
// Used only in bootstrap/wire.go (production). Tests inject fakes.
package exec

import (
	"bytes"
	"context"
	"os/exec"
	"time"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
)

// RealRunner implements initphase.ExecRunner using os/exec.Command.
type RealRunner struct{}

// NewRealRunner constructs a RealRunner.
func NewRealRunner() *RealRunner { return &RealRunner{} }

// Run executes name with args. The timeout from opts.TimeoutMS takes precedence
// over the ctx deadline when > 0.
func (r *RealRunner) Run(ctx context.Context, name string, args []string, opts initphase.ExecOpts) (stdout, stderr []byte, exitCode int, err error) {
	if opts.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	//nolint:forbidigo // production-only adapter
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name+args come from injected interface, callers validate
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Env != nil {
		cmd.Env = opts.Env
	}

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	} else if runErr != nil {
		code = -1
	}
	return outBuf.Bytes(), errBuf.Bytes(), code, runErr
}
