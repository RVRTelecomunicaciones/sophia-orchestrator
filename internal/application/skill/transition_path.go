package skill

import (
	domainskill "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
)

// transitionPath returns the shortest sequence of legal status transitions that
// walks from `from` to `to`, using the same allowedTransitions graph that
// PatchStatus enforces. The returned slice lists each successive target status
// (it excludes `from`); applying PatchStatus once per element reaches `to`
// without ever bypassing the transition guard.
//
// It returns (nil, true) when from == to (a zero-hop no-op), and (nil, false)
// when no legal path exists (e.g. archived is terminal). Because revert must
// reach the prior status through legal hops only, callers that get ok=false
// MUST skip-and-report the skill rather than force a raw status write.
//
// The walk is a breadth-first search, so the path is the shortest legal one and
// is deterministic for a fixed graph: neighbours are visited in the closed-enum
// order declared by statusOrder, which makes the chosen path stable across runs.
func transitionPath(from, to domainskill.Status) ([]domainskill.Status, bool) {
	if from == to {
		return nil, true
	}

	// BFS. prev records the node we arrived from, so we can reconstruct the path.
	visited := map[domainskill.Status]bool{from: true}
	prev := map[domainskill.Status]domainskill.Status{}
	queue := []domainskill.Status{from}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, next := range orderedTargets(cur) {
			if visited[next] {
				continue
			}
			visited[next] = true
			prev[next] = cur
			if next == to {
				return reconstruct(prev, from, to), true
			}
			queue = append(queue, next)
		}
	}
	return nil, false
}

// statusOrder is the deterministic visitation order for BFS neighbours. It is
// the closed-enum declaration order from the domain lifecycle, so the shortest
// path chosen by the search never depends on Go map iteration order.
var statusOrder = []domainskill.Status{
	domainskill.StatusCandidate,
	domainskill.StatusValidated,
	domainskill.StatusActive,
	domainskill.StatusDeprecated,
	domainskill.StatusBlocked,
	domainskill.StatusArchived,
}

// orderedTargets returns the legal direct transition targets of cur in the
// deterministic statusOrder, drawn from the authoritative allowedTransitions
// guard map (single source of truth shared with PatchStatus).
func orderedTargets(cur domainskill.Status) []domainskill.Status {
	allowed := allowedTransitions[cur]
	if len(allowed) == 0 {
		return nil
	}
	out := make([]domainskill.Status, 0, len(allowed))
	for _, s := range statusOrder {
		if allowed[s] {
			out = append(out, s)
		}
	}
	return out
}

// reconstruct walks the prev map from `to` back to `from`, returning the path
// in forward order excluding `from`.
func reconstruct(prev map[domainskill.Status]domainskill.Status, from, to domainskill.Status) []domainskill.Status {
	var rev []domainskill.Status
	for cur := to; cur != from; cur = prev[cur] {
		rev = append(rev, cur)
	}
	// Reverse in place.
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}
