// Package ids provides typed ULID identifiers for sophia-orchestator domain
// aggregates. Use the Parse* constructors at every boundary; the typed wrappers
// prevent cross-aggregate ID mixups at compile time.
package ids

import (
	"errors"
	"fmt"

	"github.com/oklog/ulid/v2"
)

// ErrInvalidID is returned when an input string is not a valid ULID.
var ErrInvalidID = errors.New("invalid id")

type (
	// ChangeID identifies a SDD Change aggregate root.
	ChangeID struct{ raw string }
	// PhaseID identifies a Phase within a Change.
	PhaseID struct{ raw string }
	// BoardID identifies an Apply Board.
	BoardID struct{ raw string }
	// GroupID identifies a Group within an Apply Board.
	GroupID struct{ raw string }
	// TaskID identifies a Task within a Group.
	TaskID struct{ raw string }
	// SessionID identifies an AgentSession.
	SessionID struct{ raw string }
	// WorktreeID identifies a Worktree.
	WorktreeID struct{ raw string }
	// SkillID identifies a Skill aggregate root.
	SkillID struct{ raw string }
)

func parseULID(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidID)
	}
	if _, err := ulid.Parse(raw); err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidID, err.Error())
	}
	return raw, nil
}

// ParseChangeID validates and constructs a ChangeID.
func ParseChangeID(raw string) (ChangeID, error) {
	r, err := parseULID(raw)
	return ChangeID{r}, err
}

// ParsePhaseID validates and constructs a PhaseID.
func ParsePhaseID(raw string) (PhaseID, error) {
	r, err := parseULID(raw)
	return PhaseID{r}, err
}

// ParseBoardID validates and constructs a BoardID.
func ParseBoardID(raw string) (BoardID, error) {
	r, err := parseULID(raw)
	return BoardID{r}, err
}

// ParseGroupID validates and constructs a GroupID.
func ParseGroupID(raw string) (GroupID, error) {
	r, err := parseULID(raw)
	return GroupID{r}, err
}

// ParseTaskID validates and constructs a TaskID.
func ParseTaskID(raw string) (TaskID, error) {
	r, err := parseULID(raw)
	return TaskID{r}, err
}

// ParseSessionID validates and constructs a SessionID.
func ParseSessionID(raw string) (SessionID, error) {
	r, err := parseULID(raw)
	return SessionID{r}, err
}

// ParseWorktreeID validates and constructs a WorktreeID.
func ParseWorktreeID(raw string) (WorktreeID, error) {
	r, err := parseULID(raw)
	return WorktreeID{r}, err
}

// ParseSkillID validates and constructs a SkillID.
func ParseSkillID(raw string) (SkillID, error) {
	r, err := parseULID(raw)
	return SkillID{r}, err
}

// String implementations for each ID type.
func (i ChangeID) String() string   { return i.raw }
func (i PhaseID) String() string    { return i.raw }
func (i BoardID) String() string    { return i.raw }
func (i GroupID) String() string    { return i.raw }
func (i TaskID) String() string     { return i.raw }
func (i SessionID) String() string  { return i.raw }
func (i WorktreeID) String() string { return i.raw }
func (i SkillID) String() string    { return i.raw }

// IsZero reports whether the ID is the zero value.
func (i ChangeID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i PhaseID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i BoardID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i GroupID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i TaskID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i SessionID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i WorktreeID) IsZero() bool { return i.raw == "" }

// IsZero reports whether the ID is the zero value.
func (i SkillID) IsZero() bool { return i.raw == "" }
