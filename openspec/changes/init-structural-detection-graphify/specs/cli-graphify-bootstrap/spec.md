# Delta: cli-graphify-bootstrap

## Capability

The `sophia-cli` `init` command MUST detect the presence and version of the Graphify toolchain (Python 3.10+, `uv` or `pyenv`, and `graphifyy[mcp]==0.8.35`) via a `GraphifyProber` outbound port, record the detection result so that downstream INIT orchestration can read it, and optionally install Graphify when the `--auto-bootstrap-graphify` flag is supplied. No installation occurs by default (degraded-first per V4.1 §7-ter.7).

---

## ADDED Requirements

### Requirement: Graphify Toolchain Detection

The `Initializer.Run()` MUST invoke `GraphifyProber.Probe(ctx)` after writing `.sophia.yaml` and MUST record the result — including `available bool`, `version string`, and `missing_deps []string` — so that the orchestrator INIT service can read it without re-probing.

The `GraphifyProber` MUST be injected via `InitializerDeps` (interface, not concrete type) so unit tests can supply a fake without executing any subprocess.

#### Scenario: detection-success

- GIVEN a machine has Python >= 3.10 and `graphifyy[mcp]==0.8.35` installed
- WHEN `sophia init` runs
- THEN `GraphifyProber.Probe` returns `available=true`, `version="0.8.35"`, `missing_deps=[]`
- AND `Initializer.Run()` logs at INFO level: `graphify detected, version=0.8.35`

#### Scenario: detection-missing-python

- GIVEN a machine does not have Python >= 3.10
- WHEN `sophia init` runs
- THEN `GraphifyProber.Probe` returns `available=false`, `missing_deps=["python>=3.10"]`
- AND `Initializer.Run()` logs at WARN level: `graphify not available; INIT will run in degraded mode`
- AND the init command exits with code 0 (degraded is not fatal)

#### Scenario: detection-missing-graphify

- GIVEN Python >= 3.10 is present but `graphifyy[mcp]==0.8.35` is not installed
- WHEN `sophia init` runs
- THEN `GraphifyProber.Probe` returns `available=false`, `missing_deps=["graphifyy[mcp]==0.8.35"]`
- AND `Initializer.Run()` logs at WARN level indicating degraded mode

---

### Requirement: Opt-in Auto-Bootstrap Flag

The CLI MUST expose a `--auto-bootstrap-graphify` flag. When supplied, `GraphifyProber` MUST attempt `uv tool install "graphifyy[mcp]==0.8.35"`. The flag MUST default to OFF; auto-installation MUST NOT occur without explicit opt-in.

#### Scenario: auto-bootstrap-success

- GIVEN `--auto-bootstrap-graphify` is passed and `uv` is available
- WHEN `sophia init --auto-bootstrap-graphify` runs
- THEN the prober installs `graphifyy[mcp]==0.8.35` via `uv tool install`
- AND a subsequent `Probe` call returns `available=true`
- AND `Initializer.Run()` logs installation success at INFO level

#### Scenario: auto-bootstrap-failure

- GIVEN `--auto-bootstrap-graphify` is passed but `uv` is not available
- WHEN `sophia init --auto-bootstrap-graphify` runs
- THEN the prober returns an error describing the missing `uv` dependency
- AND `Initializer.Run()` logs at WARN level and falls back to degraded mode
- AND the init command exits with code 0

---

### Requirement: Degraded-Mode Warning

When `GraphifyProber.Probe` returns `available=false`, `Initializer.Run()` MUST log a structured warning that includes the list of `missing_deps` and MUST NOT fail or abort the init flow. The `graph_available=false` state MUST be observable by downstream consumers (e.g., via the written `.sophia.yaml` or a returned result struct).

#### Scenario: degraded-mode-observable

- GIVEN graphify is not installed
- WHEN `sophia init` completes
- THEN `.sophia.yaml` contains `graph_available: false`
- AND the log output contains `missing_deps` with the list of absent tools
