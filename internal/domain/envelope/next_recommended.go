package envelope

import (
	"encoding/json"
	"fmt"
	"strings"
)

// NextRecommended is the agent's list of suggested follow-up actions.
// Wire shape is `["string1", "string2", ...]` per the SDD envelope
// schema, but real LLMs drift: gpt-5.4 in smoke v4 emitted
// `[{"action":"...","rationale":"..."}, ...]` (objects, not strings)
// and the strict `[]string` target failed envelope validation, blocking
// the spec phase with an empty envelope (Spec #57).
//
// To stay resilient, NextRecommended implements json.Unmarshaler and
// projects mixed string/object input into a clean []string by probing
// known descriptive keys (action → description → message → summary)
// and falling back to compact JSON when nothing fits. Empty and
// whitespace-only entries are dropped. Marshal output stays plain
// strings so downstream consumers see a uniform shape regardless of
// what the LLM produced.
type NextRecommended []string

// nextRecommendedKeys is the priority order used to project an object
// item into a string. First non-empty match wins. Order chosen from
// observed LLM outputs across providers (gpt-5.4 uses "action",
// gemini tends toward "description", smaller models use "message"
// or "summary").
var nextRecommendedKeys = []string{"action", "description", "message", "summary"}

// UnmarshalJSON accepts a JSON array whose items may be strings or
// objects and projects them into []string. Returns an error only when
// the top-level value is not an array (or null) — item-level coercion
// fails silently by falling back to raw JSON so the operator at least
// sees the original payload.
func (n *NextRecommended) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*n = nil
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("next_recommended: expected array, got %s: %w",
			snippet(data), err)
	}
	out := make(NextRecommended, 0, len(raw))
	for _, item := range raw {
		if s := projectNextRecommendation(item); s != "" {
			out = append(out, s)
		}
	}
	*n = out
	return nil
}

// projectNextRecommendation reduces a single array item to its most
// descriptive string. Probes (in order): JSON string, JSON object with
// a known key, JSON object fallback (compact re-marshal), scalar
// stringify. Trims whitespace; returns "" for unusable inputs so the
// caller can drop the item.
func projectNextRecommendation(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// Path 1: plain string.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	// Path 2: object with a descriptive key.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err == nil {
		hadKnownKey := false
		for _, key := range nextRecommendedKeys {
			val, ok := obj[key]
			if !ok {
				continue
			}
			hadKnownKey = true
			var sv string
			if err := json.Unmarshal(val, &sv); err == nil {
				if t := strings.TrimSpace(sv); t != "" {
					return t
				}
			}
		}
		// When the object had a known descriptive key but its value
		// was empty/whitespace, drop the item rather than fall back to
		// compact JSON — an LLM that emitted {"action":""} signalled
		// "no action", not "show me the raw object".
		if hadKnownKey {
			return ""
		}
		// Path 3: object with no recognised key — compact JSON so the
		// operator at least sees the original payload.
		compact, err := json.Marshal(obj)
		if err == nil {
			return strings.TrimSpace(string(compact))
		}
	}
	// Path 4: scalar (number, bool) — stringify raw.
	return strings.TrimSpace(string(raw))
}

// snippet returns at most the first 80 bytes of data for use in error
// messages, replacing the rest with an ellipsis. Avoids dumping large
// payloads into validation error chains.
func snippet(data []byte) string {
	const limit = 80
	if len(data) <= limit {
		return string(data)
	}
	return string(data[:limit]) + "…"
}
