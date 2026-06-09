//go:build integration

package pg_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// TestSkillPhaseBaselineCapture captures pre-M1 SkillsForPhase output for all
// 9 SDD phases as regression golden files.
//
// Run modes:
//
//	GOLDEN_UPDATE=1 go test -tags=integration -run TestSkillPhaseBaselineCapture ./internal/adapters/outbound/pg/ -count=1
//	  → writes testdata/skill_phase_baseline/<phase>.golden.json for each phase.
//
//	go test -tags=integration -run TestSkillPhaseBaselineCapture ./internal/adapters/outbound/pg/ -count=1
//	  → reads committed goldens and asserts byte-equivalent output (regression gate).
//
// IMPORTANT: This test is the Group A hard gate. Run GOLDEN_UPDATE=1 on the
// main branch BEFORE any M1 production changes land. The pool is created via
// setupSkillPG which applies all migrations present in migrations/postgres/;
// on the main branch (pre-M1) that means migrations 001-009 only, which is the
// desired pre-migration-010 baseline state.
func TestSkillPhaseBaselineCapture(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()

	// setupSkillPG (defined in skill_repo_integration_test.go) starts a
	// testcontainer PG 16, applies all existing migrations, and returns a pool.
	// On the main branch this naturally stops at migration 009.
	pool := setupSkillPG(t)

	// Seed the 9 canonical skills using the current pre-M1 seeder.
	repo := pg.NewSkillRepo(pool)
	err := bootstrap.SeedSkills(ctx, repo, shared.SystemClock{}, slog.Default())
	require.NoError(t, err, "seeder must succeed against migration-009 DB")

	provider := pg.NewSkillProvider(repo)
	goldenDir := baselineGoldenDir(t)
	updateMode := os.Getenv("GOLDEN_UPDATE") == "1"

	if updateMode {
		t.Logf("GOLDEN_UPDATE=1: writing golden files to %s", goldenDir)
	}

	for _, pt := range phase.AllPhaseTypes() {
		pt := pt // capture loop var for parallel-safe closure
		t.Run(string(pt), func(t *testing.T) {
			skills, err := provider.SkillsForPhase(ctx, pt)
			require.NoError(t, err, "SkillsForPhase must not error for phase %q", pt)

			entries := buildBaselineEntries(skills)
			goldenData, err := json.MarshalIndent(entries, "", "  ")
			require.NoError(t, err, "marshal golden entries for phase %q", pt)

			goldenPath := filepath.Join(goldenDir, fmt.Sprintf("%s.golden.json", string(pt)))

			if updateMode {
				require.NoError(t,
					os.WriteFile(goldenPath, goldenData, 0o644),
					"write golden file for phase %q", pt)
				t.Logf("wrote golden: %s (%d skills)", goldenPath, len(entries))
				return
			}

			// Regression assert mode: read committed golden and compare.
			rawGolden, readErr := os.ReadFile(goldenPath)
			if os.IsNotExist(readErr) {
				t.Fatalf("missing golden file for phase %q — run GOLDEN_UPDATE=1 to capture baseline: %s",
					pt, goldenPath)
			}
			require.NoError(t, readErr, "read golden file for phase %q", pt)

			require.JSONEq(t, string(rawGolden), string(goldenData),
				"regression mismatch for phase %q — SkillsForPhase diverged from PR #76 baseline", pt)
		})
	}
}

// baselineEntryJSON is the stable JSON shape stored in each golden file.
// Sorted by SkillID ascending to make output deterministic regardless of DB
// insertion order.
type baselineEntryJSON struct {
	SkillID       string `json:"skill_id"`
	ContentSHA256 string `json:"content_sha256"`
}

// buildBaselineEntries converts a slice of Skills to the golden shape:
//
//	[{"skill_id": "...", "content_sha256": "<hex>"}, ...]
//
// sorted by skill_id ascending.
func buildBaselineEntries(skills []*skill.Skill) []baselineEntryJSON {
	entries := make([]baselineEntryJSON, 0, len(skills))
	for _, s := range skills {
		hash := sha256.Sum256([]byte(s.Content()))
		entries = append(entries, baselineEntryJSON{
			SkillID:       s.ID().String(),
			ContentSHA256: hex.EncodeToString(hash[:]),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SkillID < entries[j].SkillID
	})
	return entries
}

// baselineGoldenDir returns the absolute path to testdata/skill_phase_baseline/
// relative to this test file's location.
func baselineGoldenDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir, err := filepath.Abs(
		filepath.Join(filepath.Dir(here), "testdata", "skill_phase_baseline"),
	)
	require.NoError(t, err, "resolve testdata/skill_phase_baseline path")
	require.DirExists(t, dir, "testdata/skill_phase_baseline must exist")
	return dir
}
