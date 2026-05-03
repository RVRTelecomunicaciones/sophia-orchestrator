# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial V1 design spec (`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`).
- V1 implementation plan with 13 milestones / ~90 tasks (`docs/superpowers/plans/2026-05-03-sophia-orchestator-v1.md`).
- Project scaffolding: `go.mod` pinned to **Go 1.26.2** via toolchain directive, Makefile, `.golangci.yaml`, directory layout.
- Documentation: `CLAUDE.md`, `AGENTS.md`, `README.md`, `docs/architecture.md`, `docs/rules.md`, `docs/domain-invariants.md`, `docs/ai-orientation.md`.
- ADRs: `_template`, **0001** (project init), **0002** (dispatcher abstraction), **0003** (sophia-memory-engine integration contract), **0004** (PostgreSQL 16+ minimum, recommended PG 17, PG 18 feature-flagged).
- Domain layer (Milestone 2): typed ULID identifiers, injectable Clock/IDGenerator, PhaseType + PhaseStatus enums, Envelope value object with validation, IronLaw catalog, Change aggregate with phase transitions, Phase aggregate with retry budget and threshold gating, Apply aggregates (Board/Group/Task) with DAG validation and Iron Law #5, AgentSession aggregate, Worktree value object lifecycle.

### Changed
- Renamed `ArtifactStoreEngram` → `ArtifactStoreMemoryEngine` (string value `memory-engine`). Engram is a session-level personal memory tool, NOT part of the Sophia ecosystem. The orchestrator persists artifacts via `sophia-memory-engine` HTTP API. See ADR-0003.
- DB minimum version: PG 15 → **PG 16+** (recommended PG 17). PG 15 EOL is Nov 2027; PG 16 EOL is Nov 2028. PG 17 brings `MERGE … RETURNING` and major vacuum/WAL improvements; PG 18 adds async I/O and UUIDv7 (feature-flagged for future). See ADR-0004.
