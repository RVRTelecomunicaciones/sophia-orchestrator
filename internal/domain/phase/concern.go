package phase

// Concern is a single advisory signal raised by the optional critic (design
// GAP B / D-GA-2). Concerns are strictly informational: they never block a
// phase, never escalate to governance, and never force a HARD-GATE. The
// operator reviews them post-hoc; cycle progression continues regardless
// (PhaseStatus.AdvanceAllowed returns true for done_with_concerns).
//
// Concern lives in the phase package — same as PhaseStatus — because the
// critic derives concerns from a phase's envelope and the phase aggregate
// records them. The phase package already imports envelope, so no new import
// cycle is introduced.
type Concern struct {
	// Severity is the advisory weight (e.g. "low" | "medium" | "high"). It is
	// NEVER used to block or escalate — even "high" is purely informational.
	Severity string

	// Category groups the concern (e.g. "risk" | "confidence").
	Category string

	// Message is the human-readable advisory note.
	Message string

	// Evidence cites the envelope fact that produced the concern (e.g.
	// "risks[0].level=high"), keeping the stub deterministic and auditable.
	Evidence string
}
