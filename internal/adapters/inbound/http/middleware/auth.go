// Package middleware provides chi-compatible HTTP middlewares used by the
// orchestrator's inbound HTTP layer.
package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
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

// APIKey returns chi-compatible middleware that requires an X-Sophia-API-Key
// header (or X-API-Key fallback). On success, it injects the resolved
// project name into the request context under ContextKeyProject.
//
// In V1 the validator is a simple hash-table lookup. V2 swaps for OIDC.
func APIKey(authn Authenticator) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := strings.TrimSpace(r.Header.Get("X-Sophia-API-Key"))
			if key == "" {
				key = strings.TrimSpace(r.Header.Get("X-API-Key"))
			}
			if key == "" {
				http.Error(w, `{"error":"missing api key","code":"unauthenticated"}`, http.StatusUnauthorized)
				return
			}
			project, err := authn.Validate(r.Context(), key)
			if err != nil {
				http.Error(w, `{"error":"invalid api key","code":"unauthenticated"}`, http.StatusUnauthorized)
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
