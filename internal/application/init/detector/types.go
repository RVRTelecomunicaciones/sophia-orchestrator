// Package detector provides the structural detection types for the INIT phase.
// StructuralContext and its supporting types now live in internal/domain/structural;
// this file re-exports them as type aliases for transitional source compatibility
// (D-M3-3). Detector code may continue referencing detector.StructuralContext;
// new code SHOULD import internal/domain/structural directly.
package detector

import "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"

// StructuralContextSchemaV1 is re-exported from domain/structural for
// transitional source compatibility. The canonical constant is structural.SchemaV1.
const StructuralContextSchemaV1 = structural.SchemaV1

// SophiaDetectorVer is the 7th cache key component. Bump this constant
// whenever the detector parsing logic changes to ensure stale caches are
// invalidated automatically.
// v1.1.0: added Greenfield detection + ManifestHash 8th cache key component (DG-C7-2/3).
const SophiaDetectorVer = "v1.1.0"

// Type aliases — detector.StructuralContext is THE SAME TYPE as
// structural.StructuralContext (type alias, not a new type). All existing
// detector consumers compile without modification; no conversion needed at
// any call site.

// StructuralContext re-exports structural.StructuralContext for source compatibility.
type StructuralContext = structural.StructuralContext

// LanguageInfo re-exports structural.LanguageInfo for source compatibility.
type LanguageInfo = structural.LanguageInfo

// FrameworkInfo re-exports structural.FrameworkInfo for source compatibility.
type FrameworkInfo = structural.FrameworkInfo

// GraphSummary re-exports structural.GraphSummary for source compatibility.
type GraphSummary = structural.GraphSummary
