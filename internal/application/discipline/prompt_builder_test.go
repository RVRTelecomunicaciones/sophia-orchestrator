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
	// TestsRequired defaults to false; TDD gate is omitted per Spec #46.
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseApply, ChangeName: "x", Project: "y", TaskDescription: "apply",
	})
	require.NoError(t, err)
	require.Contains(t, out, "<HARD-GATE>")
	require.Contains(t, out, "files_pattern")
	require.NotContains(t, out, "DO NOT proceed without TDD",
		"TDD gate must be absent when TestsRequired is false (Spec #46)")
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

// ---------------------------------------------------------------------------
// Spec #45: Tasks output schema alignment
// ---------------------------------------------------------------------------

// TestPromptBuilder_TasksPhase_EmitsGroupedSchema verifies that when building
// a prompt for PhaseTasks the required-output block contains the exact
// data.groups[].tasks[].{description,files_pattern} schema so apply can
// deserialize without adapters.
func TestPromptBuilder_TasksPhase_EmitsGroupedSchema(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseTasks,
		ChangeName:      "feat-x",
		Project:         "proj",
		TaskDescription: "produce tasks",
	})
	require.NoError(t, err)
	require.Contains(t, out, `"groups"`, "tasks schema must include groups array")
	require.Contains(t, out, `"tasks"`, "tasks schema must include tasks array")
	require.Contains(t, out, `"description"`, "task item must have description field")
	require.Contains(t, out, `"files_pattern"`, "task item must have files_pattern field")
	require.Contains(t, out, `"depends_on"`, "group must have depends_on field")
}

// TestPromptBuilder_NonTasksPhase_EmitsEmptyDataObject verifies that phases
// other than PhaseTasks still use the generic {} data placeholder.
func TestPromptBuilder_NonTasksPhase_EmitsEmptyDataObject(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseSpec,
		ChangeName:      "feat-x",
		Project:         "proj",
		TaskDescription: "spec",
	})
	require.NoError(t, err)
	require.Contains(t, out, `"data": {}`, "non-tasks phases must use empty data object")
}

// ---------------------------------------------------------------------------
// Spec #46: Conditional TDD hard-gate
// ---------------------------------------------------------------------------

// TestPromptBuilder_ApplyPhase_TDDGateAbsent_WhenTestsNotRequired verifies
// that when TestsRequired is false the TDD hard-gate clause is omitted.
func TestPromptBuilder_ApplyPhase_TDDGateAbsent_WhenTestsNotRequired(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseApply,
		ChangeName:      "x",
		Project:         "y",
		TaskDescription: "apply",
		TestsRequired:   false,
	})
	require.NoError(t, err)
	require.NotContains(t, out, "DO NOT proceed without TDD",
		"TDD hard-gate must be absent when TestsRequired is false")
	// Other apply gates must still be present.
	require.Contains(t, out, "files_pattern")
	require.Contains(t, out, "fix #4")
}

// TestPromptBuilder_ApplyPhase_TDDGatePresent_WhenTestsRequired verifies
// that when TestsRequired is true the TDD hard-gate clause is included.
func TestPromptBuilder_ApplyPhase_TDDGatePresent_WhenTestsRequired(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseApply,
		ChangeName:      "x",
		Project:         "y",
		TaskDescription: "apply",
		TestsRequired:   true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "DO NOT proceed without TDD",
		"TDD hard-gate must be present when TestsRequired is true")
}

// Regression: existing test that checked for "TDD" in the apply prompt must
// now use TestsRequired=true to trigger the gate.
func TestPromptBuilder_IncludesHardGatesForApply_WithTDDEnabled(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseApply, ChangeName: "x", Project: "y", TaskDescription: "apply",
		TestsRequired: true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "<HARD-GATE>")
	require.Contains(t, out, "files_pattern")
	require.Contains(t, out, "TDD")
	require.Contains(t, out, "fix #4")
}

// ---------------------------------------------------------------------------
// Spec #51: PriorPhasesStatus snapshot + Iron Laws reframe
// ---------------------------------------------------------------------------

// TestPromptBuilder_IronLawsHeaderReframed verifies that the Iron Laws block
// is framed as orchestrator-enforced rather than something the agent must
// re-verify itself. Pre-fix the header said "(NON-NEGOTIABLE)" which led
// LLMs (verified with gpt-5.4 in smoke v3) to interpret IL2_NO_APPLY_
// WITHOUT_TASKS_DONE as "I must find evidence tasks are done" and block
// with confidence=0.98 when no such evidence appeared in the prompt.
func TestPromptBuilder_IronLawsHeaderReframed(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseApply, ChangeName: "x", Project: "y", TaskDescription: "apply",
	})
	require.NoError(t, err)
	require.NotContains(t, out, "(NON-NEGOTIABLE)",
		"Iron Laws header must not use NON-NEGOTIABLE — agents read that as a rule they must verify themselves")
	require.Contains(t, out, "enforced server-side",
		"Iron Laws header must clarify the orchestrator enforces them, not the agent")
}

