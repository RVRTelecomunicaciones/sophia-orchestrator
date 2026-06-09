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

// TestSkillProvider_SkillsForPhase_RegressionZero is the M1 regression gate.
//
// It seeds all 9 canonical skills against a fully-migrated (010) testcontainers
// DB, then calls provider.SkillsForPhase for each of the 9 SDD phases and
// compares the result against the committed golden files in
// testdata/skill_phase_baseline/<phase>.golden.json.
//
// If any phase diverges from the golden baseline, the test FAILS HARD — this
// indicates that M1 changes broke the pre-change SkillsForPhase behavior
// established by Group A (commit e83c140).
//
// This is the Spec regression snapshot gate per D-M1-8.
func TestSkillProvider_SkillsForPhase_RegressionZero(t *testing.T) {
	skipIfNoDocker(t)

	ctx := context.Background()
	pool := setupSkillPG(t) // applies ALL migrations including 010

	repo := pg.NewSkillRepo(pool)
	matcher := pg.NewPGSkillMatcher(pool, repo)
	provider := pg.NewSkillProvider(matcher)
	clock := shared.SystemClock{}

	// Seed all 9 skills with V4.1 §7 legacy payload.
	require.NoError(t,
		bootstrap.SeedSkills(ctx, repo, clock, slog.Default()),
		"seeder must succeed against fully-migrated DB",
	)

	goldenDir := regressionGoldenDir(t)

	for _, pt := range phase.AllPhaseTypes() {
		pt := pt // capture range var for parallel-safe closure
		t.Run(string(pt), func(t *testing.T) {
			skills, err := provider.SkillsForPhase(ctx, pt)
			require.NoError(t, err, "SkillsForPhase must not error for phase %q", pt)

			entries := buildRegressionEntries(skills)
			currentJSON, err := json.MarshalIndent(entries, "", "  ")
			require.NoError(t, err, "marshal regression entries for phase %q", pt)

			goldenPath := filepath.Join(goldenDir, fmt.Sprintf("%s.golden.json", string(pt)))

			rawGolden, readErr := os.ReadFile(goldenPath)
			if os.IsNotExist(readErr) {
				t.Fatalf("BLOCKING: missing golden file for phase %q — "+
					"run GOLDEN_UPDATE=1 go test -tags=integration -run TestSkillPhaseBaselineCapture "+
					"to capture baseline first: %s", pt, goldenPath)
			}
			require.NoError(t, readErr, "read golden file for phase %q", pt)

			require.JSONEq(t, string(rawGolden), string(currentJSON),
				"REGRESSION: phase %q diverged from pre-M1 baseline (commit e83c140) — "+
					"M1 changes must not alter SkillsForPhase output for any of the 9 phases",
				pt)
		})
	}
}

// buildRegressionEntries converts a skills slice to the same stable golden shape
// used by TestSkillPhaseBaselineCapture:
//
//	[{"skill_id":"...","content_sha256":"<hex>"}, ...]
//
// sorted by skill_id ascending for determinism.
func buildRegressionEntries(skills []*skill.Skill) []regressionEntryJSON {
	entries := make([]regressionEntryJSON, 0, len(skills))
	for _, s := range skills {
		hash := sha256.Sum256([]byte(s.Content()))
		entries = append(entries, regressionEntryJSON{
			SkillID:       s.ID().String(),
			ContentSHA256: hex.EncodeToString(hash[:]),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SkillID < entries[j].SkillID
	})
	return entries
}

type regressionEntryJSON struct {
	SkillID       string `json:"skill_id"`
	ContentSHA256 string `json:"content_sha256"`
}

// regressionGoldenDir returns the absolute path to the committed golden
// baseline captured in Group A.
func regressionGoldenDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	dir, err := filepath.Abs(
		filepath.Join(filepath.Dir(here), "testdata", "skill_phase_baseline"),
	)
	require.NoError(t, err, "resolve testdata/skill_phase_baseline path")
	require.DirExists(t, dir, "testdata/skill_phase_baseline must exist (committed in Group A)")
	return dir
}
