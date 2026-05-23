// Package envelope defines the JSON contract returned by every SDD phase
// agent and validated by the orchestrator before persisting. Status is the
// closed enum DONE / DONE_WITH_CONCERNS / BLOCKED / NEEDS_CONTEXT (spec § 1.3).
//
// Envelope.Phase is stored as a raw string here to avoid a circular import
// with the phase package (which imports envelope for the Phase aggregate).
// Callers (Discipline.Validator) cross-check that Phase matches expected
// PhaseType at the boundary.
package envelope

import "encoding/json"

// Status is the closed envelope status set.
type Status string

// Envelope status values.
const (
	StatusDone             Status = "DONE"
	StatusDoneWithConcerns Status = "DONE_WITH_CONCERNS"
	StatusBlocked          Status = "BLOCKED"
	StatusNeedsContext     Status = "NEEDS_CONTEXT"
)

// IsValid reports whether s is a known status.
func (s Status) IsValid() bool {
	switch s {
	case StatusDone, StatusDoneWithConcerns, StatusBlocked, StatusNeedsContext:
		return true
	}
	return false
}

// SchemaVersionV1 is the only schema version supported in V1.
const SchemaVersionV1 = "v1"

// Risk describes a risk reported by an agent.
type Risk struct {
	Description string `json:"description"`
	Level       string `json:"level"` // low | medium | high
}

// ArtifactRef references an artifact saved by the agent (e.g. into
// sophia-memory-engine via topic_key).
type ArtifactRef struct {
	TopicKey string `json:"topic_key"`
	Type     string `json:"type"`
}

// Envelope is the agent → orchestrator JSON contract. See spec § 1.3.
type Envelope struct {
	SchemaVersion    string          `json:"schema_version"`
	Phase            string          `json:"phase"`
	ChangeName       string          `json:"change_name"`
	Project          string          `json:"project"`
	Status           Status          `json:"status"`
	Confidence       float64         `json:"confidence"`
	ExecutiveSummary string          `json:"executive_summary"`
	ArtifactsSaved   []ArtifactRef   `json:"artifacts_saved"`
	NextRecommended  NextRecommended `json:"next_recommended"`
	Risks            []Risk          `json:"risks"`
	Data             json.RawMessage `json:"data"`
}
