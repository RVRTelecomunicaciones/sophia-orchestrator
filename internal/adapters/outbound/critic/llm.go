package critic

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Dispatcher is the minimal slice of outbound.AgentDispatcher the LLM critic
// needs. Declared locally so unit tests can substitute a fake without spawning
// a real OpenCode subprocess. The production wiring passes the shared
// outbound.AgentDispatcher, which satisfies this interface.
//
// CLAUDE.md "LLM calls happen inside the OpenCode subprocess": the critic never
// talks to an LLM provider directly — it dispatches a REVIEW prompt through the
// same dispatcher every phase uses.
type Dispatcher interface {
	Dispatch(ctx context.Context, req outbound.DispatchRequest) (*outbound.DispatchResult, error)
}

// SpawnGovernor is the minimal acquire/release contract from
// discipline.SpawnGovernor. CLAUDE.md rule 7 / D1.4: every dispatcher
// subprocess MUST go through the governor, including the advisory critic's
// review dispatch. Declared locally for the same testability reason.
type SpawnGovernor interface {
	Acquire(ctx context.Context) error
	Release(ctx context.Context) error
}

// LLMConfig parameterizes the LLM critic. All fields are optional and have
// safe defaults so a zero LLMConfig works out of the box.
type LLMConfig struct {
	// TimeoutMS is the per-review dispatch timeout. Default reviewTimeoutMS.
	TimeoutMS int
}

// allowedCategories is the closed set of advisory categories the critic emits.
// The critic NEVER escalates to governance/policy, so any LLM-emitted concern
// outside this set is dropped (strictly-advisory invariant, design GAP B).
var allowedCategories = map[string]bool{
	"correctness":  true,
	"risk":         true,
	"completeness": true,
}

// reviewTimeoutMS is the default dispatch timeout for a critic review (60s).
// A review is a single bounded prompt, so it does not need the full
// phase-dispatch budget.
const reviewTimeoutMS = 60_000

// fencedJSONRE matches a fenced ```json { ... } ``` block. Mirrors the
// opencode adapter's extractor so the critic parses dispatcher Stdout the same
// way phase envelopes are extracted. (?s) lets . match newlines.
var fencedJSONRE = regexp.MustCompile("(?s)```json\\s*(\\{.*?\\})\\s*```")

// LLMCritic is the LLM-backed advisory critic (design D3 / D-GA-3 follow-up).
// It builds a rubric review prompt from the phase envelope, dispatches it
// through the OpenCode dispatcher (the only place LLM calls happen) behind the
// SpawnGovernor, and parses the structured concerns response into
// []phase.Concern.
//
// It is strictly advisory: it can only return concerns or none. It NEVER
// blocks, escalates, or returns a non-nil error from Review — every failure
// path (governor refusal, dispatch error, malformed output) degrades to zero
// concerns plus a log line. The insertion points in phase.Service already
// enforce non-blocking/non-escalating semantics; this adapter upholds the same
// contract independently.
//
// The adapter uses NO clock and NO randomness: given a fixed dispatcher
// response it is deterministic.
type LLMCritic struct {
	dispatcher Dispatcher
	gov        SpawnGovernor
	timeoutMS  int
}

// NewLLM constructs the LLM-backed critic. dispatcher and gov are required in
// production wiring; tests pass fakes.
func NewLLM(dispatcher Dispatcher, gov SpawnGovernor, cfg LLMConfig) *LLMCritic {
	timeout := cfg.TimeoutMS
	if timeout <= 0 {
		timeout = reviewTimeoutMS
	}
	return &LLMCritic{
		dispatcher: dispatcher,
		gov:        gov,
		timeoutMS:  timeout,
	}
}

// concernsResponse is the strict structured schema the review prompt asks the
// LLM to return as a single fenced JSON object.
type concernsResponse struct {
	Concerns []concernDTO `json:"concerns"`
}

// concernDTO is the wire shape of one rubric concern.
type concernDTO struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Evidence string `json:"evidence"`
}

