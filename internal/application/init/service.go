// Package initphase provides the InitService that orchestrates the INIT phase:
// structural detection (pure Go FS), graphify spawn (Pattern B), cache, and
// dual persistence. The INIT branch fires at the TOP of phase.Service.runAsync
// BEFORE governance/IronLaw/dispatch — design D-INIT-3.
//
// All subprocess and HTTP calls are behind injected interfaces (D-INIT-10).
// No direct time.Now() or ulid.Make() — use injected Clock and IDGenerator
// (Sophia CLAUDE.md D1.5).
package initphase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
)

// Deps bundles InitService dependencies.
type Deps struct {
	Detector         SophiaDetector
	Spawner          GraphifySpawner
	Persister        StructuralPersister
	ProfilePersister ProfilePersister
	Cache            CacheStore
	CacheKey         CacheKeyBuilder
	Clock            shared.Clock
	IDGen            shared.IDGenerator
	Logger           *slog.Logger
	CacheTTL         time.Duration
	ProfileExtractor ProfileExtractor
}

// Service orchestrates the INIT phase: cache lookup → parallel detect+spawn →
// merge → persist → build envelope.
type Service struct {
	d Deps
}

// NewService constructs an InitService.
func NewService(d Deps) *Service {
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(nilServiceWriter{}, nil))
	}
	if d.CacheTTL == 0 {
		d.CacheTTL = 24 * time.Hour
	}
	return &Service{d: d}
}

// Run executes the INIT phase for change c.
//
// Sequence per design D-INIT-3:
//  1. CacheKey.Build
//  2. CacheStore.Lookup — on hit returns without detection
//  3. errgroup: Detector.Detect || Spawner.Build (parallel)
//  4. Merge StructuralContext with SchemaVersion=1, identifiers from c
//  5. Persister.Persist (WARN on error, non-fatal)
//  6. Return sc, buildEnvelope(c, sc), nil
//
// Iron Law D1.2: Persist is called INSIDE Run (artifact durable before phase
// state change). The caller (runInitPhase) calls PhaseRepo.Save AFTER Run
// returns.
func (s *Service) Run(ctx context.Context, c *change.Change) (detector.StructuralContext, *envelope.Envelope, error) {
	// Step 1: build cache key (graphify version unknown pre-spawn; pass empty string).
	cacheKey, err := s.d.CacheKey.Build(ctx, ".", "")
	if err != nil {
		// Non-fatal: if key build fails, skip cache entirely.
		s.d.Logger.Warn("initphase: cache key build failed; skipping cache", "error", err.Error())
		cacheKey = ""
	}

	// Step 2: cache lookup.
	if cacheKey != "" {
		if cached, ok, lookupErr := s.d.Cache.Lookup(ctx, cacheKey); lookupErr == nil && ok && cached != nil {
			if cached.SchemaVersion == detector.StructuralContextSchemaV1 {
				env := buildEnvelope(c, *cached)
				return *cached, env, nil
			}
		}
	}

	// Step 3: parallel detect + spawn via errgroup.
	var detResult DetectorResult
	var graphSummary *detector.GraphSummary
	var graphVersion string
	var degradedReason string

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		res, dErr := s.d.Detector.Detect(egCtx, ".")
		if dErr != nil {
			return fmt.Errorf("detector: %w", dErr)
		}
		detResult = res
		return nil
	})

	eg.Go(func() error {
		sum, ver, spawnErr := s.d.Spawner.Build(egCtx, ".")
		if spawnErr != nil {
			// Spawner errors are degraded — absorbed, never hard-fail.
			if errors.Is(spawnErr, ErrGraphifyDegraded) {
				degradedReason = spawnErr.Error()
				return nil
			}
			// Non-ErrGraphifyDegraded spawner error also degrades (belt-and-suspenders).
			degradedReason = spawnErr.Error()
			return nil
		}
		graphSummary = sum
		graphVersion = ver
		return nil
	})

	if err := eg.Wait(); err != nil {
		// Only detector hard errors surface here.
		return detector.StructuralContext{SchemaVersion: detector.StructuralContextSchemaV1}, nil, fmt.Errorf("init detect+spawn errgroup: %w", err)
	}

	// Step 4: merge StructuralContext.
	now := s.d.Clock.Now().UTC()
	sc := detector.StructuralContext{
		SchemaVersion:     detector.StructuralContextSchemaV1,
		ProjectID:         c.Project(),
		ChangeID:          c.ID().String(),
		ChangeName:        c.Name(),
		Languages:         detResult.Languages,
		Frameworks:        detResult.Frameworks,
		PackageManagers:   detResult.PackageManagers,
		ArchStyle:         detResult.ArchStyle,
		ConventionHints:   detResult.ConventionHints,
		GraphSummary:      graphSummary,
		GraphAvailable:    graphSummary != nil,
		DegradedReason:    degradedReason,
		DetectedAt:        now,
		GraphifyVersion:   graphVersion,
		SophiaDetectorVer: detector.SophiaDetectorVer,
	}

	// Step 5: persist — non-fatal (WARN on error).
	if cacheKey != "" {
		if pErr := s.d.Persister.Persist(ctx, sc, cacheKey); pErr != nil {
			s.d.Logger.Warn("initphase: persist failed (non-fatal)", "error", pErr.Error())
		}
	}

	// Step 5b: convention profile extraction + persistence (optional, non-fatal).
	// If ProfileExtractor is nil, skip silently (graceful degradation).
	if s.d.ProfileExtractor != nil {
		profile, extErr := s.d.ProfileExtractor.Extract(ctx, ".", sc)
		if extErr != nil {
			s.d.Logger.Warn("initphase: profile extraction failed (non-fatal)", "error", extErr.Error())
		} else if profile != nil && s.d.ProfilePersister != nil {
			if ppErr := s.d.ProfilePersister.PersistProfile(ctx, *profile); ppErr != nil {
				s.d.Logger.Warn("initphase: profile persist failed (non-fatal)", "error", ppErr.Error())
			}
		}
	}

	// Step 6: build envelope and return.
	env := buildEnvelope(c, sc)
	return sc, env, nil
}

// buildEnvelope constructs the INIT phase envelope per design:
// Phase="init", Status=ok (DONE), Confidence=1.0,
// ArtifactsSaved=[{TopicKey:"sdd/<change_name>/init", Type:"sdd_init"}],
// NextRecommended=explore.
func buildEnvelope(c *change.Change, sc detector.StructuralContext) *envelope.Envelope {
	artifacts := []envelope.ArtifactRef{}
	if sc.ChangeName != "" {
		artifacts = append(artifacts, envelope.ArtifactRef{
			TopicKey: "sdd/" + sc.ChangeName + "/init",
			Type:     "sdd_init",
		})
	}
	return &envelope.Envelope{
		SchemaVersion:    envelope.SchemaVersionV1,
		Phase:            "init",
		ChangeName:       c.Name(),
		Project:          c.Project(),
		Status:           envelope.StatusDone,
		Confidence:       1.0,
		ExecutiveSummary: "INIT phase complete: structural detection finished",
		ArtifactsSaved:   artifacts,
		NextRecommended:  envelope.NextRecommended{"explore"},
	}
}

// nilServiceWriter discards log output.
type nilServiceWriter struct{}

func (nilServiceWriter) Write(p []byte) (n int, err error) { return len(p), nil }
