// Package recovery implements the boot-time scan that marks phases
// stranded in PhaseStatusRunning by a crashed orchestrator process
// as PhaseStatusInterrupted, so the operator can explicitly Resume
// them rather than have them appear "still running" forever.
//
// Spec #68 (BUG-23). The orch process runs phases in goroutines
// driven from POST /phases/.../run; the goroutine writes "running"
// to the DB before dispatching the agent CLI. A SIGKILL or container
// crash mid-flight leaves the DB row at "running" with no live
// owner. Without this scan the row never transitions, every status
// poll keeps showing "running", and operators have no clear signal
// that the work was lost.
//
// The scan runs ONCE at boot, BEFORE the HTTP server starts
// accepting requests. Phases newly Started after the scan are not
// affected because they pass through the scan window already
// terminated (or are owned by the current process). The recovery is
// fail-soft: a transient DB error during MarkInterrupted/Save is
// logged but does not block boot — the next operator Resume call
// will still try to take ownership and the audit trail records the
// recovery attempt.
package recovery

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Service implements the boot recovery scan. Constructed from the
// PhaseRepository port plus the application logger so the boot path
// can wire it without dragging concrete infrastructure types.
type Service struct {
	phases outbound.PhaseRepository
	log    *slog.Logger
}

// NewService constructs a recovery service. Logger may be nil; we
// substitute a discard logger so unit tests don't need to set up
// observability scaffolding.
func NewService(phases outbound.PhaseRepository, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{phases: phases, log: log}
}

// MarkStuckInterrupted is the boot entry point. Lists every phase in
// PhaseStatusRunning and marks each as PhaseStatusInterrupted via
// the domain's MarkInterrupted transition, persisting after every
// successful transition.
//
// Returns the number of phases marked AND the first persistence
// error encountered (if any). Callers should log the count and
// continue boot — a non-nil error is a degraded mode rather than a
// fatal failure: the operator can still Resume each phase manually.
//
// Concurrent invocation is safe: every Save is idempotent and the
// MarkInterrupted transition is a no-op for phases already in
// PhaseStatusInterrupted. In practice this method runs exactly once
// per process boot, before the HTTP server begins serving.
func (s *Service) MarkStuckInterrupted(ctx context.Context) (int, error) {
	stuck, err := s.phases.FindAllRunning(ctx)
	if err != nil {
		return 0, fmt.Errorf("recovery: list running phases: %w", err)
	}
	if len(stuck) == 0 {
		s.log.Info("recovery: no stuck running phases detected at boot")
		return 0, nil
	}
	s.log.Warn("recovery: stranded running phases detected; marking interrupted",
		slog.Int("count", len(stuck)),
	)
	marked := 0
	var firstErr error
	for _, p := range stuck {
		if err := p.MarkInterrupted(); err != nil {
			// MarkInterrupted only fails when the source status is
			// terminal — which contradicts our query (status=running).
			// Log and keep going so a single corrupted row doesn't stall
			// the whole recovery sweep.
			s.log.Error("recovery: MarkInterrupted refused",
				slog.String("phase_id", p.ID().String()),
				slog.String("status", string(p.Status())),
				slog.String("err", err.Error()),
			)
			if firstErr == nil {
				firstErr = fmt.Errorf("recovery: MarkInterrupted %s: %w", p.ID(), err)
			}
			continue
		}
		if err := s.phases.Save(ctx, p); err != nil {
			s.log.Error("recovery: Save interrupted phase failed",
				slog.String("phase_id", p.ID().String()),
				slog.String("err", err.Error()),
			)
			if firstErr == nil {
				firstErr = fmt.Errorf("recovery: Save %s: %w", p.ID(), err)
			}
			continue
		}
		marked++
		s.log.Info("recovery: phase marked interrupted",
			slog.String("phase_id", p.ID().String()),
			slog.String("phase_type", string(p.Type())),
		)
	}
	return marked, firstErr
}

// Compile-time sanity: domain transitions match what we use.
var _ = phase.PhaseStatusRunning
var _ = phase.PhaseStatusInterrupted
