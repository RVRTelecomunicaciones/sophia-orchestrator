package ids_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/stretchr/testify/require"
)

const validULID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestParseChangeID_Valid(t *testing.T) {
	id, err := ids.ParseChangeID(validULID)
	require.NoError(t, err)
	require.Equal(t, validULID, id.String())
	require.False(t, id.IsZero())
}

func TestParseChangeID_RejectsEmpty(t *testing.T) {
	_, err := ids.ParseChangeID("")
	require.ErrorIs(t, err, ids.ErrInvalidID)
}

func TestParseChangeID_RejectsTooShort(t *testing.T) {
	_, err := ids.ParseChangeID("not-a-ulid")
	require.ErrorIs(t, err, ids.ErrInvalidID)
}

func TestZeroValue_IsZero(t *testing.T) {
	var id ids.ChangeID
	require.True(t, id.IsZero())
	require.Empty(t, id.String())
}

func TestAllIDTypes_RoundTrip(t *testing.T) {
	type parser func(string) (string, error)
	cases := map[string]parser{
		"ChangeID":   func(s string) (string, error) { id, err := ids.ParseChangeID(s); return id.String(), err },
		"PhaseID":    func(s string) (string, error) { id, err := ids.ParsePhaseID(s); return id.String(), err },
		"BoardID":    func(s string) (string, error) { id, err := ids.ParseBoardID(s); return id.String(), err },
		"GroupID":    func(s string) (string, error) { id, err := ids.ParseGroupID(s); return id.String(), err },
		"TaskID":     func(s string) (string, error) { id, err := ids.ParseTaskID(s); return id.String(), err },
		"SessionID":  func(s string) (string, error) { id, err := ids.ParseSessionID(s); return id.String(), err },
		"WorktreeID": func(s string) (string, error) { id, err := ids.ParseWorktreeID(s); return id.String(), err },
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := p(validULID)
			require.NoError(t, err)
			require.Equal(t, validULID, got)

			_, err = p("")
			require.ErrorIs(t, err, ids.ErrInvalidID)
		})
	}
}
