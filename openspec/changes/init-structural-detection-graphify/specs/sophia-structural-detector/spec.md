# Delta: sophia-structural-detector

## Capability

A new pure-Go package at `sophia-orchestator/internal/application/init/detector/` that reads repository manifest files from the filesystem to detect languages, framework identities and versions, package managers, and architectural style, then returns a `StructuralContext` value with `schema_version=1`. No subprocess execution, no network calls. The detector MUST tolerate missing manifests by returning a partial — not empty — result.

---

## ADDED Requirements

### Requirement: Manifest Parsing

The detector MUST parse the following manifest files when present: `go.mod`, `package.json`, `tsconfig.json`, `pyproject.toml`, `requirements.txt`, `setup.py`, `Cargo.toml`, `build.gradle`, `build.gradle.kts`, `pom.xml`. Missing manifests MUST be silently skipped; their absence MUST NOT produce an error.

#### Scenario: Go project detection

- GIVEN a repository with `go.mod` declaring `module example.com/app` and `go 1.22`
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.Languages` contains `{name: "Go", version_evidence: "1.22"}`
- AND no error is returned

#### Scenario: partial detection with some manifests missing

- GIVEN a repository with `go.mod` present but no `package.json` or `pyproject.toml`
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.Languages` contains `Go` only
- AND `StructuralContext.Frameworks` is empty or contains only Go-inferred frameworks
- AND no error is returned

---

### Requirement: Framework Fingerprinting

The detector MUST fingerprint the following frameworks at minimum: Angular (with signals vs NgRx distinction), React, Next.js, Vue, Spring Boot, Django, FastAPI. Fingerprinting MUST identify the major version when inferable from the manifest. Additional frameworks are optional.

The Angular distinction MUST be based on: presence of `@ngrx/store` dependency indicates NgRx style; absence of `@ngrx/store` combined with Angular >= 17 and presence of `app.config.ts` indicates signals style.

#### Scenario: Angular 17 with signals detection

- GIVEN `package.json` contains `@angular/core` at version `^17.x` and no `@ngrx/store` entry, and `app.config.ts` is present in the project
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.Frameworks` contains `{name: "Angular", version: "17", variant: "signals"}`

#### Scenario: Angular 14 with NgRx detection

- GIVEN `package.json` contains `@angular/core` at version `^14.x` and `@ngrx/store` is present
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.Frameworks` contains `{name: "Angular", version: "14", variant: "ngrx"}`

---

### Requirement: Architectural Style Heuristics

The detector MUST apply filesystem heuristics to identify the predominant architectural style. At minimum it MUST detect: hexagonal (presence of `domain/`, `application/`, `infrastructure/` directories), microservices (presence of multiple `cmd/*/` or `services/*/` top-level entries), monorepo (presence of a workspace manifest such as `pnpm-workspace.yaml`, `nx.json`, or `go.work`), and monolith (single `main.go` or `main.py` at root, no workspace markers).

#### Scenario: hexagonal detection

- GIVEN a repository whose top-level contains `internal/domain/`, `internal/application/`, and `internal/infrastructure/` directories
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.ArchStyle` contains `"hexagonal"`

#### Scenario: monorepo detection

- GIVEN a repository with a `pnpm-workspace.yaml` or `go.work` file at the root
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN `StructuralContext.ArchStyle` contains `"monorepo"`

---

### Requirement: StructuralContext Schema Version

`StructuralContext` MUST include a `schema_version int` field set to `1` in all values produced by this detector. Consumers MUST be able to branch on `schema_version` before deserializing to support future migrations (explore §13 risk 6).

#### Scenario: schema_version always present

- GIVEN any valid repository
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN the returned `StructuralContext.SchemaVersion` equals `1`

---

### Requirement: Pure-Go Filesystem-Only Implementation

The detector MUST NOT invoke `exec.Command` or make any network calls. All detection MUST be performed via filesystem reads only. This ensures the detector is safe to run in any environment without external tooling (degraded-first).

#### Scenario: no subprocess spawned during detection

- GIVEN a repository root is provided
- WHEN `SophiaDetector.Detect(repoRoot)` is called
- THEN no child process is spawned (assertable in unit tests via the absence of any `exec.Command` calls in the detector package)
