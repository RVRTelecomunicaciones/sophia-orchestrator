package session

import "errors"

// Sentinel errors raised by the AgentSession aggregate.
var (
	ErrInvalidRole     = errors.New("session: invalid role")
	ErrInvalidProvider = errors.New("session: invalid provider")
	ErrEmptyPromptHash = errors.New("session: empty prompt hash")
	ErrEmptyCommand    = errors.New("session: empty command")
	ErrTerminal        = errors.New("session: already terminal")
	ErrNotRunning      = errors.New("session: not running")
)
