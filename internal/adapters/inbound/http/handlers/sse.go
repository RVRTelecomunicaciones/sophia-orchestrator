package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// SSEHandler streams phase events to clients per sophia-wire-v1 §5.
// Sends `event: heartbeat` every HeartbeatInterval to keep proxies alive
// (Cloudflare tears down idle connections at 100s — heartbeat <60s is safe).
//
// Each event carries a fresh ULID in its `id:` SSE field
// (sophia-wire-v1 §5.1) so clients can resume via Last-Event-ID
// (§4.3) without timestamp collisions.
type SSEHandler struct {
	stream    inbound.EventStream
	heartbeat time.Duration
	writeErr  func(http.ResponseWriter, error)
	idGen     shared.IDGenerator
}

// NewSSEHandler constructs an SSEHandler. heartbeat ≤ 0 defaults to 5s.
// idGen MUST be a working ULID generator (NewSystemIDGenerator in
// production, FixedIDGenerator in tests).
func NewSSEHandler(stream inbound.EventStream, heartbeat time.Duration, writeErr func(http.ResponseWriter, error), idGen shared.IDGenerator) *SSEHandler {
	if heartbeat <= 0 {
		heartbeat = 5 * time.Second
	}
	return &SSEHandler{stream: stream, heartbeat: heartbeat, writeErr: writeErr, idGen: idGen}
}

// Stream handles GET /api/v1/phases/{phase_id}/events.
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	phaseID, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering
	w.WriteHeader(http.StatusOK)

	ch, cancel, err := h.stream.Subscribe(r.Context(), phaseID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	defer cancel()

	hb := time.NewTicker(h.heartbeat)
	defer hb.Stop()

	// `open` event with {phase_id} payload (sophia-wire-v1 §5.3
	// Phase 1.5 amendment: documented as Optional; clients MAY use
	// for fast reconnect detection).
	openID := h.idGen.NewID()
	fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"phase_id\":%q}\n\n",
		openID, contract.EventOpen, phaseID.String())
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			id := h.idGen.NewID()
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"ts\":%q}\n\n",
				id, contract.EventHeartbeat, time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, _ := json.Marshal(ev.Payload)
			id := h.idGen.NewID()
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n",
				id, ev.Type, payload)
			flusher.Flush()
		}
	}
}
