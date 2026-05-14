package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/apply"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/inbound"
)

// ApplyHandler exposes /api/v1/changes/{cid}/phases/{pid}/board.
type ApplyHandler struct {
	svc       inbound.ApplyService
	writeErr  func(http.ResponseWriter, error)
	writeJSON func(http.ResponseWriter, int, any)
}

// NewApplyHandler constructs an ApplyHandler.
func NewApplyHandler(svc inbound.ApplyService, writeErr func(http.ResponseWriter, error), writeJSON func(http.ResponseWriter, int, any)) *ApplyHandler {
	return &ApplyHandler{svc: svc, writeErr: writeErr, writeJSON: writeJSON}
}

type boardDTO struct {
	BoardID string     `json:"board_id"`
	PhaseID string     `json:"phase_id"`
	Status  string     `json:"status"`
	Groups  []groupDTO `json:"groups"`
}

type groupDTO struct {
	GroupID      string   `json:"group_id"`
	Name         string   `json:"name"`
	Status       string   `json:"status"`
	DependsOn    []string `json:"depends_on"`
	WorktreePath string   `json:"worktree_path,omitempty"`
	BranchName   string   `json:"branch_name,omitempty"`
	Tasks        []taskDTO `json:"tasks"`
}

type taskDTO struct {
	TaskID       string   `json:"task_id"`
	Description  string   `json:"description"`
	Status       string   `json:"status"`
	FilesPattern []string `json:"files_pattern"`
	Attempts     int      `json:"attempts"`
}

func toBoardDTO(b *apply.Board) boardDTO {
	groups := make([]groupDTO, 0, len(b.Groups()))
	for _, g := range b.Groups() {
		deps := make([]string, 0, len(g.DependsOn()))
		for _, d := range g.DependsOn() {
			deps = append(deps, d.String())
		}
		tasks := make([]taskDTO, 0, len(g.Tasks()))
		for _, t := range g.Tasks() {
			tasks = append(tasks, taskDTO{
				TaskID: t.ID().String(), Description: t.Description(),
				Status: string(t.Status()), FilesPattern: t.FilesPattern(),
				Attempts: t.Attempts(),
			})
		}
		groups = append(groups, groupDTO{
			GroupID: g.ID().String(), Name: g.Name(),
			Status: string(g.Status()), DependsOn: deps,
			WorktreePath: g.WorktreePath(), BranchName: g.BranchName(),
			Tasks: tasks,
		})
	}
	return boardDTO{
		BoardID: b.ID().String(), PhaseID: b.PhaseID().String(),
		Status: string(b.Status()), Groups: groups,
	}
}

// GetBoard handles GET /api/v1/changes/{cid}/phases/{pid}/board.
func (h *ApplyHandler) GetBoard(w http.ResponseWriter, r *http.Request) {
	pid, err := ids.ParsePhaseID(chi.URLParam(r, "phase_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	b, err := h.svc.GetBoard(r.Context(), pid)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, toBoardDTO(b))
}

// HealthHandler exposes /api/v1/health and /api/v1/ready.
type HealthHandler struct {
	startedAt time.Time
	ready     func() error
	writeJSON func(http.ResponseWriter, int, any)
}

// NewHealthHandler constructs a HealthHandler. ready() should return nil
// when all downstream deps are reachable; non-nil triggers 503.
func NewHealthHandler(startedAt time.Time, ready func() error, writeJSON func(http.ResponseWriter, int, any)) *HealthHandler {
	return &HealthHandler{startedAt: startedAt, ready: ready, writeJSON: writeJSON}
}

// Check handles GET /api/v1/health (always 200 if process is up).
func (h *HealthHandler) Check(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"uptime_s":   int(time.Since(h.startedAt).Seconds()),
		"checked_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready handles GET /api/v1/ready (200 only if downstream deps healthy).
//
// Response contract (ADR-0005 P1.4):
//
//	200  {"status":"ready",    "checks":{"db":"ok"}}
//	503  {"status":"degraded", "checks":{"db":"<error message>"}}
//
// Phase 1 only probes the Postgres pool. Additional checks (memory-engine,
// runtime-adapters, governance-core) are folded in as the platform matures;
// any additional probe MUST report ok / error string under its own key in
// the `checks` map so consumers (compose healthcheck, k8s probes) can keep
// reading the same envelope.
func (h *HealthHandler) Ready(w http.ResponseWriter, _ *http.Request) {
	checks := map[string]string{"db": "ok"}
	if h.ready != nil {
		if err := h.ready(); err != nil {
			checks["db"] = err.Error()
			h.writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "degraded",
				"checks": checks,
			})
			return
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ready",
		"checks": checks,
	})
}
