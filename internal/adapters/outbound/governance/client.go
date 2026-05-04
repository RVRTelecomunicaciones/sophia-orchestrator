// Package governance implements outbound.GovernanceClient against
// agent-governance-core's HTTP API. Endpoints (per governance spec):
//
//   POST /governance/v1/decisions/phase     → evaluate a phase transition
//   POST /governance/v1/decisions/sensitive → evaluate a sensitive runtime call
//   GET  /governance/v1/approvals/{cid}/{pid}/status → poll approval status
package governance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/http_base"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// Sentinel errors raised by Client.
var (
	ErrApprovalDenied  = errors.New("governance: approval denied")
	ErrApprovalTimeout = errors.New("governance: approval timeout")
)

// Config tunes Client.
type Config struct {
	HTTPBase            http_base.Config
	APIKey              string
	ApprovalPollEvery   time.Duration
	ApprovalMaxWait     time.Duration
}

// DefaultConfig returns production defaults.
func DefaultConfig(baseURL, apiKey string) Config {
	hb := http_base.DefaultConfig("governance", baseURL)
	hb.DefaultHeaders = http.Header{"X-API-Key": {apiKey}}
	return Config{
		HTTPBase:          hb,
		APIKey:            apiKey,
		ApprovalPollEvery: 5 * time.Second,
		ApprovalMaxWait:   30 * time.Minute,
	}
}

// Client implements outbound.GovernanceClient.
type Client struct {
	cfg  Config
	http *http_base.Client
}

// New constructs a Client.
func New(cfg Config) (*Client, error) {
	hc, err := http_base.New(cfg.HTTPBase)
	if err != nil {
		return nil, fmt.Errorf("governance client: %w", err)
	}
	if cfg.ApprovalPollEvery <= 0 {
		cfg.ApprovalPollEvery = 5 * time.Second
	}
	if cfg.ApprovalMaxWait <= 0 {
		cfg.ApprovalMaxWait = 30 * time.Minute
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// --- wire shapes ---

type evalPhaseRequest struct {
	ChangeID        string `json:"change_id"`
	PhaseType       string `json:"phase_type"`
	TaskDescription string `json:"task_description,omitempty"`
	Sensitive       bool   `json:"sensitive"`
}

type evalSensitiveRequest struct {
	ChangeID   string         `json:"change_id"`
	Capability string         `json:"capability"`
	Payload    []byte         `json:"payload,omitempty"`
}

type decisionResponse struct {
	Decision    string                 `json:"decision"`
	AgentRole   string                 `json:"agent_role,omitempty"`
	Strategy    string                 `json:"strategy,omitempty"`
	Reason      string                 `json:"reason,omitempty"`
	Constraints map[string]any         `json:"constraints,omitempty"`
	Approval    *approvalGateResponse  `json:"approval,omitempty"`
}

type approvalGateResponse struct {
	URL string `json:"url"`
}

type approvalStatusResponse struct {
	Status string `json:"status"` // "pending" | "granted" | "denied"
}

// EvaluatePhase POSTs to /governance/v1/decisions/phase.
func (c *Client) EvaluatePhase(ctx context.Context, in outbound.EvaluatePhaseInput) (*outbound.GovernanceDecision, error) {
	req := evalPhaseRequest{
		ChangeID:        in.ChangeID.String(),
		PhaseType:       string(in.PhaseType),
		TaskDescription: in.TaskDescription,
		Sensitive:       in.Sensitive,
	}
	var resp decisionResponse
	if err := c.http.PostJSON(ctx, "/governance/v1/decisions/phase", req, &resp); err != nil {
		return nil, fmt.Errorf("governance EvaluatePhase: %w", err)
	}
	return toDecision(&resp), nil
}

// EvaluateSensitiveAction POSTs to /governance/v1/decisions/sensitive.
func (c *Client) EvaluateSensitiveAction(ctx context.Context, changeID ids.ChangeID, capability string, payload []byte) (*outbound.GovernanceDecision, error) {
	req := evalSensitiveRequest{
		ChangeID:   changeID.String(),
		Capability: capability,
		Payload:    payload,
	}
	var resp decisionResponse
	if err := c.http.PostJSON(ctx, "/governance/v1/decisions/sensitive", req, &resp); err != nil {
		return nil, fmt.Errorf("governance EvaluateSensitive: %w", err)
	}
	return toDecision(&resp), nil
}

// AwaitApproval polls /governance/v1/approvals/{cid}/{pid}/status every
// ApprovalPollEvery until granted, denied, or ApprovalMaxWait elapses.
func (c *Client) AwaitApproval(ctx context.Context, changeID ids.ChangeID, phaseID ids.PhaseID) error {
	deadline := time.Now().Add(c.cfg.ApprovalMaxWait)
	path := fmt.Sprintf("/governance/v1/approvals/%s/%s/status", changeID.String(), phaseID.String())
	for {
		var resp approvalStatusResponse
		if err := c.http.GetJSON(ctx, path, &resp); err != nil {
			return fmt.Errorf("governance AwaitApproval: %w", err)
		}
		switch resp.Status {
		case "granted":
			return nil
		case "denied":
			return ErrApprovalDenied
		}
		if time.Now().After(deadline) {
			return ErrApprovalTimeout
		}
		t := time.NewTimer(c.cfg.ApprovalPollEvery)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err() //nolint:wrapcheck
		case <-t.C:
		}
	}
}

func toDecision(r *decisionResponse) *outbound.GovernanceDecision {
	d := &outbound.GovernanceDecision{
		Decision:    outbound.GovernanceDecisionType(r.Decision),
		AgentRole:   r.AgentRole,
		Strategy:    r.Strategy,
		Reason:      r.Reason,
		Constraints: r.Constraints,
	}
	if r.Approval != nil {
		d.Approval = &outbound.ApprovalGate{URL: r.Approval.URL}
	}
	return d
}

// Compile-time interface check.
var _ outbound.GovernanceClient = (*Client)(nil)

// Avoid unused-import warning for phase in this file (used by interface check transitively).
var _ phase.PhaseType = ""
