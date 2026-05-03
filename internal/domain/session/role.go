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

// Provider is the dispatcher target. V1 ships only OpenCode; V2 adds the rest.
type Provider string

// Dispatcher providers.
const (
	ProviderOpenCode   Provider = "opencode"
	ProviderClaudeCode Provider = "claude-code"
	ProviderCursor     Provider = "cursor"
	ProviderGemini     Provider = "gemini"
)

// IsValidV1 reports whether p is a provider supported in V1.
func (p Provider) IsValidV1() bool {
	return p == ProviderOpenCode
}

// IsValid reports whether p is a known provider in any version.
func (p Provider) IsValid() bool {
	switch p {
	case ProviderOpenCode, ProviderClaudeCode, ProviderCursor, ProviderGemini:
		return true
	}
	return false
}
