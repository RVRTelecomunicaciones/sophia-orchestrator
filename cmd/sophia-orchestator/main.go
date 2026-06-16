// Command sophia-orchestator is the deterministic SDD workflow coordinator.
// It boots the HTTP server, wires all dependencies, and runs until SIGINT
// or SIGTERM is received. The `reeval` subcommand re-evaluates skill
// promotion/demotion against real apply_attempts (loop-hardening D-LH-3).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	skillapp "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/config"
)

func main() {
	os.Exit(mainWithExit())
}

// mainWithExit runs the program and returns a process exit code. Splitting this
// from main keeps deferred cleanup (signal cancel, app.Close) running before exit.
func mainWithExit() int {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// reeval subcommand dispatch happens before the server boots. When the args
	// do not match a subcommand, dispatch returns handled=false and the server runs.
	handled, err := dispatch(ctx, os.Args[1:], runReeval, runRevert)
	if err != nil {
		slog.Error("sophia-orchestator subcommand failed", slog.String("err", err.Error()))
		return 1
	}
	if handled {
		return 0
	}

	if err := run(ctx); err != nil {
		slog.Error("sophia-orchestator exited with error", slog.String("err", err.Error()))
		return 1
	}
	return 0
}

// reevalRunner executes the retroactive re-evaluation. confirm=true applies the
// proposed transitions; confirm=false (default) is a dry-run.
type reevalRunner func(ctx context.Context, confirm bool) error

// reevalReverter reverses a previously-applied reeval run. When last is true the
// most recent run is reversed and runID is ignored; otherwise runID names the
// run to reverse.
type reevalReverter func(ctx context.Context, runID string, last bool) error

// dispatch routes recognized subcommands. It returns handled=true when it consumed
// the args (the caller must NOT boot the server), or handled=false to fall through
// to the HTTP server. Flag semantics: `reeval` defaults to dry-run; `--apply`
// requires `--confirm` to mutate, otherwise it stays a dry-run. `--revert <id>`
// and `--revert-last` reverse a recorded run and are mutually exclusive with
// `--apply`.
func dispatch(ctx context.Context, args []string, reeval reevalRunner, revert reevalReverter) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if args[0] != "reeval" {
		return false, nil
	}

	fs := flag.NewFlagSet("reeval", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "report projected status changes without mutating (default)")
	apply := fs.Bool("apply", false, "apply the projected status transitions (requires --confirm)")
	confirm := fs.Bool("confirm", false, "confirm mutation; without it --apply stays a dry-run")
	revertID := fs.String("revert", "", "reverse the transitions of the named reeval run id (inverse op via the same guard)")
	revertLast := fs.Bool("revert-last", false, "reverse the most recent reeval run")
	fs.Usage = func() {
		_, _ = io.WriteString(fs.Output(), strings.Join([]string{
			"Usage: sophia-orchestator reeval [--dry-run | --apply --confirm | --revert <id> | --revert-last]",
			"  Re-evaluates skill promotion/demotion against real apply_attempts.",
			"  Default is dry-run (no mutation). --apply --confirm applies transitions and",
			"  records a revertible prior-state snapshot.",
			"  An explicit --dry-run always wins and forces no mutation.",
			"  --revert <id> / --revert-last replay the INVERSE transitions of a recorded run",
			"  through the same status-transition guard. A direct single-step inverse is not",
			"  always legal (e.g. deprecated->active); revert walks the legal chain",
			"  deprecated->blocked->candidate->validated->active. Where no legal path exists",
			"  the skill is skipped and reported for manual intervention. The revert is itself",
			"  recorded as an immutable audit run.",
			"",
		}, "\n"))
	}
	if err := fs.Parse(args[1:]); err != nil {
		return true, fmt.Errorf("reeval: parse flags: %w", err)
	}

	// Detect whether --revert was explicitly passed (even with an empty value)
	// so `--revert ""` is a clear usage error rather than a silent dry-run.
	revertSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "revert" {
			revertSet = true
		}
	})

	wantRevert := revertSet || *revertLast

	// Revert is mutually exclusive with apply: reversing while also requesting a
	// fresh apply in the same invocation is ambiguous and refused.
	if wantRevert && *apply {
		return true, errors.New("reeval: --revert/--revert-last cannot be combined with --apply")
	}

	if wantRevert {
		if *revertLast {
			if err := revert(ctx, "", true); err != nil {
				return true, fmt.Errorf("reeval revert: %w", err)
			}
			return true, nil
		}
		if *revertID == "" {
			return true, errors.New("reeval: --revert requires a non-empty run id")
		}
		if err := revert(ctx, *revertID, false); err != nil {
			return true, fmt.Errorf("reeval revert: %w", err)
		}
		return true, nil
	}

	// Mutation requires BOTH --apply and --confirm. An explicit --dry-run always
	// wins: it forces no-mutation even alongside --apply --confirm, so operators
	// scripting `--dry-run` as a safety guard never accidentally mutate.
	wantConfirm := *apply && *confirm && !*dryRun

	if err := reeval(ctx, wantConfirm); err != nil {
		return true, fmt.Errorf("reeval: %w", err)
	}
	return true, nil
}

