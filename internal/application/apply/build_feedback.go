package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// stderrBudgetBytes is the maximum total bytes retained from stderr.
// Head + tail are each half of this, so the most important first error
// and the last lines of context are always preserved. 4 KB total.
const stderrBudgetBytes = 4 * 1024

// truncationIndicator is inserted between head and tail when stderr is
// larger than stderrBudgetBytes.
const truncationIndicator = "\n... [output truncated] ...\n"

// ErrGroupBuildFailed is returned by runGroupBuildFeedbackLoop when the
// build-attempt budget is exhausted without a passing build.
var ErrGroupBuildFailed = errors.New("apply: group build failed (budget exhausted)")

// TruncateStderr trims output to at most stderrBudgetBytes, retaining
// the first half and the last half with a truncation indicator in the
// middle. If the input fits within the budget it is returned unchanged.
// Exported for tests.
func TruncateStderr(raw []byte) (string, bool) {
	if len(raw) <= stderrBudgetBytes {
		return string(raw), false
	}
	half := stderrBudgetBytes / 2
	head := string(raw[:half])
	tail := string(raw[len(raw)-half:])
	return head + truncationIndicator + tail, true
}

// buildOutcome is the result of a single build execution attempt.
type buildOutcome struct {
	exitCode   int
	stderr     string
	truncated  bool
	durationMS int
}

// executeBuild runs the resolved plan for the given group in its worktree
// via shell.exec@v1 and returns a buildOutcome.
func (s *RunService) executeBuild(ctx context.Context, plan *BuildPlan, group *apply.Group) (buildOutcome, error) {
	cmdAndArgs := append([]string{plan.Command}, plan.Args...) //nolint:gocritic
	payload, err := json.Marshal(map[string]any{
		"command":     cmdAndArgs[0],
		"args":        cmdAndArgs[1:],
		"working_dir": group.WorktreePath(),
	})
	if err != nil {
		return buildOutcome{}, fmt.Errorf("marshal build payload: %w", err)
	}

	receipt, err := s.d.Runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  plan.TimeoutMS,
	})
	if err != nil {
		return buildOutcome{exitCode: -1, stderr: err.Error()}, fmt.Errorf("build execute: %w", err)
	}

	stderrStr, wasTruncated := TruncateStderr(receipt.Stderr)
	return buildOutcome{
		exitCode:   receipt.ExitCode,
		stderr:     stderrStr,
		truncated:  wasTruncated,
		durationMS: receipt.DurationMS,
	}, nil
}

