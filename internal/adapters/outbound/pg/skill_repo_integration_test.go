//go:build integration

package pg_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/pg"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	dbpkg "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/db"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// setupSkillPG spins a Postgres 16 testcontainer, applies all migrations
// (including 009_skills), and returns a connected pool.
//
// This helper is local to this test file; when Docker is unavailable the
// test is skipped via skipIfNoDocker.
func setupSkillPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
	skipIfNoDocker(t)

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("sophia_test"),
		tcpostgres.WithUsername("sophia"),
		tcpostgres.WithPassword("sophia"),
		tcpostgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = container.Terminate(ctx)
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	require.NoError(t, dbpkg.MigrateUp(migrationsDir(t), dsn))

	pool, err := dbpkg.Open(ctx, dbpkg.DefaultConfig(dsn))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// migrationsDir returns the absolute path to migrations/postgres.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, here, _, _ := runtime.Caller(0)
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(here), "..", "..", "..", "..", "migrations", "postgres"))
	require.NoError(t, err)
	require.DirExists(t, abs)
	return abs
}

// skipIfNoDocker skips the test when the Docker socket is unavailable so that
// CI without Docker support does not fail the integration suite.
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/var/run/docker.sock"); os.IsNotExist(err) {
		// Also check DOCKER_HOST for non-standard sockets.
		if os.Getenv("DOCKER_HOST") == "" {
			t.Skip("Docker socket not available — skipping integration test (run in CI with Docker)")
		}
	}
}

func mustSkillIDInteg(t *testing.T, raw string) ids.SkillID {
	t.Helper()
	id, err := ids.ParseSkillID(raw)
	require.NoError(t, err)
	return id
}

var (
	integNow = time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	skillID1 = "01ARZ3NDEKTSV4RRFFQ69G5SK1"
	skillID2 = "01ARZ3NDEKTSV4RRFFQ69G5SK2"
	skillID3 = "01ARZ3NDEKTSV4RRFFQ69G5SK3"
)

func newTestSkill(t *testing.T, rawID, name string, phases []phase.PhaseType) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustSkillIDInteg(t, rawID),
		name,
		phases,
		"Apply constitutional self-critique to review each change before committing.",
		[]skill.Technique{skill.TechniqueConstitutionalSelfCritique, skill.TechniqueInlineWhy},
		skill.LifecycleInput{},
		integNow,
	)
	require.NoError(t, err)
	return s
}

// ── Migration 009 sanity check ────────────────────────────────────────────────

func TestSkillRepo_Migration009_TableExists(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)

	// List on an empty table must return an empty slice, not an error.
	skills, err := repo.List(context.Background())
	require.NoError(t, err)
	require.Empty(t, skills)
}

// ── Upsert ────────────────────────────────────────────────────────────────────

func TestSkillRepo_Upsert_Insert(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "apply-implement-safely", list[0].Name())
}

func TestSkillRepo_Upsert_Replaces(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	// Update and upsert again.
	later := integNow.Add(time.Hour)
	require.NoError(t, s.Update("apply-implement-safely",
		[]phase.PhaseType{phase.PhaseApply, phase.PhaseVerify},
		"Revised guidance: step-back before diving in.",
		[]skill.Technique{skill.TechniqueStepBack},
		skill.LifecycleInput{},
		later,
	))
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "Revised guidance: step-back before diving in.", list[0].Content())
	require.Equal(t, []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify}, list[0].Phases())
}

// ── InsertIfAbsent ────────────────────────────────────────────────────────────

func TestSkillRepo_InsertIfAbsent_InsertsOnEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.InsertIfAbsent(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

func TestSkillRepo_InsertIfAbsent_NoOpOnExisting(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	original := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, original))

	// Operator edits content directly (simulated via Upsert).
	later := integNow.Add(time.Hour)
	require.NoError(t, original.Update("apply-implement-safely",
		[]phase.PhaseType{phase.PhaseApply},
		"OPERATOR EDITED: do not overwrite me.",
		[]skill.Technique{skill.TechniqueStepBack},
		skill.LifecycleInput{},
		later,
	))
	require.NoError(t, repo.Upsert(ctx, original))

	// InsertIfAbsent with the same name but different content must not overwrite.
	different := newTestSkill(t, skillID2, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.InsertIfAbsent(ctx, different))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "OPERATOR EDITED: do not overwrite me.", list[0].Content(),
		"InsertIfAbsent must not overwrite operator-edited content")
}

