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
	handled, err := dispatch(ctx, os.Args[1:], runReeval)
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

// dispatch routes recognized subcommands. It returns handled=true when it consumed
// the args (the caller must NOT boot the server), or handled=false to fall through
// to the HTTP server. Flag semantics: `reeval` defaults to dry-run; `--apply`
// requires `--confirm` to mutate, otherwise it stays a dry-run.
func dispatch(ctx context.Context, args []string, reeval reevalRunner) (bool, error) {
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
	fs.Usage = func() {
		_, _ = io.WriteString(fs.Output(), strings.Join([]string{
			"Usage: sophia-orchestator reeval [--dry-run | --apply --confirm]",
			"  Re-evaluates skill promotion/demotion against real apply_attempts.",
			"  Default is dry-run (no mutation). --apply --confirm applies transitions.",
			"  To reverse an applied change, use the admin PATCH /api/v1/skills/{id}/status endpoint.",
			"",
		}, "\n"))
	}
	if err := fs.Parse(args[1:]); err != nil {
		return true, fmt.Errorf("reeval: parse flags: %w", err)
	}

	// --dry-run is the default behavior; it is accepted explicitly for clarity.
	_ = dryRun
	// Mutation requires BOTH --apply and --confirm. Anything less is a dry-run.
	wantConfirm := *apply && *confirm

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
		b.WriteString("\nTo reverse a change, use admin PATCH /api/v1/skills/{id}/status.\n")
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
