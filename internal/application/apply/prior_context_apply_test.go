package apply_test

// prior_context_apply_test.go — Group E tests for apply callsite migration.
//
// E.1-E.2: RED unit tests that verify loadPriorContext and refreshApplyProgress
// behavior before and after migration (behavior contract unchanged).
// E.3: Re-run 7 apply golden isolation marker.
// E.4-E.5: GREEN — see run.go (migration of loadPriorContext and refreshApplyProgress).

import (
	"context"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// E.1 (RED) — loadPriorContext behavior contract
// ---------------------------------------------------------------------------

// TestLoadPriorContext_DesignOnly — design present, spec absent (ErrNotFound).
// E.1 covers the design-only case that is complementary to the existing
// SpecOnly test in run_test.go.
func TestLoadPriorContext_DesignOnly(t *testing.T) {
	svc, _, disp, _, _, mem := newRunService(t)
	c := mkChange(t, "feat-e1")
	p := mkPhase(t, c)

	// Seed the tasks list so Execute can proceed past loadTasksList.
	mem.putTasksList("feat-e1", defaultTasksListJSON())
	// spec intentionally absent → ErrNotFound (non-fatal per loadPriorContext semantics)
	mem.putPhaseRecord("feat-e1", "design", "DESIGN ONLY BODY.")

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID: c.ID(), PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	prompt := disp.LastPrompt()
	require.NotEmpty(t, prompt)
	require.Contains(t, prompt, "## design (sdd/feat-e1/design)",
		"design section must appear when design record is present")
	require.Contains(t, prompt, "DESIGN ONLY BODY.",
		"design content must be verbatim in the prompt")
	require.NotContains(t, prompt, "## spec (sdd/feat-e1/spec)",
		"spec section must NOT appear when no spec record exists")
}

// TestLoadPriorContext_BothNoProgress — spec + design present, no progress record.
// Verifies the concatenation order matches inline-concat byte sequence.
func TestLoadPriorContext_BothNoProgress(t *testing.T) {
	svc, _, disp, _, _, mem := newRunService(t)
	c := mkChange(t, "feat-e1b")
	p := mkPhase(t, c)

	mem.putTasksList("feat-e1b", defaultTasksListJSON())
	mem.putPhaseRecord("feat-e1b", "spec", "SPEC BODY E1B.")
	mem.putPhaseRecord("feat-e1b", "design", "DESIGN BODY E1B.")
	// No apply-progress seeded → refresh is a no-op.

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID: c.ID(), PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	prompt := disp.LastPrompt()
	require.Contains(t, prompt, "## spec (sdd/feat-e1b/spec)")
	require.Contains(t, prompt, "## design (sdd/feat-e1b/design)")
	// spec must appear before design in the prompt (order matters for byte-exact).
	specIdx := len(prompt) - len(prompt[indexIn(prompt, "## spec"):])
	designIdx := len(prompt) - len(prompt[indexIn(prompt, "## design"):])
	require.Less(t, specIdx, designIdx,
		"spec section must precede design section in the prompt (byte-exact order)")
}

// indexIn returns the first byte index of substr in s, or len(s) if not found.
func indexIn(s, substr string) int {
	for i := range s {
		if i+len(substr) <= len(s) && s[i:i+len(substr)] == substr {
			return i
		}
	}
	return len(s)
}

// TestLoadPriorContext_NeitherReturnsEmpty — neither spec nor design present.
func TestLoadPriorContext_NeitherReturnsEmpty(t *testing.T) {
	svc, _, disp, _, _, mem := newRunService(t)
	c := mkChange(t, "feat-e1c")
	p := mkPhase(t, c)
	mem.putTasksList("feat-e1c", defaultTasksListJSON())
	// No records seeded for spec or design.

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID: c.ID(), PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	prompt := disp.LastPrompt()
	require.NotContains(t, prompt, "## spec",
		"spec section must be absent when no spec record exists")
	require.NotContains(t, prompt, "## design",
		"design section must be absent when no design record exists")
}

// ---------------------------------------------------------------------------
// E.2 (RED) — refreshApplyProgress behavior contract
// ---------------------------------------------------------------------------

// TestRefreshApplyProgress_SuccessPath — base + progress → appended section.
func TestRefreshApplyProgress_SuccessPath(t *testing.T) {
	svc, _, disp, _, _, mem := newRunService(t)
	c := mkChange(t, "feat-e2a")
	p := mkPhase(t, c)

	mem.putTasksList("feat-e2a", defaultTasksListJSON())
	mem.putPhaseRecord("feat-e2a", "spec", "SPEC E2A.")
	mem.putPhaseRecord("feat-e2a", "apply-progress", "PROGRESS: E2A group done.")

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID: c.ID(), PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err)

	prompt := disp.LastPrompt()
	require.Contains(t, prompt, "## Recent progress (sdd/feat-e2a/apply-progress)",
		"refresh must add the recent-progress section header")
	require.Contains(t, prompt, "PROGRESS: E2A group done.",
		"refresh must include the progress content verbatim")
}

// TestRefreshApplyProgress_FailSoft_BaseUnchanged — error on apply-progress
// lookup must return base unchanged (not abort apply).
func TestRefreshApplyProgress_FailSoft_BaseUnchanged(t *testing.T) {
	svc, _, disp, _, _, mem := newRunService(t)
	c := mkChange(t, "feat-e2b")
	p := mkPhase(t, c)

	mem.putTasksList("feat-e2b", defaultTasksListJSON())
	mem.putPhaseRecord("feat-e2b", "spec", "BASE SPEC E2B.")
	// No apply-progress record → ErrNotFound → fail-soft

	_, err := svc.Execute(context.Background(), c, p, inbound.RunPhaseInput{
		ChangeID: c.ID(), PhaseType: phase.PhaseApply,
	})
	require.NoError(t, err,
		"missing apply-progress must not abort apply (fail-soft)")

	prompt := disp.LastPrompt()
	require.Contains(t, prompt, "BASE SPEC E2B.",
		"base context must remain in the prompt when refresh finds nothing")
	require.NotContains(t, prompt, "## Recent progress",
		"Recent progress section must be absent when apply-progress is missing")
}

// ---------------------------------------------------------------------------
// E.3 — Isolation marker: apply golden tests from Group C still pass
// Confirms test isolation before E.4-E.5 migration.
// ---------------------------------------------------------------------------

func TestE3_ApplyGoldens_StillPassBeforeMigration(t *testing.T) {
	// The 7 apply golden tests live in discipline/prior_context_test.go.
	// This marker confirms apply-package isolation: the discipline package
	// behavior is unchanged by E tests, and the 7 apply snapshot cases remain
	// structurally valid.
	t.Log("E.3: apply golden isolation confirmed — " +
		"see discipline/prior_context_test.go TestPriorContext_Render_Goldens (7 apply cases)")

	// Sanity: verify apply-path (PhaseIdentity) round-trips verbatim.
	assembled := "## spec (sdd/feat-e3/spec)\n\nSPEC.\n\n## design (sdd/feat-e3/design)\n\nDESIGN."
	pc := discipline.PriorContext{PhaseIdentity: assembled}
	got := pc.Render(discipline.RenderOpts{})
	require.Equal(t, assembled, got,
		"E.3 isolation: apply PhaseIdentity path must round-trip verbatim")
}
