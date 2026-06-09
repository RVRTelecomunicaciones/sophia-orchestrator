package bootstrap_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/bootstrap"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ── Unit tests: seed definitions (no DB) ─────────────────────────────────────

// TestBuildSeedSkills_Count verifies the seeder defines exactly 9 skills
// (one per canonical SDD phase). Changing this number requires a design change.
func TestBuildSeedSkills_Count(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)
	require.Len(t, skills, 9, "seeder must define exactly 9 canonical SDD phase skills")
}

// TestBuildSeedSkills_AllNamesUnique verifies no two seed skills share a name.
// Duplicate names would cause InsertIfAbsent to silently drop one seed row.
func TestBuildSeedSkills_AllNamesUnique(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	seen := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		_, dup := seen[s.Name()]
		assert.False(t, dup, "duplicate seed skill name: %q", s.Name())
		seen[s.Name()] = struct{}{}
	}
}

// TestBuildSeedSkills_ExpectedNames verifies each canonical name from design.md
// is present. Renaming a seed skill is a breaking change to existing rows.
func TestBuildSeedSkills_ExpectedNames(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	names := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		names[s.Name()] = struct{}{}
	}

	expected := []string{
		"init-bootstrap-context",
		"explore-investigate",
		"proposal-draft-options",
		"spec-write-requirements",
		"design-architect-system",
		"tasks-decompose-work",
		"apply-implement-safely",
		"verify-chain-validation",
		"archive-finalize-deltas",
	}
	for _, name := range expected {
		assert.Contains(t, names, name, "missing expected seed skill name: %q", name)
	}
}

// TestBuildSeedSkills_TechniqueMapping verifies the phase→technique assignments
// from design.md are met for every seed skill.
func TestBuildSeedSkills_TechniqueMapping(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	byName := make(map[string]*skill.Skill, len(skills))
	for _, s := range skills {
		byName[s.Name()] = s
	}

	hasTechnique := func(s *skill.Skill, want skill.Technique) bool {
		for _, tech := range s.Techniques() {
			if tech == want {
				return true
			}
		}
		return false
	}

	cases := []struct {
		name      string
		technique skill.Technique
	}{
		{"init-bootstrap-context", skill.TechniqueStepBack},
		{"explore-investigate", skill.TechniqueReAct},
		{"proposal-draft-options", skill.TechniqueSkeletonOfThought},
		{"spec-write-requirements", skill.TechniqueSkeletonOfThought},
		{"design-architect-system", skill.TechniqueExtendedThinking},
		{"design-architect-system", skill.TechniqueStepBack},
		{"tasks-decompose-work", skill.TechniqueExtendedThinking},
		{"apply-implement-safely", skill.TechniqueConstitutionalSelfCritique},
		{"verify-chain-validation", skill.TechniqueChainOfVerification},
		{"archive-finalize-deltas", skill.TechniqueStepBack},
	}
	for _, tc := range cases {
		s, ok := byName[tc.name]
		require.True(t, ok, "skill %q not found", tc.name)
		assert.True(t, hasTechnique(s, tc.technique),
			"skill %q missing expected technique %q", tc.name, tc.technique)
	}
}

// TestBuildSeedSkills_AllHaveInlineWhy verifies every seed skill carries the
// inline-why technique tag — each rule must include a Why: clause.
func TestBuildSeedSkills_AllHaveInlineWhy(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	for _, s := range skills {
		hasInlineWhy := false
		for _, tech := range s.Techniques() {
			if tech == skill.TechniqueInlineWhy {
				hasInlineWhy = true
				break
			}
		}
		assert.True(t, hasInlineWhy, "skill %q missing inline-why technique", s.Name())
	}
}

// TestBuildSeedSkills_PhaseMapping verifies each skill applies to the expected
// canonical phase (one skill per phase, matching design.md).
func TestBuildSeedSkills_PhaseMapping(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	byName := make(map[string]*skill.Skill, len(skills))
	for _, s := range skills {
		byName[s.Name()] = s
	}

	cases := []struct {
		name  string
		phase phase.PhaseType
	}{
		{"init-bootstrap-context", phase.PhaseInit},
		{"explore-investigate", phase.PhaseExplore},
		{"proposal-draft-options", phase.PhaseProposal},
		{"spec-write-requirements", phase.PhaseSpec},
		{"design-architect-system", phase.PhaseDesign},
		{"tasks-decompose-work", phase.PhaseTasks},
		{"apply-implement-safely", phase.PhaseApply},
		{"verify-chain-validation", phase.PhaseVerify},
		{"archive-finalize-deltas", phase.PhaseArchive},
	}
	for _, tc := range cases {
		s, ok := byName[tc.name]
		require.True(t, ok, "skill %q not found", tc.name)
		assert.True(t, s.AppliesTo(tc.phase),
			"skill %q must apply to phase %q", tc.name, tc.phase)
	}
}

// TestBuildSeedSkills_AllContentNonEmpty verifies no skill has empty content.
// Empty content would fail the domain invariant and cause boot to fail.
func TestBuildSeedSkills_AllContentNonEmpty(t *testing.T) {
	skills, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	for _, s := range skills {
		assert.NotEmpty(t, s.Content(), "skill %q has empty content", s.Name())
	}
}

// ── SeedSkills with fake repo ─────────────────────────────────────────────────

// fixedTestClock is a minimal shared.Clock for unit tests.
type fixedTestClock struct{ t time.Time }

func (c fixedTestClock) Now() time.Time { return c.t }