func TestSkillRepo_InsertIfAbsent_IdempotentMultipleCalls(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})

	// Three consecutive calls must all succeed and produce exactly one row.
	require.NoError(t, repo.InsertIfAbsent(ctx, s))
	require.NoError(t, repo.InsertIfAbsent(ctx, s))
	require.NoError(t, repo.InsertIfAbsent(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// ── FindByPhase ───────────────────────────────────────────────────────────────

func TestSkillRepo_FindByPhase_MatchingRow(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	applySkill := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	verifySkill := newTestSkill(t, skillID2, "verify-chain-validation", []phase.PhaseType{phase.PhaseVerify})
	multiSkill := newTestSkill(t, skillID3, "multi-phase", []phase.PhaseType{phase.PhaseApply, phase.PhaseVerify})

	require.NoError(t, repo.Upsert(ctx, applySkill))
	require.NoError(t, repo.Upsert(ctx, verifySkill))
	require.NoError(t, repo.Upsert(ctx, multiSkill))

	got, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Len(t, got, 2, "apply-implement-safely and multi-phase must both match")

	names := make(map[string]struct{}, len(got))
	for _, s := range got {
		names[s.Name()] = struct{}{}
	}
	require.Contains(t, names, "apply-implement-safely")
	require.Contains(t, names, "multi-phase")
}

func TestSkillRepo_FindByPhase_NoMatchReturnsEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s := newTestSkill(t, skillID1, "apply-implement-safely", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s))

	got, err := repo.FindByPhase(ctx, phase.PhaseDesign)
	require.NoError(t, err)
	require.Empty(t, got, "no skill matches design phase — must return empty slice, not error")
}

func TestSkillRepo_FindByPhase_EmptyTableReturnsEmpty(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	got, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Empty(t, got)
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestSkillRepo_List_MultipleRows(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s1 := newTestSkill(t, skillID1, "alpha", []phase.PhaseType{phase.PhaseApply})
	s2 := newTestSkill(t, skillID2, "beta", []phase.PhaseType{phase.PhaseVerify})
	require.NoError(t, repo.Upsert(ctx, s1))
	require.NoError(t, repo.Upsert(ctx, s2))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2)
}

// ── Hydration roundtrip ───────────────────────────────────────────────────────

func TestSkillRepo_Upsert_HydrationRoundtrip(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	s, err := skill.New(
		mustSkillIDInteg(t, skillID1),
		"design-architect-system",
		[]phase.PhaseType{phase.PhaseDesign},
		"Step back and consider the full system before proposing structure.",
		[]skill.Technique{skill.TechniqueExtendedThinking, skill.TechniqueStepBack, skill.TechniqueInlineWhy},
		skill.LifecycleInput{},
		integNow,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	require.Equal(t, s.ID(), got.ID())
	require.Equal(t, s.Name(), got.Name())
	require.Equal(t, s.Content(), got.Content())
	require.Equal(t, s.Phases(), got.Phases())
	require.Equal(t, s.Techniques(), got.Techniques())
	require.True(t, s.CreatedAt().Equal(got.CreatedAt()), "createdAt must round-trip")
	require.True(t, s.UpdatedAt().Equal(got.UpdatedAt()), "updatedAt must round-trip")
}

// ── NotFound sentinel via outbound package ────────────────────────────────────

func TestSkillRepo_FindByPhase_NeverReturnsErrNotFound(t *testing.T) {
	// FindByPhase must return an empty slice, NOT ErrNotFound, per the port contract.
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	got, err := repo.FindByPhase(ctx, phase.PhaseInit)
	require.NoError(t, err)
	require.NotErrorIs(t, err, outbound.ErrNotFound)
	require.NotNil(t, got)
}

// ── Group E RED tests: FindByPhase status filter, Upsert (name,version), JSONB ──

// newActiveTestSkill creates a skill with status=active for FindByPhase filter tests.
func newActiveTestSkill(t *testing.T, rawID, name string, phases []phase.PhaseType) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustSkillIDInteg(t, rawID),
		name,
		phases,
		"Active skill content.",
		[]skill.Technique{skill.TechniqueConstitutionalSelfCritique, skill.TechniqueInlineWhy},
		skill.LifecycleInput{Status: skill.StatusActive},
		integNow,
	)
	require.NoError(t, err)
	return s
}

