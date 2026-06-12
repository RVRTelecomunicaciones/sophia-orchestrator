package outbound

import (
	"context"
	"errors"
)

// ErrDocsUnavailable signals that the docs provider is not configured or
// usable (e.g. missing CONTEXT7_API_KEY, provider disabled, or transport
// unreachable). Callers MUST degrade gracefully with a WARN log and return
// without propagating the error to the caller phase.
var ErrDocsUnavailable = errors.New("docs: provider unavailable")

// ErrThinEntry signals that every candidate library entry returned by the
// provider is below the snippet threshold (MinSnippets). The bootstrap
// service skips the framework with a WARN when this is returned.
var ErrThinEntry = errors.New("docs: entry below snippet threshold")

// LibraryEntry is a single candidate result from ResolveLibrary. Each entry
// carries enough metadata for the caller to choose between a version-specific
// entry and the main (non-version-pinned) entry using the thin-entry fallback
// logic (DG-C7-10).
type LibraryEntry struct {
	// ID is the context7-compatible library identifier used by GetDocs
	// (e.g. "/websites/angular_dev", "/angular/angular@22.0.0").
	ID string

	// Snippets is the number of documentation snippets available for this
	// entry. Used to detect thin entries (< MinSnippets threshold).
	Snippets int

	// Score is the relevance score returned by the resolver. Higher is better.
	Score float64

	// IsMain is true when this entry represents the framework's main
	// (non-version-pinned) documentation entry. The bootstrap service prefers
	// a version-specific entry; IsMain entries are used as fallbacks when the
	// version-specific entry is thin.
	IsMain bool
}

// DocsResult is the raw documentation content returned by GetDocs. The Body
// field is treated strictly as DATA — it is sanitised and stored verbatim as
// skill content, NEVER passed to an LLM or executed as instructions (D-C7-5,
// D11, ContextCrush guard).
type DocsResult struct {
	// LibraryID is the context7 library ID actually used for this fetch.
	// May differ from the version-specific ID when the main entry was used
	// as a thin-entry fallback; the importer records this in Provenance.
	LibraryID string

	// Snippets is the snippet count for the fetched entry.
	Snippets int

	// Score is the relevance score for the fetched entry.
	Score float64

	// Body is the raw markdown documentation text. Treated as opaque DATA.
	// The SkillImporter sanitises it before storing as skill content.
	Body string
}

// DocsProvider is the orchestrator's outbound port to an external documentation
// source. V1 implementation is the Context7 adapter in
// internal/adapters/outbound/docs/context7/ which reaches Context7 via the
// agent-mcp bridge using the same StreamableClientTransport the dispatcher uses
// (DG-C7-8).
//
// All implementations MUST be safe for concurrent use from multiple goroutines.
type DocsProvider interface {
	// ResolveLibrary returns candidate library entries ranked by relevance for
	// the given framework name and query. Callers should prefer version-specific
	// entries (IsMain==false) and fall back to main entries when the version
	// entry is thin.
	//
	// Returns ErrDocsUnavailable when the provider is not configured or the
	// underlying transport cannot be reached.
	ResolveLibrary(ctx context.Context, framework, query string) ([]LibraryEntry, error)

	// GetDocs fetches the documentation body for a resolved library ID. The
	// topic and tokens parameters are forwarded to the underlying tool call
	// (topic="" and tokens=8000 are the V1 defaults used by the bootstrap
	// service).
	//
	// Returns ErrDocsUnavailable when the provider is not configured.
	GetDocs(ctx context.Context, libraryID, query, topic string, tokens int) (DocsResult, error)
}
