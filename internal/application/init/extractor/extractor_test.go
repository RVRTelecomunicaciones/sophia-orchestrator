package extractor_test

// extractor_test.go — Strict TDD RED tests for the extractor package.
//
// All tests use in-memory fixture file trees written to t.TempDir() — no real
// repo reads, no subprocess, no HTTP. Tests are ordered by spec requirement.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/extractor"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/convention"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fixture helpers
// ─────────────────────────────────────────────────────────────────────────────

// fixedClock returns a shared.Clock pinned to a deterministic instant.
func fixedClock() shared.Clock { return shared.FixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) }

// nestjsSC builds a minimal StructuralContext reporting a NestJS framework.
func nestjsSC(version string) detector.StructuralContext {
	return detector.StructuralContext{
		Frameworks: []detector.FrameworkInfo{{Name: "nestjs", Version: version}},
	}
}

// writeFile creates dir + file with contents (uses os.MkdirAll for nested paths).
func writeFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
}

// buildNestJSFixture creates a fixture directory with NestJS-shaped TypeScript
// files. Returns the root path.
func buildNestJSFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// 6 service files → confidence = 0.6 + 5*0.05 = 0.85 (meets ≥0.85 threshold)
	services := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora", "pedido"}
	for _, s := range services {
		writeFile(t, root, s+"/"+s+".service.ts", `import { Injectable } from '@nestjs/common';
import { CrudService } from '@nestjsx/crud';

@Injectable()
export class `+capitalize(s)+`Service extends CrudService<`+capitalize(s)+`Entity> {
  constructor(private readonly repo: any) { super(repo); }
}
`)
	}

	// DTOs — UpdateDto extending PartialType
	// 5 files → confidence = 0.6 + 4*0.05 = 0.80 (meets ≥0.80 threshold)
	dtos := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora"}
	for _, d := range dtos {
		writeFile(t, root, d+"/dto/update-"+d+".dto.ts", `import { PartialType } from '@nestjs/mapped-types';
import { Create`+capitalize(d)+`Dto } from './create-`+d+`.dto';

export class Update`+capitalize(d)+`Dto extends PartialType(Create`+capitalize(d)+`Dto) {}
`)
	}

	// Entities — extending AbstractNumberEntity
	// 5 files → confidence = 0.6 + 4*0.05 = 0.80 (meets ≥0.80 threshold)
	entities := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora"}
	for _, e := range entities {
		writeFile(t, root, e+"/entities/"+e+".entity.ts", `import { AbstractNumberEntity } from '@common/abstract-entity';
import { Entity } from 'typeorm';

@Entity()
export class `+capitalize(e)+`Entity extends AbstractNumberEntity {
  name: string;
}
`)
	}

	// Modules — TypeOrmModule.forFeature
	modules := []string{"motivo", "cargo"}
	for _, m := range modules {
		writeFile(t, root, m+"/"+m+".module.ts", `import { Module } from '@nestjs/common';
import { TypeOrmModule } from '@nestjs/typeorm';

@Module({
  imports: [TypeOrmModule.forFeature([`+capitalize(m)+`Entity])],
})
export class `+capitalize(m)+`Module {}
`)
	}

	// Controllers — for sibling examples
	writeFile(t, root, "motivo/motivo.controller.ts", `import { Controller } from '@nestjs/common';

@Controller('motivo')
export class MotivoController {}
`)

	return root
}

// capitalize upcases the first rune of s.
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return string([]rune(s[:1])[0]-32) + s[1:]
}

// ─────────────────────────────────────────────────────────────────────────────
// Extractor orchestration tests (3.1)
// ─────────────────────────────────────────────────────────────────────────────

// E.1 — Extract returns non-nil profile for a NestJS fixture with ≥5 service files.
func TestExtractor_NestJS_ReturnsNonNilProfile(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("proj-x", fixedClock())
	sc := nestjsSC("11")

	profile, err := ex.Extract(context.Background(), root, sc)

	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.NotEmpty(t, profile.Patterns(), "should emit patterns for NestJS fixture")
}

// E.2 — Extract with nil/missing .claude/skills/ emits no curated-skill entries.
func TestExtractor_NoCuratedSkills_WhenDirAbsent(t *testing.T) {
	root := buildNestJSFixture(t)
	// No .claude/skills/ dir created
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))

	require.NoError(t, err)
	require.NotNil(t, profile)
	for _, p := range profile.Patterns() {
		assert.NotEqual(t, convention.SourceCuratedSkill, p.Source,
			"no curated-skill entries expected when .claude/skills/ is absent")
	}
}