// newDeprecatedTestSkill creates a skill with status=deprecated.
func newDeprecatedTestSkill(t *testing.T, rawID, name string, phases []phase.PhaseType) *skill.Skill {
	t.Helper()
	s, err := skill.New(
		mustSkillIDInteg(t, rawID),
		name,
		phases,
		"Deprecated skill content.",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{Status: skill.StatusDeprecated},
		integNow,
	)
	require.NoError(t, err)
	return s
}

// E.1: FindByPhase returns only active skills.
func TestSkillRepo_FindByPhase_ReturnsOnlyActiveSkills(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	// Insert one active and one deprecated skill both for phase apply.
	active := newActiveTestSkill(t, skillID1, "active-skill-apply", []phase.PhaseType{phase.PhaseApply})
	deprecated := newDeprecatedTestSkill(t, skillID2, "deprecated-skill-apply", []phase.PhaseType{phase.PhaseApply})

	require.NoError(t, repo.Upsert(ctx, active))
	require.NoError(t, repo.Upsert(ctx, deprecated))

	got, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Len(t, got, 1,
		"FindByPhase must only return active skills; deprecated must be filtered out")
	require.Equal(t, "active-skill-apply", got[0].Name())
}

// E.2: Upsert ON CONFLICT (name, version) merges lifecycle fields; idempotent re-run.
func TestSkillRepo_Upsert_OnConflictNameVersion_MergesLifecycle(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	// Insert initial skill with default lifecycle.
	s1 := newActiveTestSkill(t, skillID1, "unique-skill", []phase.PhaseType{phase.PhaseApply})
	require.NoError(t, repo.Upsert(ctx, s1))

	// Create same name+version but with updated content.
	s2, err := skill.New(
		mustSkillIDInteg(t, skillID2), // different ID, same name+version
		"unique-skill",
		[]phase.PhaseType{phase.PhaseApply},
		"Updated content via Upsert.",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{
			Status:           skill.StatusValidated,
			ActivationSource: skill.SourceLegacySeed,
		},
		integNow,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, s2))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "Upsert ON CONFLICT (name, version) must merge, not duplicate")
	require.Equal(t, "Updated content via Upsert.", list[0].Content(),
		"content must be updated by the second Upsert")

	// Idempotent re-run — must still produce exactly 1 row.
	require.NoError(t, repo.Upsert(ctx, s2))
	list2, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list2, 1, "second Upsert must be idempotent")
}

// E.3: scanSkill correctly decodes non-empty Scope, AppliesWhen, Metrics JSONB.
func TestSkillRepo_Upsert_HydrationRoundtrip_WithLifecycle(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	lastUsed := integNow.Add(-24 * time.Hour)
	lastVal := integNow.Add(-48 * time.Hour)
	stackVer := "v1.2.3"

	lc := skill.LifecycleInput{
		Status:           skill.StatusActive,
		Version:          "v2",
		RiskLevel:        skill.RiskHigh,
		ActivationSource: skill.SourceLegacySeed,
		Scope: skill.Scope{
			ProjectID: "proj-123",
			RepoID:    "repo-456",
			Phases:    []string{"apply", "verify"},
		},
		AppliesWhen: skill.AppliesWhen{
			FeatureType:  []string{"bugfix"},
			TouchedPaths: []string{"internal/**/*.go"},
			ExcludePaths: []string{"vendor/**"},
		},
		Metrics: skill.Metrics{
			UsageCount:        42,
			SuccessCount:      38,
			FailureCount:      4,
			AvgRetryReduction: 0.25,
			LastStackVersion:  &stackVer,
		},
		LastUsedAt:      &lastUsed,
		LastValidatedAt: &lastVal,
	}

	s, err := skill.New(
		mustSkillIDInteg(t, skillID1),
		"lifecycle-roundtrip-skill",
		[]phase.PhaseType{phase.PhaseApply},
		"Lifecycle roundtrip test content.",
		[]skill.Technique{skill.TechniqueConstitutionalSelfCritique, skill.TechniqueInlineWhy},
		lc,
		integNow,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, s))

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	require.Equal(t, skill.StatusActive, got.Status(), "status must round-trip")
	require.Equal(t, "v2", got.Version(), "version must round-trip")
	require.Equal(t, skill.RiskHigh, got.RiskLevel(), "risk_level must round-trip")
	require.Equal(t, skill.SourceLegacySeed, got.ActivationSource(), "activation_source must round-trip")

	// Scope JSONB round-trip.
	require.Equal(t, "proj-123", got.Scope().ProjectID, "scope.project_id must round-trip")
	require.Equal(t, "repo-456", got.Scope().RepoID, "scope.repo_id must round-trip")
	require.Equal(t, []string{"apply", "verify"}, got.Scope().Phases, "scope.phases must round-trip")

	// AppliesWhen JSONB round-trip.
	require.Equal(t, []string{"bugfix"}, got.AppliesWhen().FeatureType, "applies_when.feature_type must round-trip")
	require.Equal(t, []string{"internal/**/*.go"}, got.AppliesWhen().TouchedPaths, "applies_when.touched_paths must round-trip")
	require.Equal(t, []string{"vendor/**"}, got.AppliesWhen().ExcludePaths, "applies_when.exclude_paths must round-trip")

	// Metrics JSONB round-trip.
	require.Equal(t, 42, got.Metrics().UsageCount, "metrics.usage_count must round-trip")
	require.Equal(t, 38, got.Metrics().SuccessCount, "metrics.success_count must round-trip")
	require.NotNil(t, got.Metrics().LastStackVersion, "metrics.last_stack_version must round-trip")
	require.Equal(t, stackVer, *got.Metrics().LastStackVersion, "metrics.last_stack_version value must round-trip")

	// Timestamp round-trips.
	require.NotNil(t, got.LastUsedAt(), "last_used_at must round-trip")
	require.True(t, got.LastUsedAt().Equal(lastUsed), "last_used_at value must round-trip")
	require.NotNil(t, got.LastValidatedAt(), "last_validated_at must round-trip")
	require.True(t, got.LastValidatedAt().Equal(lastVal), "last_validated_at value must round-trip")
}

