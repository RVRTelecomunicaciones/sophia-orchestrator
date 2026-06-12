// Package bootstrap — ports.go defines the narrow inbound ports required by
// BootstrapTriggerService from the infrastructure layer (PR3c-ii, DG-C7-6).
//
// Only SkillLookup is defined here. SkillRepoInserter is defined in importer.go
// (already part of this package) and shared by both the importer and the service.
package bootstrap

import (
	"context"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SkillLookup is the narrow port the service uses to find active skills for
// drift detection. It only exposes the query needed; bulk reads and writes are
// out of scope (DG-C7-9 — drift comparison lives in the service).
//
// Implemented by pg.SkillRepo.ActiveByName (T5.9).
type SkillLookup interface {
	// ActiveByName returns all active skills whose name matches the given
	// name string. Returns an empty (non-nil) slice when none match.
	// Used by drift detection to find the current stack/<fw>-<major> skill.
	ActiveByName(ctx context.Context, name string) ([]*skill.Skill, error)
}

// SkillImporterPort is the narrow port the service uses to delegate insertion
// of a new candidate skill. Satisfied by *SkillImporter via its ImportDocs
// wrapper method, which drops the *Skill return value.
//
// Using a separate interface (rather than *SkillImporter directly) keeps the
// service testable with a fake that records calls without touching the repo.
type SkillImporterPort interface {
	// ImportDocs builds and inserts a candidate skill from the resolved
	// documentation. Returns nil on success or when the row already exists
	// (idempotent). Any other error is returned for the caller to log+discard.
	// The constructed *Skill is not returned — callers only need the side effect.
	ImportDocs(
		ctx context.Context,
		name, version, fw string,
		r outbound.DocsResult,
	) error
}
