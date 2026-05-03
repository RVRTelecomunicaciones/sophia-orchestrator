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

	sb.WriteString("# IRON LAWS (NON-NEGOTIABLE)\n")
	for _, law := range ironlaw.All() {
		fmt.Fprintf(&sb, "- [%s] %s\n", law.ID, law.Description)
	}
	sb.WriteString("\n")

	if gates := hardGatesFor(in.Phase); len(gates) > 0 {
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
  "data": {}
}`, in.Phase, in.ChangeName, in.Project, in.ChangeName, in.Phase, in.Phase)
	sb.WriteString("\n```\n\n")
	fmt.Fprintf(&sb, "Confidence threshold for this phase: %.2f. ", in.Phase.ConfidenceThreshold())
	sb.WriteString("Status MUST be one of the four enum values.\n")

	return sb.String(), nil
}

// hardGatesFor returns the per-phase HARD-GATE markers injected into the
// agent prompt. These are short imperative statements the agent must NOT
// rationalize past.
func hardGatesFor(p phase.PhaseType) []string {
	switch p {
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
		return []string{
			"DO NOT include placeholders (TBD/TODO/'fill in details').",
			"DO NOT proceed if proposal is not DONE.",
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
		return []string{
			"DO NOT modify files outside your assigned files_pattern.",
			"DO NOT proceed without TDD (write failing test first).",
			"DO NOT attempt fix #4 — escalate after 3 failures.",
		}
	case phase.PhaseVerify:
		return []string{
			"DO NOT claim DONE without running tests and citing exact output.",
		}
	case phase.PhaseArchive:
		return []string{
			"DO NOT archive without verify DONE at confidence ≥ 0.9.",
		}
	default:
		return nil
	}
}
