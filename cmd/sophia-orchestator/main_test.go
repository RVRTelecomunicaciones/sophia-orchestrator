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

// recordedReverter captures the parameters dispatch routed to the revert runner.
type recordedReverter struct {
	called bool
	runID  string
	last   bool
}

func (r *recordedReverter) run(_ context.Context, runID string, last bool) error {
	r.called = true
	r.runID = runID
	r.last = last
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
			rev := &recordedReverter{}
			handled, err := dispatch(context.Background(), tt.args, rec.run, rev.run)
			require.NoError(t, err)
			assert.Equal(t, tt.wantHandled, handled)
			assert.Equal(t, tt.wantCalled, rec.called)
			assert.Equal(t, tt.wantConfirm, rec.confirm)
			assert.False(t, rev.called, "revert runner must not fire for non-revert modes")
		})
	}
}

// TestDispatch_RevertModes verifies the revert flags route to the reverter and
// that --revert and --apply are mutually exclusive.
func TestDispatch_RevertModes(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantHandled bool
		wantErr     bool
		wantCalled  bool
		wantRunID   string
		wantLast    bool
	}{
		{
			name:        "revert by run id",
			args:        []string{"reeval", "--revert", "RUN0000000000000000000001"},
			wantHandled: true,
			wantCalled:  true,
			wantRunID:   "RUN0000000000000000000001",
		},
		{
			name:        "revert last",
			args:        []string{"reeval", "--revert-last"},
			wantHandled: true,
			wantCalled:  true,
			wantLast:    true,
		},
		{
			name:        "revert and apply are mutually exclusive",
			args:        []string{"reeval", "--revert", "RUN1", "--apply", "--confirm"},
			wantHandled: true,
			wantErr:     true,
		},
		{
			name:        "revert with empty id errors",
			args:        []string{"reeval", "--revert", ""},
			wantHandled: true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &recordedReeval{}
			rev := &recordedReverter{}
			handled, err := dispatch(context.Background(), tt.args, rec.run, rev.run)
			assert.Equal(t, tt.wantHandled, handled)
			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, rec.called, "apply runner must not fire on a revert error")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantCalled, rev.called)
			assert.Equal(t, tt.wantRunID, rev.runID)
			assert.Equal(t, tt.wantLast, rev.last)
			assert.False(t, rec.called, "apply runner must not fire for revert modes")
		})
	}
}
