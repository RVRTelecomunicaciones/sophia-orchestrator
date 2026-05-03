package change

// Status is the lifecycle state of a Change. Closed enum.
type Status string

// Change lifecycle states.
const (
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusAborted   Status = "aborted"
)

// IsValid reports whether s is a known Status.
func (s Status) IsValid() bool {
	switch s {
	case StatusActive, StatusCompleted, StatusAborted:
		return true
	}
	return false
}

// IsTerminal reports whether s is a terminal state (no further transitions).
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusAborted
}

// ArtifactStoreMode controls where SDD artifacts (proposal, spec, design,
// tasks, etc.) are persisted: engram, openspec files, both, or none.
type ArtifactStoreMode string

// Artifact-store modes per spec § 7.1 + global CLAUDE.md "Artifact Store Policy".
const (
	ArtifactStoreEngram   ArtifactStoreMode = "engram"
	ArtifactStoreOpenspec ArtifactStoreMode = "openspec"
	ArtifactStoreHybrid   ArtifactStoreMode = "hybrid"
	ArtifactStoreNone     ArtifactStoreMode = "none"
)

// IsValid reports whether m is a known ArtifactStoreMode.
func (m ArtifactStoreMode) IsValid() bool {
	switch m {
	case ArtifactStoreEngram, ArtifactStoreOpenspec, ArtifactStoreHybrid, ArtifactStoreNone:
		return true
	}
	return false
}
