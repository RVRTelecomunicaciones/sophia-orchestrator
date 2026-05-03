# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Initial V1 design spec (`docs/superpowers/specs/2026-05-03-sophia-orchestator-design.md`).
- V1 implementation plan with 13 milestones / ~90 tasks (`docs/superpowers/plans/2026-05-03-sophia-orchestator-v1.md`).
- Project scaffolding: `go.mod` (Go 1.26), Makefile, `.golangci.yaml`, directory layout.
- Documentation: `CLAUDE.md`, `AGENTS.md`, `README.md`, `docs/architecture.md`, `docs/rules.md`, `docs/domain-invariants.md`, `docs/ai-orientation.md`.
- ADR template (`docs/adr/_template.md`) and ADR-0001 (project init).
- Domain layer (Milestone 2): typed ULID identifiers, injectable Clock/IDGenerator, PhaseType + PhaseStatus enums, Envelope value object with validation, IronLaw catalog, Change aggregate with phase transitions, Phase aggregate with retry budget and threshold gating, Apply aggregates (Board/Group/Task) with DAG validation and Iron Law #5, AgentSession aggregate, Worktree value object lifecycle.
