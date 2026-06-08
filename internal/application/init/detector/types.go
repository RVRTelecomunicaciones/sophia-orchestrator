// Package detector provides the pure-Go StructuralContext and its supporting
// types. These are the types produced by the structural detection phase (INIT)
// and consumed by InitService, persister, and cache layers.
package detector

import "time"

// StructuralContextSchemaV1 is the current schema version for StructuralContext.
// Consumers MUST check this field before deserializing. Bump when breaking
// changes are made to the struct and accompany with a migration plan.
const StructuralContextSchemaV1 = 1

// SophiaDetectorVer is the 7th cache key component. Bump this constant
// whenever the detector parsing logic changes to ensure stale caches are
// invalidated automatically.
const SophiaDetectorVer = "v1.0.0"

// StructuralContext is the output of a completed INIT phase. It summarises
// the detected languages, frameworks, architecture style, graph summary, and
// any degraded-mode indicators for the repository.
type StructuralContext struct {
	// SchemaVersion is always StructuralContextSchemaV1 (= 1).
	SchemaVersion int `json:"schema_version"`

	// ProjectID / ChangeID / ChangeName come from the orchestrator Change.
	ProjectID  string `json:"project_id"`
	ChangeID   string `json:"change_id"`
	ChangeName string `json:"change_name"`

	// Languages detected from manifest files (go.mod, package.json, etc.).
	Languages []LanguageInfo `json:"languages,omitempty"`

	// Frameworks detected from dependency manifests + file fingerprints.
	Frameworks []FrameworkInfo `json:"frameworks,omitempty"`

	// PackageManagers detected (e.g. "go modules", "npm", "pnpm", "uv").
	PackageManagers []string `json:"package_managers,omitempty"`

	// ArchStyle holds architecture style labels ("hexagonal", "monorepo",
	// "microservices", "monolith").
	ArchStyle []string `json:"arch_style,omitempty"`

	// GraphSummary is populated when graphify ran successfully; nil in
	// degraded mode.
	GraphSummary *GraphSummary `json:"graph_summary,omitempty"`

	// AffectedModules is a best-effort list of module paths relevant to
	// this change (populated from graph analysis when available).
	AffectedModules []string `json:"affected_modules,omitempty"`

	// ConventionHints are human-readable hints detected by heuristics
	// (e.g. "Angular Signals variant: app.config.ts present, no @ngrx/store").
	ConventionHints []string `json:"convention_hints,omitempty"`

	// GraphAvailable reports whether graphify executed successfully.
	GraphAvailable bool `json:"graph_available"`

	// DegradedReason explains why GraphAvailable=false (empty when not degraded).
	DegradedReason string `json:"degraded_reason,omitempty"`

	// DetectedAt is the UTC timestamp of detection (injected via Clock).
	DetectedAt time.Time `json:"detected_at"`

	// GraphifyVersion is the version string captured from `graphify --version`.
	GraphifyVersion string `json:"graphify_version,omitempty"`

	// SophiaDetectorVer is the version of the detector logic used (7th
	// cache key component — see const SophiaDetectorVer).
	SophiaDetectorVer string `json:"sophia_detector_ver"`
}

// LanguageInfo holds language detection evidence for a single language.
type LanguageInfo struct {
	// Name is the canonical language name (e.g. "Go", "TypeScript", "Python").
	Name string `json:"name"`

	// VersionEvidence is the version string extracted from the manifest
	// (e.g. "go 1.26", "^18.0.0").
	VersionEvidence string `json:"version_evidence,omitempty"`

	// FilesCount is an estimated count of source files for this language.
	FilesCount int `json:"files_count,omitempty"`
}

// FrameworkInfo holds framework detection evidence for a single framework.
type FrameworkInfo struct {
	// Name is the canonical framework name (e.g. "Angular", "Spring Boot",
	// "FastAPI").
	Name string `json:"name"`

	// Version is the detected version string from the dependency manifest.
	Version string `json:"version,omitempty"`

	// EvidencePath is the manifest file that provided detection evidence
	// (e.g. "package.json", "build.gradle").
	EvidencePath string `json:"evidence_path,omitempty"`
}

// GraphSummary holds the high-level graph statistics extracted from
// graphify-out/graph.json.
type GraphSummary struct {
	// TotalNodes is the number of nodes in the dependency graph.
	TotalNodes int `json:"total_nodes"`

	// TotalEdges is the number of edges in the dependency graph.
	TotalEdges int `json:"total_edges"`

	// CommunityCount is the number of detected communities / clusters.
	CommunityCount int `json:"community_count"`

	// GodNodes lists the top-10 nodes by out_degree (potential God objects).
	GodNodes []string `json:"god_nodes,omitempty"`
}