// TestPromptBuilder_RendersPhaseStatusSnapshot verifies that when
// PriorPhasesStatus is non-empty the prompt contains a "# Phase Status
// Snapshot" section listing each prior phase and its terminal status.
// This is the factual evidence the LLM needs so it does not have to
// search for it locally.
func TestPromptBuilder_RendersPhaseStatusSnapshot(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase:           phase.PhaseApply,
		ChangeName:      "feat-x",
		Project:         "proj",
		TaskDescription: "apply",
		PriorPhasesStatus: map[phase.PhaseType]string{
			phase.PhaseProposal: "done",
			phase.PhaseSpec:     "done",
			phase.PhaseTasks:    "done",
		},
	})
	require.NoError(t, err)
	require.Contains(t, out, "# Phase Status Snapshot",
		"snapshot section must be rendered when PriorPhasesStatus is non-empty")
	require.Contains(t, out, "proposal: done")
	require.Contains(t, out, "spec: done")
	require.Contains(t, out, "tasks: done")
	require.Contains(t, out, "(verified by orchestrator")
}

// TestPromptBuilder_OmitsPhaseStatusSnapshot_WhenEmpty verifies the
// snapshot section is omitted when PriorPhasesStatus is nil or empty so
// init/explore prompts (no prior phases) stay clean.
func TestPromptBuilder_OmitsPhaseStatusSnapshot_WhenEmpty(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseExplore, ChangeName: "x", Project: "y", TaskDescription: "explore",
	})
	require.NoError(t, err)
	require.NotContains(t, out, "# Phase Status Snapshot")
}

// TestPromptBuilder_PhaseStatusSnapshot_StableOrder verifies the snapshot
// renders phases in canonical SDD order (init → explore → proposal → spec
// → design → tasks → apply → verify → archive) instead of Go map
// iteration order, so the prompt text stays deterministic across runs
// (important for prompt_sha256 dedup in agent_sessions).
func TestPromptBuilder_PhaseStatusSnapshot_StableOrder(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseApply, ChangeName: "x", Project: "y", TaskDescription: "apply",
		PriorPhasesStatus: map[phase.PhaseType]string{
			phase.PhaseTasks:    "done",
			phase.PhaseProposal: "done_with_concerns",
			phase.PhaseSpec:     "done",
		},
	})
	require.NoError(t, err)
	idxProposal := strings.Index(out, "proposal:")
	idxSpec := strings.Index(out, "spec:")
	idxTasks := strings.Index(out, "tasks:")
	require.Less(t, idxProposal, idxSpec, "proposal must come before spec")
	require.Less(t, idxSpec, idxTasks, "spec must come before tasks")
	require.Contains(t, out, "proposal: done_with_concerns")
}

// ---------------------------------------------------------------------------
// Spec #51 continued: remove redundant prior-phase hard-gates that overlap
// with the orchestrator's transition validation + Phase Status Snapshot.
// ---------------------------------------------------------------------------

// TestPromptBuilder_SpecPhase_OmitsPriorPhaseHardGate verifies that the
// spec prompt no longer asks the agent to verify proposal is DONE — the
// orchestrator already blocks the spec→tasks transition unless proposal
// reached a terminal advance-allowed status, and the Phase Status Snapshot
// makes the state visible. Pre-fix the literal "DO NOT proceed if proposal
// is not DONE" caused gpt-5.4 to block spec with confidence=0.96.
func TestPromptBuilder_SpecPhase_OmitsPriorPhaseHardGate(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseSpec, ChangeName: "x", Project: "y", TaskDescription: "spec",
	})
	require.NoError(t, err)
	require.NotContains(t, out, "DO NOT proceed if proposal is not DONE",
		"spec hard-gate must NOT re-verify proposal state — orchestrator enforces the transition")
	require.NotContains(t, out, "if proposal is not DONE")
	// The placeholder gate must remain — that's a SPEC-OUTPUT discipline.
	require.Contains(t, out, "placeholders")
}

// TestPromptBuilder_ArchivePhase_OmitsPriorPhaseHardGate verifies that
// the archive prompt no longer asks the agent to verify the verify phase
// is DONE — the orchestrator already blocks the archive transition until
// verify is done, and the Phase Status Snapshot exposes the state.
func TestPromptBuilder_ArchivePhase_OmitsPriorPhaseHardGate(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseArchive, ChangeName: "x", Project: "y", TaskDescription: "archive",
	})
	require.NoError(t, err)
	require.NotContains(t, out, "DO NOT archive without verify DONE",
		"archive hard-gate must NOT re-verify the verify phase — orchestrator enforces the transition")
}

// TestPromptBuilder_VerifyPhase_KeepsOutputHardGate verifies that the
// verify phase keeps its OUTPUT discipline gate ("don't claim DONE without
// running tests and citing output") — that's a gate on the agent's own
// work, not a redundant prior-phase check, so it stays.
func TestPromptBuilder_VerifyPhase_KeepsOutputHardGate(t *testing.T) {
	pb := discipline.NewPromptBuilder()
	out, err := pb.Build(discipline.PromptInput{
		Phase: phase.PhaseVerify, ChangeName: "x", Project: "y", TaskDescription: "verify",
	})
	require.NoError(t, err)
	require.Contains(t, out, "running tests",
		"verify output-discipline gate must remain — it constrains the verify agent's own output")
}

// sanity: ensure `strings` is referenced explicitly (lint hygiene)
var _ = strings.Contains
