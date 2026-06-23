package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	domainphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/infrastructure/obs"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// PhaseLookup is the narrow read-only port the SSE handler needs to
// short-circuit on terminal phases (sophia-wire-v1 §9.2:
// phase_terminal_no_events). Production wires inbound.PhaseService;
// tests pass a fake.
type PhaseLookup interface {
	Get(ctx context.Context, id ids.PhaseID) (*domainphase.Phase, error)
}

// SSEHandler streams phase events to clients per sophia-wire-v1 §5.
// Sends `event: heartbeat` every HeartbeatInterval to keep proxies alive
// (Cloudflare tears down idle connections at 100s — heartbeat <60s is safe).
//
// Each event carries a fresh ULID in its `id:` SSE field
// (sophia-wire-v1 §5.1) so clients can resume via Last-Event-ID
// (§4.3) without timestamp collisions.
type SSEHandler struct {
	stream    inbound.EventStream
	store     outbound.EventStore // durable history for Last-Event-ID resume
	phases    PhaseLookup
	heartbeat time.Duration
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
	idGen     shared.IDGenerator
	metrics   *obs.Metrics // optional; nil ⇒ no-op recording
}

// NewSSEHandler constructs an SSEHandler. heartbeat ≤ 0 defaults to 5s.
// idGen MUST be a working ULID generator (NewSystemIDGenerator in
// production, FixedIDGenerator in tests). phases MUST be a working
// PhaseService for the terminal-phase short-circuit. store MUST be a
// working EventStore so the handler can honour Last-Event-ID resume
// (audit rojo #3 fix). metrics is optional; pass nil to disable recording.
func NewSSEHandler(stream inbound.EventStream, store outbound.EventStore, phases PhaseLookup, heartbeat time.Duration, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any), idGen shared.IDGenerator, metrics *obs.Metrics) *SSEHandler {
	if heartbeat <= 0 {
		heartbeat = 5 * time.Second
	}
	return &SSEHandler{
		stream:    stream,
		store:     store,
		phases:    phases,
		heartbeat: heartbeat,
		writeErr:  writeErr,
		writeJSON: writeJSON,
		idGen:     idGen,
		metrics:   metrics,
	}
}

// parseLastEventID returns the int64 sequence from the Last-Event-ID
// header, or 0 if the header is absent / unparseable. Unparseable values
// are tolerated (returned as 0 = full replay) rather than rejected
// because the spec intentionally treats Last-Event-ID as opaque to the
// client; a corrupted value should not break the reconnect — the worst
// case is a from-the-beginning replay.
func parseLastEventID(r *http.Request) int64 {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// Stream handles GET /api/v1/phases/{phase_id}/events.
func (h *SSEHandler) Stream(w http.ResponseWriter, r *http.Request) {
	phaseID, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}

	// sophia-wire-v1 §9.2: clients attaching to a phase that is already
	// terminal MUST receive 410 + phase_terminal_no_events so they fall
	// back to the snapshot endpoint (GET /api/v1/phases/{id}).
	if h.phases != nil {
		p, perr := h.phases.Get(r.Context(), phaseID)
		switch {
		case errors.Is(perr, outbound.ErrNotFound):
			h.writeJSON(w, http.StatusNotFound, contract.ErrorResponse{
				Code:  contract.CodePhaseNotFound,
				Error: "phase not found",
			})
			return
		case perr != nil:
			h.writeErr(w, perr)
			return
		case p.Status().IsTerminal():
			h.writeJSON(w, http.StatusGone, contract.ErrorResponse{
				Code:  contract.CodePhaseTerminalNoEvents,
				Error: "phase is terminal; no further events will be emitted",
				Details: map[string]any{
					"phase_id": phaseID.String(),
					"status":   string(p.Status()),
				},
			})
			return
		}
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

	// Subscribe live BEFORE replaying history, so any event published
	// during the replay is captured in the channel buffer rather than
	// dropped on the floor. We deduplicate by Sequence at send time.
	ch, cancel, err := h.stream.Subscribe(r.Context(), phaseID)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	defer cancel()

	// Track open SSE connections.
	if h.metrics != nil {
		h.metrics.SSEConnectionsActive.Inc()
		defer h.metrics.SSEConnectionsActive.Dec()
	}

	// Audit rojo #3 fix: replay events from the durable store since the
	// client's Last-Event-ID (0 = full history). Failing here is fatal —
	// silently skipping replay would leave the client with a hole in its
	// event stream and no way to detect it.
	sinceSeq := parseLastEventID(r)
	historical, err := h.store.Replay(r.Context(), phaseID, sinceSeq)
	if err != nil {
		h.writeErr(w, err)
		return
	}

	hb := time.NewTicker(h.heartbeat)
	defer hb.Stop()

	// `open` event with {phase_id} payload (sophia-wire-v1 §5.3
	// Phase 1.5 amendment: documented as Optional; clients MAY use
	// for fast reconnect detection). Carries a fresh ULID — `open`
	// is not persisted in the EventStore, so we keep the synthesized
	// id for client-side reconnect detection.
	openID := h.idGen.NewID()
	fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"phase_id\":%q}\n\n",
		openID, contract.EventOpen, phaseID.String())
	flusher.Flush()

	// Stream replayed events first. Track maxReplayedSeq so the live
	// loop can dedup any overlap (events that landed in `ch` while we
	// were replaying — common when a client reconnects mid-phase).
	var maxReplayedSeq int64
	for _, ev := range historical {
		payload, _ := json.Marshal(ev.Payload)
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n",
			ev.Sequence, ev.Type, payload)
		flusher.Flush()
		if ev.Sequence > maxReplayedSeq {
			maxReplayedSeq = ev.Sequence
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			// Heartbeat carries a synthesized ULID — it's not persisted
			// (heartbeats are a transport concern, not state). Clients
			// MUST ignore non-numeric Last-Event-IDs on reconnect.
			id := h.idGen.NewID()
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: {\"ts\":%q}\n\n",
				id, contract.EventHeartbeat, time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			// Dedup against the replayed slice. Without this, a client
			// reconnecting mid-phase would see e.g. events 1..50 from
			// replay AND from the live channel (if they were buffered
			// between Subscribe and Replay completing).
			if ev.Sequence > 0 && ev.Sequence <= maxReplayedSeq {
				continue
			}
			payload, _ := json.Marshal(ev.Payload)
			// Use the persisted Sequence as the `id:` so the client can
			// resume from it on reconnect. Events with Sequence=0 (i.e.
			// the store Append failed for some reason — degraded mode)
			// fall back to a synthesized ULID so the stream still flows.
			var idField string
			if ev.Sequence > 0 {
				idField = strconv.FormatInt(ev.Sequence, 10)
			} else {
				idField = h.idGen.NewID()
			}
			fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n",
				idField, ev.Type, payload)
			flusher.Flush()
		}
	}
}
