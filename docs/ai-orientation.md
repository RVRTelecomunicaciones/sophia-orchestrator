# AI Orientation

If you are an AI agent making changes here, read in this order:

1. [`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`](superpowers/specs/2026-05-03-sophia-orchestator-design.md)
2. [`CLAUDE.md`](../CLAUDE.md)
3. [`docs/rules.md`](rules.md)
4. [`docs/domain-invariants.md`](domain-invariants.md)
5. [`AGENTS.md`](../AGENTS.md)

**The spec is authoritative.** Rules and invariants are the operational form of
the spec. Code disagreements with spec are bugs — fix the code, not the spec
(unless an ADR explicitly supersedes a spec section).

## Lifecycle of a contribution

1. Read the relevant spec section.
2. State your understanding (what you're touching, what invariants you might
   affect, what tests will change).
3. Write the failing test FIRST (`superpowers:test-driven-development`).
4. Write the minimal implementation.
5. Run the test until it passes.
6. Run `make lint` and `make test-unit`.
7. Commit using conventional format with the right scope.
8. Update `CHANGELOG.md` under `[Unreleased]` if this is user-visible.

## Banned patterns (lint-enforced)

- `time.Now()` in `internal/domain` or `internal/application` — use injectable `shared.Clock`.
- `ulid.Make()` in `internal/domain` or `internal/application` — use injectable `shared.IDGenerator`.
- Importing `internal/adapters/*` from `internal/domain` or `internal/application`.
- Importing `internal/infrastructure/*` from `internal/domain` or `internal/application`.

## Mandatory anti-patterns (manual review)

- Bypassing `SpawnGovernor` to spawn an AI subprocess.
- Persisting Envelope AFTER returning to the caller.
- Calling governance only "when needed" — every transition goes through governance.
- Coercing `BLOCKED` → `DONE` because "the third retry will work".