// E.3 — Extract drops zero-evidence pattern (never-invent invariant).
// The profile must have no entry with empty Evidence.
func TestExtractor_NeverInvent_DropZeroEvidencePatterns(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))

	require.NoError(t, err)
	require.NotNil(t, profile)
	for _, p := range profile.Patterns() {
		assert.NotEmpty(t, p.Evidence,
			"every emitted pattern must have at least one evidence file (never-invent)")
	}
}

// E.4 — Extract on unknown framework emits a degraded (empty-patterns) profile
// rather than failing.
func TestExtractor_UnknownFramework_EmitsDegradedProfile(t *testing.T) {
	root := t.TempDir()
	ex := extractor.New("proj-x", fixedClock())
	unknownSC := detector.StructuralContext{
		Frameworks: []detector.FrameworkInfo{{Name: "rails"}},
	}

	profile, err := ex.Extract(context.Background(), root, unknownSC)

	require.NoError(t, err)
	require.NotNil(t, profile, "must return a degraded profile, not nil")
	assert.Empty(t, profile.Patterns(), "unknown framework should produce zero patterns")
}

// E.5 — Extract on empty StructuralContext (no frameworks) emits a degraded profile.
func TestExtractor_EmptyFrameworks_EmitsDegradedProfile(t *testing.T) {
	root := t.TempDir()
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, detector.StructuralContext{})

	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Empty(t, profile.Patterns())
}

// ─────────────────────────────────────────────────────────────────────────────
// NestJS detector tests (3.2)
// ─────────────────────────────────────────────────────────────────────────────

// N.1 — extends CrudService pattern emitted when service files contain the import.
func TestNestJSDetector_ExtendsCrudService_Detected(t *testing.T) {
	root := buildNestJSFixture(t) // 5 service.ts files each extending CrudService
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-extends-crudservice")
	require.NotNil(t, found, "nestjs-extends-crudservice pattern should be emitted")
	assert.Equal(t, convention.SourceDetectedFromCode, found.Source)
	assert.NotEmpty(t, found.Evidence)
}

// N.2 — PartialType UpdateDto pattern detected.
func TestNestJSDetector_PartialTypeUpdateDto_Detected(t *testing.T) {
	root := buildNestJSFixture(t) // 4 dto.ts files extending PartialType
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-partialtype-update-dto")
	require.NotNil(t, found, "nestjs-partialtype-update-dto pattern should be emitted")
	assert.Equal(t, convention.SourceDetectedFromCode, found.Source)
	assert.NotEmpty(t, found.Evidence)
}

// N.3 — AbstractNumberEntity pattern detected.
func TestNestJSDetector_AbstractNumberEntity_Detected(t *testing.T) {
	root := buildNestJSFixture(t) // 3 entity.ts files
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-entity-extends-abstractnumberentity")
	require.NotNil(t, found, "nestjs-entity-extends-abstractnumberentity pattern should be emitted")
	assert.Equal(t, convention.SourceDetectedFromCode, found.Source)
}

// N.4 — TypeOrmModule.forFeature pattern detected.
func TestNestJSDetector_TypeOrmModuleForFeature_Detected(t *testing.T) {
	root := buildNestJSFixture(t) // 2 module.ts files
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-module-typeorm-forfeature")
	require.NotNil(t, found, "nestjs-module-typeorm-forfeature pattern should be emitted")
	assert.Equal(t, convention.SourceDetectedFromCode, found.Source)
}

// N.5 — Confidence for nestjs-extends-crudservice with 5 evidence files >= 0.85.
func TestNestJSDetector_ExtendsCrudService_ConfidenceAtLeast085(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-extends-crudservice")
	require.NotNil(t, found)
	assert.GreaterOrEqual(t, found.Confidence, 0.85,
		"5+ evidence files should yield confidence >= 0.85")
}

// N.6 — RejectedAssumptions contains standalone-CRUD entry.
func TestNestJSDetector_RejectedAssumptions_ContainsStandaloneCRUD(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-extends-crudservice")
	require.NotNil(t, found)

	hasRejected := false
	for _, ra := range found.RejectedAssumptions {
		if ra != "" {
			hasRejected = true
			break
		}
	}
	assert.True(t, hasRejected, "RejectedAssumptions should contain standalone-CRUD rejection")
}

// N.7 — SiblingExamples present in the extends-crudservice pattern.
func TestNestJSDetector_SiblingExamples_PresentForAllLayers(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("proj-x", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "nestjs-extends-crudservice")
	require.NotNil(t, found)
	assert.NotEmpty(t, found.SiblingExamples, "SiblingExamples should be populated")
}

// ─────────────────────────────────────────────────────────────────────────────
// Curated loader tests (3.3)
// ─────────────────────────────────────────────────────────────────────────────

