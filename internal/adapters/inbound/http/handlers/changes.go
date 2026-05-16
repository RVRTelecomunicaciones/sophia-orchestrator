// Package handlers contains the HTTP handlers (one struct per resource:
// Changes, Phases, Apply, SSE, Health). Handlers depend only on inbound
// service interfaces — no direct repo or runtime access.
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ChangesHandler exposes /api/v1/changes endpoints.
type ChangesHandler struct {
	svc       inbound.ChangeService
	phaseRepo outbound.PhaseRepository // optional — used to populate current_phase_id on Get
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
	logger    *slog.Logger
}

// NewChangesHandler constructs a ChangesHandler.
//
// phaseRepo is OPTIONAL: when supplied, GET /api/v1/changes/{id} populates
// the `current_phase_id` field on the response with the ID of the running
// phase (if any). When nil, the field is omitted from the JSON, preserving
// backward-compat with V1 responses that only had `current_phase` (the
// phase TYPE, not the instance ID). sophia-cli's Attach + Status commands
// rely on `current_phase_id` to subscribe to SSE — without it they fail
// with "snapshot has no current_phase_id".
func NewChangesHandler(svc inbound.ChangeService, phaseRepo outbound.PhaseRepository, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any)) *ChangesHandler {
	return &ChangesHandler{svc: svc, phaseRepo: phaseRepo, writeErr: writeErr, writeJSON: writeJSON, logger: slog.Default()}
}

type createChangeReq struct {
	Name              string `json:"name"`
	Project           string `json:"project"`
	ArtifactStoreMode string `json:"artifact_store_mode,omitempty"`
	BaseRef           string `json:"base_ref,omitempty"`
}

type changeDTO struct {
	ChangeID string `json:"change_id"`
	Name     string `json:"name"`
	Project  string `json:"project"`
	Status   string `json:"status"`
	// CurrentPhase is the phase TYPE pointer maintained on the change
	// aggregate (e.g. "init", "explore", "spec"). Stable since V1.
	CurrentPhase string `json:"current_phase"`
	// CurrentPhaseID is the ULID of the currently-running phase
	// instance, or empty when no phase is running. Populated only when
	// the handler was constructed with a PhaseRepository (omitempty
	// keeps V1 responses unchanged). Consumers (notably sophia-cli's
	// Attach + Status commands) use this to subscribe to SSE on the
	// active phase without having to first list phases by change.
	CurrentPhaseID    string `json:"current_phase_id,omitempty"`
	ArtifactStoreMode string `json:"artifact_store_mode"`
	BaseRef           string `json:"base_ref,omitempty"`
}

func toChangeDTO(c *change.Change) changeDTO {
	return changeDTO{
		ChangeID:          c.ID().String(),
		Name:              c.Name(),
		Project:           c.Project(),
		Status:            string(c.Status()),
		CurrentPhase:      string(c.CurrentPhase()),
		ArtifactStoreMode: string(c.ArtifactStore()),
		BaseRef:           c.BaseRef(),
	}
}

// resolveCurrentPhaseID looks up the running phase for a change via the
// optional PhaseRepository and returns its ID, or "" when no running
// phase exists (or the repo wasn't wired). Errors are swallowed: this
// field is best-effort and must NOT fail a GET that would otherwise
// succeed. The CLI degrades gracefully when the field is absent.
func (h *ChangesHandler) resolveCurrentPhaseID(ctx context.Context, changeID ids.ChangeID) string {
	if h.phaseRepo == nil {
		return ""
	}
	p, err := h.phaseRepo.FindRunningByChange(ctx, changeID)
	if err != nil || p == nil {
		return ""
	}
	return p.ID().String()
}

// Create handles POST /api/v1/changes.
func (h *ChangesHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createChangeReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	mode := change.ArtifactStoreMode(req.ArtifactStoreMode)
	if mode == "" {
		mode = change.ArtifactStoreMemoryEngine
	}
	c, err := h.svc.Create(r.Context(), inbound.CreateChangeInput{
		Name: req.Name, Project: req.Project, ArtifactStoreMode: mode, BaseRef: req.BaseRef,
	})
	if err != nil {
		h.writeErr(w, err)
		return
	}
	// Showcase ADR-0005 P2.2a: the TraceHandler wrapper on h.logger injects
	// trace_id and span_id automatically when r.Context() carries a W3C Trace.
	h.logger.LogAttrs(r.Context(), slog.LevelInfo, "change created",
		slog.String("change_id", c.ID().String()),
		slog.String("project", c.Project()),
	)
	h.writeJSON(w, http.StatusCreated, toChangeDTO(c))
}

// Get handles GET /api/v1/changes/{change_id}.
func (h *ChangesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := ids.ParseChangeID(chi.URLParam(r, "change_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	dto := toChangeDTO(c)
	dto.CurrentPhaseID = h.resolveCurrentPhaseID(r.Context(), id)
	h.writeJSON(w, http.StatusOK, dto)
}

// MaxListLimit caps the `limit` query parameter for paginated GETs.
// sophia-wire-v1 §9.2: requests above this MUST receive 400 +
// limit_too_large. Hard-coded for v0.2.0; v0.3.0 may surface this via
// configuration for tenant-specific tuning.
const MaxListLimit = 100

// List handles GET /api/v1/changes.
func (h *ChangesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := q.Get("project")
	status := q.Get("status")
	limit := atoiDefault(q.Get("limit"), 50)
	offset := atoiDefault(q.Get("offset"), 0)
	if limit > MaxListLimit {
		h.writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":  "limit_too_large",
			"error": "limit exceeds maximum allowed",
			"details": map[string]any{
				"limit":     limit,
				"max_limit": MaxListLimit,
			},
		})
		return
	}
	cs, err := h.svc.List(r.Context(), project, status, limit, offset)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	dtos := make([]changeDTO, 0, len(cs))
	for _, c := range cs {
		dtos = append(dtos, toChangeDTO(c))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": dtos})
}

type abortReq struct {
	Reason string `json:"reason,omitempty"`
}

// Abort handles POST /api/v1/changes/{change_id}/abort.
func (h *ChangesHandler) Abort(w http.ResponseWriter, r *http.Request) {
	id, err := ids.ParseChangeID(chi.URLParam(r, "change_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	var req abortReq
	_ = json.NewDecoder(r.Body).Decode(&req) // body optional
	if err := h.svc.Abort(r.Context(), id, req.Reason); err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "aborted"})
}
