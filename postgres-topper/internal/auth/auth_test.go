package auth_test

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"postgres-topper/internal/auth"
	"postgres-topper/internal/config"
)

const (
	readTok  = "read-token-0123456789abcdef0123456789abcdef"
	writeTok = "write-token-0123456789abcdef0123456789abcde"
)

func keyring() *auth.Keyring {
	return auth.NewKeyring([]config.APIKey{
		{Name: "reader", Scope: config.ScopeRead, Hash: sha256.Sum256([]byte(readTok))},
		{Name: "writer", Scope: config.ScopeReadWrite, Hash: sha256.Sum256([]byte(writeTok))},
	})
}

func TestLookup(t *testing.T) {
	k := keyring()
	if c, ok := k.Lookup(readTok); !ok || c.Name != "reader" || c.Scope != config.ScopeRead {
		t.Errorf("Lookup(read) = %+v %v", c, ok)
	}
	if c, ok := k.Lookup(writeTok); !ok || c.Name != "writer" {
		t.Errorf("Lookup(write) = %+v %v", c, ok)
	}
	for _, bad := range []string{"", "x", readTok + "x", readTok[:len(readTok)-1], strings.ToUpper(readTok)} {
		if _, ok := k.Lookup(bad); ok {
			t.Errorf("Lookup(%q) matched", bad)
		}
	}
}

func TestAllows(t *testing.T) {
	cases := []struct {
		scope  config.Scope
		method string
		want   bool
	}{
		{config.ScopeRead, "GET", true},
		{config.ScopeRead, "HEAD", true},
		{config.ScopeRead, "OPTIONS", true},
		{config.ScopeRead, "POST", false},
		{config.ScopeRead, "DELETE", false},
		{config.ScopeRead, "PUT", false},
		{config.ScopeReadWrite, "GET", true},
		{config.ScopeReadWrite, "POST", true},
		{config.ScopeReadWrite, "DELETE", true},
		{config.ScopeReadWrite, "PUT", true},
		{config.ScopeReadWrite, "PATCH", true},
		{config.Scope("admin"), "GET", false},
	}
	for _, tc := range cases {
		if got := auth.Allows(tc.scope, tc.method); got != tc.want {
			t.Errorf("Allows(%s, %s) = %v", tc.scope, tc.method, got)
		}
	}
}

func TestMiddleware(t *testing.T) {
	var seen auth.Caller
	h := auth.Middleware(keyring())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = auth.CallerFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	do := func(method, authz string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/v1/x", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	errOf := func(rec *httptest.ResponseRecorder) string {
		var m map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("body is not JSON: %s", rec.Body.String())
		}
		return m["error"]
	}

	if rec := do("GET", ""); rec.Code != 401 || errOf(rec) != "missing bearer token" || rec.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no header: %d %s %q", rec.Code, rec.Body.String(), rec.Header().Get("WWW-Authenticate"))
	}
	if rec := do("GET", "Basic abc"); rec.Code != 401 || errOf(rec) != "missing bearer token" {
		t.Errorf("basic: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "Bearer "); rec.Code != 401 {
		t.Errorf("empty bearer: %d", rec.Code)
	}
	if rec := do("GET", "Bearer nope"); rec.Code != 401 || errOf(rec) != "invalid token" {
		t.Errorf("bad token: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("GET", "bearer "+readTok); rec.Code != 204 || seen.Name != "reader" {
		t.Errorf("lowercase scheme: %d caller %+v", rec.Code, seen)
	}
	if rec := do("POST", "Bearer "+readTok); rec.Code != 403 || errOf(rec) != "scope read does not allow POST" {
		t.Errorf("read POST: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do("DELETE", "Bearer "+writeTok); rec.Code != 204 || seen.Name != "writer" {
		t.Errorf("write DELETE: %d caller %+v", rec.Code, seen)
	}
	if rec := do("PUT", "Bearer "+readTok); rec.Code != 403 {
		t.Errorf("read PUT: %d", rec.Code)
	}
	if rec := do("PUT", "Bearer "+writeTok); rec.Code != 204 {
		t.Errorf("readwrite PUT reaches the handler: %d", rec.Code)
	}
	// A token in the query string is not accepted.
	req := httptest.NewRequest("GET", "/v1/x?token="+readTok, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("query token accepted: %d", rec.Code)
	}
}
