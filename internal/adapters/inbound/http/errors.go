// Package http exposes the orchestrator's HTTP API: chi/v5 router,
// middleware (auth/logging/recover), handlers (changes/phases/apply/sse/
// health), and the SSE streaming handler.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	domainphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

type errorBody struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("http write json", "err", err)
	}
}

// writeError maps an application/domain error to the right HTTP status.
func writeError(w http.ResponseWriter, err error) {
	if err == nil {
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "unknown error", Code: "internal"})
		return
	}
	status, code := mapError(err)
	writeJSON(w, status, errorBody{Error: err.Error(), Code: code})
}

func mapError(err error) (int, string) {
	switch {
	case errors.Is(err, outbound.ErrNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, ids.ErrInvalidID):
		return http.StatusBadRequest, "invalid_id"
	case errors.Is(err, change.ErrAlreadyExists):
		return http.StatusConflict, "already_exists"
	case errors.Is(err, phase.ErrInvalidTransition):
		return http.StatusConflict, "invalid_transition"
	case errors.Is(err, phase.ErrPhaseRunning):
		return http.StatusConflict, "phase_running"
	case errors.Is(err, phase.ErrAlreadyTerminal):
		return http.StatusConflict, "already_terminal"
	case errors.Is(err, domainchange.ErrAlreadyTerminal):
		return http.StatusConflict, "change_terminal"
	case errors.Is(err, domainchange.ErrEmptyName),
		errors.Is(err, domainchange.ErrEmptyProject),
		errors.Is(err, domainchange.ErrInvalidArtifactStore),
		errors.Is(err, domainchange.ErrInvalidTransition):
		return http.StatusBadRequest, "validation_error"
	case errors.Is(err, domainphase.ErrInvalidType),
		errors.Is(err, domainphase.ErrBudgetExhausted):
		return http.StatusBadRequest, "validation_error"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