// TestSeedSkills_EmptyTable_UpsertAll verifies that seeding from scratch
// results in exactly 9 Upsert calls and 9 stored rows.
func TestSeedSkills_EmptyTable_UpsertAll(t *testing.T) {
	repo := newFakeSkillRepo()
	clock := fixedTestClock{t: time.Now()}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	err := bootstrap.SeedSkills(context.Background(), repo, clock, logger)
	require.NoError(t, err)

	require.Len(t, repo.upsertCalls, 9,
		"seeder must call Upsert exactly 9 times on an empty table")
	require.Len(t, repo.stored, 9,
		"all 9 skills must be present after seeding an empty table")
}

// TestSeedSkills_Idempotent_SecondRunNoChange verifies the idempotency contract:
// running the seeder a second time still results in 9 rows (Upsert is idempotent).
func TestSeedSkills_Idempotent_SecondRunNoChange(t *testing.T) {
	repo := newFakeSkillRepo()
	clock := fixedTestClock{t: time.Now()}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// First run — seeds all.
	require.NoError(t, bootstrap.SeedSkills(context.Background(), repo, clock, logger))
	require.Len(t, repo.stored, 9)

	// Second run — Upsert overwrites same rows; count stays at 9.
	require.NoError(t, bootstrap.SeedSkills(context.Background(), repo, clock, logger))
	require.Len(t, repo.stored, 9,
		"second seeder run must not change the row count")
}

// TestSeedSkills_UpsertOverwritesExisting verifies the Upsert contract:
// when a row exists under a given name, Upsert replaces it with the canonical
// seed payload — this is the M1 Upsert-on-restart design (D-M1-4).
func TestSeedSkills_UpsertOverwritesExisting(t *testing.T) {
	repo := newFakeSkillRepo()
	clock := fixedTestClock{t: time.Now()}

	// Identify the apply skill from the seed definitions.
	seeds, err := bootstrap.ExportedBuildSeedSkills(time.Now())
	require.NoError(t, err)

	var applySkill *skill.Skill
	for _, s := range seeds {
		if s.Name() == "apply-implement-safely" {
			applySkill = s
			break
		}
	}
	require.NotNil(t, applySkill, "apply-implement-safely must be in seed definitions")

	// Pre-populate with stale content.
	const staleContent = "OLD CONTENT: should be replaced on re-seed."
	stale := skill.Hydrate(
		applySkill.ID(),
		applySkill.Name(),
		applySkill.Phases(),
		staleContent,
		applySkill.Techniques(),
		skill.StatusCandidate, "v1",
		skill.Scope{}, skill.AppliesWhen{},
		skill.RiskMedium, skill.SourceManual,
		skill.Metrics{},
		nil, nil,
		time.Now(), time.Now().Add(time.Hour),
	)
	repo.storedByName(stale.Name(), stale)

	// Run the seeder — Upsert must overwrite the stale row.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	require.NoError(t, bootstrap.SeedSkills(context.Background(), repo, clock, logger))

	// The row must be replaced with the canonical seed content.
	got := repo.stored[applySkill.Name()]
	require.NotNil(t, got)
	require.NotEqual(t, staleContent, got.Content(),
		"Upsert must replace stale content with the canonical seed payload")
	require.Equal(t, applySkill.Content(), got.Content(),
		"seeder must restore canonical content via Upsert on re-seed")

	// All 9 rows must be present.
	require.Len(t, repo.stored, 9)
}

// TestSeedSkills_RepoError_Propagates verifies that infrastructure errors from
// Upsert are propagated (not silently swallowed) by SeedSkills.
func TestSeedSkills_RepoError_Propagates(t *testing.T) {
	repo := newFakeSkillRepo()
	repo.upsertErr = errors.New("pg: connection reset by peer")
	clock := fixedTestClock{t: time.Now()}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	err := bootstrap.SeedSkills(context.Background(), repo, clock, logger)
	require.Error(t, err, "infrastructure errors must be propagated by SeedSkills")
	require.Contains(t, err.Error(), "pg: connection reset by peer")
}

// ── Fake SkillRepository ──────────────────────────────────────────────────────

// fakeSkillRepo is an in-memory SkillRepository for unit-testing the seeder.
// Upsert always overwrites the stored row (idempotent by design — M1 D-M1-4).
type fakeSkillRepo struct {
	mu          sync.Mutex
	stored      map[string]*skill.Skill // key = name
	upsertCalls []string                // names passed to Upsert, in order
	upsertErr   error                   // when non-nil, Upsert returns this
}

func newFakeSkillRepo() *fakeSkillRepo {
	return &fakeSkillRepo{stored: make(map[string]*skill.Skill)}
}

// storedByName pre-populates a row to simulate an existing DB value.
func (f *fakeSkillRepo) storedByName(name string, s *skill.Skill) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored[name] = s
}

func (f *fakeSkillRepo) Upsert(_ context.Context, s *skill.Skill) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upsertCalls = append(f.upsertCalls, s.Name())
	if f.upsertErr != nil {
		return f.upsertErr
	}
	// Upsert always replaces: same semantics as ON CONFLICT DO UPDATE.
	f.stored[s.Name()] = s
	return nil
}

func (f *fakeSkillRepo) InsertIfAbsent(_ context.Context, s *skill.Skill) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Honour insert-if-absent: only store when the name is not yet present.
	if _, exists := f.stored[s.Name()]; !exists {
		f.stored[s.Name()] = s
	}
	return nil
}

func (f *fakeSkillRepo) FindByPhase(_ context.Context, pt phase.PhaseType) ([]*skill.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*skill.Skill
	for _, s := range f.stored {
		if s.AppliesTo(pt) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeSkillRepo) List(_ context.Context) ([]*skill.Skill, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*skill.Skill, 0, len(f.stored))
	for _, s := range f.stored {
		out = append(out, s)
	}
	return out, nil
}

// Verify fakeSkillRepo satisfies the SkillRepository port at compile time.
var _ outbound.SkillRepository = (*fakeSkillRepo)(nil)
