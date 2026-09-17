package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey struct{}

// CallerRecorder is implemented by a ResponseWriter that wants to know who
// the caller was, for the access log. The access-log middleware sits
// outside this one and cannot see the request context it creates, so the
// caller is handed back through the writer instead.
type CallerRecorder interface {
	RecordCaller(Caller)
}

// CallerFromContext returns the authenticated caller, if any.
func CallerFromContext(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(ctxKey{}).(Caller)
	return c, ok
}

// Middleware authenticates every request with the keyring and checks the
// caller's scope against the method. Failures are JSON: 401 with a
// WWW-Authenticate challenge when the token is missing or unknown, 403
// when the scope does not permit the method. On success the caller is
// attached to the request context.
func Middleware(k *Keyring) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, present := bearerToken(r)
			if !present {
				unauthorized(w, "missing bearer token")
				return
			}
			caller, ok := k.Lookup(token)
			if !ok {
				unauthorized(w, "invalid token")
				return
			}
			if !Allows(caller.Scope, r.Method) {
				writeJSONError(w, http.StatusForbidden, "scope "+string(caller.Scope)+" does not allow "+r.Method)
				return
			}
			if rec, ok := w.(CallerRecorder); ok {
				rec.RecordCaller(caller)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, caller)))
		})
	}
}

// bearerToken extracts the token from the Authorization header. Only the
// Bearer scheme is accepted, case-insensitively; a token in the query
// string is deliberately not supported because query strings land in logs
// and caches.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, rest, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok := strings.TrimSpace(rest)
	if tok == "" {
		return "", false
	}
	return tok, true
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="topper"`)
	writeJSONError(w, http.StatusUnauthorized, msg)
}

// writeJSONError is a local copy of the API's error envelope so this
// package does not import internal/api.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
