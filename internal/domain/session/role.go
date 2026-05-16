// Package session models AgentSession — one invocation of an AI CLI
// subprocess (OpenCode V1; Claude Code/Cursor/Gemini in V2). The aggregate
// is unified across phase-agent sessions and apply-phase team-lead/implement
// sessions, discriminated by AgentRole.
package session

// AgentRole is the closed enum of agent roles dispatched by the orchestrator.
type AgentRole string

// SDD phase agent roles + apply-phase coordination roles.
const (
	RoleSDDInit     AgentRole = "sdd-init"
	RoleSDDExplore  AgentRole = "sdd-explore"
	RoleSDDProposal AgentRole = "sdd-proposal"
	RoleSDDSpec     AgentRole = "sdd-spec"
	RoleSDDDesign   AgentRole = "sdd-design"
	RoleSDDTasks    AgentRole = "sdd-tasks"
	RoleSDDVerify   AgentRole = "sdd-verify"
	RoleSDDArchive  AgentRole = "sdd-archive"
	RoleTeamLead    AgentRole = "team-lead"
	RoleImplement   AgentRole = "implement"
)

// IsValid reports whether r is a known role.
func (r AgentRole) IsValid() bool {
	switch r {
	case RoleSDDInit, RoleSDDExplore, RoleSDDProposal, RoleSDDSpec,
		RoleSDDDesign, RoleSDDTasks, RoleSDDVerify, RoleSDDArchive,
		RoleTeamLead, RoleImplement:
		return true
	}
	return false
}

// Provider is the dispatcher target. The aggregate carries this so audit
// logs and per-call provenance can identify WHICH adapter executed the
// session. V1 shipped only OpenCode; V2 wired Ollama + Aider (PRs #19
// and #20, merged 2026-05-16); ClaudeCode / Cursor / Gemini are
// forward stubs reserved for adapters that haven't shipped yet.
type Provider string

// Dispatcher providers. The string value is the on-the-wire name used
// by SOPHIA_DISPATCHER_PROVIDER[_<PHASE>] env vars and by the V2
// factory's Register / Get keys — keep them aligned with the values
// declared in internal/adapters/outbound/dispatcher/*/dispatcher.go.
const (
	ProviderOpenCode   Provider = "opencode"
	ProviderOllama     Provider = "ollama"     // shipped 2026-05-16 (PR #19)
	ProviderAider      Provider = "aider"      // shipped 2026-05-16 (PR #20)
	ProviderClaudeCode Provider = "claude-code"
	ProviderCursor     Provider = "cursor"
	ProviderGemini     Provider = "gemini"
)

// IsValidV1 reports whether p is a provider supported in V1.
//
// Deprecated: V1 is closed. New call sites SHOULD use IsValid() which
// accepts every shipped adapter (opencode + ollama + aider) plus the
// forward-declared stubs. Kept for backward-compat with persisted
// session rows from V1 deployments.
func (p Provider) IsValidV1() bool {
	return p == ProviderOpenCode
}

// IsValid reports whether p is a known provider in any version. Used
// by session.New as of V2.1 (2026-05-16) so the aggregate accepts the
// real V2 adapters that were already shipping at the factory layer
// but were being rejected by the V1-only validation gate.
func (p Provider) IsValid() bool {
	switch p {
	case ProviderOpenCode, ProviderOllama, ProviderAider,
		ProviderClaudeCode, ProviderCursor, ProviderGemini:
		return true
	}
	return false
}
