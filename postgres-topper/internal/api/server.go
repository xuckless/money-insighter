package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"postgres-topper/internal/auth"
	"postgres-topper/internal/cache"
	"postgres-topper/internal/catalog"
	"postgres-topper/internal/config"
)

// Server holds what the handlers need.
type Server struct {
	cfg   *config.Config
	pool  *pgxpool.Pool
	cat   *catalog.Catalog
	keys  *auth.Keyring
	cache *cache.Cache
	log   *slog.Logger
}

// Deps are the collaborators of a Server.
type Deps struct {
	Config  *config.Config
	Pool    *pgxpool.Pool
	Catalog *catalog.Catalog
	Keyring *auth.Keyring
	Cache   *cache.Cache
	Logger  *slog.Logger
}

// New builds a Server. Every field of d is required except Logger, which
// defaults to slog.Default.
func New(d Deps) *Server {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Server{cfg: d.Config, pool: d.Pool, cat: d.Catalog, keys: d.Keyring, cache: d.Cache, log: d.Logger}
}

// Handler returns the complete HTTP handler:
//
//	GET /healthz, GET /readyz            unauthenticated probes
//	/v1/                                 bearer auth, then gzip, then:
//	  GET  /v1/                          list tables and views
//	  GET  /v1/{table}                   read
//	  POST /v1/{table}                   upsert (writable tables)
//	  DELETE /v1/{table}?filters         delete (writable tables)
//	  GET  /v1/views/{name}              read a view
//	anything else                        JSON 404
//
// wrapped in recover, access log and CORS.
func (s *Server) Handler() http.Handler {
	v1 := http.NewServeMux()
	v1.HandleFunc("/v1/{$}", s.handleList)
	v1.HandleFunc("/v1/views/{view...}", s.handleView)
	v1.HandleFunc("/v1/{table}", s.handleTable)
	v1.HandleFunc("/v1/", notFound)

	root := http.NewServeMux()
	root.Handle("GET /healthz", HealthHandler())
	root.Handle("GET /readyz", ReadyHandler(s.pool.Ping))
	root.Handle("/v1/", auth.Middleware(s.keys)(withGzip(v1)))
	root.HandleFunc("/", notFound)

	var h http.Handler = root
	h = withCORS(s.cfg.CORSOrigins)(h)
	h = requestLog(s.log)(h)
	h = recoverer(s.log)(h)
	return h
}

// handleTable dispatches /v1/{table} on the method.
func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("table")
	rel, ok := s.cat.Table(name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown table "+quote(name))
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		s.serveRead(w, r, rel)
	case http.MethodPost:
		if rel.Mode != catalog.ModeReadWrite {
			s.readOnly(w, name)
			return
		}
		s.handleUpsert(w, r, rel)
	case http.MethodDelete:
		if rel.Mode != catalog.ModeReadWrite {
			s.readOnly(w, name)
			return
		}
		s.handleDelete(w, r, rel)
	default:
		if rel.Mode == catalog.ModeReadWrite {
			writeMethodNotAllowed(w, http.MethodGet, http.MethodHead, http.MethodPost, http.MethodDelete)
		} else {
			writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		}
	}
}

// readOnly is the 405 for a write on a read-only table; it says why.
func (s *Server) readOnly(w http.ResponseWriter, name string) {
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, http.StatusMethodNotAllowed, "table "+quote(name)+" is read-only")
}

// handleView serves /v1/views/{name}.
func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("view")
	rel, ok := s.cat.View(name)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown view "+quote(name))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	s.serveRead(w, r, rel)
}

func quote(s string) string { return `"` + s + `"` }
