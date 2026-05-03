// Package ironlaw catalogs the 5 Iron Laws enforced by the orchestrator.
// These are non-rationalizable invariants. The IDs (IL1..IL5) are stable
// identifiers used in metrics, audit logs, and HARD-GATE prompts.
// See spec § 1.5 and Appendix A for anti-rationalization tables.
package ironlaw

// ID is the stable identifier for an Iron Law.
type ID string

// The 5 Iron Laws.
const (
	IronLaw1 ID = "IL1_PERSIST_BEFORE_TRANSITION"
	IronLaw2 ID = "IL2_NO_APPLY_WITHOUT_TASKS_APPROVED"
	IronLaw3 ID = "IL3_NO_ARCHIVE_WITHOUT_VERIFY"
	IronLaw4 ID = "IL4_NO_RUNTIME_WITHOUT_GOVERNANCE"
	IronLaw5 ID = "IL5_NO_FIX_4_WITHOUT_ESCALATION"
)

// Law is the catalog entry for an Iron Law.
type Law struct {
	ID          ID
	Description string
	Rationale   string
}

var catalog = []Law{
	{
		ID:          IronLaw1,
		Description: "No phase transition without persisted envelope.",
		Rationale:   "Crash before persistence loses the envelope and prevents resume.",
	},
	{
		ID:          IronLaw2,
		Description: "No apply without tasks phase DONE and approved.",
		Rationale:   "Approval gate ensures explicit consent before code edits at scale.",
	},
	{
		ID:          IronLaw3,
		Description: "No archive without verify DONE at confidence ≥ 0.9.",
		Rationale:   "Archive without verify locks in incorrect work.",
	},
	{
		ID:          IronLaw4,
		Description: "No runtime call without governance decision.",
		Rationale:   "Unaudited side effects compound; fail closed when governance is down.",
	},
	{
		ID:          IronLaw5,
		Description: "No fix #4 without architectural escalation.",
		Rationale:   "Three failures with the same approach indicate the wrong approach.",
	},
}

// All returns every Iron Law in the catalog (stable order: IL1..IL5).
func All() []Law {
	out := make([]Law, len(catalog))
	copy(out, catalog)
	return out
}

// ByID returns the Law identified by id, or false if id is unknown.
func ByID(id ID) (Law, bool) {
	for _, l := range catalog {
		if l.ID == id {
			return l, true
		}
	}
	return Law{}, false
}

// IsValid reports whether id is a known Iron Law.
func (id ID) IsValid() bool {
	_, ok := ByID(id)
	return ok
}
