# Contributing — sophia-orchestator

Conventions live in [`AGENTS.md`](AGENTS.md) (quickstart, hard rules, commit
prefixes) and [`CLAUDE.md`](CLAUDE.md) (principles). This file documents the
cross-repo workflow that those files do not cover.

## Cross-repo changes (orch ↔ agent-governance-core)

The orchestrator is contract-locked to `agent-governance-core` (govcore). CI runs
a dedicated `contract` job (`.github/workflows/ci.yaml`) that checks out **both**
repos into a workspace, generates a `go.work` linking them, and runs the
`-tags=contract` test (`test/integration/governance_contract_lock_test.go`). This
guards the orch↔govcore governance contract against silent drift under
independent CI. See [ADR-0015](docs/adr/0015-governance-advisory-contract-lock-and-critic.md).

### Merge order

When a change spans **both** govcore's public surface (the `govhttptest` seam or
the `/governance/v1/*` facade) **and** orch's contract test, land them in this order:

1. **Merge the govcore PR first**, onto govcore `main`.
2. **Then merge the orch PR.**

The `contract` job checks out govcore `main`, so the orch PR's job can only go
green once govcore's change is already on `main`. Merge the orch PR first and the
`contract` job fails — it cannot resolve the seam import until govcore lands.

### Prerequisite: `ECOSYSTEM_REPO_TOKEN`

The cross-repo checkout of govcore (a separate private repo) requires the
`ECOSYSTEM_REPO_TOKEN` repo secret: a fine-grained PAT with **Contents: Read** on
`RVRTelecomunicaciones/agent-governance-core`. Without it the `contract` job
cannot check out govcore and will fail.

### Why `go.work`, not a `go.mod` require

govcore's module path (`github.com/russellcxl/agent-governance-core`) does not
match its remote (`RVRTelecomunicaciones/agent-governance-core`). A `go.mod`
require would force a failing VCS lookup, so the cross-repo dependency is resolved
via `go.work` (locally and in the `contract` job) instead. This mismatch is a
known, govcore-owned ecosystem cleanup item — see ADR-0015.
