// Package bootstrap — service.go implements BootstrapTriggerService, the
// single decision point for firing a Context7 documentation import.
//
// It implements two trigger paths:
//  1. Greenfield — fires when StructuralContext.Greenfield == true.
//  2. Drift — fires when the detected major version of a framework exceeds
//     the major version encoded in the active skill's AppliesWhen.FrameworkMinVersion.
//
// All failure paths (provider down, rate limit, thin entries, import error) end
// in a logged WARN + skip. The method always returns nil and never panics out.
// D11 is preserved: no LLM is called; only InsertIfAbsent mutates state.
//
// Design references: DG-C7-5, DG-C7-6, DG-C7-8, DG-C7-9, DG-C7-10.
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ServiceDeps holds all constructor-injected dependencies of Service.
// D11: no LLM/dispatcher port is present. Only InsertIfAbsent mutates state.
type ServiceDeps struct {
	// Docs is the outbound DocsProvider port (DG-C7-8). When the provider
	// is unconfigured it returns ErrDocsUnavailable — the service treats that
	// as a key-guard failure and returns immediately.
	Docs outbound.DocsProvider

	// Skills is the SkillLookup port used for drift detection: given a skill
	// name (e.g. "stack/angular-22"), return the active version(s).
	Skills SkillLookup

	// Importer handles skill assembly + InsertIfAbsent for each triggered import.
	Importer SkillImporterPort

	// Rate is the per-project call quota guard (DG-C7-6).
	Rate RateGuard

	// Logger is the structured logger. If nil, slog.Default() is used.
	Logger *slog.Logger

	// MinSnippets is the minimum snippet count required to use a version-specific
	// library entry; below this threshold the main entry is tried (DG-C7-10).
	// Default 50 when zero.
	MinSnippets int
}

// Service is the BootstrapTriggerService application service (DG-C7-5/6).
// It is safe for concurrent use.
type Service struct {
	d ServiceDeps
}

// NewService constructs a Service. All deps are required; nil Docs/Skills/Importer/Rate
// will cause panic-free WARN-and-skip at call time (nil-safe guards inside TriggerIfNeeded).
func NewService(d ServiceDeps) *Service {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.MinSnippets <= 0 {
		d.MinSnippets = 50
	}
	return &Service{d: d}
}

// TriggerIfNeeded is the sole entry point called by runInitPhase (phase/service.go).
// It must be called from a goroutine — it never blocks the INIT response.
// Satisfies the phase.BootstrapDep interface (no return value).
//
//   - All errors are logged at WARN and discarded; never returned.
//   - Never panics out of this method (callers already have a recover() wrapper).
//   - Never calls any LLM (D11).
func (s *Service) TriggerIfNeeded(ctx context.Context, sc structural.StructuralContext) {
	log := s.d.Logger.With("project_id", sc.ProjectID)

	// 1. Key guard: if the docs provider is unavailable, skip immediately.
	if s.d.Docs == nil {
		log.Warn("bootstrap: docs provider not configured; skipping")
		return
	}
	// In the greenfield and drift branches below, if Resolve returns
	// ErrDocsUnavailable we WARN and exit (context7 adapter returns this
	// sentinel when BridgeURL/Token are absent — no network dial attempted).

	// 2. Rate guard.
	if s.d.Rate != nil {
		allowed, err := s.d.Rate.Allow(ctx, sc.ProjectID)
		if err != nil {
			log.Warn("bootstrap: rate guard error; skipping", "err", err)
			return
		}
		if !allowed {
			log.Warn("bootstrap: rate limit exceeded; skipping", "project_id", sc.ProjectID)
			return
		}
	}

	// 3 + 4. Greenfield branch — fires for each detected framework.
	if sc.Greenfield {
		if len(sc.Frameworks) == 0 {
			log.Warn("bootstrap: greenfield but no framework detected; skipping")
			return
		}
		for _, fw := range sc.Frameworks {
			s.runImport(ctx, log, fw.Name, fw.Version)
		}
		return
	}

	// 4. Drift branch — for each detected framework, check active skill major.
	for _, fw := range sc.Frameworks {
		s.runDriftCheck(ctx, log, fw.Name, fw.Version)
	}
}