// ── T4.6: Idempotent candidate/imported rows (DG-C7-7) ───────────────────────
//
// These tests verify that InsertIfAbsent correctly handles the lifecycle fields
// used by SkillImporter: status=candidate, activation_source=imported, plus
// FrameworkMinVersion in the applies_when JSONB column.

const (
	importedSkillID1 = "01ARZ3NDEKTSV4RRFFQ69G5IA1"
	importedSkillID2 = "01ARZ3NDEKTSV4RRFFQ69G5IA2"
	importedSkillID3 = "01ARZ3NDEKTSV4RRFFQ69G5IA3"
	importedSkillID4 = "01ARZ3NDEKTSV4RRFFQ69G5IA4"
)

// newImportedCandidateSkill creates a skill modelling what SkillImporter produces:
// status=candidate, activation_source=imported, FrameworkMinVersion in AppliesWhen.
func newImportedCandidateSkill(
	t *testing.T,
	rawID, name, version, fw string,
	phases []phase.PhaseType,
) *skill.Skill {
	t.Helper()
	major := version
	if dot := len(version); dot > 0 {
		for i, c := range version {
			if c == '.' {
				major = version[:i]
				break
			}
		}
	}
	s, err := skill.New(
		mustSkillIDInteg(t, rawID),
		name,
		phases,
		"# "+name+"  (imported, candidate)\n\n## Best practices\n\nImported best practices for "+fw+".\n\n## Provenance\n\n- framework: "+fw+" v"+version+"\n- activation_source: imported ; status: candidate\n",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{
			Status:           skill.StatusCandidate,
			Version:          version,
			RiskLevel:        skill.RiskMedium,
			ActivationSource: skill.SourceImported,
			AppliesWhen: skill.AppliesWhen{
				Framework: []string{fw},
				FrameworkMinVersion: map[string]string{
					fw: major,
				},
			},
		},
		integNow,
	)
	require.NoError(t, err)
	return s
}

// T4.6 (a): insert stack/angular-22 candidate/imported → row present with
// correct lifecycle fields, NOT returned by active-only FindByPhase queries.
func TestSkillRepo_ImportedCandidate_InsertAndNotVisibleToMatcher(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	candidate := newImportedCandidateSkill(
		t, importedSkillID1,
		"stack/angular-22", "22.0.0", "angular",
		[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
	)
	require.NoError(t, repo.InsertIfAbsent(ctx, candidate))

	// Row must be present in List (all-rows query).
	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)

	got := list[0]
	require.Equal(t, "stack/angular-22", got.Name())
	require.Equal(t, "22.0.0", got.Version())
	require.Equal(t, skill.StatusCandidate, got.Status())
	require.Equal(t, skill.SourceImported, got.ActivationSource())
	require.Equal(t, skill.RiskMedium, got.RiskLevel())

	// AppliesWhen with FrameworkMinVersion must round-trip through JSONB.
	aw := got.AppliesWhen()
	require.Equal(t, []string{"angular"}, aw.Framework)
	require.NotNil(t, aw.FrameworkMinVersion)
	require.Equal(t, "22", aw.FrameworkMinVersion["angular"])

	// Candidate rows MUST NOT be returned by active-only FindByPhase queries.
	activeSkills, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Empty(t, activeSkills,
		"candidate/imported skill must NOT appear in active-only matcher results")
}

