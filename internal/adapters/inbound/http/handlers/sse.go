package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// SSEHandler streams phase events to clients per spec § 7.2.
// Sends `event: heartbeat` every HeartbeatInterval to keep proxies alive
// (Cloudflare tears down idle connections at 100s — heartbeat <60s is safe).
type SSEHandler struct {
	stream    inbound.EventStream
	heartbeat time.Duration
	writeErr  func(http.ResponseWriter, error)
}

// NewSSEHandler constructs an SSEHandler. heartbeat ≤ 0 defaults to 5s.
func NewSSEHandler(stream inbound.EventStream, heartbeat time.Duration, writeErr func(http.ResponseWriter, error)) *SSEHandler {
	if heartbeat <= 0 {
		heartbeat = 5 * time.Second
	}
	return &SSEHandler{stream: stream, heartbeat: heartbeat, writeErr: writeErr}
}

// Stream handles GET /api/v1/changes/{change_id}/phases/{phase_id}/events.
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

	// Send an initial open event so clients know the connection is live.
	fmt.Fprintf(w, "event: open\ndata: {\"phase_id\":%q}\n\n", phaseID.String())
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {\"ts\":%q}\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, _ := json.Marshal(ev.Payload)
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n",
				ev.Timestamp.UTC().Format(time.RFC3339Nano), ev.Type, payload)
			flusher.Flush()
		}
	}
}
