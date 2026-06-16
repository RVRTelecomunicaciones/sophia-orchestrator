package critic_test

import (
	"context"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/critic"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

func mkInput(env *envelope.Envelope) outbound.CriticInput {
	return outbound.CriticInput{
		ChangeID:  ids.ChangeID{},
		PhaseType: phase.PhaseSpec,
		Envelope:  env,
	}
}

// TestStubCritic_Review_DerivesConcernsDeterministically is a table-driven
// test pinning the deterministic mapping from envelope contents to advisory
// concerns (design D-GA-3). The stub uses NO clock, NO randomness — the same
// envelope always yields the same concerns.
func TestStubCritic_Review_DerivesConcernsDeterministically(t *testing.T) {
	tests := []struct {
		name     string
		env      *envelope.Envelope
		wantLen  int
		assertFn func(t *testing.T, got []phase.Concern)
	}{
		{
			name: "high risk yields a high/risk concern",
			env: &envelope.Envelope{
				Status:     envelope.StatusDone,
				Confidence: 0.9,
				Risks:      []envelope.Risk{{Description: "data loss", Level: "high"}},
			},
			wantLen: 1,
			assertFn: func(t *testing.T, got []phase.Concern) {
				require.Equal(t, "high", got[0].Severity)
				require.Equal(t, "risk", got[0].Category)
				require.NotEmpty(t, got[0].Message)
				require.NotEmpty(t, got[0].Evidence)
			},
		},
		{
			name: "low confidence yields a medium/confidence concern",
			env: &envelope.Envelope{
				Status:     envelope.StatusDone,
				Confidence: 0.4,
			},
			wantLen: 1,
			assertFn: func(t *testing.T, got []phase.Concern) {
				require.Equal(t, "medium", got[0].Severity)
				require.Equal(t, "confidence", got[0].Category)
			},
		},
		{
			name: "high risk and low confidence yield both concerns",
			env: &envelope.Envelope{
				Status:     envelope.StatusDone,
				Confidence: 0.3,
				Risks:      []envelope.Risk{{Description: "data loss", Level: "high"}},
			},
			wantLen: 2,
		},
		{
			name: "clean envelope yields no concerns",
			env: &envelope.Envelope{
				Status:     envelope.StatusDone,
				Confidence: 0.95,
				Risks:      []envelope.Risk{{Description: "minor", Level: "low"}},
			},
			wantLen: 0,
		},
	}

	stub := critic.NewStub()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := stub.Review(context.Background(), mkInput(tt.env))
			require.NoError(t, err)
			require.Len(t, got, tt.wantLen)
			if tt.assertFn != nil {
				tt.assertFn(t, got)
			}
		})
	}
}

// TestStubCritic_Review_IsReproducible runs the same input twice and asserts
// byte-identical output — proving no time.Now()/random leaked in (CLAUDE.md
// rule 5, design D-GA-3).
func TestStubCritic_Review_IsReproducible(t *testing.T) {
	stub := critic.NewStub()
	env := &envelope.Envelope{
		Status:     envelope.StatusDone,
		Confidence: 0.4,
		Risks:      []envelope.Risk{{Description: "x", Level: "high"}},
	}
	first, err := stub.Review(context.Background(), mkInput(env))
	require.NoError(t, err)
	second, err := stub.Review(context.Background(), mkInput(env))
	require.NoError(t, err)
	require.Equal(t, first, second)
}

// TestStubCritic_Review_NilEnvelope returns no concerns and no error — the
// critic must never break the phase even on degenerate input (D-GA-5).
func TestStubCritic_Review_NilEnvelope(t *testing.T) {
	stub := critic.NewStub()
	got, err := stub.Review(context.Background(), outbound.CriticInput{
		ChangeID:  ids.ChangeID{},
		PhaseType: phase.PhaseSpec,
		Envelope:  nil,
	})
	require.NoError(t, err)
	require.Empty(t, got)
}

// Compile-time assertion that StubCritic satisfies the outbound port.
var _ outbound.CriticPort = critic.NewStub()
