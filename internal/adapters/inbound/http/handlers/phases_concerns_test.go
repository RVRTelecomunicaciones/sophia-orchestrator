package handlers

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

func mustPhaseIDDTO(t *testing.T, raw string) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID(raw)
	require.NoError(t, err)
	return id
}

func mustChangeIDDTO(t *testing.T, raw string) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID(raw)
	require.NoError(t, err)
	return id
}

// TestToPhaseDTO_Concerns asserts the phase read path exposes the advisory
// critic's persisted concerns so an operator can review them post-hoc, while a
// phase without concerns omits the field entirely (byte-identical to before).
func TestToPhaseDTO_Concerns(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	env := &envelope.Envelope{
		SchemaVersion: envelope.SchemaVersionV1,
		Phase:         string(phase.PhaseSpec),
		Status:        envelope.StatusDoneWithConcerns,
		Confidence:    0.9,
	}

	t.Run("with concerns", func(t *testing.T) {
		p := phase.Hydrate(
			mustPhaseIDDTO(t, "01ARZ3NDEKTSV4RRFFQ69G5DT1"),
			mustChangeIDDTO(t, "01ARZ3NDEKTSV4RRFFQ69G5DC1"),
			phase.PhaseSpec, phase.PhaseStatusDoneWithConcerns,
			env, 0.9, 3, 1, &now, &now,
		)
		p.SetConcerns([]phase.Concern{
			{Severity: "high", Category: "risk", Message: "risky", Evidence: "risks[0].level=high"},
		})

		dto := toPhaseDTO(p)
		require.Equal(t, "done_with_concerns", dto.Status)
		require.Len(t, dto.Concerns, 1)
		require.Equal(t, inbound.ConcernPayload{
			Severity: "high", Category: "risk", Message: "risky", Evidence: "risks[0].level=high",
		}, dto.Concerns[0])
	})

	t.Run("without concerns omits field", func(t *testing.T) {
		clean := &envelope.Envelope{
			SchemaVersion: envelope.SchemaVersionV1,
			Phase:         string(phase.PhaseSpec),
			Status:        envelope.StatusDone,
			Confidence:    0.95,
		}
		p := phase.Hydrate(
			mustPhaseIDDTO(t, "01ARZ3NDEKTSV4RRFFQ69G5DT2"),
			mustChangeIDDTO(t, "01ARZ3NDEKTSV4RRFFQ69G5DC1"),
			phase.PhaseSpec, phase.PhaseStatusDone,
			clean, 0.95, 3, 0, &now, &now,
		)

		dto := toPhaseDTO(p)
		require.Empty(t, dto.Concerns, "phase without concerns must not carry concerns")
	})
}
