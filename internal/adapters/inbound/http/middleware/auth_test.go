package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAuthn returns valid for one specific key, rejects everything else.
type fakeAuthn struct{ validKey, project string }

func (f fakeAuthn) Validate(_ ContextProvider, key string) (string, error) {
	if key == f.validKey {
		return f.project, nil
	}
	return "", errors.New("invalid")
}

// passthrough records that the inner handler ran.
func passthrough(t *testing.T, gotProject *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		*gotProject = ProjectFromContext(r.Context())
	})
}

func newReq(t *testing.T, method, target, key string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if key != "" {
		r.Header.Set("X-Sophia-API-Key", key)
	}
	return r.WithContext(context.Background())
}

// 1. allowAnon=true + missing header → request passes through.
func TestAuth_AllowAnon_MissingHeader_Passes(t *testing.T) {
	var got string
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "k", project: "p"}, true)
	rec := httptest.NewRecorder()
	mw(passthrough(t, &got)).ServeHTTP(rec, newReq(t, "GET", "/x", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != AnonymousLoopbackProject {
		t.Errorf("project = %q, want %q", got, AnonymousLoopbackProject)
	}
}

// 2. allowAnon=false + missing header → 401 unauthorized.
func TestAuth_DenyAnon_MissingHeader_401(t *testing.T) {
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "k", project: "p"}, false)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inner handler should not run")
	})).ServeHTTP(rec, newReq(t, "GET", "/x", ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"code":"unauthorized"`) {
		t.Errorf("body missing stable code: %s", body)
	}
	if !strings.Contains(string(body), `"X-Sophia-API-Key required"`) {
		t.Errorf("body missing spec-conformant message: %s", body)
	}
}

// 3. allowAnon=false + invalid key → 401 unauthorized.
func TestAuth_DenyAnon_InvalidKey_401(t *testing.T) {
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "good", project: "p"}, false)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inner handler should not run on invalid key")
	})).ServeHTTP(rec, newReq(t, "GET", "/x", "bad"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"code":"unauthorized"`) {
		t.Errorf("body missing stable code: %s", body)
	}
}

// 4. allowAnon=false + valid key → request passes through with project ctx.
func TestAuth_DenyAnon_ValidKey_PassesWithProject(t *testing.T) {
	var got string
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "good", project: "myproj"}, false)
	rec := httptest.NewRecorder()
	mw(passthrough(t, &got)).ServeHTTP(rec, newReq(t, "GET", "/x", "good"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != "myproj" {
		t.Errorf("project = %q, want myproj", got)
	}
}

// 5. allowAnon=true + valid key STILL takes the auth path (anon is a fallback,
// not a bypass when a key is provided).
func TestAuth_AllowAnon_ValidKey_StillUsesAuthProject(t *testing.T) {
	var got string
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "good", project: "myproj"}, true)
	rec := httptest.NewRecorder()
	mw(passthrough(t, &got)).ServeHTTP(rec, newReq(t, "GET", "/x", "good"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got != "myproj" {
		t.Errorf("project = %q, want myproj (key took precedence over anon)", got)
	}
}

// 6. allowAnon=true + invalid key → 401 (a wrong key is NOT silently
// downgraded to anon; that would be a foot-gun).
func TestAuth_AllowAnon_InvalidKey_StillRejects(t *testing.T) {
	mw := APIKeyWithAnonOption(fakeAuthn{validKey: "good", project: "p"}, true)
	rec := httptest.NewRecorder()
	mw(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inner handler should not run on invalid key")
	})).ServeHTTP(rec, newReq(t, "GET", "/x", "bad"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (invalid key not downgraded to anon)", rec.Code)
	}
}

// IsLoopbackAddr coverage.

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr     string
		expected bool
	}{
		{"127.0.0.1:8080", true},
		{"127.0.0.5:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{":8080", false},
		{"0.0.0.0:8080", false},
		{"10.0.0.5:8080", false},
		{"example.com:8080", false},
		{"not-a-host", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsLoopbackAddr(tc.addr); got != tc.expected {
			t.Errorf("IsLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.expected)
		}
	}
}
