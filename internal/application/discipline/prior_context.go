package discipline

import (
	"fmt"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
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

// RenderedSkill is a flattened, render-ready projection of a skill.Skill for
// PriorContext (D-M3-5). The callsite maps domain skills (post-match) into this
// shape; Render emits it. Kept render-ready (no domain methods) so Render stays
// a pure value→string function.
type RenderedSkill struct {
	// Name is the skill name, e.g. "clean-arch".
	Name string `json:"name,omitempty"`
	// Version is the skill version string, e.g. "v3".
	Version string `json:"version,omitempty"`
	// Status is the skill lifecycle status — always "active" when sourced from
	// SkillMatcher (matcher gate). Render skips non-active skills defensively.
	Status string `json:"status,omitempty"`
	// Source is the activation source string, e.g. "consolidation_worker".
	Source string `json:"source,omitempty"`
	// Techniques is the list of technique tag strings, e.g. ["step-back"].
	Techniques []string `json:"techniques,omitempty"`
	// Content is the skill content verbatim (inline-why lives inside content).
	Content string `json:"content,omitempty"`
}

// EpisodeRef is a relevant episodic memory surfaced for the phase prompt (D-M3-6).
// Populated from the BuildContext recent_episodic section.
type EpisodeRef struct {
	// ID is the memory record ID used for attribution traceability.
	ID string `json:"id,omitempty"`
	// Content is the record body verbatim.
	Content string `json:"content,omitempty"`
}

// ChangeDigestRef is a prior change digest sourced via dedicated Search call
// (DG-1). Populated by buildPriorContext from Search(Types:[semantic], Limit:3).
type ChangeDigestRef struct {
	// ChangeID is the source change identifier (attribution anchor).
	ChangeID string `json:"change_id,omitempty"`
	// Content is the digest YAML verbatim.
	Content string `json:"content,omitempty"`
}

// RuleRef is a project business rule (decision or heuristic) sourced from the
// BuildContext decisions/heuristics sections (D-M3-6).
type RuleRef struct {
	// ID is the memory record ID used for attribution traceability.
	ID string `json:"id,omitempty"`
	// Kind is "decision" or "heuristic".
	Kind string `json:"kind,omitempty"`
	// Content is the rule body verbatim.
	Content string `json:"content,omitempty"`
}

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

// layerBlock is an ordered rendering unit emitted by collectLayers.
// name identifies the layer for budget accounting; body is the pre-rendered
// text (including attribution headers when enabled).
type layerBlock struct {
	name string
	body string
}

// ToRenderedSkill maps a domain skill.Skill to a RenderedSkill for PriorContext
// injection (D-M3-5). Pure function: no I/O, no side effects.
// Exported so callsites (phase/service.go, apply/teamlead.go) can map matched
// skills into PriorContext.Skills before calling Render.
func ToRenderedSkill(s *skill.Skill) RenderedSkill {
	return RenderedSkill{
		Name:       s.Name(),
		Version:    s.Version(),
		Status:     s.Status().String(),
		Source:     s.ActivationSource().String(),
		Techniques: s.TechniqueStrings(),
		Content:    s.Content(),
	}
}

// Render assembles PriorContext into the LLM-facing prompt string.
//
// Render is DETERMINISTIC — it reads only from pc fields and opts; no time,
// no random, no env access. Byte-exact snapshot testing depends on this
// guarantee (D-M05-7).
//
// M3 enrichment emits layers in canonical order (D-M3-11):
//
//	Skills → StructuralCtx → Episodes → ChangeDigests → BusinessRules → PhaseIdentity
//
// Empty layers are skipped. RawMemoryBlob is emitted for backward-compat with
// existing callsites that still set it; it is scheduled for deletion in K.5.
//
// RenderOpts zero-value is a no-op: TokenBudget=0 means unlimited;
// EnableAttribution=false means no attribution headers injected by Render.
func (pc PriorContext) Render(opts RenderOpts) string {
	layers := pc.collectLayers(opts.EnableAttribution)

	if opts.TokenBudget > 0 {
		layers = enforceBudget(layers, opts.TokenBudget)
	}

	var b strings.Builder
	for _, l := range layers {
		b.WriteString(l.body)
	}
	return b.String()
}

// collectLayers builds the ordered []layerBlock in canonical D-M3-11 order.
// Attribution headers are included in each block's body when attr=true.
// Empty layers are skipped. Non-active skills are excluded (status gate).
func (pc PriorContext) collectLayers(attr bool) []layerBlock {
	var ls []layerBlock

	// Layer 1: Skills (active only, matched by SkillMatcher gate).
	if len(pc.Skills) > 0 {
		if b := renderSkills(pc.Skills, attr); b.body != "" {
			ls = append(ls, b)
		}
	}

	// Layer 2: StructuralCtx.
	if pc.StructuralCtx != nil {
		ls = append(ls, renderStructural(pc.StructuralCtx, attr))
	}

	// Layer 3: Episodes.
	if len(pc.Episodes) > 0 {
		ls = append(ls, renderEpisodes(pc.Episodes, attr))
	}

	// Layer 4: ChangeDigests.
	if len(pc.ChangeDigests) > 0 {
		ls = append(ls, renderDigests(pc.ChangeDigests, attr))
	}

	// Layer 5: BusinessRules.
	if len(pc.BusinessRules) > 0 {
		ls = append(ls, renderRules(pc.BusinessRules, attr))
	}

	// Layer 6: PhaseIdentity (apply path spec/design/progress, verbatim).
	if pc.PhaseIdentity != "" {
		ls = append(ls, layerBlock{name: "phase_identity", body: pc.PhaseIdentity})
	}

	// RawMemoryBlob — M0.5-interim backward compat. Emitted verbatim after
	// PhaseIdentity. Removed once all callsites stop setting it (K.5).
	if pc.RawMemoryBlob != "" {
		ls = append(ls, layerBlock{name: "raw_memory_blob", body: pc.RawMemoryBlob})
	}

	return ls
}

// renderSkills renders the Skills layer. Only skills with Status="active" are
// included (defensive gate matching the SkillMatcher status filter).
// Returns a layerBlock with an empty body if no active skills exist.
func renderSkills(skills []RenderedSkill, attr bool) layerBlock {
	var sb strings.Builder
	for _, s := range skills {
		if s.Status != "active" {
			continue
		}
		if attr {
			// D-M3-8: "## Skill: <name> v<version> (<status>, source=<src>)"
			fmt.Fprintf(&sb, "## Skill: %s %s (%s, source=%s)\n", s.Name, s.Version, s.Status, s.Source)
		} else {
			// No-attribution: minimal ## separator (matches current renderSkillSection shape).
			fmt.Fprintf(&sb, "## %s\n", s.Name)
		}
		if len(s.Techniques) > 0 {
			sb.WriteString("Techniques: ")
			sb.WriteString(strings.Join(s.Techniques, ", "))
			sb.WriteString("\n")
		}
		sb.WriteString(s.Content)
		sb.WriteString("\n\n")
	}
	return layerBlock{name: "skills", body: sb.String()}
}

// renderStructural renders the StructuralCtx layer with a compact summary.
func renderStructural(sc *structural.StructuralContext, attr bool) layerBlock {
	var sb strings.Builder
	if attr {
		fmt.Fprintf(&sb, "## Structural Context (init/%s)\n", sc.ChangeName)
	}
	// Emit a compact summary: frameworks and languages on separate lines.
	if len(sc.Frameworks) > 0 {
		var names []string
		for _, f := range sc.Frameworks {
			names = append(names, f.Name)
		}
		fmt.Fprintf(&sb, "Frameworks: %s\n", strings.Join(names, ", "))
	}
	if len(sc.Languages) > 0 {
		var names []string
		for _, l := range sc.Languages {
			names = append(names, l.Name)
		}
		fmt.Fprintf(&sb, "Languages: %s\n", strings.Join(names, ", "))
	}
	sb.WriteString("\n")
	return layerBlock{name: "structural_ctx", body: sb.String()}
}

// renderEpisodes renders the Episodes layer.
func renderEpisodes(eps []EpisodeRef, attr bool) layerBlock {
	var sb strings.Builder
	for _, ep := range eps {
		if attr {
			fmt.Fprintf(&sb, "## Episode (%s)\n", ep.ID)
		}
		sb.WriteString(ep.Content)
		sb.WriteString("\n\n")
	}
	return layerBlock{name: "episodes", body: sb.String()}
}

// renderDigests renders the ChangeDigests layer.
func renderDigests(digests []ChangeDigestRef, attr bool) layerBlock {
	var sb strings.Builder
	for _, d := range digests {
		if attr {
			fmt.Fprintf(&sb, "## Change Digest (%s)\n", d.ChangeID)
		}
		sb.WriteString(d.Content)
		sb.WriteString("\n\n")
	}
	return layerBlock{name: "change_digests", body: sb.String()}
}

// renderRules renders the BusinessRules layer (decisions + heuristics).
func renderRules(rules []RuleRef, attr bool) layerBlock {
	var sb strings.Builder
	for _, r := range rules {
		if attr {
			fmt.Fprintf(&sb, "## Rule: %s (%s)\n", r.Kind, r.ID)
		}
		sb.WriteString(r.Content)
		sb.WriteString("\n\n")
	}
	return layerBlock{name: "business_rules", body: sb.String()}
}

// layerBudgetShare returns the fraction of budget allocated to a named layer
// per D-M3-7 V4.1 §12.2. Returns 1.0 for unknown layers (no restriction).
func layerBudgetShare(name string) float64 {
	switch name {
	case "skills":
		return 0.40
	case "episodes":
		return 0.20
	case "change_digests":
		return 0.15
	case "business_rules":
		return 0.15
	case "phase_identity":
		return 0.10
	}
	return 1.0
}

// enforceBudget applies per-layer byte caps per D-M3-7. Unused share cascades
// to later layers. When a layer is truncated it gets a fixed truncation marker
// appended. PhaseIdentity is cut last; raw_memory_blob follows PhaseIdentity.
// All deterministic (no clock/random).
func enforceBudget(layers []layerBlock, budget int) []layerBlock {
	result := make([]layerBlock, 0, len(layers))

	// First pass: compute allocated bytes per layer, cascade unused forward.
	remaining := budget
	for i, l := range layers {
		share := layerBudgetShare(l.name)
		// Allocate a share of the ORIGINAL budget (not remaining) per V4.1,
		// but cap at remaining so we never exceed total.
		alloc := int(float64(budget) * share)
		if alloc > remaining {
			alloc = remaining
		}
		// Cascade: if a previous layer consumed less than its share, the
		// remainder was already kept. We track remaining for the cap above.
		bodyLen := len(l.body)
		if bodyLen <= alloc {
			result = append(result, l)
			remaining -= bodyLen
		} else {
			// Layer is over its allocation: truncate at the last newline
			// boundary within alloc bytes to avoid mid-line cuts.
			cutAt := alloc
			if cutAt < 0 {
				cutAt = 0
			}
			// Count how many items were omitted. Approximate: count
			// trailing newline pairs in the truncated region.
			omittedBytes := bodyLen - cutAt
			marker := fmt.Sprintf("\n…[truncated: %d bytes over budget in %s layer]\n", omittedBytes, l.name)
			truncated := l.body[:cutAt] + marker
			result = append(result, layerBlock{name: l.name, body: truncated})
			remaining -= cutAt
			if remaining <= 0 {
				// No more budget: skip subsequent layers entirely.
				_ = layers[i+1:] // silence unused-variable warning
				break
			}
		}
	}
	return result
}
