package apply

import (
	"encoding/json"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/stretchr/testify/require"
)

// Spec #51 — extractBlockingReasons surfaces the LLM-emitted blocking
// signals so SSE consumers see the actual reason without needing DB
// access to agent_sessions.envelope.

func TestExtractBlockingReasons_NilEnvelope_ReturnsNil(t *testing.T) {
	require.Nil(t, extractBlockingReasons(nil))
}

func TestExtractBlockingReasons_NilData_ReturnsNil(t *testing.T) {
	env := &envelope.Envelope{Status: envelope.StatusBlocked}
	require.Nil(t, extractBlockingReasons(env))
}

func TestExtractBlockingReasons_BlockingReasonsKey(t *testing.T) {
	// gpt-5.4 in smoke v3 used this key verbatim.
	data := json.RawMessage(`{"blocking_reasons":["No local proposal-phase DONE evidence found.","Provided spec context is BLOCKED."]}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	got := extractBlockingReasons(env)
	require.Equal(t, []string{
		"No local proposal-phase DONE evidence found.",
		"Provided spec context is BLOCKED.",
	}, got)
}

func TestExtractBlockingReasons_BlockingRequirementsKey(t *testing.T) {
	// Alternative naming seen in earlier smoke runs.
	data := json.RawMessage(`{"blocking_requirements":["IL2_NO_APPLY_WITHOUT_TASKS_DONE"]}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	got := extractBlockingReasons(env)
	require.Equal(t, []string{"IL2_NO_APPLY_WITHOUT_TASKS_DONE"}, got)
}

func TestExtractBlockingReasons_BlockersKey(t *testing.T) {
	// Compact form some prompts produce.
	data := json.RawMessage(`{"blockers":["Missing evidence that the proposal phase is DONE."]}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	got := extractBlockingReasons(env)
	require.Equal(t, []string{"Missing evidence that the proposal phase is DONE."}, got)
}

func TestExtractBlockingReasons_NoBlockerKey_ReturnsNil(t *testing.T) {
	data := json.RawMessage(`{"foo":"bar","tasks":[]}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	require.Nil(t, extractBlockingReasons(env))
}

func TestExtractBlockingReasons_MalformedData_ReturnsNil(t *testing.T) {
	data := json.RawMessage(`{"blocking_reasons":"not-an-array"}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	require.Nil(t, extractBlockingReasons(env))
}

func TestExtractBlockingReasons_IgnoresEmptyStrings(t *testing.T) {
	data := json.RawMessage(`{"blocking_reasons":["valid",""," "]}`)
	env := &envelope.Envelope{Status: envelope.StatusBlocked, Data: data}
	require.Equal(t, []string{"valid"}, extractBlockingReasons(env))
}
