package discipline_test

import (
	"strings"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/discipline"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/stretchr/testify/require"
)

func TestPromptBuilder_Build_AllPhasesProduceValidPrompt(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	for _, pt := range phase.AllPhaseTypes() {
		t.Run(string(pt), func(t *testing.T) {
			out, err := pb.Build(discipline.PromptInput{
				Phase:           pt,
				ChangeName:      "feat-x",
				Project:         "proj",
				PriorContext:    "prior",
				TaskDescription: "do the thing",
			})
			require.NoError(t, err)
			require.Contains(t, out, "# SDD Phase: "+string(pt))
			require.Contains(t, out, "Change: feat-x")
			require.Contains(t, out, "Project: proj")
			require.Contains(t, out, "# IRON LAWS")
			require.Contains(t, out, "IL1_PERSIST_BEFORE_TRANSITION")
			require.Contains(t, out, "IL5_NO_FIX_4_WITHOUT_ESCALATION")
			require.Contains(t, out, "# Required Output")
			require.Contains(t, out, "schema_version")
			require.Contains(t, out, "DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT")
		})
	}
}

func TestPromptBuilder_RejectsInvalidPhase(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	_, err := pb.Build(discipline.PromptInput{Phase: phase.PhaseType("nope")})
	require.ErrorIs(t, err, discipline.ErrInvalidPhase)
}

func TestPromptBuilder_OmitsHardGatesForInit(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseInit, ChangeName: "x", Project: "y", TaskDescription: "init",
	})
	require.NoError(t, err)
	require.NotContains(t, out, "HARD-GATE")
}

func TestPromptBuilder_IncludesHardGatesForApply(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseApply, ChangeName: "x", Project: "y", TaskDescription: "apply",
	})
	require.NoError(t, err)
	require.Contains(t, out, "<HARD-GATE>")
	require.Contains(t, out, "files_pattern")
	require.Contains(t, out, "TDD")
	require.Contains(t, out, "fix #4")
}

func TestPromptBuilder_IncludesHardGatesForSpec(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseSpec, ChangeName: "x", Project: "y", TaskDescription: "draft spec",
	})
	require.NoError(t, err)
	require.Contains(t, out, "<HARD-GATE>")
	require.Contains(t, out, "placeholders")
}

func TestPromptBuilder_OmitsPriorContextWhenEmpty(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseSpec, ChangeName: "x", Project: "y",
		TaskDescription: "draft spec",
		// PriorContext intentionally empty
	})
	require.NoError(t, err)
	require.NotContains(t, out, "# Prior Context")
}

func TestPromptBuilder_IncludesPriorContextWhenSet(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseSpec,
		ChangeName:      "x",
		Project:         "y",
		PriorContext:    "the proposal said WIDGETS",
		TaskDescription: "draft spec",
	})
	require.NoError(t, err)
	require.Contains(t, out, "# Prior Context")
	require.Contains(t, out, "WIDGETS")
}

func TestPromptBuilder_TopicKeyMatchesPhase(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseDesign, ChangeName: "feat-x", Project: "proj", TaskDescription: "design",
	})
	require.NoError(t, err)
	require.Contains(t, out, `"topic_key": "sdd/feat-x/design"`)
}

func TestPromptBuilder_ConfidenceThresholdMatchesPhase(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseVerify, ChangeName: "x", Project: "y", TaskDescription: "verify",
	})
	require.NoError(t, err)
	require.Contains(t, out, "Confidence threshold for this phase: 0.90")
}

func TestPromptBuilder_AllFiveIronLawsListed(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseSpec, ChangeName: "x", Project: "y", TaskDescription: "spec",
	})
	require.NoError(t, err)
	for _, id := range []string{"IL1_", "IL2_", "IL3_", "IL4_", "IL5_"} {
		require.Contains(t, out, id, "must mention iron law %s", id)
	}
}

// sanity: ensure `strings` is referenced explicitly (lint hygiene)
var _ = strings.Contains
