// Package api holds the HTTP handlers of plaidsync.
//
// At build-order step 1 it contains only the two unauthenticated probes,
// /healthz and /readyz. The bearer-token middleware and the /v1 routes
// (Link tokens, items, jobs, the webhook receiver) arrive in later steps and
// will live alongside these handlers.
//
// Every response from this package is JSON, and no response ever carries an
// access token, a database URL, or any other secret. The readiness probe in
// particular reports only that the database is unreachable; the reason goes
// to the log.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// readyTimeout bounds the database ping made by the readiness probe so a
// hung connection turns into a prompt 503 rather than a stalled request.
const readyTimeout = 5 * time.Second

// Status values reported in the "status" field of probe responses.
const (
	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

// statusResponse is the body of every probe response: {"status":"..."}.
type statusResponse struct {
	Status string `json:"status"`
}

// HealthHandler returns the liveness probe. It always answers
// 200 {"status":"ok"}: it proves the process is up and serving HTTP and
// nothing more. Mount it as GET /healthz.
func HealthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, http.StatusOK, statusOK)
	})
}

// ReadyHandler returns the readiness probe. It calls ping, normally
// (*store.Store).Ping, with a context derived from the request and bounded
// by readyTimeout, and answers 200 {"status":"ok"} when ping succeeds or
// 503 {"status":"unavailable"} when it fails. The failure reason is logged
// through the default slog logger and never written to the response. Mount
// it as GET /readyz.
//
// ping must not be nil.
func ReadyHandler(ping func(context.Context) error) http.Handler {
	if ping == nil {
		panic("api: ReadyHandler requires a non-nil ping function")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()

		if err := ping(ctx); err != nil {
			slog.WarnContext(ctx, "readiness check failed", "error", err)
			writeStatus(w, http.StatusServiceUnavailable, statusUnavailable)
			return
		}
		writeStatus(w, http.StatusOK, statusOK)
	})
}

// writeStatus writes a {"status":status} JSON body with the given HTTP
// status code. Probe responses must never be cached by an intermediary.
func writeStatus(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	// The body is tiny and fixed, so an encode failure can only be a
	// write error on a connection that has already gone away; there is
	// nothing useful to do about it.
	_ = json.NewEncoder(w).Encode(statusResponse{Status: status})
}
