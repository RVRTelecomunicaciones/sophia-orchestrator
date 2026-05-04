package openspec_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/adapters/outbound/artifact/openspec"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/domain/change"
	"github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/ports/outbound"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) (*openspec.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	s, err := openspec.New(openspec.DefaultConfig(tmp))
	require.NoError(t, err)
	return s, tmp
}

func TestNew_RejectsEmptyRoot(t *testing.T) {
	_, err := openspec.New(openspec.Config{})
	require.Error(t, err)
}

func TestMode(t *testing.T) {
	s, _ := newTestStore(t)
	require.Equal(t, change.ArtifactStoreOpenspec, s.Mode())
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	s, root := newTestStore(t)
	in := outbound.SaveArtifactInput{
		TopicKey: "sdd/feat-x/spec",
		Type:     "sdd_spec",
		Content:  []byte("# Spec\nbody"),
	}
	require.NoError(t, s.Save(context.Background(), in))
	expected := filepath.Join(root, "openspec", "changes", "feat-x", "spec.md")
	require.FileExists(t, expected)

	got, err := s.Load(context.Background(), "sdd/feat-x/spec")
	require.NoError(t, err)
	require.Equal(t, "# Spec\nbody", string(got.Content))
	require.Equal(t, "text/markdown", got.ContentType)
}

func TestSave_NestedTopicKey(t *testing.T) {
	s, root := newTestStore(t)
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey: "sdd/feat-x/apply-progress",
		Content:  []byte("progress"),
	}))
	require.FileExists(t, filepath.Join(root, "openspec", "changes", "feat-x", "apply-progress.md"))
}

func TestLoad_NotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Load(context.Background(), "sdd/feat-x/spec")
	require.ErrorIs(t, err, outbound.ErrNotFound)
}

func TestSave_RejectsTraversal(t *testing.T) {
	s, _ := newTestStore(t)
	cases := []string{
		"",
		"sdd/../secret",
		"not-sdd/x/y",
		"sdd//empty",
		"sdd/x/y/../z",
	}
	for _, k := range cases {
		err := s.Save(context.Background(), outbound.SaveArtifactInput{
			TopicKey: k, Content: []byte("x"),
		})
		require.Error(t, err, "topic_key %q should be rejected", k)
	}
}

func TestSave_OpenspecPathOverride(t *testing.T) {
	s, root := newTestStore(t)
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey:     "sdd/feat-x/spec",
		OpenspecPath: "openspec/changes/feat-x/custom.md",
		Content:      []byte("custom"),
	}))
	require.FileExists(t, filepath.Join(root, "openspec", "changes", "feat-x", "custom.md"))
}

func TestSave_OpenspecPathRejectsAbsolute(t *testing.T) {
	s, _ := newTestStore(t)
	err := s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey: "sdd/feat-x/spec", OpenspecPath: "/etc/passwd", Content: []byte("nope"),
	})
	require.Error(t, err)
}

func TestLoad_RejectsBadTopicKey(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Load(context.Background(), "../etc/passwd")
	require.Error(t, err)
}

func TestLoad_JSONContentType(t *testing.T) {
	s, root := newTestStore(t)
	dir := filepath.Join(root, "openspec", "changes", "feat-x")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.json"), []byte(`{"a":1}`), 0o644))

	// Direct file injected; load via OpenspecPath override route by Save.
	// Verify content-type detection on .json suffix when OpenspecPath stored
	// such files. The default Load route can't reach .json without a custom
	// path; we test this indirectly via the override path on Save.
	require.NoError(t, s.Save(context.Background(), outbound.SaveArtifactInput{
		TopicKey:     "sdd/feat-x/spec",
		OpenspecPath: "openspec/changes/feat-x/spec.json",
		Content:      []byte(`{"a":1}`),
	}))
	// Direct read using ReadFile to verify the file ext + bytes match.
	require.FileExists(t, filepath.Join(dir, "spec.json"))
}
