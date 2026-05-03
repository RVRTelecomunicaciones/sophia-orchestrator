package ids_test

import (
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/stretchr/testify/require"
)

func TestAllIDTypes_ZeroValueIsZero(t *testing.T) {
	var (
		c  ids.ChangeID
		p  ids.PhaseID
		b  ids.BoardID
		g  ids.GroupID
		tk ids.TaskID
		s  ids.SessionID
		w  ids.WorktreeID
	)
	require.True(t, c.IsZero())
	require.True(t, p.IsZero())
	require.True(t, b.IsZero())
	require.True(t, g.IsZero())
	require.True(t, tk.IsZero())
	require.True(t, s.IsZero())
	require.True(t, w.IsZero())
}

func TestAllIDTypes_ParsedIsNotZero(t *testing.T) {
	cases := map[string]func() (string, bool, error){
		"ChangeID":   func() (string, bool, error) { id, err := ids.ParseChangeID(validULID); return id.String(), id.IsZero(), err },
		"PhaseID":    func() (string, bool, error) { id, err := ids.ParsePhaseID(validULID); return id.String(), id.IsZero(), err },
		"BoardID":    func() (string, bool, error) { id, err := ids.ParseBoardID(validULID); return id.String(), id.IsZero(), err },
		"GroupID":    func() (string, bool, error) { id, err := ids.ParseGroupID(validULID); return id.String(), id.IsZero(), err },
		"TaskID":     func() (string, bool, error) { id, err := ids.ParseTaskID(validULID); return id.String(), id.IsZero(), err },
		"SessionID":  func() (string, bool, error) { id, err := ids.ParseSessionID(validULID); return id.String(), id.IsZero(), err },
		"WorktreeID": func() (string, bool, error) { id, err := ids.ParseWorktreeID(validULID); return id.String(), id.IsZero(), err },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			s, isZero, err := fn()
			require.NoError(t, err)
			require.False(t, isZero)
			require.Equal(t, validULID, s)
		})
	}
}