// runImport executes the resolve → threshold/fallback select → GetDocs → import
// flow for a single framework. All errors are logged and discarded.
func (s *Service) runImport(ctx context.Context, log *slog.Logger, fw, version string) {
	fwLower := strings.ToLower(fw)

	entries, err := s.d.Docs.ResolveLibrary(ctx, fwLower, "best practices")
	if err != nil {
		if isDocsUnavailable(err) {
			log.Warn("bootstrap: CONTEXT7_API_KEY not set or provider unavailable; skipping", "framework", fw)
		} else {
			log.Warn("bootstrap: resolve-library-id failed; skipping", "framework", fw, "err", err)
		}
		return
	}

	// Select entry: version-specific if fat, fall back to main, skip if all thin.
	selectedID, ok := s.selectEntry(log, fw, entries)
	if !ok {
		return // already logged in selectEntry
	}

	result, err := s.d.Docs.GetDocs(ctx, selectedID, "best practices", "", 8000)
	if err != nil {
		log.Warn("bootstrap: get-library-docs failed; skipping", "framework", fw, "err", err)
		return
	}

	// Compute canonical skill name: "stack/<fw>-<major>"
	major := extractMajor(version)
	name := fmt.Sprintf("stack/%s-%s", fwLower, major)

	if err := s.d.Importer.ImportDocs(ctx, name, version, fwLower, result); err != nil {
		log.Warn("bootstrap: import failed; skipping", "framework", fw, "name", name, "err", err)
	}
}

// runDriftCheck checks whether the detected framework version drifts forward
// from any active skill's FrameworkMinVersion baseline for this framework.
// If drift is detected, triggers an import for the new version.
//
// V1 strategy (DG-C7-9): look up the active skill at stack/<fw>-<prevMajor>
// where prevMajor = detectedMajorInt-1 (the immediately preceding major).
// If no such skill exists, or none has FrameworkMinVersion set, no drift fires
// (SVC-G). In practice only one import per framework major exists at a time.
func (s *Service) runDriftCheck(ctx context.Context, log *slog.Logger, fw, version string) {
	fwLower := strings.ToLower(fw)

	detectedMajorInt, ok := skill.MajorOf(version)
	if !ok {
		log.Warn("bootstrap: cannot parse detected version; skipping drift check",
			"framework", fw, "version", version)
		return
	}

	if detectedMajorInt <= 1 {
		// Major 0 or 1 — no previous major to compare against.
		return
	}

	// Look up the active skill for the immediately preceding major.
	// Example: detected=23 → look up "stack/angular-22".
	prevMajor := detectedMajorInt - 1
	prevName := fmt.Sprintf("stack/%s-%d", fwLower, prevMajor)
	actives, err := s.d.Skills.ActiveByName(ctx, prevName)
	if err != nil {
		log.Warn("bootstrap: skill lookup failed; skipping drift check",
			"framework", fw, "err", err)
		return
	}

	for _, active := range actives {
		aw := active.AppliesWhen()
		minVersion, hasMin := aw.FrameworkMinVersion[fwLower]
		if !hasMin || minVersion == "" {
			// No baseline set → no drift check for this skill (SVC-G).
			continue
		}
		// Validate that the stored min is parseable.
		if _, minOK := skill.MajorOf(minVersion); !minOK {
			log.Warn("bootstrap: cannot parse stored min version; skipping drift",
				"framework", fw, "min_version", minVersion)
			continue
		}
		// Compare: detected major vs stored min major (DG-C7-9).
		if !skill.DriftsForward(version, minVersion) {
			// Same or earlier major — no drift for this framework.
			return
		}
		// Drift detected — trigger import for the new version.
		log.Info("bootstrap: version drift detected; triggering import",
			"framework", fw, "detected", version, "active_min", minVersion)
		s.runImport(ctx, log, fw, version)
		return // one drift import per framework per call
	}
}

// selectEntry applies the threshold/fallback logic (DG-C7-10):
//  1. Prefer version-specific entry with snippets >= minSnippets.
//  2. Fall back to main entry if version-specific is thin.
//  3. Return ("", false) and log WARN if even the main is thin.
func (s *Service) selectEntry(log *slog.Logger, fw string, entries []outbound.LibraryEntry) (string, bool) {
	if len(entries) == 0 {
		log.Warn("bootstrap: no library entries returned; skipping", "framework", fw)
		return "", false
	}

	var versionEntry *outbound.LibraryEntry
	var mainEntry *outbound.LibraryEntry

	for i := range entries {
		e := &entries[i]
		if e.IsMain {
			if mainEntry == nil || e.Score > mainEntry.Score {
				mainEntry = e
			}
		} else {
			if versionEntry == nil || e.Score > versionEntry.Score {
				versionEntry = e
			}
		}
	}

	// Use the version-specific entry if fat enough.
	if versionEntry != nil && versionEntry.Snippets >= s.d.MinSnippets {
		return versionEntry.ID, true
	}
	// Fall back to the main entry.
	if mainEntry != nil && mainEntry.Snippets >= s.d.MinSnippets {
		log.Info("bootstrap: using main entry as fallback (version-specific entry thin)",
			"framework", fw,
			"version_snippets", func() int {
				if versionEntry != nil {
					return versionEntry.Snippets
				}
				return 0
			}())
		return mainEntry.ID, true
	}

	// All entries thin → WARN skip.
	log.Warn("bootstrap: thin entry below threshold; skipping", "framework", fw)
	return "", false
}

// isDocsUnavailable reports whether err wraps outbound.ErrDocsUnavailable.
func isDocsUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), outbound.ErrDocsUnavailable.Error())
}
