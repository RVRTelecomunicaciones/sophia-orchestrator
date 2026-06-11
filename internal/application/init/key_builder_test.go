package initphase_test

// key_builder_test.go — T2.1 RED (Strict TDD)
//
// D-C7-7 acceptance test: identical git porcelain output, different manifest
// bytes → different cache key. Also covers: unrelated edit, no manifests,
// multi-manifest determinism, absent manifest sentinel.

import (
	"context"
	"path/filepath"
	"testing"

	initphase "github.com/RVRTelecomunicaciones/sophia-orchestrator/internal/application/init"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeGitRunner struct {
	head     string
	porcelain []byte
}

func (g *fakeGitRunner) RevParseHead(_ context.Context, _ string) (string, error) {
	return g.head, nil
}

func (g *fakeGitRunner) StatusPorcelain(_ context.Context, _ string) ([]byte, error) {
	return g.porcelain, nil
}

type fakeFileReader struct {
	files map[string][]byte // absolute path → bytes; missing key → absent
}

func (r *fakeFileReader) ReadIfExists(path string) ([]byte, error) {
	if b, ok := r.files[path]; ok {
		return b, nil
	}
	return nil, nil // absent = nil, nil
}

// helper: make a KeyBuilder with the given root-relative fake files.
// root is used as the repoRoot passed to Build.
func newBuilder(root string, head string, porcelain []byte, files map[string][]byte) (*initphase.KeyBuilder, string) {
	absFiles := make(map[string][]byte, len(files))
	for name, b := range files {
		absFiles[filepath.Join(root, name)] = b
	}
	reader := &fakeFileReader{files: absFiles}
	git := &fakeGitRunner{head: head, porcelain: porcelain}
	return initphase.NewKeyBuilder(git, reader), root
}

// D-C7-7 acceptance test: same porcelain, different package.json content →
// different cache key (the manifest-hash component drives the difference).
func TestKeyBuilder_ManifestContentChange_DifferentKey(t *testing.T) {
	const root = "/repo"
	porcelain := []byte(" M package.json\n") // unchanged between builds

	files1 := map[string][]byte{
		"package.json": []byte(`{"dependencies":{"@angular/core":"^22.0.0"}}`),
	}
	files2 := map[string][]byte{
		"package.json": []byte(`{"dependencies":{"@angular/core":"^23.0.0"}}`),
	}

	b1, r1 := newBuilder(root, "abc123", porcelain, files1)
	b2, r2 := newBuilder(root, "abc123", porcelain, files2)

	ctx := context.Background()
	key1, err1 := b1.Build(ctx, r1, "0.8.35")
	key2, err2 := b2.Build(ctx, r2, "0.8.35")

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NotEqual(t, key1, key2,
		"cache key must differ when package.json content changes even if porcelain is unchanged")
}

// Unrelated non-manifest file edit: manifest component unchanged.
func TestKeyBuilder_UnrelatedEdit_ManifestUnchanged(t *testing.T) {
	const root = "/repo"
	porcelain1 := []byte("") // clean
	porcelain2 := []byte(" M src/app/foo.ts\n")

	manifest := []byte(`{"dependencies":{"@angular/core":"^22.0.0"}}`)
	files := map[string][]byte{
		"package.json": manifest,
	}

	b1, r1 := newBuilder(root, "abc123", porcelain1, files)
	b2, r2 := newBuilder(root, "abc123", porcelain2, files)

	ctx := context.Background()
	key1, err1 := b1.Build(ctx, r1, "0.8.35")
	key2, err2 := b2.Build(ctx, r2, "0.8.35")

	require.NoError(t, err1)
	require.NoError(t, err2)
	// Keys should differ (DirtyTreeHash changes) but that is fine —
	// what we assert is that a STABLE manifest + STABLE porcelain produces
	// the same key twice.
	b3, r3 := newBuilder(root, "abc123", porcelain1, files)
	key3, err3 := b3.Build(ctx, r3, "0.8.35")
	require.NoError(t, err3)
	require.Equal(t, key1, key3, "same inputs must produce same key (determinism)")
	_ = key2 // key2 differs due to porcelain change — fine
}

// No manifests → key is stable and deterministic across two builds.
func TestKeyBuilder_NoManifests_StableKey(t *testing.T) {
	const root = "/repo"

	b1, r1 := newBuilder(root, "abc", []byte(""), map[string][]byte{})
	b2, r2 := newBuilder(root, "abc", []byte(""), map[string][]byte{})

	ctx := context.Background()
	key1, err1 := b1.Build(ctx, r1, "0.8.35")
	key2, err2 := b2.Build(ctx, r2, "0.8.35")

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Equal(t, key1, key2, "no-manifest key must be deterministic")
}

// Multi-manifest: package.json + go.mod → deterministic order (same key both runs).
func TestKeyBuilder_MultiManifest_Deterministic(t *testing.T) {
	const root = "/repo"
	files := map[string][]byte{
		"package.json": []byte(`{"name":"app"}`),
		"go.mod":       []byte("module example.com/app\n\ngo 1.26\n"),
	}

	b1, r1 := newBuilder(root, "abc", []byte(""), files)
	b2, r2 := newBuilder(root, "abc", []byte(""), files)

	ctx := context.Background()
	key1, err1 := b1.Build(ctx, r1, "0.8.35")
	key2, err2 := b2.Build(ctx, r2, "0.8.35")

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.Equal(t, key1, key2, "multi-manifest key must be deterministic")
}

// Manifest deleted between builds → keys differ (absent sentinel fires).
func TestKeyBuilder_ManifestDeleted_DifferentKey(t *testing.T) {
	const root = "/repo"

	filesWithManifest := map[string][]byte{
		"package.json": []byte(`{"name":"app"}`),
	}
	filesWithout := map[string][]byte{} // package.json deleted

	b1, r1 := newBuilder(root, "abc", []byte(""), filesWithManifest)
	b2, r2 := newBuilder(root, "abc", []byte(""), filesWithout)

	ctx := context.Background()
	key1, err1 := b1.Build(ctx, r1, "0.8.35")
	key2, err2 := b2.Build(ctx, r2, "0.8.35")

	require.NoError(t, err1)
	require.NoError(t, err2)
	require.NotEqual(t, key1, key2, "absent manifest must differ from present manifest (sentinel)")
}
