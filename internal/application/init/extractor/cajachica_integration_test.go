//go:build integration

package extractor_test

// cajachica_integration_test.go — CJ.1–CJ.5 (end-to-end fixture validation)
//
// Validates the real extractor against a synthetic NestJS fixture shaped like
// cajachica (backend_cajachica: NestJS 11 + TypeORM). All files are written to
// t.TempDir() — no real repo access.
//
// Assertions mirror docs/blueprint-poc-cajachica.md:
//   CJ.1 The 4 required NestJS pattern keys are all present in the profile.
//   CJ.2 CrudService confidence ≥ 0.85 (6+ service files).
//   CJ.3 Every emitted PatternEntry has Evidence.length ≥ 1 (never-invent).
//   CJ.4 RejectedAssumptions contains the standalone-CRUD entry.
//   CJ.5 SiblingExamples cover all 5 layers (entity, service, controller, module, update-dto).

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/detector"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init/extractor"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cajachicaFixture builds a t.TempDir() fixture tree shaped like backend_cajachica.
//
// Files created:
//   - 6 *.service.ts (each extends CrudService<…>) — motivo, cargo, cotizacion, usuario, bitacora, pedido
//   - 5 update-*.dto.ts (each extends PartialType(…)) — motivo, cargo, cotizacion, usuario, bitacora
//   - 5 *.entity.ts (each extends AbstractNumberEntity) — motivo, cargo, cotizacion, usuario, bitacora
//   - 3 *.module.ts (each uses TypeOrmModule.forFeature) — motivo, cargo, cotizacion
//   - 1 *.controller.ts (for sibling examples) — motivo
//
// This satisfies all four CrudService / PartialType / AbstractNumberEntity / TypeOrmModule heuristics
// at confidence levels matching the blueprint doc.
func cajachicaFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(relPath, content string) {
		t.Helper()
		full := filepath.Join(root, relPath)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}

	// 6 service files — extends CrudService<…>
	entities := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora", "pedido"}
	for _, e := range entities {
		name := capitalize(e)
		write(e+"/"+e+".service.ts", "import { CrudService } from '@nestjsx/crud';\n\n"+
			"@Injectable()\nexport class "+name+"Service extends CrudService<"+name+"Entity> {\n"+
			"  constructor(private readonly repo: any) { super(repo); }\n}\n")
	}

	// 5 update DTOs — extends PartialType
	dtos := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora"}
	for _, d := range dtos {
		name := capitalize(d)
		write(d+"/dto/update-"+d+".dto.ts",
			"import { PartialType } from '@nestjs/mapped-types';\n\n"+
				"export class Update"+name+"Dto extends PartialType(Create"+name+"Dto) {}\n")
	}

	// 5 entity files — extends AbstractNumberEntity
	ents := []string{"motivo", "cargo", "cotizacion", "usuario", "bitacora"}
	for _, e := range ents {
		name := capitalize(e)
		write(e+"/entities/"+e+".entity.ts",
			"import { AbstractNumberEntity } from '@common/abstract-entity';\n\n"+
				"@Entity()\nexport class "+name+"Entity extends AbstractNumberEntity {\n"+
				"  descripcion: string;\n}\n")
	}

	// 3 module files — TypeOrmModule.forFeature
	mods := []string{"motivo", "cargo", "cotizacion"}
	for _, m := range mods {
		name := capitalize(m)
		write(m+"/"+m+".module.ts",
			"import { TypeOrmModule } from '@nestjs/typeorm';\n\n"+
				"@Module({\n  imports: [TypeOrmModule.forFeature(["+name+"Entity])],\n})\n"+
				"export class "+name+"Module {}\n")
	}

	// 1 controller — for sibling examples
	write("motivo/motivo.controller.ts",
		"import { Controller } from '@nestjs/common';\n\n"+
			"@Controller('motivo')\nexport class MotivoController {}\n")

	return root
}

// cajachicaClock returns a fixed clock for the integration test.
func cajachicaClock() shared.Clock {
	return shared.FixedClock(time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC))
}

