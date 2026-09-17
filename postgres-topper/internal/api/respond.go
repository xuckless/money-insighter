package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// errorResponse is the body of every error: {"error":"message"}.
type errorResponse struct {
	Error string `json:"error"`
}

// writeJSON encodes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error body. Error responses are never cached.
func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, errorResponse{Error: msg})
}

// writeMethodNotAllowed answers 405 with an Allow header, as JSON. The
// stdlib mux would answer in text/plain, so /v1 handlers dispatch on the
// method themselves and call this.
func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

// notFound is the JSON 404 for unknown paths.
func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not found")
}
