// Command stubs runs minimal in-process stub implementations of the three
// downstream services sophia-orchestator depends on (governance, memory,
// runtime). Used in local docker-compose to bring the binary up end-to-end
// without requiring real services.
//
// Each stub returns canned-but-shaped responses so the orchestrator's HTTP
// clients (gobreaker + JSON unmarshal) succeed.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func main() {
	addr := flag.String("addr", ":9000", "listen address")
	mode := flag.String("mode", "all", "all | governance | memory | runtime")
	flag.Parse()

	mux := http.NewServeMux()

	switch *mode {
	case "governance":
		mountGovernance(mux)
	case "memory":
		mountMemory(mux)
	case "runtime":
		mountRuntime(mux)
	default:
		mountGovernance(mux)
		mountMemory(mux)
		mountRuntime(mux)
	}

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	log.Printf("stub %s listening on %s", *mode, *addr)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("stub: %v", err)
	}
}

// --- governance ------------------------------------------------------------

func mountGovernance(mux *http.ServeMux) {
	mux.HandleFunc("/governance/v1/decisions/phase", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"decision":   "allow",
			"agent_role": "sdd-spec",
			"strategy":   "direct",
			"reason":     "stub allows all phases",
		})
	})
	mux.HandleFunc("/governance/v1/decisions/sensitive", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"decision":   "allow",
			"reason":     "stub allows sensitive actions in dev",
		})
	})
	mux.HandleFunc("/governance/v1/approvals/", func(w http.ResponseWriter, _ *http.Request) {
		// Always granted in stubs.
		writeJSON(w, 200, map[string]any{"status": "granted"})
	})
}

// --- memory ----------------------------------------------------------------

func mountMemory(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/memories", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, 201, map[string]any{
				"id":         "01ARZ3NDEKTSV4RRFFQ69G5MEM",
				"created_at": time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
		w.WriteHeader(405)
	})
	mux.HandleFunc("/api/v1/memories/", func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/memories/{id} or /{id}/archive.
		if strings.HasSuffix(r.URL.Path, "/archive") {
			writeJSON(w, 200, map[string]any{"status": "archived"})
			return
		}
		writeJSON(w, 200, map[string]any{
			"id":         "01ARZ3NDEKTSV4RRFFQ69G5MEM",
			"type":       "sdd_spec",
			"status":     "active",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"results":     []any{},
			"total_count": 0,
			"query":       "",
		})
	})
	mux.HandleFunc("/api/v1/search/context", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{
			"sections":     []any{},
			"total_tokens": 0,
			"truncated":    false,
			"generated_at": time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/v1/decisions", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 201, map[string]any{
			"id":         "01ARZ3NDEKTSV4RRFFQ69G5DEC",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		})
	})
	mux.HandleFunc("/api/v1/relations", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 201, map[string]any{})
	})
}

// --- runtime ---------------------------------------------------------------

func mountRuntime(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/executions", func(w http.ResponseWriter, r *http.Request) {
		// Parse the runtime request to extract the dispatcher payload.
		var req struct {
			Capability string `json:"capability"`
			PayloadB64 string `json:"payload_b64"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		payload, _ := base64.StdEncoding.DecodeString(req.PayloadB64)

		// Pull the prompt out of the inner shell.exec@v1 payload (stdin
		// field) and grep for the "# SDD Phase: <type>" / "Change: <name>"
		// lines the PromptBuilder emits. Falls back to "spec" / "stubbed".
		phase, change, project := parsePromptHeaders(string(payload))

		envelope := fmt.Sprintf(
			`{"schema_version":"v1","phase":%q,"change_name":%q,"project":%q,"status":"DONE","confidence":0.85,"executive_summary":"stub","artifacts_saved":[],"next_recommended":[],"risks":[],"data":{}}`,
			phase, change, project,
		)
		stdout := "stub stdout\n```json\n" + envelope + "\n```\n"
		writeJSON(w, 200, map[string]any{
			"status":      "success",
			"stdout_b64":  base64.StdEncoding.EncodeToString([]byte(stdout)),
			"stderr_b64":  base64.StdEncoding.EncodeToString([]byte("")),
			"exit_code":   0,
			"duration_ms": 100,
			"receipt_id":  "stub-rec-001",
			"retry_hint":  "non_retryable",
			"started_at":  time.Now().UTC().Format(time.RFC3339),
			"ended_at":    time.Now().UTC().Format(time.RFC3339),
		})
	})
}

// parsePromptHeaders extracts phase/change/project from the JSON-shaped
// shell.exec@v1 payload. The payload looks like:
//
//	{"cmd":"opencode","args":[...],"stdin":"# SDD Phase: spec\nChange: feat-x\nProject: demo\n..."}
func parsePromptHeaders(rawPayload string) (phase, change, project string) {
	phase, change, project = "spec", "stubbed", "stubbed"
	var p struct {
		Stdin string `json:"stdin"`
	}
	if err := json.Unmarshal([]byte(rawPayload), &p); err != nil {
		return
	}
	for _, line := range strings.Split(p.Stdin, "\n") {
		switch {
		case strings.HasPrefix(line, "# SDD Phase: "):
			phase = strings.TrimPrefix(line, "# SDD Phase: ")
		case strings.HasPrefix(line, "Change: "):
			change = strings.TrimPrefix(line, "Change: ")
		case strings.HasPrefix(line, "Project: "):
			project = strings.TrimPrefix(line, "Project: ")
		}
	}
	return
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
