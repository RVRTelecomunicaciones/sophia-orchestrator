// Package bootstrap seeds initial Skill rows at startup.
// This file is the boot-time seeder — NOT a data migration.
// M1: seeding uses Upsert (ON CONFLICT (name, version) DO UPDATE) with the
// V4.1 §7 legacy payload (status=active, version=v1, activation_source=legacy_seed,
// risk_level=medium, scope={project_id:*, repo_id:*, phases:[<phase>]}).
// Running the seeder multiple times is idempotent (D-M1-4).
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/phase"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/shared"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/skill"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// SeedSkills upserts the 9 canonical Sophia SDD phase skills using the V4.1 §7
// legacy payload. It uses Upsert (ON CONFLICT (name, version) DO UPDATE) so
// re-running on an already-seeded DB is idempotent and keeps lifecycle fields
// up-to-date.
//
// The clock parameter is injected so timestamps are deterministic in tests
// (CLAUDE.md rule #5: no direct time.Now() in application packages).
//
// All errors are returned so the caller can decide to fail-soft or hard-fail at
// startup.
func SeedSkills(ctx context.Context, repo outbound.SkillRepository, clock shared.Clock, logger *slog.Logger) error {
	seeds, err := buildSeedSkills(clock.Now().UTC())
	if err != nil {
		// Construction errors indicate a programming mistake — hard-fail.
		return fmt.Errorf("bootstrap: SeedSkills: build seed definitions: %w", err)
	}

	for _, s := range seeds {
		if err := repo.Upsert(ctx, s); err != nil {
			return fmt.Errorf("bootstrap: SeedSkills: upsert %q: %w", s.Name(), err)
		}
	}

	logger.Info("bootstrap: skill seeder complete",
		slog.Int("seeds_attempted", len(seeds)))
	return nil
}

