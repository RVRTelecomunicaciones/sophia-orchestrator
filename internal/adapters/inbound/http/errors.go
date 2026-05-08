// Package http exposes the orchestrator's HTTP API: chi/v5 router,
// middleware (auth/logging/recover), handlers (changes/phases/apply/sse/
// health), and the SSE streaming handler.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/phase"
	domainchange "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	domainphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

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

// writeError maps an application/domain error to the right HTTP status +
// stable contract code (sophia-wire-v1 §9.1 / §9.2). The error envelope
// shape is contract.ErrorResponse: {code, error, details?}. Resource
// disambiguation (CodeChangeNotFound vs CodePhaseNotFound) is handled by
// writeErrorResource; callers without a resource hint fall back to a
// generic 404.
func writeError(w http.ResponseWriter, err error) {
	writeErrorResource(w, err, "")
}

// writeErrorResource is the resource-aware variant of writeError. The
// resource hint ("change" | "phase") lets us pick CodeChangeNotFound or
// CodePhaseNotFound for outbound.ErrNotFound; any other hint falls back
// to CodeValidationFailed.
func writeErrorResource(w http.ResponseWriter, err error, resource string) {
	if err == nil {
		writeJSON(w, http.StatusInternalServerError, contract.ErrorResponse{
			Code:  contract.CodeInternalError,
			Error: "unknown error",
		})
		return
	}
	status, code := mapError(err, resource)
	writeJSON(w, status, contract.ErrorResponse{
		Code:  code,
		Error: err.Error(),
	})
}

// writeErrorWithDetails writes an error envelope with structured details.
// Used by handlers that want to surface validation context (e.g. the
// invalid limit value for CodeLimitTooLarge).
func writeErrorWithDetails(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	writeJSON(w, status, contract.ErrorResponse{
		Code:    code,
		Error:   msg,
		Details: details,
	})
}

func mapError(err error, resource string) (int, string) {
	switch {
	case errors.Is(err, outbound.ErrNotFound):
		switch resource {
		case "change":
			return http.StatusNotFound, contract.CodeChangeNotFound
		case "phase":
			return http.StatusNotFound, contract.CodePhaseNotFound
		default:
			return http.StatusNotFound, contract.CodeValidationFailed
		}
	case errors.Is(err, ids.ErrInvalidID):
		return http.StatusBadRequest, contract.CodeValidationFailed
	case errors.Is(err, change.ErrAlreadyExists):
		return http.StatusConflict, contract.CodeChangeAlreadyExists
	case errors.Is(err, phase.ErrInvalidTransition):
		return http.StatusConflict, contract.CodeValidationFailed
	case errors.Is(err, phase.ErrPhaseRunning):
		return http.StatusConflict, contract.CodePhaseNotResumable
	case errors.Is(err, phase.ErrAlreadyTerminal):
		return http.StatusConflict, contract.CodeChangeAlreadyTerminal
	case errors.Is(err, phase.ErrApproverRequired):
		return http.StatusBadRequest, contract.CodeApproverRequired
	case errors.Is(err, phase.ErrPhaseNotGated):
		return http.StatusConflict, contract.CodePhaseNotGated
	case errors.Is(err, phase.ErrGateAlreadyDecided):
		return http.StatusConflict, contract.CodeGateAlreadyDecided
	case errors.Is(err, domainchange.ErrAlreadyTerminal):
		return http.StatusConflict, contract.CodeChangeAlreadyTerminal
	case errors.Is(err, domainchange.ErrEmptyName),
		errors.Is(err, domainchange.ErrEmptyProject),
		errors.Is(err, domainchange.ErrInvalidArtifactStore),
		errors.Is(err, domainchange.ErrInvalidTransition):
		return http.StatusBadRequest, contract.CodeValidationFailed
	case errors.Is(err, domainphase.ErrInvalidType),
		errors.Is(err, domainphase.ErrBudgetExhausted):
		return http.StatusBadRequest, contract.CodeValidationFailed
	default:
		return http.StatusInternalServerError, contract.CodeInternalError
	}
}
