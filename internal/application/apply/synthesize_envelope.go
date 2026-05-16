package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// synthesizeEnvelopeFromGit reconstructs an opencode-shape apply
// envelope from the worktree's `git status --porcelain` output. It is
// the apply executor's bridge for adapters (currently aider) that
// edit files in-place and return EnvelopeRaw=nil.
//
// The reconstructed envelope has shape:
//
//	{
//	  "schema_version": "v1",
//	  "phase": "apply",
//	  "status": "DONE" | "PARTIAL",
//	  "edits": [{"path": "<path>", "action": "modified|created|deleted|renamed"}, ...]
//	}
//
// Status semantics:
//   - DONE: the dispatch's receipt was successful AND git status reports
//     at least one changed entry — the adapter actually edited files.
//   - PARTIAL: the dispatch's receipt was successful but git status is
//     empty — the adapter ran cleanly without touching anything (model
//     refused, or pre-existing state already matched the request). The
//     apply executor records this as a non-fatal but flag-worthy
//     outcome; downstream verify will decide whether the absence of
//     edits is acceptable.
//
// On a runtime-level failure of `git status` itself (e.g. the worktree
// path doesn't exist), the function returns an error; the caller maps
// that to envelope-validation-failed so the operator sees a clear
// signal rather than a silent empty envelope.
func synthesizeEnvelopeFromGit(ctx context.Context, runtime outbound.RuntimeClient, worktreePath string) ([]byte, error) {
	if worktreePath == "" {
		return nil, fmt.Errorf("synthesize envelope: empty worktree path")
	}

	payload, err := json.Marshal(map[string]any{
		"command":     "git",
		"args":        []string{"status", "--porcelain=v1"},
		"working_dir": worktreePath,
	})
	if err != nil {
		return nil, fmt.Errorf("synthesize envelope: marshal payload: %w", err)
	}

	receipt, err := runtime.Execute(ctx, outbound.ExecutionRequest{
		Capability: "shell.exec@v1",
		Payload:    payload,
		TimeoutMS:  5000,
	})
	if err != nil {
		return nil, fmt.Errorf("synthesize envelope: runtime: %w", err)
	}
	if receipt.Status != outbound.ReceiptSuccess {
		return nil, fmt.Errorf("synthesize envelope: git status receipt status=%s exit=%d stderr=%q",
			receipt.Status, receipt.ExitCode, receipt.Stderr)
	}

	edits := parsePorcelainV1(string(receipt.Stdout))
	status := "DONE"
	if len(edits) == 0 {
		status = "PARTIAL"
	}

	out, err := json.Marshal(map[string]any{
		"schema_version": "v1",
		"phase":          "apply",
		"status":         status,
		"edits":          edits,
	})
	if err != nil {
		return nil, fmt.Errorf("synthesize envelope: marshal envelope: %w", err)
	}
	return out, nil
}

// edit is the per-file entry inside the synthetic envelope's `edits`
// array. The shape is intentionally a subset of what opencode emits
// so the existing envelope validator + apply executor accept it
// without special-casing.
type edit struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// parsePorcelainV1 parses `git status --porcelain=v1` output into a
// slice of edits. The porcelain v1 format is two status chars (X for
// staged, Y for unstaged) followed by a space and the path:
//
//	" M file.go"      → modified
//	"A  new.go"       → created (added but unstaged delta)
//	" D removed.go"   → deleted
//	"?? untracked.go" → created (new file, never tracked)
//	"R  old.go -> new.go" → renamed (we report the NEW path with action=renamed)
//
// We collapse staged/unstaged status: the apply executor cares about
// "what changed in the worktree", not git index bookkeeping. The first
// non-space char of (X, Y) wins:
//
//	M, A, ? → "modified" / "created" / "created"
//	D       → "deleted"
//	R, C    → "renamed" (new path)
//	U       → "modified" (conflict markers present, treat as modified)
//
// Unknown codes are skipped silently rather than failing the whole
// envelope — git porcelain v1 has been stable for years but defensive
// parsing keeps us forward-compatible with whatever new codes future
// git releases introduce.
func parsePorcelainV1(stdout string) []edit {
	out := []edit{}
	for _, line := range strings.Split(stdout, "\n") {
		if len(line) < 4 { // need at least 2 status chars + space + path
			continue
		}
		x, y := line[0], line[1]
		rest := line[3:] // skip the space at index 2
		path := rest
		action := classifyStatus(x, y)
		if action == "renamed" {
			// Porcelain v1 rename format: "old -> new"
			if idx := strings.Index(rest, " -> "); idx >= 0 {
				path = rest[idx+len(" -> "):]
			}
		}
		if action == "" {
			continue
		}
		out = append(out, edit{Path: path, Action: action})
	}
	return out
}

// classifyStatus maps the (X, Y) status pair to one of the four
// envelope actions. The first interesting char wins (X has priority
// over Y when both are set, mirroring how the operator perceives the
// change — "I staged a deletion" beats "and then re-modified it").
func classifyStatus(x, y byte) string {
	pick := x
	if pick == ' ' || pick == 0 {
		pick = y
	}
	switch pick {
	case 'M', 'U':
		return "modified"
	case 'A', '?':
		return "created"
	case 'D':
		return "deleted"
	case 'R', 'C':
		return "renamed"
	}
	return ""
}
