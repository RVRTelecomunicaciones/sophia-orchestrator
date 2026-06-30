package extractor

// nestjs_detector.go — NestJS-specific convention heuristics.
//
// Scans *.service.ts, *.dto.ts, *.entity.ts, and *.module.ts files in the
// repo tree and emits PatternEntry records that feed the ConventionProfile
// aggregate. All pattern detection is regex-based, pure-FS. No subprocess,
// no HTTP.
//
// Patterns emitted:
//   - nestjs-extends-crudservice
//   - nestjs-partialtype-update-dto
//   - nestjs-entity-extends-abstractnumberentity
//   - nestjs-module-typeorm-forfeature
//
// Never-invent invariant enforced here: if no matching files are found the
// pattern is NOT included in the returned slice.

import (
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
)

var (
	reCrudService       = regexp.MustCompile(`extends\s+CrudService\s*<`)
	rePartialType       = regexp.MustCompile(`extends\s+PartialType\s*\(`)
	reAbstractEntity    = regexp.MustCompile(`extends\s+AbstractNumberEntity`)
	reTypeOrmForFeature = regexp.MustCompile(`TypeOrmModule\.forFeature`)
	reStandaloneMethod  = regexp.MustCompile(`(?m)^\s+(?:async\s+)?(?:findAll|findOne|create|update|remove)\s*\(`)
)

// nestjsResult carries all patterns emitted by detectNestJS.
type nestjsResult struct {
	patterns []convention.PatternEntry
}

// detectNestJS walks repoRoot and emits NestJS convention patterns. It returns
// an empty slice (no patterns) when none of the detection heuristics find evidence.
func detectNestJS(repoRoot string) []convention.PatternEntry {
	var out []convention.PatternEntry

	// ── nestjs-extends-crudservice ─────────────────────────────────────────────
	crudEvidence := scanFiles(repoRoot, "*.service.ts", reCrudService)
	standaloneFiles := scanFiles(repoRoot, "*.service.ts", reStandaloneMethod)

	if len(crudEvidence) > 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(crudEvidence))

		// RejectedAssumptions: if all service files use CrudService and no
		// standalone hand-written CRUD methods are found, reject the assumption
		// that a standalone CRUD implementation exists.
		var rejected []string
		if len(standaloneFiles) == 0 {
			rejected = append(rejected, "Has a standalone per-entity CRUD implementation")
		}

		// SiblingExamples: collect one canonical file per layer.
		siblings := collectSiblingExamples(repoRoot)

		out = append(out, convention.PatternEntry{
			Pattern:    "nestjs-extends-crudservice",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   crudEvidence,
			Rule: "Every service class MUST extend CrudService<Entity> and inject its " +
				"repository via the constructor. Do NOT implement findAll, findOne, " +
				"create, update, or remove by hand.",
			SiblingExamples:     siblings,
			RejectedAssumptions: rejected,
		})
	}

	// ── nestjs-partialtype-update-dto ─────────────────────────────────────────
	dtoEvidence := scanFiles(repoRoot, "*.dto.ts", rePartialType)
	if len(dtoEvidence) > 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(dtoEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "nestjs-partialtype-update-dto",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   dtoEvidence,
			Rule: "Every Update DTO MUST extend PartialType(CreateDto) using " +
				"@nestjs/mapped-types. Do NOT redefine fields in Update DTOs.",
		})
	}

	// ── nestjs-entity-extends-abstractnumberentity ─────────────────────────────
	entityEvidence := scanFiles(repoRoot, "*.entity.ts", reAbstractEntity)
	if len(entityEvidence) > 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(entityEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "nestjs-entity-extends-abstractnumberentity",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   entityEvidence,
			Rule: "Every TypeORM entity MUST extend AbstractNumberEntity which provides " +
				"the numeric auto-increment primary key. Do NOT redefine @PrimaryGeneratedColumn().",
		})
	}

	// ── nestjs-module-typeorm-forfeature ──────────────────────────────────────
	moduleEvidence := scanFiles(repoRoot, "*.module.ts", reTypeOrmForFeature)
	if len(moduleEvidence) > 0 {
		conf := convention.ComputeConfidence(convention.SourceDetectedFromCode, len(moduleEvidence))
		out = append(out, convention.PatternEntry{
			Pattern:    "nestjs-module-typeorm-forfeature",
			Source:     convention.SourceDetectedFromCode,
			Confidence: conf,
			Evidence:   moduleEvidence,
			Rule: "Every feature module that owns TypeORM entities MUST register them " +
				"with TypeOrmModule.forFeature([Entity]) in its imports array.",
		})
	}

	return out
}

// scanFiles walks repoRoot recursively and returns the relative paths of files
// matching glob whose content matches re. Never returns nil — returns an empty
// (non-nil) slice when no files match.
func scanFiles(repoRoot, glob string, re *regexp.Regexp) []string {
	var matches []string
	_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		matched, _ := filepath.Match(glob, filepath.Base(path))
		if !matched {
			return nil
		}
		content, readErr := readFileBytes(path)
		if readErr != nil {
			return nil
		}
		if re.Match(content) {
			rel, _ := filepath.Rel(repoRoot, path)
			matches = append(matches, rel)
		}
		return nil
	})
	if matches == nil {
		return []string{}
	}
	return matches
}

// collectSiblingExamples finds one canonical file per NestJS layer: entity,
// service, controller, module, update-dto.
func collectSiblingExamples(repoRoot string) []string {
	var examples []string

	type layerRule struct {
		glob   string
		suffix string // optional filter on filename
	}
	layers := []layerRule{
		{"*.entity.ts", ""},
		{"*.service.ts", ""},
		{"*.controller.ts", ""},
		{"*.module.ts", ""},
		{"update-*.dto.ts", ""},
	}

	for _, lr := range layers {
		_ = filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || len(examples) >= cap(layers)+len(examples) {
				return nil
			}
			matched, _ := filepath.Match(lr.glob, filepath.Base(path))
			if matched {
				if lr.suffix == "" || strings.HasSuffix(path, lr.suffix) {
					rel, _ := filepath.Rel(repoRoot, path)
					// Only add one per layer (stop after first match)
					examples = append(examples, rel)
					return fs.SkipAll
				}
			}
			return nil
		})
	}

	return examples
}
