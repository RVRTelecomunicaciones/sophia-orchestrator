package governance_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/governance"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

func newClient(t *testing.T, srv *httptest.Server, modify func(*governance.Config)) *governance.Client {
	t.Helper()
	cfg := governance.DefaultConfig(srv.URL, "test-key")
	cfg.HTTPBase.MaxAttempts = 1
	cfg.ApprovalPollEvery = 5 * time.Millisecond
	cfg.ApprovalMaxWait = 200 * time.Millisecond
	if modify != nil {
		modify(&cfg)
	}
	c, err := governance.New(cfg)
	require.NoError(t, err)
	return c
}

func mustChangeID(t *testing.T) ids.ChangeID {
	t.Helper()
	id, err := ids.ParseChangeID("01ARZ3NDEKTSV4RRFFQ69G5C01")
	require.NoError(t, err)
	return id
}

func mustPhaseID(t *testing.T) ids.PhaseID {
	t.Helper()
	id, err := ids.ParsePhaseID("01ARZ3NDEKTSV4RRFFQ69G5P01")
	require.NoError(t, err)
	return id
}

func TestEvaluatePhase_Allow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/governance/v1/decisions/phase", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("X-API-Key"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"allow","agent_role":"sdd-spec","strategy":"direct","reason":"ok"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	d, err := c.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID:        mustChangeID(t),
		PhaseType:       phase.PhaseSpec,
		TaskDescription: "draft spec",
	})
	require.NoError(t, err)
	require.Equal(t, outbound.DecisionAllow, d.Decision)
	require.Equal(t, "sdd-spec", d.AgentRole)
}

func TestEvaluatePhase_RequireApproval_WithGate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"require_approval","approval":{"url":"/approve/abc"}}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	d, err := c.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID: mustChangeID(t), PhaseType: phase.PhaseSpec,
	})
	require.NoError(t, err)
	require.Equal(t, outbound.DecisionRequireApproval, d.Decision)
	require.NotNil(t, d.Approval)
	require.Equal(t, "/approve/abc", d.Approval.URL)
}

func TestEvaluatePhase_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	_, err := c.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID: mustChangeID(t), PhaseType: phase.PhaseSpec,
	})
	require.Error(t, err)
}

func TestEvaluateSensitiveAction(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/governance/v1/decisions/sensitive", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"deny","reason":"capability not allowed"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	d, err := c.EvaluateSensitiveAction(context.Background(), mustChangeID(t), "git.push@v1", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, outbound.DecisionDeny, d.Decision)
}

func TestAwaitApproval_Granted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			_, _ = w.Write([]byte(`{"status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"granted"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	require.NoError(t, c.AwaitApproval(context.Background(), mustChangeID(t), mustPhaseID(t)))
}

func TestAwaitApproval_Denied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"denied"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	err := c.AwaitApproval(context.Background(), mustChangeID(t), mustPhaseID(t))
	require.ErrorIs(t, err, governance.ErrApprovalDenied)
}

func TestAwaitApproval_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, func(cfg *governance.Config) {
		cfg.ApprovalPollEvery = 5 * time.Millisecond
		cfg.ApprovalMaxWait = 30 * time.Millisecond
	})
	err := c.AwaitApproval(context.Background(), mustChangeID(t), mustPhaseID(t))
	require.ErrorIs(t, err, governance.ErrApprovalTimeout)
}

func TestAwaitApproval_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := c.AwaitApproval(ctx, mustChangeID(t), mustPhaseID(t))
	require.Error(t, err)
}

func TestNew_RejectsBadConfig(t *testing.T) {
	_, err := governance.New(governance.Config{})
	require.Error(t, err)
}

func TestEvaluatePhase_RequestShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"allow"}`))
	}))
	defer srv.Close()

	c := newClient(t, srv, nil)
	_, err := c.EvaluatePhase(context.Background(), outbound.EvaluatePhaseInput{
		ChangeID: mustChangeID(t), PhaseType: phase.PhaseDesign, TaskDescription: "design x", Sensitive: true,
	})
	require.NoError(t, err)
	require.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5C01", got["change_id"])
	require.Equal(t, "design", got["phase_type"])
	require.Equal(t, "design x", got["task_description"])
	require.Equal(t, true, got["sensitive"])
}
