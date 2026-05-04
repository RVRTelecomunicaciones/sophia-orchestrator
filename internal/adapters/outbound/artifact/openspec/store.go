// Package openspec implements outbound.ArtifactStore by writing artifacts
// to the local filesystem under {Root}/openspec/changes/{change_name}/...
// The on-disk structure mirrors the OpenSpec convention so that committed
// artifacts can be reviewed in the source repo by humans + version control.
package openspec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
)

// ErrInvalidTopicKey is returned when a topic_key contains traversal
// patterns (e.g., "..", absolute paths) that would escape the configured root.
var ErrInvalidTopicKey = errors.New("openspec: invalid topic_key")

// Config tunes Store.
type Config struct {
	// Root is the absolute filesystem path under which {Root}/openspec/...
	// directories are created. Required.
	Root string
	// FileMode is the chmod for written artifact files. Default 0o644.
	FileMode os.FileMode
	// DirMode is the chmod for created directories. Default 0o755.
	DirMode os.FileMode
}

// DefaultConfig returns production defaults.
func DefaultConfig(root string) Config {
	return Config{Root: root, FileMode: 0o644, DirMode: 0o755}
}

// Store implements outbound.ArtifactStore against the local filesystem.
type Store struct {
	cfg Config
}

// New constructs a Store. Root must be non-empty and resolvable (the
// directory is created lazily on Save).
func New(cfg Config) (*Store, error) {
	if cfg.Root == "" {
		return nil, errors.New("openspec: empty Root")
	}
	if cfg.FileMode == 0 {
		cfg.FileMode = 0o644
	}
	if cfg.DirMode == 0 {
		cfg.DirMode = 0o755
	}
	abs, err := filepath.Abs(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("openspec: resolve Root: %w", err)
	}
	cfg.Root = abs
	return &Store{cfg: cfg}, nil
}

// Mode reports change.ArtifactStoreOpenspec.
func (s *Store) Mode() change.ArtifactStoreMode { return change.ArtifactStoreOpenspec }

// Save writes the artifact to {Root}/openspec/changes/{change_name}/{phase}.md
// (or .json depending on Type/ContentType). The OpenspecPath input field
// overrides the computed relative path if non-empty.
func (s *Store) Save(_ context.Context, in outbound.SaveArtifactInput) error {
	rel, err := s.relPath(in)
	if err != nil {
		return err
	}
	full := filepath.Join(s.cfg.Root, rel)
	if !strings.HasPrefix(full, s.cfg.Root+string(filepath.Separator)) && full != s.cfg.Root {
		return fmt.Errorf("%w: would escape root: %q", ErrInvalidTopicKey, in.TopicKey)
	}
	if err := os.MkdirAll(filepath.Dir(full), s.cfg.DirMode); err != nil {
		return fmt.Errorf("openspec.Save: mkdir: %w", err)
	}
	if err := os.WriteFile(full, in.Content, s.cfg.FileMode); err != nil {
		return fmt.Errorf("openspec.Save: write: %w", err)
	}
	return nil
}

// Load reads {Root}/openspec/changes/{change_name}/{phase}.md and returns
// the artifact. Returns outbound.ErrNotFound if the file does not exist.
func (s *Store) Load(_ context.Context, topicKey string) (*outbound.Artifact, error) {
	rel := relPathFromTopicKey(topicKey)
	if rel == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTopicKey, topicKey)
	}
	full := filepath.Join(s.cfg.Root, rel)
	if !strings.HasPrefix(full, s.cfg.Root+string(filepath.Separator)) && full != s.cfg.Root {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTopicKey, topicKey)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, outbound.ErrNotFound
		}
		return nil, fmt.Errorf("openspec.Load: read: %w", err)
	}
	contentType := "text/markdown"
	if strings.HasSuffix(rel, ".json") {
		contentType = "application/json"
	}
	return &outbound.Artifact{
		TopicKey: topicKey,
		Content:  data,
		ContentType: contentType,
	}, nil
}

func (s *Store) relPath(in outbound.SaveArtifactInput) (string, error) {
	if in.OpenspecPath != "" {
		return validateRelPath(in.OpenspecPath)
	}
	rel := relPathFromTopicKey(in.TopicKey)
	if rel == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidTopicKey, in.TopicKey)
	}
	return rel, nil
}

// relPathFromTopicKey converts "sdd/{change}/{phase}" into
// "openspec/changes/{change}/{phase}.md". Paths with ".." or absolute
// segments are rejected.
func relPathFromTopicKey(topicKey string) string {
	if topicKey == "" {
		return ""
	}
	parts := strings.Split(topicKey, "/")
	if len(parts) < 3 || parts[0] != "sdd" {
		return ""
	}
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, "\\:") {
			return ""
		}
	}
	change := parts[1]
	tail := strings.Join(parts[2:], "-")
	return filepath.Join("openspec", "changes", change, tail+".md")
}

func validateRelPath(rel string) (string, error) {
	clean := filepath.Clean(rel)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("%w: %q", ErrInvalidTopicKey, rel)
	}
	return clean, nil
}

// Compile-time interface check.
var _ outbound.ArtifactStore = (*Store)(nil)
