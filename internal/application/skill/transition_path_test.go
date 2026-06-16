package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"

	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// transitionPath is an internal (whitebox) function, so this test lives in
// package skill (not skill_test). It verifies the BFS over allowedTransitions
// that the revert path uses to walk a legal multi-hop inverse.

func TestTransitionPath(t *testing.T) {
	tests := []struct {
		name string
		from domainskill.Status
		to   domainskill.Status
		want []domainskill.Status // intermediate+target steps (excludes `from`)
		ok   bool
	}{
		{
			name: "same status is a zero-hop no-op path",
			from: domainskill.StatusActive,
			to:   domainskill.StatusActive,
			want: nil,
			ok:   true,
		},
		{
			name: "direct single-hop inverse: validated->active demotion back",
			// Promotion validated->active; inverse active->? not direct, but
			// active->deprecated is direct (single hop) the other way.
			from: domainskill.StatusActive,
			to:   domainskill.StatusDeprecated,
			want: []domainskill.Status{domainskill.StatusDeprecated},
			ok:   true,
		},
		{
			name: "multi-hop inverse: deprecated->active walks the legal chain",
			from: domainskill.StatusDeprecated,
			to:   domainskill.StatusActive,
			want: []domainskill.Status{
				domainskill.StatusBlocked,
				domainskill.StatusCandidate,
				domainskill.StatusValidated,
				domainskill.StatusActive,
			},
			ok: true,
		},
		{
			name: "multi-hop inverse: active->validated takes the shortest legal chain active->blocked->candidate->validated",
			// active->blocked is a direct legal transition, so BFS reaches
			// validated in 3 hops (via blocked), never 4 (via deprecated).
			from: domainskill.StatusActive,
			to:   domainskill.StatusValidated,
			want: []domainskill.Status{
				domainskill.StatusBlocked,
				domainskill.StatusCandidate,
				domainskill.StatusValidated,
			},
			ok: true,
		},
		{
			name: "no legal path: archived is terminal, cannot reach active",
			from: domainskill.StatusArchived,
			to:   domainskill.StatusActive,
			want: nil,
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := transitionPath(tt.from, tt.to)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
