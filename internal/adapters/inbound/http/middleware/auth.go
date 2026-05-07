// Package middleware provides chi-compatible HTTP middlewares used by the
// orchestrator's inbound HTTP layer.
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"

	"github.com/RVRTelecomunicaciones/sophia/pkg/contract"
)

// splitHostPort and parseIP wrap stdlib calls so the rest of the package
// stays import-light. Defined as variables to keep the file's exported
// surface minimal and to allow tests to swap if ever needed.
var (
	splitHostPort = net.SplitHostPort
	parseIP       = net.ParseIP
)

// Authenticator validates an API key.
type Authenticator interface {
	// Validate checks the (project, key) pair. Returns the project name on
	// success, or an empty string + error on failure.
	Validate(ctx ContextProvider, key string) (project string, err error)
}

// ContextProvider wraps context.Context to keep this package zero-import.
type ContextProvider = interface {
	Done() <-chan struct{}
}

// AnonymousLoopbackProject is the synthetic project name injected into the
// request context when AllowAnonLocalhost permits a missing-key request.
// Downstream handlers MAY use this to distinguish anon-loopback callers
// from authenticated key holders.
const AnonymousLoopbackProject = "anonymous-loopback"

// APIKey returns chi-compatible middleware that requires an X-Sophia-API-Key
// header (or X-API-Key fallback). Equivalent to APIKeyWithAnonOption(authn, false).
func APIKey(authn Authenticator) func(next http.Handler) http.Handler {
	return APIKeyWithAnonOption(authn, false)
}

// APIKeyWithAnonOption returns chi-compatible middleware that requires an
// X-Sophia-API-Key header (or X-API-Key fallback). On success, it injects
// the resolved project name into the request context under
// ContextKeyProject.
//
// When allowAnon is true, requests without an API-key header are
// PERMITTED and the project context is set to AnonymousLoopbackProject.
// Bootstrap MUST only set allowAnon=true when the HTTP listener is bound
// exclusively to a loopback address per sophia-wire-v1 §3.2 / D-M10-02.
// The middleware itself does NOT verify the listener address; that is
// the bootstrap layer's responsibility (the listener-bound check is
// config-time, not per-request).
//
// Error envelope per sophia-wire-v1 §3.4 / §9.1:
//
//	{"code":"unauthorized","error":"X-Sophia-API-Key required"}
//
// Stable error code is "unauthorized"; "error" text may vary across
// server versions and clients MUST switch on "code".
//
// In V1 the validator is a simple hash-table lookup. V2 swaps for OIDC.
func APIKeyWithAnonOption(authn Authenticator, allowAnon bool) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get(contract.HeaderAPIKey))
			if key == "" {
				key = strings.TrimSpace(r.Header.Get(contract.HeaderAPIKeyLegacy))
			}
			if key == "" {
				if allowAnon {
					ctx := WithProject(r.Context(), AnonymousLoopbackProject)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				writeUnauthorized(w, "X-Sophia-API-Key required")
				return
			}
			project, err := authn.Validate(r.Context(), key)
			if err != nil {
				writeUnauthorized(w, "invalid X-Sophia-API-Key")
				return
			}
			ctx := WithProject(r.Context(), project)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// HashAPIKey returns the SHA256 hex digest of key. Used by both the
// validator (compares against stored hash) and the bootstrapper (creating
// new keys from random bytes).
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// IsLoopbackAddr reports whether the given listen address (in host:port
// form, as accepted by net.Listen) binds exclusively to a loopback
// interface. Returns true ONLY for 127.0.0.0/8, ::1, or the literal
// "localhost". Returns false for ":port" (binds 0.0.0.0), "0.0.0.0:...",
// or any specific routable IP.
//
// Bootstrap uses this to decide whether to construct middleware in
// allow-anon mode (per sophia-wire-v1 §3.2 / D-M10-02).
func IsLoopbackAddr(addr string) bool {
	host, _, err := splitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		// ":8080" binds to all interfaces (0.0.0.0). NOT loopback-only.
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := parseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// writeUnauthorized writes a 401 with the spec-compliant JSON envelope.
func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	body := `{"code":"` + contract.CodeUnauthorized + `","error":"` + jsonEscape(msg) + `"}`
	_, _ = w.Write([]byte(body))
}

// jsonEscape returns a string safe to embed in a JSON string literal.
// Defensive against future error messages with special characters.
func jsonEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				_, _ = b.WriteString(`\u00`)
				const hex = "0123456789abcdef"
				_ = b.WriteByte(hex[r>>4])
				_ = b.WriteByte(hex[r&0x0f])
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}
