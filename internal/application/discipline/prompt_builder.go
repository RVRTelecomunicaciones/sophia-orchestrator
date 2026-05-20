package discipline

import (
	"fmt"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ironlaw"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
)

// PromptInput captures everything PromptBuilder.Build needs to produce a
// disciplined agent prompt for a SDD phase.
type PromptInput struct {
	Phase           phase.PhaseType
	ChangeName      string
	Project         string
	PriorContext    string // pulled from sophia-memory-engine
	TaskDescription string
	// TestsRequired gates the apply-phase TDD hard-stop. When false the
	// "DO NOT proceed without TDD" clause is omitted from the prompt so
	// changes that explicitly disable strict TDD are not blocked by it.
	// Spec #46: only injected when scope.tests_required == true.
	TestsRequired bool
	// PriorPhasesStatus is the orchestrator-verified terminal status of
	// each prior phase in the change (e.g. {proposal: "done", spec:
	// "done_with_concerns"}). Rendered as "# Phase Status Snapshot" so
	// the LLM sees factual evidence that prior-phase gates have been
	// satisfied instead of looking for that evidence locally and
	// blocking when it cannot find it. Spec #51: pre-fix the LLM
	// (gpt-5.4 in smoke v3) interpreted IL2_NO_APPLY_WITHOUT_TASKS_DONE
	// as "I must verify tasks are done" and returned BLOCKED with
	// confidence=0.98 because no local DONE evidence was provided.
	PriorPhasesStatus map[phase.PhaseType]string
}

// PromptBuilder produces phase-specific agent prompts with Iron Laws,
// HARD-GATE markers, prior context, task body, and the required envelope
// schema. The output is plain text suitable for stdin to the dispatcher
// (OpenCode V1; Claude Code/Cursor/Gemini in V2).
type PromptBuilder struct{}

// NewPromptBuilder constructs a stateless PromptBuilder.
func NewPromptBuilder() *PromptBuilder { return &PromptBuilder{} }

// Build assembles the prompt. Returns ErrInvalidPhase if input.Phase is unknown.
func (pb *PromptBuilder) Build(in PromptInput) (string, error) {
	if !in.Phase.IsValid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidPhase, in.Phase)
	}

	var sb strings.Builder

	sb.WriteString("# SDD Phase: ")
	sb.WriteString(string(in.Phase))
	sb.WriteString("\n")
	sb.WriteString("Change: ")
	sb.WriteString(in.ChangeName)
	sb.WriteString("\n")
	sb.WriteString("Project: ")
	sb.WriteString(in.Project)
	sb.WriteString("\n\n")

	sb.WriteString("# IRON LAWS (enforced server-side by the orchestrator)\n")
	sb.WriteString("These invariants are validated BEFORE this prompt fires. Your job is to produce a valid envelope for THIS phase — do NOT re-verify prior phases or look for their evidence locally.\n")
	for _, law := range ironlaw.All() {
		fmt.Fprintf(&sb, "- [%s] %s\n", law.ID, law.Description)
	}
	sb.WriteString("\n")

	if snapshot := renderPhaseStatusSnapshot(in.PriorPhasesStatus); snapshot != "" {
		sb.WriteString(snapshot)
	}

	if gates := hardGatesFor(in); len(gates) > 0 {
		sb.WriteString("# HARD-GATE Markers\n")
		for _, gate := range gates {
			sb.WriteString("<HARD-GATE>")
			sb.WriteString(gate)
			sb.WriteString("</HARD-GATE>\n")
		}
		sb.WriteString("\n")
	}

	if in.PriorContext != "" {
		sb.WriteString("# Prior Context\n")
		sb.WriteString(in.PriorContext)
		sb.WriteString("\n\n")
	}

	sb.WriteString("# Task\n")
	sb.WriteString(in.TaskDescription)
	sb.WriteString("\n\n")

	sb.WriteString("# Required Output\n")
	sb.WriteString("Return JSON envelope as the LAST fenced ```json block in stdout:\n\n")
	sb.WriteString("```json\n")
	dataSchema := dataSchemaFor(in.Phase)
	fmt.Fprintf(&sb, `{
  "schema_version": "v1",
  "phase": %q,
  "change_name": %q,
  "project": %q,
  "status": "DONE | DONE_WITH_CONCERNS | BLOCKED | NEEDS_CONTEXT",
  "confidence": 0.0,
  "executive_summary": "...",
  "artifacts_saved": [{"topic_key": "sdd/%s/%s", "type": %q}],
  "next_recommended": [],
  "risks": [{"description": "...", "level": "low|medium|high"}],
  "data": %s
}`, in.Phase, in.ChangeName, in.Project, in.ChangeName, in.Phase, in.Phase, dataSchema)
	sb.WriteString("\n```\n\n")
	fmt.Fprintf(&sb, "Confidence threshold for this phase: %.2f. ", in.Phase.ConfidenceThreshold())
	sb.WriteString("Status MUST be one of the four enum values.\n")

	return sb.String(), nil
}

