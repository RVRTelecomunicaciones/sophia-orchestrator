// Package cache provides the deterministic cache key and file-based
// StructuralContext cache for the INIT phase.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// CacheKey holds the 8 components used to compute the INIT phase cache key.
// All 8 components are included in the hash to ensure cache invalidation when
// any component changes.
//
// Components (in hash order):
//  1. GraphifyVersion    — invalidates when graphify binary is upgraded
//  2. RepoRoot           — per-repo isolation
//  3. GitHead            — invalidates on commit
//  4. DirtyTreeHash      — invalidates on uncommitted changes
//  5. IncludeGlobs       — sorted before hashing to normalise order
//  6. ConfigHash         — invalidates on .sophia.yaml changes
//  7. SophiaDetectorVer  — invalidates when detector parsing logic changes
//     (see detector.SophiaDetectorVer constant — bump when parser logic changes)
//  8. ManifestHash       — sha256 of manifest file CONTENT (DG-C7-2); ensures
//     an uncommitted version bump (e.g. package.json v22→v23) changes the key
//     even when the dirty-tree hash is unchanged (same porcelain line).
//
// include_globs default = ["**/*"] when .sophia.yaml is absent; the default
// is normalised here so the hash is stable regardless of how the default was
// produced.
//nolint:revive // CacheKey is intentionally named for clarity at call sites (cache.CacheKey)
type CacheKey struct {
	GraphifyVersion string
	RepoRoot        string
	GitHead         string
	DirtyTreeHash   string
	// IncludeGlobs are sorted before hashing to normalise order variations.
	// Default ["**/*"] when .sophia.yaml absent (documented above).
	IncludeGlobs      []string
	ConfigHash        string
	SophiaDetectorVer string
	// ManifestHash is the 8th component: a 16-hex-char truncated sha256 over
	// the content of the detected manifest set (DG-C7-2). Absent files fold
	// a sentinel so file creation/deletion also changes the key.
	ManifestHash string
}

// Hash computes a sha256 hex digest over all 8 components. Components are
// joined with a null byte separator to prevent ambiguity between adjacent
// fields (e.g. "ab" + "cd" vs "a" + "bcd" → same bytes without separator).
// IncludeGlobs are sorted before joining so order does not affect the result.
func (k CacheKey) Hash() string {
	globs := make([]string, len(k.IncludeGlobs))
	copy(globs, k.IncludeGlobs)
	sort.Strings(globs)
	globsJoined := strings.Join(globs, ",")

	// Null byte as field separator (prevents component boundary ambiguity).
	sep := string([]byte{0})
	components := strings.Join([]string{
		k.GraphifyVersion,
		k.RepoRoot,
		k.GitHead,
		k.DirtyTreeHash,
		globsJoined,
		k.ConfigHash,
		k.SophiaDetectorVer,
		k.ManifestHash,
	}, sep)

	sum := sha256.Sum256([]byte(components))
	return hex.EncodeToString(sum[:])
}

// String returns a loggable representation of all 8 cache key components.
// The format is not stable across releases; use Hash() for cache lookups.
func (k CacheKey) String() string {
	return fmt.Sprintf(
		"graphify=%s root=%s head=%s dirty=%s globs=%v config=%s detector=%s manifest=%s",
		k.GraphifyVersion,
		k.RepoRoot,
		k.GitHead,
		k.DirtyTreeHash,
		k.IncludeGlobs,
		k.ConfigHash,
		k.SophiaDetectorVer,
		k.ManifestHash,
	)
}