// Review dispatches a rubric review of the envelope through the OpenCode
// dispatcher (behind the SpawnGovernor) and returns the parsed concerns. Every
// failure degrades to (nil, nil): an advisory critic must never break a phase.
func (c *LLMCritic) Review(ctx context.Context, in outbound.CriticInput) ([]phase.Concern, error) {
	env := in.Envelope
	if env == nil {
		// Nothing to review — never dispatch.
		return nil, nil
	}

	prompt, err := c.buildPrompt(in)
	if err != nil {
		slog.WarnContext(ctx, "llm critic: prompt build failed; degrading to no concerns",
			"change_id", in.ChangeID.String(), "phase_type", string(in.PhaseType), "error", err)
		return nil, nil
	}

	// D1.4 / CLAUDE.md rule 7: dispatcher subprocesses go through the governor.
	if err := c.gov.Acquire(ctx); err != nil {
		slog.WarnContext(ctx, "llm critic: governor refused slot; skipping review (non-blocking)",
			"change_id", in.ChangeID.String(), "phase_type", string(in.PhaseType), "error", err)
		return nil, nil
	}
	result, dispatchErr := c.dispatcher.Dispatch(ctx, outbound.DispatchRequest{
		Prompt:       prompt,
		WorktreePath: ".",
		TimeoutMS:    c.timeoutMS,
		EnvelopeOut:  "stdout-fenced-json",
		PhaseType:    string(in.PhaseType),
	})
	_ = c.gov.Release(ctx)

	if dispatchErr != nil {
		slog.WarnContext(ctx, "llm critic: review dispatch failed; swallowing (non-blocking)",
			"change_id", in.ChangeID.String(), "phase_type", string(in.PhaseType), "error", dispatchErr)
		return nil, nil
	}

	concerns := parseConcerns(extractConcernsRaw(result))
	if concerns == nil {
		slog.InfoContext(ctx, "llm critic: no parseable concerns in review response",
			"change_id", in.ChangeID.String(), "phase_type", string(in.PhaseType))
	}
	return concerns, nil
}

// buildPrompt renders the bounded, rubric-based review prompt. Bias controls
// per D3 research: a fixed rubric, structured-output instruction, and an
// explicit instruction to judge on substance (not length).
func (c *LLMCritic) buildPrompt(in outbound.CriticInput) (string, error) {
	envJSON, err := json.MarshalIndent(in.Envelope, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal envelope: %w", err)
	}

	var b strings.Builder
	b.WriteString("You are an ADVISORY reviewer of one completed SDD phase. ")
	b.WriteString("Your output is strictly informational: it never blocks, escalates, or gates the phase.\n\n")
	b.WriteString("Phase type: ")
	b.WriteString(string(in.PhaseType))
	b.WriteString("\n\n")
	b.WriteString("Review the phase envelope below against this rubric and flag only material concerns:\n")
	b.WriteString("  - correctness: the work as described is wrong, internally inconsistent, or unsupported by its evidence.\n")
	b.WriteString("  - risk: a genuine operational/data/security risk is present or under-stated.\n")
	b.WriteString("  - completeness: the phase omits something its own stated scope requires.\n\n")
	b.WriteString("Rules:\n")
	b.WriteString("  - Judge on substance, not length or verbosity. A short envelope is not a concern by itself.\n")
	b.WriteString("  - Do NOT raise governance or policy concerns — those are out of scope and will be discarded.\n")
	b.WriteString("  - If there are no material concerns, return an empty concerns array.\n")
	b.WriteString("  - Each concern's evidence MUST cite a specific field/value from the envelope.\n\n")
	b.WriteString("Respond with EXACTLY ONE fenced JSON object and nothing else, in this shape:\n")
	b.WriteString("```json\n")
	b.WriteString("{\"concerns\":[{\"severity\":\"low|medium|high\",\"category\":\"correctness|risk|completeness\",\"message\":\"...\",\"evidence\":\"...\"}]}\n")
	b.WriteString("```\n\n")
	b.WriteString("Phase envelope:\n")
	b.Write(envJSON)
	b.WriteString("\n")

	return b.String(), nil
}

// extractConcernsRaw returns the JSON bytes carrying the concerns object. It
// prefers the dispatcher's pre-extracted EnvelopeRaw; if that is empty it falls
// back to scanning Stdout for the last fenced ```json``` block (mirroring the
// opencode adapter's extraction).
func extractConcernsRaw(result *outbound.DispatchResult) []byte {
	if result == nil {
		return nil
	}
	if len(result.EnvelopeRaw) > 0 {
		return result.EnvelopeRaw
	}
	m := fencedJSONRE.FindAllSubmatch(result.Stdout, -1)
	if len(m) == 0 {
		return nil
	}
	// Last fenced block wins, matching the phase envelope convention.
	return m[len(m)-1][1]
}

// parseConcerns decodes the concerns object and maps it to []phase.Concern.
// Any malformed/empty/wrong-shape input yields nil (no concerns). Governance/
// policy and unknown categories are dropped to uphold the strictly-advisory
// invariant. Returns nil (not an empty slice) when nothing valid is produced.
func parseConcerns(raw []byte) []phase.Concern {
	if len(raw) == 0 {
		return nil
	}
	var resp concernsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil
	}
	var out []phase.Concern
	for _, dto := range resp.Concerns {
		cat := strings.ToLower(strings.TrimSpace(dto.Category))
		if !allowedCategories[cat] {
			continue
		}
		msg := strings.TrimSpace(dto.Message)
		if msg == "" {
			continue
		}
		out = append(out, phase.Concern{
			Severity: strings.ToLower(strings.TrimSpace(dto.Severity)),
			Category: cat,
			Message:  msg,
			Evidence: strings.TrimSpace(dto.Evidence),
		})
	}
	return out
}
