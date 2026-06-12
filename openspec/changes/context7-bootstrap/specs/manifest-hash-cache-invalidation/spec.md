# Delta: manifest-hash-cache-invalidation

## Capability

The INIT cache key gains a manifest-content-hash component: the SHA-256 hash of
the concatenated byte content of detected manifest files (`package.json`, `go.mod`,
`pyproject.toml`, `requirements.txt`). This ensures that an uncommitted manual
version bump (e.g., `package.json` v22 → v23 while the file is already marked
dirty by `git status`) immediately produces a different cache key, preventing a
stale INIT result from being served. The existing `DirtyTreeHash` component
(component 4, `key_builder.go:43-45`) covers dirty file paths and status codes
but NOT file content; the new component fills that gap for the operator's exact
scenario (D-C7-7).

This is an 8th key component added to the existing 7-component key
(`cache/key.go:30-66`). Folding into an existing component is acceptable only
if the spec author explicitly chooses to do so AND the resulting key change is
tested. The default implementation adds a discrete 8th component named
`ManifestContentHash`. All existing cache entries are invalidated on deploy
(one-time accepted operational cost — see also `SophiaDetectorVer` bump in
`greenfield-detection` spec, which causes the same invalidation independently).

## MODIFIED Requirements

### Requirement: Manifest Content Hash Component

The cache key builder (`init/key_builder.go`) MUST compute a `ManifestContentHash`
by:

1. Collecting the absolute paths of all manifest files found by the detector
   (any of: `package.json`, `go.mod`, `pyproject.toml`, `requirements.txt`)
   within the repo root.
2. Sorting the paths lexicographically (deterministic order).
3. For each path, reading the file bytes.
4. Feeding all bytes (in sorted-path order) into a single SHA-256 digest.
5. Encoding the digest as a lowercase hex string.

If no manifest files are found (empty repo or pure language project with no
recognized manifest), the hash MUST be the SHA-256 of an empty byte sequence
(`"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"`).

The hash MUST be computed from file content, NOT from file paths, status codes,
or modification times.

#### Scenario: Uncommitted version bump changes cache key

- GIVEN a repo with `package.json` already marked dirty (`M package.json` in
  `git status --porcelain`)
- AND the user edits `package.json` in-place to change `@angular/core` from
  `^22.0.0` to `^23.0.0` (file is still dirty; porcelain output is unchanged)
- WHEN the cache key is built before and after the edit
- THEN the `ManifestContentHash` component differs between the two builds
- AND the full cache key differs
- AND a cache lookup after the edit returns a MISS

#### Scenario: Committed version bump also changes cache key

- GIVEN a repo where `package.json` is committed and clean
- AND the user commits a change bumping `@angular/core` from `^22.0.0` to `^23.0.0`
- WHEN the cache key is built before and after the commit
- THEN both `GitHead` (component 1) and `ManifestContentHash` (component 8) differ
- AND the full cache key differs
- AND a cache lookup after the commit returns a MISS

#### Scenario: Unrelated file change does not change manifest hash

- GIVEN a repo with a stable `package.json`
- WHEN a non-manifest source file (e.g., `src/app/foo.ts`) is edited
- THEN the `ManifestContentHash` is identical before and after the edit
- AND the cache key difference, if any, comes from `DirtyTreeHash` (not manifest hash)

#### Scenario: No manifests → stable empty hash

- GIVEN a repo root that contains no recognized manifest files
- WHEN the cache key is built twice with no changes
- THEN the `ManifestContentHash` is `"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"` both times
- AND the component is deterministic and stable

#### Scenario: Multiple manifests hashed in deterministic order

- GIVEN a monorepo root with both `package.json` and `go.mod`
- WHEN the cache key is built
- THEN the `ManifestContentHash` is computed from the concatenated content of
  both files in lexicographic path order
- AND building the key twice on the same unchanged repo produces the same hash

### Requirement: Cache Key Structure After Change

The cache key structure as defined in `cache/key.go` MUST include `ManifestContentHash`
as an explicit named component. The component MUST appear after the existing
7 components. The string representation of the key MUST include the manifest
hash field so that the combined key can be logged and debugged.

#### Scenario: Key components are all present

- GIVEN a repo with a `package.json`
- WHEN the cache key is built
- THEN the serialized key contains all 8 components including `ManifestContentHash`
- AND the component is non-empty

### Requirement: Accepted One-Time Global Cache Miss

The combined effect of the `SophiaDetectorVer` bump (component 7 changing from
`v1.0.0` to `v1.1.0`) and the new 8th component (absent in old keys) means ALL
existing INIT cache entries are invalidated on deploy. This is explicitly accepted
behavior. No migration or warm-up is required. Operations MUST be informed of
this one-time full cache miss.

This requirement has no runtime test counterpart — it is a documented acceptance
of operational impact.
