// Package handlers contains the HTTP handlers (one struct per resource:
// Changes, Phases, Apply, SSE, Health). Handlers depend only on inbound
// service interfaces — no direct repo or runtime access.
package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// ChangesHandler exposes /api/v1/changes endpoints.
type ChangesHandler struct {
	svc       inbound.ChangeService
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
}

// NewChangesHandler constructs a ChangesHandler.
func NewChangesHandler(svc inbound.ChangeService, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any)) *ChangesHandler {
	return &ChangesHandler{svc: svc, writeErr: writeErr, writeJSON: writeJSON}
}

type createChangeReq struct {
	Name              string `json:"name"`
	Project           string `json:"project"`
	ArtifactStoreMode string `json:"artifact_store_mode,omitempty"`
	BaseRef           string `json:"base_ref,omitempty"`
}

type changeDTO struct {
	ChangeID          string `json:"change_id"`
	Name              string `json:"name"`
	Project           string `json:"project"`
	Status            string `json:"status"`
	CurrentPhase      string `json:"current_phase"`
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
	h.writeJSON(w, http.StatusOK, toChangeDTO(c))
}

// List handles GET /api/v1/changes.
func (h *ChangesHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := q.Get("project")
	status := q.Get("status")
	limit := atoiDefault(q.Get("limit"), 50)
	offset := atoiDefault(q.Get("offset"), 0)
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
