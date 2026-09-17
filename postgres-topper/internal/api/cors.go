package api

import (
	"net/http"
	"strings"
)

// withCORS adds CORS headers for requests whose Origin exactly matches one
// of the configured origins, and answers preflight OPTIONS requests for
// those origins with 204 before authentication (a preflight carries no
// Authorization header). With no configured origins it is a no-op: a
// browser on another origin is then blocked by its own same-origin
// policy, which is the intended default for a service-to-service API.
func withCORS(origins []string) func(http.Handler) http.Handler {
	if len(origins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[strings.ToLower(o)] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" || !allowed[strings.ToLower(origin)] {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-None-Match")
			h.Set("Access-Control-Expose-Headers", "ETag, X-Request-Id")
			h.Set("Access-Control-Max-Age", "600")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
