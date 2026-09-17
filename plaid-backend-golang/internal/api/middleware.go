package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"plaidsync/internal/secret"
)

type ctxKey int

const requestIDKey ctxKey = iota

// requestIDFrom returns the request id the middleware attached, or "".
func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// bytesReader is a tiny adapter so readJSON can decode from a byte slice.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// newRequestID returns 16 random bytes as hex.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unavailable"
	}
	return hex.EncodeToString(b[:])
}

// statusWriter records the status and size of a response for the log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// withRequestID attaches a request id to the context and the response.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

// withAccessLog logs one line per request: method, path, status, bytes,
// duration, request id, remote address. Never the query string (a
// misconfigured client might put a token there) and never a body.
func withAccessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		logger.InfoContext(r.Context(), "request",
			"method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes,
			"duration", time.Since(start).Round(time.Millisecond),
			"request_id", requestIDFrom(r.Context()), "remote", r.RemoteAddr)
	})
}

// withRecover turns a handler panic into a 500 and a log line with the
// stack, so one bad request cannot take the process down.
func withRecover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				logger.ErrorContext(r.Context(), "handler panic", "panic", p, "stack", string(debug.Stack()), "request_id", requestIDFrom(r.Context()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// bearerAuth admits requests whose Authorization header carries the
// configured token and rejects everything else with a 401. The comparison
// is over SHA-256 digests in constant time, so neither the token length
// nor a partial match leaks through timing. A token in the query string
// is never accepted.
func bearerAuth(token secret.Token, next http.Handler) http.Handler {
	want := sha256.Sum256([]byte(token.Expose()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="plaidsync"`)
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		got := sha256.Sum256([]byte(strings.TrimSpace(h[len(prefix):])))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="plaidsync"`)
			writeError(w, http.StatusUnauthorized, "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
