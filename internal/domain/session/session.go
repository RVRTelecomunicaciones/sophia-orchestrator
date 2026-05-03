package session

import (
	"fmt"
	"time"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/envelope"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/ids"
)

// Session is one AI CLI invocation: prompt + worktree + captured envelope.
// Unified for phase-agent and apply-phase coordination roles, discriminated
// by AgentRole.
type Session struct {
	id           ids.SessionID
	changeID     ids.ChangeID
	phaseID      ids.PhaseID
	role         AgentRole
	provider     Provider
	worktreeID   *ids.WorktreeID
	promptSHA256 string
	command      string
	status       Status
	exitCode     *int
	envelope     *envelope.Envelope
	startedAt    time.Time
	endedAt      *time.Time
}

// New constructs a Session in StatusPending.
func New(
	id ids.SessionID,
	changeID ids.ChangeID,
	phaseID ids.PhaseID,
	role AgentRole,
	provider Provider,
	promptSHA256 string,
	command string,
	startedAt time.Time,
) (*Session, error) {
	if !role.IsValid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidRole, role)
	}
	if !provider.IsValidV1() {
		return nil, fmt.Errorf("%w: %q (V1 supports opencode only)", ErrInvalidProvider, provider)
	}
	if promptSHA256 == "" {
		return nil, ErrEmptyPromptHash
	}
	if command == "" {
		return nil, ErrEmptyCommand
	}
	return &Session{
		id: id, changeID: changeID, phaseID: phaseID,
		role: role, provider: provider,
		promptSHA256: promptSHA256, command: command,
		status:    StatusPending,
		startedAt: startedAt,
	}, nil
}

// Hydrate reconstructs a Session from persisted fields.
func Hydrate(
	id ids.SessionID,
	changeID ids.ChangeID,
	phaseID ids.PhaseID,
	role AgentRole,
	provider Provider,
	worktreeID *ids.WorktreeID,
	promptSHA256, command string,
	status Status,
	exitCode *int,
	env *envelope.Envelope,
	startedAt time.Time,
	endedAt *time.Time,
) *Session {
	return &Session{
		id: id, changeID: changeID, phaseID: phaseID,
		role: role, provider: provider, worktreeID: worktreeID,
		promptSHA256: promptSHA256, command: command,
		status: status, exitCode: exitCode, envelope: env,
		startedAt: startedAt, endedAt: endedAt,
	}
}

// ID returns the session identifier.
func (s *Session) ID() ids.SessionID { return s.id }

// ChangeID returns the parent Change identifier.
func (s *Session) ChangeID() ids.ChangeID { return s.changeID }

// PhaseID returns the parent Phase identifier.
func (s *Session) PhaseID() ids.PhaseID { return s.phaseID }

// Role returns the agent role.
func (s *Session) Role() AgentRole { return s.role }

// Provider returns the dispatcher provider.
func (s *Session) Provider() Provider { return s.provider }

// WorktreeID returns the worktree id if assigned, nil otherwise.
func (s *Session) WorktreeID() *ids.WorktreeID { return s.worktreeID }

// PromptSHA256 returns the SHA256 hash of the prompt sent to the agent.
func (s *Session) PromptSHA256() string { return s.promptSHA256 }

// Command returns the dispatched shell command.
func (s *Session) Command() string { return s.command }

// Status returns the current lifecycle status.
func (s *Session) Status() Status { return s.status }

// ExitCode returns the process exit code if known.
func (s *Session) ExitCode() *int { return s.exitCode }

// Envelope returns the captured envelope (or nil).
func (s *Session) Envelope() *envelope.Envelope { return s.envelope }

// StartedAt returns the session creation timestamp.
func (s *Session) StartedAt() time.Time { return s.startedAt }

// EndedAt returns the session termination timestamp (nil if still running).
func (s *Session) EndedAt() *time.Time { return s.endedAt }

// AssignWorktree records the worktree id used by this session.
func (s *Session) AssignWorktree(wid ids.WorktreeID) { s.worktreeID = &wid }

// MarkRunning transitions Pending → Running.
func (s *Session) MarkRunning() error {
	if s.status != StatusPending {
		return fmt.Errorf("%w: state %q", ErrTerminal, s.status)
	}
	s.status = StatusRunning
	return nil
}

// RecordOutcome captures the dispatcher result and transitions to a terminal
// status. exitCode == 0 → Done; non-zero → Failed.
func (s *Session) RecordOutcome(env *envelope.Envelope, exit int, endedAt time.Time) error {
	if s.status != StatusRunning {
		return ErrNotRunning
	}
	s.envelope = env
	s.exitCode = &exit
	s.endedAt = &endedAt
	if exit == 0 {
		s.status = StatusDone
	} else {
		s.status = StatusFailed
	}
	return nil
}

// MarkTimeout transitions a non-terminal session to StatusTimeout.
func (s *Session) MarkTimeout(endedAt time.Time) error {
	if s.status.IsTerminal() {
		return ErrTerminal
	}
	s.status = StatusTimeout
	s.endedAt = &endedAt
	return nil
}
