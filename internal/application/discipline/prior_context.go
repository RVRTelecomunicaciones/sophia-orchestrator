package discipline

import (
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/structural"
)

// PriorContext is the structured assembly of prior-context content fed to
// LLM phase prompts. M0.5 introduces this struct as a refactor of inline
// string concatenation; M3 enriches it with skills/episodes/digests/routines.
//
// Field order follows V4.1 §16 M0.5 milestone spec. RawMemoryBlob is an
// M0.5-interim field appended last for the phase-service callsite;
// M3 will decompose it into Episodes / ChangeDigests / BusinessRules and
// remove the field.
type PriorContext struct {
	// PhaseIdentity holds the apply-path assembled string:
	// "## spec ...\n\n## design ..." (+ optional progress section).
	// Rendered verbatim; section headers live INSIDE this field (not added by Render).
	PhaseIdentity string

	// Skills holds rendered skill summaries. M3 populates this field.
	// Empty in M0.5.
	Skills []RenderedSkill

	// StructuralCtx carries the detected structural context for this change.
	// Populated by the phase-service callsite from the INIT structural record.
	// Nil when INIT-0 has not run or structural data is unavailable — the
	// structural filter and Render layer are skipped in that case (fail-open).
	StructuralCtx *structural.StructuralContext

	// Episodes holds relevant episodic memories. M3 populates this field.
	// Empty in M0.5.
	Episodes []EpisodeRef

	// ChangeDigests holds prior change digests. M3 populates this field.
	// Empty in M0.5.
	ChangeDigests []ChangeDigestRef

	// BusinessRules holds project rules. M3 populates this field.
	// Empty in M0.5.
	BusinessRules []RuleRef

	// Routines holds deterministic routine outputs. M3 populates this field.
	// Empty in M0.5.
	Routines []RoutineOutput

	// AuxiliaryMemory holds the aux memory provider block. M3 populates this field.
	// Nil in M0.5.
	AuxiliaryMemory *AuxiliaryBlock

	// RawMemoryBlob is the M0.5-interim unstructured memory bundle from the
	// phase-service callsite. The phase/service.go:buildPriorContext path
	// assembles memory-engine records into this field via a strings.Builder
	// loop; Render emits it verbatim.
	//
	// M3 will decompose RawMemoryBlob into Episodes / ChangeDigests /
	// BusinessRules and remove this field entirely.
	RawMemoryBlob string
}

// RenderedSkill is a forward-compat stub for M3 skill rendering integration.
// Concrete shape chosen in M3.
type RenderedSkill struct{}

// EpisodeRef is a forward-compat stub for M3 episodic memory integration.
// Empty struct = zero-cost anchor. M3 populates with concrete episodic shape.
type EpisodeRef struct{}

// ChangeDigestRef is a forward-compat stub for M3 change-digest integration.
// Empty struct = zero-cost anchor. M3 populates with concrete digest shape.
type ChangeDigestRef struct{}

// RuleRef is a forward-compat stub for M3 business-rule integration.
// Empty struct = zero-cost anchor. M3 populates with concrete rule shape.
type RuleRef struct{}

// RoutineOutput is a forward-compat stub for M3 routine integration.
// Empty struct = zero-cost anchor. M3 populates with concrete routine shape.
type RoutineOutput struct{}

// AuxiliaryBlock is a forward-compat stub for M3 auxiliary memory integration.
// Empty struct = zero-cost anchor. M3 populates with concrete aux-memory shape.
type AuxiliaryBlock struct{}

// RenderOpts configures the Render method. Zero-value MUST be a no-op for
// ALL hooks (operator decision #9 — forward-compat hook surface declared now,
// no-op semantics in M0.5 so callers need no migration when M3 enriches them).
type RenderOpts struct {
	// TokenBudget caps total bytes emitted. 0 = unlimited (no-op in M0.5).
	// M3 will enforce sub-budget allocation across layers.
	TokenBudget int

	// EnableAttribution emits "## {layer} ({topic_key})" headers per layer
	// when true. false = no attribution added by Render (no-op in M0.5).
	// M3 will emit source-attribution headers for each populated layer.
	EnableAttribution bool
}

// Render assembles PriorContext into the LLM-facing prompt string.
//
// Render is DETERMINISTIC — it reads only from pc fields and opts; no time,
// no random, no env access. Byte-exact snapshot testing depends on this
// guarantee (D-M05-7).
//
// M0.5 emits exactly two layers:
//   - PhaseIdentity (apply path): the "## spec / ## design / ## progress" block.
//   - RawMemoryBlob (phase-service path): unstructured memory-engine bundle.
//
// All other layers (Skills, StructuralCtx, Episodes, ChangeDigests,
// BusinessRules, Routines, AuxiliaryMemory) are empty/nil in M0.5 and are
// skipped. M3 enrichment populates them and Render learns to emit them.
//
// RenderOpts zero-value is a no-op: TokenBudget=0 means unlimited;
// EnableAttribution=false means no headers injected by Render.
func (pc PriorContext) Render(opts RenderOpts) string {
	var b strings.Builder

	// Layer 1: PhaseIdentity — apply path's assembled "## spec / ## design /
	// ## progress" block. Rendered verbatim; section headers live INSIDE
	// PhaseIdentity (assembled by the callsite) so Render adds nothing.
	// Byte-exact preservation is guaranteed by pass-through semantics.
	if pc.PhaseIdentity != "" {
		b.WriteString(pc.PhaseIdentity)
	}

	// Layer 2: RawMemoryBlob — phase-service path's unstructured memory
	// bundle. Rendered verbatim; the callsite already assembled the
	// strings.Builder loop output. M0.5-interim — M3 decomposes into
	// structured layers below.
	if pc.RawMemoryBlob != "" {
		b.WriteString(pc.RawMemoryBlob)
	}

	// M3 future layers — all empty/nil in M0.5, skipped.
	// Stubs documented here so the wiring points are clear for M3.
	//
	// if len(pc.Skills) > 0 { ... }          // operator decision #4: skills stay in sibling section
	// if pc.StructuralCtx != nil { ... }      // D-M05-2: opaque marker, nil in M0.5
	// if len(pc.Episodes) > 0 { ... }
	// if len(pc.ChangeDigests) > 0 { ... }
	// if len(pc.BusinessRules) > 0 { ... }
	// if len(pc.Routines) > 0 { ... }
	// if pc.AuxiliaryMemory != nil { ... }

	out := b.String()

	// RenderOpts.TokenBudget — zero-value no-op (operator decision #9).
	// Truncation applies only when budget is explicitly set > 0.
	if opts.TokenBudget > 0 && len(out) > opts.TokenBudget {
		out = out[:opts.TokenBudget]
	}

	// RenderOpts.EnableAttribution — zero-value no-op (operator decision #9).
	// M3 will emit "## {layer} ({topic_key})" attribution headers when true.
	// Referenced (not used) to prevent linter unused-variable complaints.
	_ = opts.EnableAttribution

	return out
}