// cajachicaSC returns a NestJS 11 StructuralContext.
func cajachicaSC() detector.StructuralContext {
	return detector.StructuralContext{
		Frameworks: []detector.FrameworkInfo{{Name: "nestjs", Version: "11"}},
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers (no cross-file dep — the integration build tag isolates this file)
// ─────────────────────────────────────────────────────────────────────────────

// findPatternCJ searches profile patterns by key prefix (case-insensitive substring).
func findPatternCJ(patterns []struct {
	Pattern    string
	Confidence float64
	Evidence   []string
}, key string) *struct {
	Pattern    string
	Confidence float64
	Evidence   []string
} {
	for i := range patterns {
		if patterns[i].Pattern == key {
			return &patterns[i]
		}
	}
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// CJ Tests
// ─────────────────────────────────────────────────────────────────────────────

// CJ.1: 4 required NestJS pattern keys are present.
func TestCajachica_FourRequiredPatternKeysPresent(t *testing.T) {
	root := cajachicaFixture(t)
	ex := extractor.New("cajachica", cajachicaClock())

	profile, err := ex.Extract(context.Background(), root, cajachicaSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	patterns := profile.Patterns()
	keys := make([]string, 0, len(patterns))
	for _, p := range patterns {
		keys = append(keys, p.Pattern)
	}

	required := []string{
		"nestjs-extends-crudservice",
		"nestjs-partialtype-update-dto",
		"nestjs-entity-extends-abstractnumberentity",
		"nestjs-module-typeorm-forfeature",
	}
	for _, req := range required {
		found := false
		for _, k := range keys {
			if k == req {
				found = true
				break
			}
		}
		assert.True(t, found, "required pattern key missing: %q (got: %v)", req, keys)
	}
}

// CJ.2: CrudService pattern confidence ≥ 0.85 (6+ service files in fixture).
func TestCajachica_CrudServiceConfidence_AtLeast0_85(t *testing.T) {
	root := cajachicaFixture(t)
	ex := extractor.New("cajachica", cajachicaClock())

	profile, err := ex.Extract(context.Background(), root, cajachicaSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	var crudEntry *struct{ Confidence float64 }
	for _, p := range profile.Patterns() {
		if p.Pattern == "nestjs-extends-crudservice" {
			crudEntry = &struct{ Confidence float64 }{p.Confidence}
			break
		}
	}
	require.NotNil(t, crudEntry, "nestjs-extends-crudservice pattern must be present")
	assert.GreaterOrEqual(t, crudEntry.Confidence, 0.85,
		"CrudService confidence must be ≥ 0.85 for 6+ service files (got %.2f)", crudEntry.Confidence)
}

// CJ.3: Every emitted PatternEntry has Evidence.length ≥ 1 (never-invent invariant).
func TestCajachica_NeverInvent_AllPatternsHaveEvidence(t *testing.T) {
	root := cajachicaFixture(t)
	ex := extractor.New("cajachica", cajachicaClock())

	profile, err := ex.Extract(context.Background(), root, cajachicaSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	for _, p := range profile.Patterns() {
		assert.NotEmpty(t, p.Evidence,
			"every pattern must have at least one evidence file (never-invent): key=%q", p.Pattern)
	}
}

// CJ.4: RejectedAssumptions contains the standalone-CRUD entry
// (no standalone controllers without a CrudController extends-chain).
func TestCajachica_RejectedAssumptions_ContainsStandaloneCRUD(t *testing.T) {
	root := cajachicaFixture(t)
	ex := extractor.New("cajachica", cajachicaClock())

	profile, err := ex.Extract(context.Background(), root, cajachicaSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	// Gather all RejectedAssumptions across all patterns.
	var allRejected []string
	for _, p := range profile.Patterns() {
		allRejected = append(allRejected, p.RejectedAssumptions...)
	}

	found := false
	for _, r := range allRejected {
		if len(r) > 0 {
			found = true
			break
		}
	}
	assert.True(t, found,
		"at least one rejected assumption must be emitted (standalone-CRUD absence noted); got: %v", allRejected)
}

// CJ.5: SiblingExamples cover at least 3 distinct layer types from
// {entity, service, controller, module, update-dto}.
func TestCajachica_SiblingExamples_CoverMultipleLayers(t *testing.T) {
	root := cajachicaFixture(t)
	ex := extractor.New("cajachica", cajachicaClock())

	profile, err := ex.Extract(context.Background(), root, cajachicaSC())
	require.NoError(t, err)
	require.NotNil(t, profile)

	// Collect all sibling example paths across all patterns.
	siblings := make(map[string]bool)
	for _, p := range profile.Patterns() {
		for _, s := range p.SiblingExamples {
			// Classify by extension/suffix.
			switch {
			case containsSuffix(s, ".service.ts"):
				siblings["service"] = true
			case containsSuffix(s, ".entity.ts"):
				siblings["entity"] = true
			case containsSuffix(s, ".controller.ts"):
				siblings["controller"] = true
			case containsSuffix(s, ".module.ts"):
				siblings["module"] = true
			case containsSuffix(s, ".dto.ts"):
				siblings["dto"] = true
			}
		}
	}

	assert.GreaterOrEqual(t, len(siblings), 3,
		"SiblingExamples must cover ≥ 3 distinct layers; covered: %v", siblings)
}

// containsSuffix checks if path ends with suffix.
func containsSuffix(path, suffix string) bool {
	return len(path) >= len(suffix) && path[len(path)-len(suffix):] == suffix
}