// hardGatesFor returns the per-phase HARD-GATE markers injected into the
// agent prompt. These are short imperative statements the agent must NOT
// rationalize past.
//
// Spec #46: the apply-phase TDD clause is only injected when in.TestsRequired
// is true so changes that explicitly disable strict TDD are not blocked by it.
func hardGatesFor(in PromptInput) []string {
	switch in.Phase {
	case phase.PhaseInit:
		return nil
	case phase.PhaseExplore:
		return []string{
			"DO NOT write code in this phase; explore only.",
		}
	case phase.PhaseProposal:
		return []string{
			"DO NOT skip alternatives — present at least 2 approaches with tradeoffs.",
		}
	case phase.PhaseSpec:
		// Spec #51: removed "DO NOT proceed if proposal is not DONE" —
		// the orchestrator already blocks the proposal→spec transition
		// unless proposal reached an advance-allowed terminal status,
		// and the Phase Status Snapshot exposes that fact to the agent.
		// The pre-fix gate caused gpt-5.4 to block spec at 21s with
		// confidence=0.96 in smoke v3 because the agent searched for
		// local DONE evidence the orchestrator does not embed in prompts.
		return []string{
			"DO NOT include placeholders (TBD/TODO/'fill in details').",
		}
	case phase.PhaseDesign:
		return []string{
			"DO NOT skip architectural decisions or their rationale.",
		}
	case phase.PhaseTasks:
		return []string{
			"DO NOT include vague tasks ('similar to', 'fill in details').",
			"Tasks must be bite-sized (2-5 minute steps each).",
		}
	case phase.PhaseApply:
		gates := []string{
			"DO NOT modify files outside your assigned files_pattern.",
			"DO NOT attempt fix #4 — escalate after 3 failures.",
		}
		// Spec #46: TDD hard-gate only when tests_required == true.
		if in.TestsRequired {
			gates = append([]string{"DO NOT proceed without TDD (write failing test first)."}, gates...)
		}
		return gates
	case phase.PhaseVerify:
		return []string{
			"DO NOT claim DONE without running tests and citing exact output.",
		}
	case phase.PhaseArchive:
		// Spec #51: removed "DO NOT archive without verify DONE at
		// confidence ≥ 0.9" — IL3_NO_ARCHIVE_WITHOUT_VERIFY plus the
		// orchestrator's transition validation already enforce this,
		// and the Phase Status Snapshot exposes the verify state to the
		// agent. Keep this branch empty so the HARD-GATE block is
		// omitted for archive (no agent-output discipline to assert).
		return nil
	default:
		return nil
	}
}

// renderPhaseStatusSnapshot returns the "# Phase Status Snapshot" block
// listing each prior phase and its orchestrator-verified terminal status.
// Order is canonical SDD lifecycle order (init → explore → proposal → spec
// → design → tasks → apply → verify → archive) so the rendered text stays
// deterministic across runs — important for prompt_sha256 dedup in
// agent_sessions. Returns "" when statuses is nil or empty.
//
// Spec #51: the snapshot is the factual evidence the LLM needs to honor
// IL2_NO_APPLY_WITHOUT_TASKS_DONE et al. without having to search for
// that evidence locally and bail when none is found.
func renderPhaseStatusSnapshot(statuses map[phase.PhaseType]string) string {
	if len(statuses) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Phase Status Snapshot (verified by orchestrator before this prompt fired)\n")
	for _, pt := range phase.AllPhaseTypes() {
		if st, ok := statuses[pt]; ok && st != "" {
			fmt.Fprintf(&sb, "- %s: %s\n", pt, st)
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// dataSchemaFor returns the phase-specific "data" value to embed in the
// required-output JSON schema block.
//
// Spec #45: for PhaseTasks, emit the exact grouped schema so the apply
// phase can deserialize without adapters.
func dataSchemaFor(p phase.PhaseType) string {
	if p == phase.PhaseTasks {
		return `{"groups":[{"name":"domain","depends_on":["optional-group"],"tasks":[{"description":"...","files_pattern":["internal/domain/*.go"]}]}]}`
	}
	return `{}`
}
