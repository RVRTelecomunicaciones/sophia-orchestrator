package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// J.1 RED — subcommand arg dispatch.

// recordedReeval captures the parameters dispatch routed to the reeval runner.
type recordedReeval struct {
	called  bool
	confirm bool
}

func (r *recordedReeval) run(_ context.Context, confirm bool) error {
	r.called = true
	r.confirm = confirm
	return nil
}

func TestDispatch_ReevalModes(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantHandled bool
		wantCalled  bool
		wantConfirm bool
	}{
		{
			name:        "no args runs server (not handled by reeval dispatch)",
			args:        nil,
			wantHandled: false,
		},
		{
			name:        "reeval default is dry-run, no confirm",
			args:        []string{"reeval"},
			wantHandled: true,
			wantCalled:  true,
			wantConfirm: false,
		},
		{
			name:        "reeval --dry-run is dry-run",
			args:        []string{"reeval", "--dry-run"},
			wantHandled: true,
			wantCalled:  true,
			wantConfirm: false,
		},
		{
			name:        "reeval --apply without --confirm stays dry-run",
			args:        []string{"reeval", "--apply"},
			wantHandled: true,
			wantCalled:  true,
			wantConfirm: false,
		},
		{
			name:        "reeval --apply --confirm applies",
			args:        []string{"reeval", "--apply", "--confirm"},
			wantHandled: true,
			wantCalled:  true,
			wantConfirm: true,
		},
		{
			name:        "reeval --apply --dry-run --confirm stays dry-run (dry-run wins)",
			args:        []string{"reeval", "--apply", "--dry-run", "--confirm"},
			wantHandled: true,
			wantCalled:  true,
			wantConfirm: false,
		},
		{
			name:        "unknown subcommand not handled",
			args:        []string{"serve"},
			wantHandled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordedReeval{}
			handled, err := dispatch(context.Background(), tt.args, rec.run)
			require.NoError(t, err)
			assert.Equal(t, tt.wantHandled, handled)
			assert.Equal(t, tt.wantCalled, rec.called)
			assert.Equal(t, tt.wantConfirm, rec.confirm)
		})
	}
}
