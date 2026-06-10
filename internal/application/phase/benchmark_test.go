package phase_test

// Group M (M3 PR3a): p95 benchmark for enriched PriorContext pipeline.
// BenchmarkBuildPriorContext_50Skills measures Render() with 50 active skills
// to give a p95 latency baseline. Run with:
//   go test ./internal/application/phase/... -bench=BenchmarkBuildPriorContext_50Skills -benchmem

import (
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	skdomain "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// BenchmarkBuildPriorContext_50Skills benchmarks PriorContext.Render()
// with 50 active skills — represents a realistic upper-bound skill set.
// Establishes the p95 latency baseline for M3 enriched pipeline (D-M3-7).
func BenchmarkBuildPriorContext_50Skills(b *testing.B) {
	skills := make([]discipline.RenderedSkill, 50)
	for i := range skills {
		sid, _ := ids.ParseSkillID("01ARZ3NDEKTSV4RRFFQ69G5SK1")
		s, err := skdomain.New(
			sid,
			"bench-skill",
			[]phase.PhaseType{phase.PhaseApply},
			"Benchmark skill content — Use constitutional self-critique after each change. Why: keeps changes minimal.",
			[]skdomain.Technique{skdomain.TechniqueConstitutionalSelfCritique, skdomain.TechniqueInlineWhy},
			skdomain.LifecycleInput{
				Status:           skdomain.StatusActive,
				Version:          "v1",
				RiskLevel:        skdomain.RiskLow,
				ActivationSource: skdomain.SourceManual,
			},
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
		if err != nil {
			b.Fatalf("failed to build benchmark skill: %v", err)
		}
		skills[i] = discipline.ToRenderedSkill(s)
	}

	pc := discipline.PriorContext{
		Skills: skills,
		Episodes: []discipline.EpisodeRef{
			{ID: "ep-01", Content: "Recent episodic memory 1."},
			{ID: "ep-02", Content: "Recent episodic memory 2."},
			{ID: "ep-03", Content: "Recent episodic memory 3."},
		},
		ChangeDigests: []discipline.ChangeDigestRef{
			{ChangeID: "skills-lifecycle-matcher", Content: "Digest: lifecycle fields added."},
			{ChangeID: "priorcontext-enrichment", Content: "Digest: enriched PriorContext pipeline."},
			{ChangeID: "structural-domain", Content: "Digest: structural context framework."},
		},
		BusinessRules: []discipline.RuleRef{
			{ID: "rule-01", Kind: "decision", Content: "Use pgx/v5 for all database access."},
			{ID: "rule-02", Kind: "heuristic", Content: "Freeze Clock and IDGenerator in golden tests."},
		},
		PhaseIdentity: "spec: ...\ndesign: ...\ntasks: ...",
	}
	opts := discipline.RenderOpts{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pc.Render(opts)
	}
}