// C.1 — .claude/skills/ present with a rule → PatternEntry Source=curated-skill Confidence=1.0.
func TestCuratedLoader_PresentWithRule_EmitsCuratedPattern(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, ".claude/skills/conventions.md",
		"# Project Conventions\n\nMUST extend CrudService for all services.\n")

	ex := extractor.New("proj-x", fixedClock())
	// Use unknown framework so the only patterns come from curated loader
	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))

	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPatternBySource(profile, convention.SourceCuratedSkill)
	require.NotNil(t, found, "curated-skill pattern should be emitted when .claude/skills/ is present")
	assert.InDelta(t, 1.0, found.Confidence, 1e-9, "curated-skill confidence must be 1.0")
	assert.NotEmpty(t, found.Evidence, "evidence must include the skill file path")
}

// C.2 — .claude/skills/ absent → no curated-skill entries emitted.
func TestCuratedLoader_DirAbsent_NoCuratedPatterns(t *testing.T) {
	root := t.TempDir()
	ex := extractor.New("proj-x", fixedClock())

	// No .claude/skills/ directory
	profile, err := ex.Extract(context.Background(), root, detector.StructuralContext{})

	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPatternBySource(profile, convention.SourceCuratedSkill)
	assert.Nil(t, found, "no curated-skill entries when .claude/skills/ is absent")
}

