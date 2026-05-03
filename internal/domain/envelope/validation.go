package envelope

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Sentinel errors raised by Parse / Validate. Wrapped with %w by callers.
var (
	ErrUnsupportedSchemaVersion = errors.New("envelope: unsupported schema_version")
	ErrInvalidStatus            = errors.New("envelope: invalid status")
	ErrConfidenceOutOfRange     = errors.New("envelope: confidence must be in [0,1]")
	ErrEmptyPhase               = errors.New("envelope: empty phase")
	ErrEmptyChangeName          = errors.New("envelope: empty change_name")
	ErrEmptyProject             = errors.New("envelope: empty project")
	ErrInvalidJSON              = errors.New("envelope: invalid json")
)

// Parse decodes raw JSON into an Envelope and validates schema_version,
// status enum, confidence range, and required fields. Returns the validated
// envelope or a wrapped sentinel error.
func Parse(raw []byte) (*Envelope, error) {
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidJSON, err.Error())
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}

// Validate runs the structural checks on an already-decoded Envelope.
func (e *Envelope) Validate() error {
	if e.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("%w: got %q", ErrUnsupportedSchemaVersion, e.SchemaVersion)
	}
	if e.Phase == "" {
		return ErrEmptyPhase
	}
	if !e.Status.IsValid() {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, e.Status)
	}
	if e.Confidence < 0 || e.Confidence > 1 {
		return fmt.Errorf("%w: %v", ErrConfidenceOutOfRange, e.Confidence)
	}
	if e.ChangeName == "" {
		return ErrEmptyChangeName
	}
	if e.Project == "" {
		return ErrEmptyProject
	}
	return nil
}
