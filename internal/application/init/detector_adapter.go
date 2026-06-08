package initphase

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
)

// DetectorAdapter wraps a *detector.Detector to satisfy the SophiaDetector port
// interface. The port interface returns DetectorResult (language/framework/arch
// data only); the full StructuralContext (which adds graph data and timestamps)
// is assembled by InitService.Run.
type DetectorAdapter struct {
	d *detector.Detector
}

// NewDetectorAdapter wraps d in a SophiaDetector-compatible adapter.
func NewDetectorAdapter(d *detector.Detector) *DetectorAdapter {
	return &DetectorAdapter{d: d}
}

// Detect runs structural detection and returns a DetectorResult.
func (a *DetectorAdapter) Detect(ctx context.Context, repoRoot string) (DetectorResult, error) {
	sc, err := a.d.Detect(ctx, repoRoot)
	if err != nil {
		return DetectorResult{}, err
	}
	return DetectorResult{
		Languages:       sc.Languages,
		Frameworks:      sc.Frameworks,
		PackageManagers: sc.PackageManagers,
		ArchStyle:       sc.ArchStyle,
		ConventionHints: sc.ConventionHints,
	}, nil
}

// Ensure DetectorAdapter satisfies SophiaDetector.
var _ SophiaDetector = &DetectorAdapter{}
