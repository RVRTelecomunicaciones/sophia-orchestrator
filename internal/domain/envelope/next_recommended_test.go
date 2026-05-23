package envelope_test

import (
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/stretchr/testify/require"
)

// Spec #57 — envelope validator rejected payloads where next_recommended
// was a JSON array of OBJECTS (gpt-5.4 in smoke v4 emitted
// `{"action": "...", "rationale": "..."}` items). The validator's
// `[]string` target failed: "cannot unmarshal object into Go struct
// field Envelope.next_recommended of type string", blocking the spec
// phase in 14s with an empty envelope. NextRecommended now accepts
// strings AND objects, projecting objects to their most-descriptive
// string field.

func TestNextRecommended_UnmarshalsStringArray_Unchanged(t *testing.T) {
	raw := []byte(`["proceed to tasks","approve the plan"]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Equal(t, envelope.NextRecommended{"proceed to tasks", "approve the plan"}, nr)
}

func TestNextRecommended_UnmarshalsObjectArray_PrefersActionKey(t *testing.T) {
	// gpt-5.4 verbatim shape from smoke v4.
	raw := []byte(`[
		{"action":"proceed to tasks","rationale":"spec is complete"},
		{"action":"approve the plan"}
	]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Equal(t, envelope.NextRecommended{"proceed to tasks", "approve the plan"}, nr,
		"action key must win when present")
}

func TestNextRecommended_UnmarshalsObjectArray_FallsBackThroughKeys(t *testing.T) {
	raw := []byte(`[
		{"description":"only desc"},
		{"message":"only msg"},
		{"summary":"only summary"}
	]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Equal(t, envelope.NextRecommended{"only desc", "only msg", "only summary"}, nr)
}

func TestNextRecommended_UnmarshalsMixedArray(t *testing.T) {
	raw := []byte(`[
		"a plain string",
		{"action":"some action"},
		{"description":"some desc"}
	]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Equal(t, envelope.NextRecommended{"a plain string", "some action", "some desc"}, nr)
}

func TestNextRecommended_FallsBackToRawJSON_WhenNoKnownKey(t *testing.T) {
	// Unknown shape — preserve as compact JSON so the operator at least
	// sees the original payload instead of losing the data.
	raw := []byte(`[{"foo":"bar","baz":42}]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Len(t, nr, 1)
	require.Contains(t, nr[0], "foo")
	require.Contains(t, nr[0], "bar")
}

func TestNextRecommended_StripsEmptyAndWhitespace(t *testing.T) {
	raw := []byte(`["valid","","   ",{"action":"  spaced action  "},{"action":""}]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	require.Equal(t, envelope.NextRecommended{"valid", "spaced action"}, nr,
		"empty + whitespace-only entries dropped; trims kept entries")
}

func TestNextRecommended_HandlesNullAndEmpty(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"null", `null`},
		{"empty array", `[]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var nr envelope.NextRecommended
			require.NoError(t, json.Unmarshal([]byte(tc.raw), &nr))
			require.Empty(t, nr)
		})
	}
}

func TestNextRecommended_NonArrayInput_ReturnsError(t *testing.T) {
	// Defensive: an object or scalar at the top level is a contract
	// violation we surface as an error rather than silently coercing.
	var nr envelope.NextRecommended
	require.Error(t, json.Unmarshal([]byte(`{"key":"val"}`), &nr))
}

// Integration: full envelope parse with object-shaped next_recommended
// (the exact failure mode from smoke v4 that broke spec phase).
func TestEnvelope_FullParse_WithObjectNextRecommended(t *testing.T) {
	raw := []byte(`{
		"schema_version":"v1",
		"phase":"spec",
		"change_name":"x",
		"project":"y",
		"status":"DONE",
		"confidence":0.9,
		"executive_summary":"ok",
		"artifacts_saved":[],
		"next_recommended":[
			{"action":"proceed to tasks","rationale":"spec is complete"}
		],
		"risks":[],
		"data":{}
	}`)
	var env envelope.Envelope
	require.NoError(t, json.Unmarshal(raw, &env),
		"envelope with object-shaped next_recommended must parse without the spec-v4 BLOCK error")
	require.Equal(t, envelope.NextRecommended{"proceed to tasks"}, env.NextRecommended)
}

// Round-trip: ensure marshaling produces clean string array (not object array)
// so downstream consumers reading the persisted envelope see a uniform shape.
func TestNextRecommended_RoundTrip_NormalizesToStrings(t *testing.T) {
	raw := []byte(`[{"action":"do it"},"plain"]`)
	var nr envelope.NextRecommended
	require.NoError(t, json.Unmarshal(raw, &nr))
	out, err := json.Marshal(nr)
	require.NoError(t, err)
	require.JSONEq(t, `["do it","plain"]`, string(out),
		"round-trip must normalize object items to their projected strings")
}
