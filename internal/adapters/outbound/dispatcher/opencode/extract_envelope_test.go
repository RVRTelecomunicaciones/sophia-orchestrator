package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Spec #58 (BUG-13) — opencode 1.3.14 with --format json emits NDJSON
// streaming events (`{"type":"text","part":{"text":"..."}}` ...) instead
// of plain text. The pre-fix extractor only knew about markdown fences
// in plain stdout, so streaming smokes finished with `envRaw=nil` and
// the orch rejected with "unsupported schema_version: got """ — even
// though the bridge had delivered 20KB of valid LLM output (exit 0,
// 112s). These tests pin the hybrid behaviour: fence-first for backward
// compat, NDJSON-fallback for streaming.

// Path 1 (existing behaviour): plain-text stdout with markdown fences.
// Must keep working so opencode --format text (the legacy default) and
// any future provider that emits plain stdout continue to extract OK.
func TestExtractEnvelope_ClassicTextWithFence(t *testing.T) {
	raw := loadFixture(t, "classic_text_with_fence.txt")
	got := extractEnvelopeRaw(raw)
	require.NotNil(t, got, "fence-first path must keep working for plain stdout")
	requireValidEnvelopeJSON(t, got, "classic text mode")
}

// Path 2 (new): NDJSON streaming where the LLM wrapped its envelope in
// markdown fences inside a `text` part. Hybrid extractor concatenates
// all `part.text` from `type=="text"` events, then runs the fence regex
// over the concatenated text.
func TestExtractEnvelope_StreamWithFencedEnvelope(t *testing.T) {
	raw := loadFixture(t, "stream_with_envelope.ndjson")
	got := extractEnvelopeRaw(raw)
	require.NotNil(t, got, "NDJSON stream with fenced envelope must be extracted")
	requireValidEnvelopeJSON(t, got, "ok")
}

// Path 3 (new): NDJSON streaming where the LLM emitted the envelope as
// a bare JSON object (no fences). After concatenation the fence regex
// matches nothing, so the fallback parses the concatenated text as JSON
// directly. Real LLMs occasionally drop the fences under --format json
// because they treat the JSON envelope as "their structured response".
func TestExtractEnvelope_StreamWithRawEnvelopeNoFence(t *testing.T) {
	raw := loadFixture(t, "stream_raw_envelope_no_fence.ndjson")
	got := extractEnvelopeRaw(raw)
	require.NotNil(t, got, "NDJSON stream with raw envelope (no fence) must be extracted")
	requireValidEnvelopeJSON(t, got, "raw envelope, no fence")
}

// Path 4 (regression): NDJSON stream that contains NO envelope at all
// (LLM only emitted plain text like "hello"). Extractor must return nil
// so the validator surfaces a clean "no envelope" error instead of
// passing junk downstream.
func TestExtractEnvelope_StreamWithNoEnvelope_ReturnsNil(t *testing.T) {
	raw := loadFixture(t, "stream_plain_text.ndjson")
	got := extractEnvelopeRaw(raw)
	require.Nil(t, got, "stream with no envelope must yield nil, not garbage")
}

// Path 5 (defensive): empty stdout, malformed JSON lines mixed in, nil
// input — none should panic, all should return nil.
func TestExtractEnvelope_EdgeCases(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
	}{
		{"nil", nil},
		{"empty", []byte{}},
		{"whitespace only", []byte("   \n\n\t")},
		{"malformed json lines", []byte("{not json\n{\"type\":\"x\"\n")},
		{"random text no fence", []byte("just some words, no envelope here")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Nil(t, extractEnvelopeRaw(tc.in))
		})
	}
}

// Path 6: backward compat — `extractLastFencedJSON` is the old name and
// the symbol the dispatcher historically called. It must remain a
// public-to-package shim that delegates to the hybrid extractor so the
// rest of the dispatcher keeps compiling without touching call sites.
func TestExtractLastFencedJSON_StillWorks(t *testing.T) {
	raw := loadFixture(t, "classic_text_with_fence.txt")
	got := extractLastFencedJSON(raw)
	require.NotNil(t, got, "shim must keep working")
}

// --- helpers ---

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return b
}

func requireValidEnvelopeJSON(t *testing.T, raw []byte, wantSummary string) {
	t.Helper()
	var env struct {
		SchemaVersion    string `json:"schema_version"`
		ExecutiveSummary string `json:"executive_summary"`
	}
	require.NoError(t, json.Unmarshal(raw, &env))
	require.Equal(t, "v1", env.SchemaVersion)
	require.Equal(t, wantSummary, env.ExecutiveSummary)
}