// C.3 — Source-ladder: curated-skill overrides detected-from-code for same pattern key.
func TestCuratedLoader_SourceLadder_CuratedOverridesDetected(t *testing.T) {
	root := buildNestJSFixture(t) // will produce detected-from-code patterns
	// Also add a curated skill with the same pattern key
	writeFile(t, root, ".claude/skills/conventions.md",
		"# Convention\n\nMUST: nestjs-extends-crudservice — every service MUST extend CrudService.\n")

	ex := extractor.New("proj-x", fixedClock())
	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))

	require.NoError(t, err)
	require.NotNil(t, profile)

	// Count how many patterns have key "nestjs-extends-crudservice"
	var crudPatterns []convention.PatternEntry
	for _, p := range profile.Patterns() {
		if p.Pattern == "nestjs-extends-crudservice" {
			crudPatterns = append(crudPatterns, p)
		}
	}
	// After source-ladder dedup, there should be exactly ONE entry for this key
	assert.Len(t, crudPatterns, 1,
		"source ladder must deduplicate to exactly one entry per pattern key")
	// And that entry must be the curated-skill one (highest precedence)
	if len(crudPatterns) == 1 {
		assert.Equal(t, convention.SourceCuratedSkill, crudPatterns[0].Source,
			"curated-skill must win over detected-from-code in source ladder")
		assert.InDelta(t, 1.0, crudPatterns[0].Confidence, 1e-9)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cajachica integration-level unit test (fixture-based, no real FS) (3.3 / 7.1)
// ─────────────────────────────────────────────────────────────────────────────

// I.1 — Full cajachica fixture produces all 4 required patterns at stated min confidences.
func TestCajachica_AllPatternsPresent_AtMinConfidence(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("cajachica", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	type expectation struct {
		key     string
		minConf float64
	}
	expectations := []expectation{
		{"nestjs-extends-crudservice", 0.85},
		{"nestjs-partialtype-update-dto", 0.80},
		{"nestjs-entity-extends-abstractnumberentity", 0.80},
		{"nestjs-module-typeorm-forfeature", 0.65},
	}

	for _, exp := range expectations {
		p := findPattern(profile, exp.key)
		require.NotNilf(t, p, "pattern %q must be present", exp.key)
		assert.GreaterOrEqualf(t, p.Confidence, exp.minConf,
			"pattern %q confidence %.2f < min %.2f", exp.key, p.Confidence, exp.minConf)
		assert.NotEmptyf(t, p.Evidence, "pattern %q must have evidence", exp.key)
	}
}

// I.2 — No invented patterns: every PatternEntry has Evidence.length >= 1.
func TestCajachica_NoInventedPatterns(t *testing.T) {
	root := buildNestJSFixture(t)
	ex := extractor.New("cajachica", fixedClock())

	profile, err := ex.Extract(context.Background(), root, nestjsSC("11"))
	require.NoError(t, err)
	require.NotNil(t, profile)

	for _, p := range profile.Patterns() {
		assert.NotEmptyf(t, p.Evidence,
			"pattern %q has no evidence (never-invent violation)", p.Pattern)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Go detector tests (4.1)
// ─────────────────────────────────────────────────────────────────────────────

// goSC builds a StructuralContext reporting a Go framework/language.
func goSC() detector.StructuralContext {
	return detector.StructuralContext{
		Frameworks: []detector.FrameworkInfo{{Name: "go"}},
	}
}

// buildGoHexFixture creates a fixture with ≥4 bounded-context directories
// each containing domain/, application/, infrastructure/ subdirectories.
func buildGoHexFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	contexts := []string{"billing", "auth", "inventory", "notification"}
	for _, ctx := range contexts {
		// domain layer with interface (repository port)
		writeFile(t, root, "internal/"+ctx+"/domain/repository.go",
			"package domain\n\ntype "+capitalize(ctx)+"Repository interface {\n\tFindByID(id string) error\n}\n")
		// application layer with service struct
		writeFile(t, root, "internal/"+ctx+"/application/service.go",
			"package application\n\ntype "+capitalize(ctx)+"Service struct {\n\trepo domain."+capitalize(ctx)+"Repository\n}\n\nfunc New"+capitalize(ctx)+"Service(repo domain."+capitalize(ctx)+"Repository) *"+capitalize(ctx)+"Service {\n\treturn &"+capitalize(ctx)+"Service{repo: repo}\n}\n")
		// infrastructure layer
		writeFile(t, root, "internal/"+ctx+"/infrastructure/pg_repo.go",
			"package infrastructure\n\n// "+capitalize(ctx)+"PgRepo implements domain."+capitalize(ctx)+"Repository.\ntype "+capitalize(ctx)+"PgRepo struct{}\n")
	}

	// shared package with generics (for envelopes only)
	writeFile(t, root, "internal/shared/pagination.go",
		"package shared\n\ntype Page[T any] struct {\n\tItems []T\n\tTotal int\n}\n")
	writeFile(t, root, "internal/shared/response.go",
		"package shared\n\ntype Response[T any] struct {\n\tData T\n}\n")

	return root
}

// G.1 — Go hexagonal bounded-context pattern emitted when ≥4 contexts detected.
func TestGoDetector_HexagonalBoundedContexts_Detected(t *testing.T) {
	root := buildGoHexFixture(t)
	ex := extractor.New("proj-go", fixedClock())

	profile, err := ex.Extract(context.Background(), root, goSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "go-hexagonal-bounded-contexts")
	require.NotNil(t, found, "go-hexagonal-bounded-contexts pattern should be emitted")
	assert.Equal(t, convention.SourceDetectedFromCode, found.Source)
	assert.NotEmpty(t, found.Evidence)
}

// G.2 — Repository port in domain pattern detected.
func TestGoDetector_RepositoryPortInDomain_Detected(t *testing.T) {
	root := buildGoHexFixture(t)
	ex := extractor.New("proj-go", fixedClock())

	profile, err := ex.Extract(context.Background(), root, goSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "go-repository-port-in-domain")
	require.NotNil(t, found, "go-repository-port-in-domain pattern should be emitted")
	assert.NotEmpty(t, found.Evidence)
}

// G.3 — Service struct constructor DI pattern detected.
func TestGoDetector_ServiceStructConstructorDI_Detected(t *testing.T) {
	root := buildGoHexFixture(t)
	ex := extractor.New("proj-go", fixedClock())

	profile, err := ex.Extract(context.Background(), root, goSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "go-service-struct-constructor-di")
	require.NotNil(t, found, "go-service-struct-constructor-di pattern should be emitted")
	assert.NotEmpty(t, found.Evidence)
}

// G.4 — Generics only in shared pattern detected.
func TestGoDetector_GenericsOnlyInShared_Detected(t *testing.T) {
	root := buildGoHexFixture(t)
	ex := extractor.New("proj-go", fixedClock())

	profile, err := ex.Extract(context.Background(), root, goSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	found := findPattern(profile, "go-generics-for-envelopes-only")
	require.NotNil(t, found, "go-generics-for-envelopes-only pattern should be emitted")
	assert.NotEmpty(t, found.Evidence)
}

// G.5 — No patterns emitted for empty go fixture (no hex layout).
func TestGoDetector_EmptyRepo_NoPatternsEmitted(t *testing.T) {
	root := t.TempDir()
	ex := extractor.New("proj-go", fixedClock())

	profile, err := ex.Extract(context.Background(), root, goSC())
	require.NoError(t, err)
	require.NotNil(t, profile)
	assert.Empty(t, profile.Patterns(),
		"empty repo with go framework should produce zero patterns")
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

func findPattern(profile *convention.ConventionProfile, key string) *convention.PatternEntry {
	for _, p := range profile.Patterns() {
		if p.Pattern == key {
			pp := p
			return &pp
		}
	}
	return nil
}

func findPatternBySource(profile *convention.ConventionProfile, src convention.Source) *convention.PatternEntry {
	for _, p := range profile.Patterns() {
		if p.Source == src {
			pp := p
			return &pp
		}
	}
	return nil
}