// T4.6 (b): second identical InsertIfAbsent → no-op, row unchanged.
func TestSkillRepo_ImportedCandidate_IdempotentSecondInsert(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	candidate := newImportedCandidateSkill(
		t, importedSkillID1,
		"stack/angular-22", "22.0.0", "angular",
		[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
	)
	require.NoError(t, repo.InsertIfAbsent(ctx, candidate))

	// Second call with same (name, version) but different ID must be a no-op.
	duplicate := newImportedCandidateSkill(
		t, importedSkillID2, // different ID
		"stack/angular-22", "22.0.0", "angular",
		[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
	)
	require.NoError(t, repo.InsertIfAbsent(ctx, duplicate),
		"second InsertIfAbsent must return nil (no-op)")

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1, "exactly one row must exist after idempotent second insert")
	// Original ID must be preserved.
	require.Equal(t, candidate.ID(), list[0].ID(),
		"original row must not be overwritten by second InsertIfAbsent")
}

// T4.6 (c): concurrent double-insert same (name, version) → exactly one row.
func TestSkillRepo_ImportedCandidate_ConcurrentInsert_ExactlyOneRow(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	const goroutines = 5
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			rawIDs := []string{
				importedSkillID1,
				importedSkillID2,
				importedSkillID3,
				importedSkillID4,
				"01ARZ3NDEKTSV4RRFFQ69G5IA5",
			}
			s := newImportedCandidateSkill(
				t, rawIDs[idx],
				"stack/angular-22", "22.0.0", "angular",
				[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
			)
			errs <- repo.InsertIfAbsent(ctx, s)
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		err := <-errs
		require.NoError(t, err, "all concurrent InsertIfAbsent calls must return nil")
	}

	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1,
		"exactly one row must exist after %d concurrent inserts", goroutines)
}

// T4.6 (d): drift row stack/angular-23 coexists with stack/angular-22;
// the angular-22 row is unchanged when pre-seeded as active.
func TestSkillRepo_ImportedCandidate_DriftRowCoexistsWithActive(t *testing.T) {
	pool := setupSkillPG(t)
	repo := pg.NewSkillRepo(pool)
	ctx := context.Background()

	// Pre-seed an ACTIVE angular-22 skill (simulates a previously promoted candidate).
	active22, err := skill.New(
		mustSkillIDInteg(t, importedSkillID1),
		"stack/angular-22",
		[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
		"Active angular-22 best practices.",
		[]skill.Technique{skill.TechniqueInlineWhy},
		skill.LifecycleInput{
			Status:           skill.StatusActive,
			Version:          "22.0.0",
			RiskLevel:        skill.RiskMedium,
			ActivationSource: skill.SourceImported,
		},
		integNow,
	)
	require.NoError(t, err)
	require.NoError(t, repo.Upsert(ctx, active22))

	// Insert drift candidate for angular-23.
	candidate23 := newImportedCandidateSkill(
		t, importedSkillID2,
		"stack/angular-23", "23.0.0", "angular",
		[]phase.PhaseType{phase.PhaseExplore, phase.PhaseProposal, phase.PhaseApply},
	)
	require.NoError(t, repo.InsertIfAbsent(ctx, candidate23))

	// Both rows must coexist.
	list, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 2, "angular-22 active and angular-23 candidate must coexist")

	names := make(map[string]skill.Status, 2)
	for _, s := range list {
		names[s.Name()] = s.Status()
	}
	require.Equal(t, skill.StatusActive, names["stack/angular-22"],
		"stack/angular-22 must remain active")
	require.Equal(t, skill.StatusCandidate, names["stack/angular-23"],
		"stack/angular-23 must be candidate")

	// Active-only query must return only angular-22.
	activeSkills, err := repo.FindByPhase(ctx, phase.PhaseApply)
	require.NoError(t, err)
	require.Len(t, activeSkills, 1)
	require.Equal(t, "stack/angular-22", activeSkills[0].Name())
}
