package opencode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
)

// extractEnvelopeRaw returns the raw JSON envelope bytes from an opencode
// invocation's stdout, or nil if no envelope can be located.
//
// Spec #58 / BUG-13 — opencode v0.x with the legacy `--format text` mode
// emits the LLM's natural response (plain text with a fenced ```json
// block at the end), but opencode 1.3.14 with `--format json` emits an
// NDJSON STREAM of events (`{"type":"text","part":{"text":"..."}}` ...).
// The pre-fix extractor only recognised the fence form and returned nil
// for streams, so smokes that actually delivered a valid envelope (exit
// 0, 20KB stdout) crashed the validator with "unsupported schema_version".
//
// Hybrid strategy (cheapest path wins):
//
//  1. Run the fence regex against the raw stdout. If it matches, the
//     caller used `--format text` or some adapter that left fences in
//     place — return the last fenced JSON. This preserves the old hot
//     path: no NDJSON parsing cost when not needed.
//  2. Otherwise, parse the stdout line-by-line as NDJSON. Concatenate
//     every `part.text` from events whose `type == "text"`. That string
//     is what the user would have seen in `--format text` mode.
//  3. Re-run the fence regex over the concatenated text. The LLM is
//     prompted to wrap its envelope in fences (Spec #58 prompt pin); if
//     it complied, this succeeds.
//  4. As a final fallback, try `json.Unmarshal` on the trimmed
//     concatenated text. Some models drop the fences under --format
//     json because they treat the JSON envelope as their "structured
//     response". Accept it only if it parses as a JSON object — random
//     scalars or arrays must not become a fake envelope.
//
// Returns nil for nil/empty input, streams without any `text` parts, and
// streams whose concatenated text contains no fence AND no parseable
// top-level JSON object.
func extractEnvelopeRaw(stdout []byte) []byte {
	if len(stdout) == 0 {
		return nil
	}

	// Path 1: classic — fence in raw stdout.
	if raw := extractLastFencedJSONFromText(stdout); raw != nil {
		return raw
	}

	// Path 2: NDJSON stream — collect every `type=="text"` part.text.
	collected := collectStreamText(stdout)
	if len(collected) == 0 {
		return nil
	}

	// Path 3: fence over collected text (LLM honoured the prompt pin).
	if raw := extractLastFencedJSONFromText(collected); raw != nil {
		return raw
	}

	// Path 4: bare JSON object as the entire collected text.
	trimmed := bytes.TrimSpace(collected)
	if isJSONObject(trimmed) {
		return trimmed
	}
	return nil
}

// extractLastFencedJSONFromText returns the JSON inside the LAST fenced
// ```json ... ``` block in src, or nil if none. Whitespace trimmed.
// Renamed from the original extractLastFencedJSON; the original name is
// preserved as a shim below for any caller that referenced it.
func extractLastFencedJSONFromText(src []byte) []byte {
	matches := fencedJSONRE.FindAllSubmatch(src, -1)
	if len(matches) == 0 {
		return nil
	}
	return bytes.TrimSpace(matches[len(matches)-1][1])
}

// streamEvent is the minimal shape of an opencode 1.3.x --format json
// event line we care about. We only extract textual content; tool
// invocations, step boundaries, and cost telemetry are intentionally
// ignored — they belong in observability metrics, not envelope parsing.
type streamEvent struct {
	Type string `json:"type"`
	Part struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"part"`
}

// collectStreamText scans stdout as NDJSON and concatenates the `text`
// field from every event whose type is "text". Lines that don't parse
// as JSON, or that aren't `text` events, are skipped silently — the
// stream may contain heartbeats, tool calls, step boundaries, etc.
//
// Buffer size is bumped from bufio's default 64 KiB to 1 MiB because a
// single text event can carry a long assistant response in one line.
// Returns an empty slice for streams with no text events at all.
func collectStreamText(stdout []byte) []byte {
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out strings.Builder
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if ev.Type != "text" {
			continue
		}
		out.WriteString(ev.Part.Text)
	}
	return []byte(out.String())
}

// isJSONObject reports whether data parses as a top-level JSON object.
// Used as the last-ditch fallback in extractEnvelopeRaw to accept a
// bare envelope without rejecting scalars or arrays as false positives.
func isJSONObject(data []byte) bool {
	if len(data) == 0 || data[0] != '{' {
		return false
	}
	var probe map[string]json.RawMessage
	return json.Unmarshal(data, &probe) == nil
}