// assembleBuildRepairPrompt builds the implement-agent prompt for a
// group repair attempt. It includes the failed build's command, the
// truncated stderr, and a summary of all tasks in the group so the
// agent has enough context to fix compilation errors.
func assembleBuildRepairPrompt(plan *BuildPlan, stderrOutput string, group *apply.Group, priorContext string) string {
	var sb strings.Builder
	sb.WriteString("## Build Repair Request\n\n")
	fmt.Fprintf(&sb,
		"The group **%s** was dispatched to build its worktree at `%s` but the build failed.\n\n",
		group.Name(), group.WorktreePath(),
	)
	fmt.Fprintf(&sb, "**Build command**: `%s %s`\n\n",
		plan.Command, strings.Join(plan.Args, " "),
	)
	sb.WriteString("### Compiler Output (stderr)\n\n```\n")
	sb.WriteString(stderrOutput)
	sb.WriteString("\n```\n\n")
	sb.WriteString("### Tasks in this group\n\n")
	for _, t := range group.Tasks() {
		fmt.Fprintf(&sb, "- %s (files: %v)\n", t.Description(), t.FilesPattern())
	}
	sb.WriteString("\n")
	if priorContext != "" {
		sb.WriteString("### Prior context\n\n")
		sb.WriteString(priorContext)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Fix the compilation errors reported above. Do not change the observable behavior of the code — only resolve the compiler errors.\n")
	return sb.String()
}

// runGroupBuildFeedbackLoop detects the build manifest, executes the
// build, and handles the pass/fail/budget-exhausted outcomes.
//
// Control-flow (per design.md):
//
//  1. Detect manifest → no manifest → SkipBuild → return nil (caller
//     will call group.Complete()).
//  2. Manifest found → emit apply.build.started → execute build.
//  3. Pass (exit 0) → RecordBuildAttempt(true) → emit apply.build.passed
//     → return nil.
//  4. Fail (exit != 0) → RecordBuildAttempt(false).
//     a. Budget remaining → emit apply.build.failed → dispatch repair →
//     loop back to step 2.
//     b. Budget exhausted (RecordBuildAttempt returns ErrBuildBudgetExhausted)
//     → emit apply.build.failed → return ErrGroupBuildFailed.
func (s *RunService) runGroupBuildFeedbackLoop(
	ctx context.Context,
	c *change.Change,
	p *phase.Phase,
	group *apply.Group,
	priorContext string,
) error {
	cwd := group.WorktreePath()

	// Detect manifest.
	plan, found, err := DetectBuildPlan(ctx, s.d.Runtime, cwd)
	if err != nil {
		// Detection error — treat as no manifest (skip) to preserve
		// backward compat; the error is emitted as a worktree event.
		s.publishEvent(ctx, p.ID(), inbound.EventApplyWorktreeError, inbound.ApplyWorktreeErrorPayload{
			GroupID: group.ID().String(),
			Err:     fmt.Sprintf("build manifest detection: %v", err),
		})
		// Fall through to SkipBuild.
	}
	if !found || plan == nil {
		if err := group.SkipBuild(); err != nil {
			return fmt.Errorf("skip build: %w", err)
		}
		if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
			slog.Default().ErrorContext(ctx, "apply: BoardRepo.SaveGroup failed; continuing",
				"operation", "BoardRepo.SaveGroup", "group_id", group.ID().String(), "error", err)
			s.appendAuditErr(ctx, c.ID(), p.ID(), "BoardRepo.SaveGroup", err)
		}
		return nil
	}

	// Build loop: repeat until pass or budget exhausted.
	for {
		attempt := group.BuildAttempts() + 1

		s.publishEvent(ctx, p.ID(), inbound.EventApplyBuildStarted, inbound.ApplyBuildStartedPayload{
			GroupID:  group.ID().String(),
			Manifest: plan.Manifest,
			Command:  plan.Command,
			Args:     plan.Args,
			Attempt:  attempt,
		})

		outcome, execErr := s.executeBuild(ctx, plan, group)
		if execErr != nil {
			// Runtime-level failure (transport, timeout). Count it as a
			// failed build attempt so it reduces the budget.
			outcome = buildOutcome{exitCode: -1, stderr: execErr.Error()}
		}

		if outcome.exitCode == 0 {
			// Build passed.
			_ = group.RecordBuildAttempt(true)
			if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
				slog.Default().ErrorContext(ctx, "apply: BoardRepo.SaveGroup failed; continuing",
					"operation", "BoardRepo.SaveGroup", "group_id", group.ID().String(), "error", err)
				s.appendAuditErr(ctx, c.ID(), p.ID(), "BoardRepo.SaveGroup", err)
			}
			s.publishEvent(ctx, p.ID(), inbound.EventApplyBuildPassed, inbound.ApplyBuildPassedPayload{
				GroupID:    group.ID().String(),
				Manifest:   plan.Manifest,
				Command:    plan.Command,
				Attempt:    attempt,
				DurationMS: outcome.durationMS,
			})
			return nil
		}

		// Build failed.
		budgetErr := group.RecordBuildAttempt(false)
		if err := s.d.BoardRepo.SaveGroup(ctx, group); err != nil {
			slog.Default().ErrorContext(ctx, "apply: BoardRepo.SaveGroup failed; continuing",
				"operation", "BoardRepo.SaveGroup", "group_id", group.ID().String(), "error", err)
			s.appendAuditErr(ctx, c.ID(), p.ID(), "BoardRepo.SaveGroup", err)
		}
		s.publishEvent(ctx, p.ID(), inbound.EventApplyBuildFailed, inbound.ApplyBuildFailedPayload{
			GroupID:   group.ID().String(),
			Manifest:  plan.Manifest,
			Command:   plan.Command,
			Attempt:   attempt,
			ExitCode:  outcome.exitCode,
			Stderr:    outcome.stderr,
			Truncated: outcome.truncated,
		})

		if errors.Is(budgetErr, apply.ErrBuildBudgetExhausted) {
			return ErrGroupBuildFailed
		}

		// Budget remaining — dispatch a group repair attempt.
		repairPrompt := assembleBuildRepairPrompt(plan, outcome.stderr, group, priorContext)
		s.dispatchBuildRepair(ctx, c, p, group, repairPrompt)
	}
}

// dispatchBuildRepair dispatches a group-level repair implement attempt.
// The repair is best-effort: if the dispatch fails we keep the build loop
// running (the next iteration will detect a still-failing build).
func (s *RunService) dispatchBuildRepair(
	ctx context.Context,
	_ *change.Change,
	p *phase.Phase,
	group *apply.Group,
	repairPrompt string,
) {
	res, err := s.d.Dispatcher.Dispatch(ctx, outbound.DispatchRequest{
		Prompt:       repairPrompt,
		WorktreePath: group.WorktreePath(),
		TimeoutMS:    s.d.Config.DispatchTimeoutMS,
		EnvelopeOut:  "stdout-fenced-json",
		PhaseType:    string(phase.PhaseApply),
	})
	if err != nil {
		s.publishEvent(ctx, p.ID(), inbound.EventApplyDispatchError, inbound.ApplyDispatchErrorPayload{
			TaskID: group.ID().String(),
			Err:    fmt.Sprintf("build repair dispatch: %v", err),
		})
		return
	}

	// If the adapter supports aider-style in-place editing (no envelope),
	// synthesize the envelope from git status so we have a receipt. Errors
	// are non-fatal; we continue the build loop regardless.
	if res.EnvelopeRaw == nil && res.AdapterID == "aider" {
		synth, synthErr := synthesizeEnvelopeFromGit(ctx, s.d.Runtime, group.WorktreePath())
		if synthErr == nil {
			res.EnvelopeRaw = synth
		}
	}
	// Envelope validation is intentionally skipped for repair dispatches:
	// we care about whether the BUILD passes on the next iteration, not
	// about the repair agent's self-declared envelope status.
	_ = res
}