// runReeval wires the live skill service and runs the re-evaluation, printing the
// per-skill report to stdout.
func runReeval(ctx context.Context, confirm bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := bootstrap.Wire(ctx, cfg)
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}
	defer app.Close()

	svc := app.SkillService()
	if svc == nil {
		return errors.New("skills are disabled (set SOPHIA_SKILLS_ENABLED=true)")
	}

	r := svc.Reevaluator()
	report, err := r.Apply(ctx, confirm)
	if err != nil {
		return fmt.Errorf("reevaluate: %w", err)
	}
	if _, err := io.WriteString(os.Stdout, formatReevalReport(report, confirm)); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// runRevert wires the live skill service and reverses a recorded reeval run,
// printing the per-skill revert outcome to stdout.
func runRevert(ctx context.Context, runID string, last bool) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := bootstrap.Wire(ctx, cfg)
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}
	defer app.Close()

	svc := app.SkillService()
	if svc == nil {
		return errors.New("skills are disabled (set SOPHIA_SKILLS_ENABLED=true)")
	}

	r := svc.Reevaluator()
	var result []skillapp.RevertRow
	if last {
		result, err = r.RevertLast(ctx)
	} else {
		result, err = r.Revert(ctx, runID)
	}
	if err != nil {
		return fmt.Errorf("revert: %w", err)
	}
	if _, err := io.WriteString(os.Stdout, formatRevertReport(result, runID, last)); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

// formatRevertReport renders a human-readable per-skill revert outcome.
func formatRevertReport(result []skillapp.RevertRow, runID string, last bool) string {
	target := runID
	if last {
		target = "(latest run)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Skill reeval revert — run %s\n", target)
	reverted, skipped := 0, 0
	for _, row := range result {
		if row.Reverted {
			reverted++
		}
		if row.Skipped {
			skipped++
		}
	}
	fmt.Fprintf(&b, "%d item(s): %d reverted, %d skipped\n\n", len(result), reverted, skipped)

	for _, row := range result {
		fmt.Fprintf(&b, "  %s  %s→%s", row.SkillID, row.FromStatus, row.ToStatus)
		if len(row.Path) > 0 {
			hops := make([]string, 0, len(row.Path))
			for _, s := range row.Path {
				hops = append(hops, s.String())
			}
			fmt.Fprintf(&b, "  via %s", strings.Join(hops, "->"))
		}
		switch {
		case row.Skipped:
			fmt.Fprintf(&b, "  [SKIPPED: %v]\n", row.RevertErr)
		case row.Reverted:
			b.WriteString("  [REVERTED]\n")
		default:
			b.WriteString("\n")
		}
	}
	return b.String()
}

// formatReevalReport renders a human-readable per-skill report.
func formatReevalReport(report []skillapp.ReevalRow, confirm bool) string {
	mode := "DRY-RUN (no mutation)"
	if confirm {
		mode = "APPLY (mutations confirmed)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Skill re-evaluation — %s\n", mode)
	fmt.Fprintf(&b, "%d skill(s) evaluated, %d projected status change(s)\n\n", len(report), skillapp.CountChanges(report))

	for _, row := range report {
		if !row.WouldChange {
			continue
		}
		fmt.Fprintf(&b, "  %s  %s→%s  metric %.3f→%.3f  apply_attempts=%d  verdict=%s",
			row.SkillID, row.CurrentStatus, row.ProposedStatus,
			row.OldMetric, row.NewMetric, row.ApplyAttempts, row.Verdict)
		switch {
		case row.Skipped:
			fmt.Fprintf(&b, "  [SKIPPED: %v]\n", row.ApplyErr)
		case row.Applied:
			b.WriteString("  [APPLIED]\n")
		default:
			b.WriteString("  [PROPOSED]\n")
		}
	}
	if confirm {
		b.WriteString("\nReversal is NOT single-step. Promotions step back one hop, but a demotion\n")
		b.WriteString("(active->deprecated) cannot return to active in one PATCH. Walk the allowed\n")
		b.WriteString("chain via admin PATCH /api/v1/skills/{id}/status:\n")
		b.WriteString("  deprecated->blocked->candidate->validated->active.\n")
	}
	return b.String()
}

func run(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app, err := bootstrap.Wire(ctx, cfg)
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}
	defer app.Close()

	return app.Run(ctx)
}
