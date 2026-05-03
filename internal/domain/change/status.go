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
// tasks, etc.) are persisted: sophia-memory-engine, openspec files, both, or
// none. See ADR-0003 for the memory-engine integration contract.
type ArtifactStoreMode string

// Artifact-store modes per spec § 7.1.
//
// MemoryEngine is the canonical V1 backend: artifacts (proposal/spec/design/
// tasks/etc.) are persisted via the sophia-memory-engine HTTP API as typed
// MemoryRecord rows with topic_key = "sdd/{change_name}/{phase_type}".
// Openspec persists to filesystem under openspec/changes/{change_name}/.
// Hybrid writes both. None returns artifacts inline (transient).
const (
	ArtifactStoreMemoryEngine ArtifactStoreMode = "memory-engine"
	ArtifactStoreOpenspec     ArtifactStoreMode = "openspec"
	ArtifactStoreHybrid       ArtifactStoreMode = "hybrid"
	ArtifactStoreNone         ArtifactStoreMode = "none"
)

// IsValid reports whether m is a known ArtifactStoreMode.
func (m ArtifactStoreMode) IsValid() bool {
	switch m {
	case ArtifactStoreMemoryEngine, ArtifactStoreOpenspec, ArtifactStoreHybrid, ArtifactStoreNone:
		return true
	}
	return false
}
