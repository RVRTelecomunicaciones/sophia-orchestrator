package change

import "errors"

// Sentinel errors raised by the Change aggregate.
var (
	ErrEmptyName            = errors.New("change: empty name")
	ErrEmptyProject         = errors.New("change: empty project")
	ErrInvalidArtifactStore = errors.New("change: invalid artifact_store")
	ErrInvalidTransition    = errors.New("change: invalid phase transition")
	ErrAlreadyTerminal      = errors.New("change: already terminal")
)