// buildSeedSkills constructs the 9 canonical Skill values using skill.NewLegacy so
// all domain invariants are validated at construction time and the V4.1 §7 payload
// (status=active, activation_source=legacy_seed, etc.) is applied automatically.
//
// Why a separate function: makes the seed definitions unit-testable without a
// real repository — callers can build the slice, inspect it, and assert counts
// and techniques without any DB.
func buildSeedSkills(now time.Time) ([]*skill.Skill, error) {
	defs := []struct {
		rawID      string
		name       string
		phases     []phase.PhaseType
		content    string
		techniques []skill.Technique
	}{
		// ── init: bootstrap ────────────────────────────────────────────────
		// Step back before diving in. The init phase sets the change context
		// so the entire SDD chain reasons from a shared understanding.
		{
			rawID:  "01JXSKLLP000000000000000IN",
			name:   "init-bootstrap-context",
			phases: []phase.PhaseType{phase.PhaseInit},
			content: `Before writing anything, step back and ask: what is the REAL problem this change solves?

Rules:
1. Capture the change name and project in the envelope — these identifiers propagate through every subsequent phase.
   Why: mismatched identifiers cause orphaned phases; anchoring them here prevents drift.
2. Do not assume scope — surface ambiguities as open questions for the explore phase.
   Why: the init phase is cheap to revisit; later phases are expensive.
3. Confirm the relevant bounded context (which aggregate, which service boundary) before proceeding.
   Why: context errors compound across the full SDD cycle.`,
			techniques: []skill.Technique{
				skill.TechniqueStepBack,
				skill.TechniqueInlineWhy,
			},
		},

		// ── explore: investigate ───────────────────────────────────────────
		// ReAct: Reason → Act → Observe in short cycles. Exploration is
		// empirical — do not speculate; look at the actual code and evidence.
		{
			rawID:  "01JXSKLLP000000000000EXPL0",
			name:   "explore-investigate",
			phases: []phase.PhaseType{phase.PhaseExplore},
			content: `Apply the ReAct (Reason–Act–Observe) cycle: for each hypothesis, take one concrete look-up action and record what you actually found before forming the next hypothesis.

Rules:
1. Never describe code you have not read — observe first, reason second.
   Why: hallucinated code structure causes the design phase to produce wrong interfaces.
2. For each finding, record: WHERE (file + line), WHAT (code excerpt), WHY it matters.
   Why: structured evidence prevents context loss when the explore artifact is handed to spec/design.
3. End with an explicit gap list: unknowns that a subsequent phase must resolve.
   Why: unexposed unknowns become silent assumptions that block the apply phase.`,
			techniques: []skill.Technique{
				skill.TechniqueReAct,
				skill.TechniqueInlineWhy,
			},
		},

		// ── proposal: draft-proposal ───────────────────────────────────────
		// Skeleton-of-Thought: emit the full proposal structure (sections,
		// alternatives, tradeoffs) before filling in details. This prevents
		// the common failure mode of writing a wall-of-text single approach.
		{
			rawID:  "01JXSKLLP000000000000PROP0",
			name:   "proposal-draft-options",
			phases: []phase.PhaseType{phase.PhaseProposal},
			content: `Use Skeleton-of-Thought: write the proposal outline first (problem statement, approach A, approach B, tradeoff table, recommendation), then fill each section in order.

Rules:
1. Present at least 2 distinct approaches before recommending one.
   Why: a single-option proposal is a decision, not a proposal; it removes stakeholder input.
2. The tradeoff table MUST include at least: implementation complexity, testability, and operational risk.
   Why: these three dimensions surface the most common post-implementation regrets.
3. The recommended approach MUST name a specific rationale, not just "simpler" or "better".
   Why: vague rationale cannot be challenged or revisited when constraints change.`,
			techniques: []skill.Technique{
				skill.TechniqueSkeletonOfThought,
				skill.TechniqueInlineWhy,
			},
		},

		// ── spec: write-specs ─────────────────────────────────────────────
		// Skeleton-of-Thought: emit the requirement skeleton (req IDs, scenario
		// slugs) before writing scenario bodies — prevents partial specs that
		// pass review but leave gaps.
		{
			rawID:  "01JXSKLLP000000000000SPEC0",
			name:   "spec-write-requirements",
			phases: []phase.PhaseType{phase.PhaseSpec},
			content: `Use Skeleton-of-Thought: list every requirement title and scenario slug before expanding any scenario body.

Rules:
1. Each scenario must follow GIVEN–WHEN–THEN with no "TBD" or "fill in details" placeholders.
   Why: vague scenarios cannot be used as acceptance criteria during the verify phase.
2. Every requirement must map to at least one observable outcome (measurable, not "should work well").
   Why: unmeasurable requirements block the verify phase and produce contested DONE calls.
3. If a requirement conflicts with an iron law, surface it explicitly — do not silently adjust the requirement.
   Why: hidden iron-law conflicts resurface at the apply or verify phase with much higher cost.`,
			techniques: []skill.Technique{
				skill.TechniqueSkeletonOfThought,
				skill.TechniqueInlineWhy,
			},
		},

		// ── design: architect ─────────────────────────────────────────────
		// Extended-Thinking + Step-Back: think through the full system
		// implications before committing to a structure. Step back means:
		// zoom out to the aggregate boundary, not just the file to be changed.
		{
			rawID:  "01JXSKLLP000000000000DSGN0",
			name:   "design-architect-system",
			phases: []phase.PhaseType{phase.PhaseDesign},
			content: `Before designing any interface, step back to the aggregate boundary level: what invariant does this aggregate own, and how does the new behavior interact with existing state transitions?

Apply extended thinking: for each architectural decision, explicitly enumerate what you are NOT doing and why.

Rules:
1. Every architectural decision MUST have a documented alternative and a rationale for rejection.
   Why: undocumented decisions are re-litigated at every future code review.
2. Identify the persistence boundary before the interface boundary.
   Why: retrofitting persistence constraints onto an already-designed interface is the most expensive rework in this codebase.
3. Cross-aggregate reads are allowed; cross-aggregate writes require an explicit event or saga.
   Why: silent cross-aggregate mutation breaks the invariant model and makes audit trails inconsistent.
4. Mark every open question — do not silently resolve them with an assumption.
   Why: silent assumptions in design become load-bearing decisions in apply that nobody can later challenge.`,
			techniques: []skill.Technique{
				skill.TechniqueExtendedThinking,
				skill.TechniqueStepBack,
				skill.TechniqueInlineWhy,
			},
		},

		// ── tasks: decompose ──────────────────────────────────────────────
		// Extended-Thinking: think through the full dependency graph before
		// emitting task groups — prevents tasks that block each other silently.
		{
			rawID:  "01JXSKLLP000000000000TASK0",
			name:   "tasks-decompose-work",
			phases: []phase.PhaseType{phase.PhaseTasks},
			content: `Use extended thinking to map the full dependency graph before writing any task. Ask: which task, if delayed, blocks all others? That task goes in the first group.

Rules:
1. Each task must be completable in 2–5 minutes of focused work on a single concern.
   Why: oversized tasks produce oversized diffs that fail review and obscure rollback boundaries.
2. Every group must declare its depends_on list explicitly — no implicit ordering.
   Why: implicit ordering is lost when tasks are parallelized across apply workers.
3. files_pattern must be specific enough that two concurrent tasks do not overlap.
   Why: overlapping patterns cause merge conflicts and partial-state corruption in parallel apply.
4. Include a verify task in the final group that asserts the full acceptance criteria from the spec.
   Why: without a spec-anchored verify task, the verify phase has no ground truth.`,
			techniques: []skill.Technique{
				skill.TechniqueExtendedThinking,
				skill.TechniqueInlineWhy,
			},
		},

		// ── apply: implement ──────────────────────────────────────────────
		// Constitutional-Self-Critique: after writing each change, evaluate
		// it against a checklist before moving on. This prevents the common
		// pattern of writing code fast and discovering constraint violations
		// in the verify phase.
		{
			rawID:  "01JXSKLLP000000000000APLY0",
			name:   "apply-implement-safely",
			phases: []phase.PhaseType{phase.PhaseApply},
			content: `After implementing each task, apply constitutional self-critique: run your own review against the checklist before marking the task done.

Self-critique checklist:
- Does this change break any iron law? (check docs/rules.md)
- Is the new code reachable by the tests in this task's scope?
- Did I introduce a cross-aggregate write without an event or saga?
- Did I call time.Now() or ulid.Make() inside domain or application packages?
- Is the public surface area I added the minimum needed by the spec scenario?

Rules:
1. Never mark a task done before running the checklist above.
   Why: defects found in the apply phase cost 5× less to fix than defects found in the verify phase.
2. If the checklist reveals a constraint violation, fix it in the same task — do not defer.
   Why: deferred fixes are frequently forgotten and cause verify to block unexpectedly.
3. When in doubt about files_pattern scope, do LESS and report it — do not expand scope silently.
   Why: scope creep in apply pollutes other tasks' boundaries and makes rollback impossible.`,
			techniques: []skill.Technique{
				skill.TechniqueConstitutionalSelfCritique,
				skill.TechniqueInlineWhy,
			},
		},

		// ── verify: validate ──────────────────────────────────────────────
		// Chain-of-Verification: each claim (tests pass, coverage gate met,
		// spec scenario satisfied) must be backed by concrete output, not
		// assertion. Do not say "tests pass" without citing the output.
		{
			rawID:  "01JXSKLLP000000000000VRFY0",
			name:   "verify-chain-validation",
			phases: []phase.PhaseType{phase.PhaseVerify},
			content: `Apply chain-of-verification: for every claim in your summary, provide the concrete evidence that backs it.

Evidence format: claim → command run → exact output excerpt.

Rules:
1. "Tests pass" is only valid when accompanied by the exact test runner output including the counts.
   Why: a claim without evidence is indistinguishable from a hallucination to the orchestrator.
2. For each spec scenario, assert WHICH test covers it and cite the test name.
   Why: uncovered scenarios silently pass verify and resurface as production bugs.
3. Coverage gates (domain 100%, app 85%) must be cited from actual coverage output — not estimated.
   Why: estimated coverage routinely overstates and hides missing cases.
4. If any scenario is NOT covered, set status=DONE_WITH_CONCERNS and list the gap.
   Why: DONE with hidden gaps causes the archive phase to lock in an incomplete state.`,
			techniques: []skill.Technique{
				skill.TechniqueChainOfVerification,
				skill.TechniqueInlineWhy,
			},
		},

		// ── archive: finalize ─────────────────────────────────────────────
		// Step-Back: before archiving, step back from the implementation
		// details and review the change as a whole against its original intent.
		{
			rawID:  "01JXSKLLP000000000000ARCH0",
			name:   "archive-finalize-deltas",
			phases: []phase.PhaseType{phase.PhaseArchive},
			content: `Before finalizing, step back and re-read the original proposal intent: does the implementation deliver what was proposed, or did scope drift occur?

Rules:
1. The archive artifact must contain a delta summary: what changed vs the original design, and why any deviation was acceptable.
   Why: deviations that are not documented become undocumented technical debt.
2. If scope drifted beyond the proposal, mark the artifact with a risks entry describing the drift.
   Why: undocumented scope changes make future archaeology misleading and break change tracking.
3. Verify that every open question from the design phase is either resolved in the archive artifact or explicitly deferred to a follow-up change.
   Why: unresolved design questions that disappear into the archive make follow-up changes impossible to scope accurately.`,
			techniques: []skill.Technique{
				skill.TechniqueStepBack,
				skill.TechniqueInlineWhy,
			},
		},
	}

	out := make([]*skill.Skill, 0, len(defs))
	for _, d := range defs {
		id, err := ids.ParseSkillID(d.rawID)
		if err != nil {
			return nil, fmt.Errorf("seed skill %q: invalid ID %q: %w", d.name, d.rawID, err)
		}
		s, err := skill.NewLegacy(id, d.name, d.phases, d.content, d.techniques, now)
		if err != nil {
			return nil, fmt.Errorf("seed skill %q: %w", d.name, err)
		}
		out = append(out, s)
	}
	return out, nil
}
