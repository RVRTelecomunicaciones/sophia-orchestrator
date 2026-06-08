package discipline_test

// prior_context_bench_test.go — Group F benchmarks.
//
// 4 benchmarks:
//   - BenchmarkPriorContext_Render_PhaseService: struct path (phase-service callsite)
//   - BenchmarkInlineConcat_PhaseService: baseline inline strings.Builder loop
//   - BenchmarkPriorContext_Render_ApplyThreeSections: struct path (apply callsite)
//   - BenchmarkInlineConcat_ApplyThreeSections: baseline inline concat
//
// Run with:
//
//	go test -bench=. -benchmem -count=10 ./internal/application/discipline/...
//
// Assert ratio: BenchmarkPriorContext_Render_* median ns/op ≤ 2× its
// BenchmarkInlineConcat_* counterpart. Record ratio in PR body.
// No CI gate (flaky on shared runners per design decision F.4).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
)

// ---------------------------------------------------------------------------
// Fixture data for benchmarks (representative sizes)
// ---------------------------------------------------------------------------

// largePhaseRecordSlice simulates a multi-record bundle from the phase-service
// path (5 records of realistic length each ~80 bytes).
var largePhaseRecordSlice = []string{
	"fix flaky test in apply phase — root cause: race condition in goroutine fan-out",
	"heuristic: always freeze Clock and IDGenerator in golden tests to ensure byte-exact reproducibility",
	"decision: PriorContext struct in discipline package, render-at-boundary preserves downstream signatures",
	"pattern: use strings.Builder for inline-concat paths to minimize allocations in hot paths",
	"discovery: golangci-lint v2.12 enforces wrapcheck on all error returns from outbound ports",
}

// largePhaseFixture is the pre-assembled RawMemoryBlob (equivalent of
// the strings.Builder loop output that buildPriorContext produces).
var largePhaseFixture = func() string {
	var sb strings.Builder
	for _, r := range largePhaseRecordSlice {
		sb.WriteString(r)
		sb.WriteString("\n\n")
	}
	return sb.String()
}()

// largeApplyFixture simulates the apply-path PhaseIdentity string with 3 sections.
const applyChangeName = "bench-prior-context-fixture"

var largeApplyFixture = func() string {
	spec := "## spec (sdd/" + applyChangeName + "/spec)\n\n" +
		"Spec content: introduce PriorContext struct for structured assembly of prior-context content."
	design := "## design (sdd/" + applyChangeName + "/design)\n\n" +
		"Design content: single file prior_context.go with Render method and 8 stub types."
	progress := "## Recent progress (sdd/" + applyChangeName + "/apply-progress)\n\n" +
		"Progress: Group A baseline capture complete. Group B struct implementation in progress."
	return spec + "\n\n" + design + "\n\n" + progress
}()

// ---------------------------------------------------------------------------
// BenchmarkPriorContext_Render_PhaseService — struct Render path (phase-svc)
// ---------------------------------------------------------------------------

func BenchmarkPriorContext_Render_PhaseService(b *testing.B) {
	pc := discipline.PriorContext{RawMemoryBlob: largePhaseFixture}
	opts := discipline.RenderOpts{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pc.Render(opts)
	}
}

// ---------------------------------------------------------------------------
// BenchmarkInlineConcat_PhaseService — baseline strings.Builder loop
// ---------------------------------------------------------------------------

func BenchmarkInlineConcat_PhaseService(b *testing.B) {
	records := largePhaseRecordSlice
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sb strings.Builder
		for _, rec := range records {
			sb.WriteString(rec)
			sb.WriteString("\n\n")
		}
		_ = sb.String()
	}
}

// ---------------------------------------------------------------------------
// BenchmarkPriorContext_Render_ApplyThreeSections — struct Render path (apply)
// ---------------------------------------------------------------------------

func BenchmarkPriorContext_Render_ApplyThreeSections(b *testing.B) {
	pc := discipline.PriorContext{PhaseIdentity: largeApplyFixture}
	opts := discipline.RenderOpts{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = pc.Render(opts)
	}
}

// ---------------------------------------------------------------------------
// BenchmarkInlineConcat_ApplyThreeSections — baseline inline section concat
// ---------------------------------------------------------------------------

func BenchmarkInlineConcat_ApplyThreeSections(b *testing.B) {
	specContent := "Spec content: introduce PriorContext struct for structured assembly of prior-context content."
	designContent := "Design content: single file prior_context.go with Render method and 8 stub types."
	progressContent := "Progress: Group A baseline capture complete. Group B struct implementation in progress."
	changeName := applyChangeName

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sections := make([]string, 0, 3)
		for _, pair := range []struct{ key, content string }{
			{"spec", specContent},
			{"design", designContent},
		} {
			sections = append(sections, fmt.Sprintf("## %s (sdd/%s/%s)\n\n%s",
				pair.key, changeName, pair.key, pair.content))
		}
		out := sections[0]
		for _, s := range sections[1:] {
			out += "\n\n" + s
		}
		// Simulate refreshApplyProgress concat.
		section := fmt.Sprintf("## Recent progress (sdd/%s/apply-progress)\n\n%s",
			changeName, progressContent)
		_ = out + "\n\n" + section
	}
}
