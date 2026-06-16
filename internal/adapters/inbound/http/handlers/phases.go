package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// PhasesHandler exposes /api/v1/changes/{change_id}/phases endpoints.
type PhasesHandler struct {
	svc       inbound.PhaseService
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
	logger    *slog.Logger
}

// NewPhasesHandler constructs a PhasesHandler.
func NewPhasesHandler(svc inbound.PhaseService, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any)) *PhasesHandler {
	return &PhasesHandler{svc: svc, writeErr: writeErr, writeJSON: writeJSON, logger: slog.Default()}
}

type runPhaseReq struct {
	TaskDescription  string         `json:"task_description,omitempty"`
	ContextOverrides map[string]any `json:"context_overrides,omitempty"`
	RetryBudget      int            `json:"retry_budget,omitempty"`
}

type runPhaseResp struct {
	PhaseID   string `json:"phase_id"`
	Status    string `json:"status"`
	EventsURL string `json:"events_url"`
	StartedAt string `json:"started_at"`
}

type phaseDTO struct {
	PhaseID     string  `json:"phase_id"`
	ChangeID    string  `json:"change_id"`
	Type        string  `json:"type"`
	Status      string  `json:"status"`
	Confidence  float64 `json:"confidence"`
	Attempts    int     `json:"attempts"`
	RetryBudget int     `json:"retry_budget"`

	// Concerns exposes the advisory critic's durably-persisted concerns so an
	// operator can review them post-hoc on the phase read path (design GAP B
	// durable follow-up). omitempty keeps a phase without concerns byte-
	// identical to the prior response. Mirrors the wire ConcernPayload shape
	// (sophia-wire-v1 §419) used on the SSE event. Strictly advisory.
	Concerns []inbound.ConcernPayload `json:"concerns,omitempty"`
}

func toPhaseDTO(p *phase.Phase) phaseDTO {
	dto := phaseDTO{
		PhaseID:     p.ID().String(),
		ChangeID:    p.ChangeID().String(),
		Type:        string(p.Type()),
		Status:      string(p.Status()),
		Confidence:  p.Confidence(),
		Attempts:    p.Attempts(),
		RetryBudget: p.RetryBudget(),
	}
	if concerns := p.Concerns(); len(concerns) > 0 {
		dto.Concerns = make([]inbound.ConcernPayload, len(concerns))
		for i, c := range concerns {
			dto.Concerns[i] = inbound.ConcernPayload{
				Severity: c.Severity,
				Category: c.Category,
				Message:  c.Message,
				Evidence: c.Evidence,
			}
		}
	}
	return dto
}

// Run handles POST /api/v1/changes/{change_id}/phases/{phase_type}/run.
func (h *PhasesHandler) Run(w http.ResponseWriter, r *http.Request) {
	cid, err := ids.ParseChangeID(chi.URLParam(r, "change_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	pt := phase.PhaseType(chi.URLParam(r, "phase_type"))
	if !pt.IsValid() {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid phase_type"})
		return
	}
	var req runPhaseReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	out, err := h.svc.Run(r.Context(), inbound.RunPhaseInput{
		ChangeID: cid, PhaseType: pt,
		TaskDescription:  req.TaskDescription,
		ContextOverrides: req.ContextOverrides,
		RetryBudget:      req.RetryBudget,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// Showcase ADR-0005 P2.2a: trace_id + span_id are injected automatically
	// by the TraceHandler wrapper when r.Context() carries a W3C Trace.
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, "phase run started",
		slog.String("change_id", cid.String()),
		slog.String("phase_type", string(pt)),
		slog.String("phase_id", out.PhaseID.String()),
	)
	// Spec #49: run/retry MUST return 200 OK so the retry path is
	// idempotent and smoke-testable without special-casing 202 vs 200.
	h.writeJSON(w, http.StatusOK, runPhaseResp{
		PhaseID: out.PhaseID.String(), Status: string(out.Status),
		EventsURL: out.EventsURL, StartedAt: out.StartedAt,
	})
}

// Get handles GET /api/v1/changes/{change_id}/phases/{phase_id}.
func (h *PhasesHandler) Get(w http.ResponseWriter, r *http.Request) {
	pid, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	p, err := h.svc.Get(r.Context(), pid)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, toPhaseDTO(p))
}

// Resume handles POST /api/v1/changes/{change_id}/phases/{phase_id}/resume.
func (h *PhasesHandler) Resume(w http.ResponseWriter, r *http.Request) {
	pid, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	out, err := h.svc.Resume(r.Context(), pid)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, runPhaseResp{
		PhaseID: out.PhaseID.String(), Status: string(out.Status),
		EventsURL: out.EventsURL, StartedAt: out.StartedAt,
	})
}

type approvalReq struct {
	Approver string `json:"approver"`
	Reason   string `json:"reason,omitempty"`
}

// Approve handles POST /api/v1/changes/{change_id}/phases/{phase_id}/approve.
func (h *PhasesHandler) Approve(w http.ResponseWriter, r *http.Request) {
	pid, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var req approvalReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.Approve(r.Context(), pid, req.Approver, req.Reason); err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// Reject handles POST /api/v1/changes/{change_id}/phases/{phase_id}/reject.
func (h *PhasesHandler) Reject(w http.ResponseWriter, r *http.Request) {
	pid, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var req approvalReq
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := h.svc.Reject(r.Context(), pid, req.Approver, req.Reason); err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}
