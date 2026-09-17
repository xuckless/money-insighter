package api

import (
	"log/slog"
	"net/http"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/jobs"
	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
)

// defaultSandboxInstitution is the Sandbox institution used when
// POST /v1/sandbox/items names none: Plaid's "First Platypus Bank", which
// supports Transactions and returns test data quickly.
const defaultSandboxInstitution = "ins_109508"

// Server holds what the handlers need. Build it with NewServer and mount
// Handler.
type Server struct {
	store    *store.Store
	plaid    plaid.Client
	keys     *crypto.Keyring
	runner   *jobs.Runner
	verifier *Verifier
	hosted   *hostedSessions
	cfg      *config.Config
	log      *slog.Logger
}

// NewServer wires the API. logger may be nil.
func NewServer(cfg *config.Config, st *store.Store, pc plaid.Client, keys *crypto.Keyring, runner *jobs.Runner, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		store:    st,
		plaid:    pc,
		keys:     keys,
		runner:   runner,
		verifier: NewVerifier(pc),
		hosted:   newHostedSessions(),
		cfg:      cfg,
		log:      logger.With("component", "api"),
	}
}

// Handler returns the complete HTTP surface:
//
//	GET  /healthz, GET /readyz                    open probes
//	POST /v1/webhooks/plaid                       verified by Plaid's JWT, no bearer
//	POST /v1/link/token                           bearer: Link token for a new item
//	POST /v1/link/exchange                        bearer: public token -> item + initial sync job
//	POST /v1/link/hosted                          bearer: Hosted Link session for a new item (URL to open)
//	POST /v1/link/hosted/status                   bearer: poll a hosted session; exchanges on completion
//	GET  /v1/items                                bearer
//	GET  /v1/items/{id}                           bearer: item, latest jobs and runs
//	POST /v1/items/{id}/link/token                bearer: update-mode Link token
//	POST /v1/items/{id}/link/hosted               bearer: update-mode Hosted Link session
//	POST /v1/items/{id}/sync                      bearer: 202 + job
//	DELETE /v1/items/{id}                         bearer: /item/remove
//	GET  /v1/jobs/{id}                            bearer
//	POST /v1/sandbox/items                        bearer, Sandbox only: link a test item without Link
func (s *Server) Handler() http.Handler {
	v1 := http.NewServeMux()
	v1.HandleFunc("POST /v1/link/token", s.handleLinkToken)
	v1.HandleFunc("POST /v1/link/exchange", s.handleExchange)
	v1.HandleFunc("POST /v1/link/hosted", s.handleHostedLink)
	v1.HandleFunc("POST /v1/link/hosted/status", s.handleHostedStatus)
	v1.HandleFunc("GET /v1/items", s.handleListItems)
	v1.HandleFunc("GET /v1/items/{id}", s.handleGetItem)
	v1.HandleFunc("POST /v1/items/{id}/link/token", s.handleUpdateLinkToken)
	v1.HandleFunc("POST /v1/items/{id}/link/hosted", s.handleUpdateHostedLink)
	v1.HandleFunc("POST /v1/items/{id}/sync", s.handleSync)
	v1.HandleFunc("DELETE /v1/items/{id}", s.handleRemoveItem)
	v1.HandleFunc("GET /v1/jobs/{id}", s.handleGetJob)
	if s.cfg.Plaid.Env == config.PlaidEnvSandbox {
		v1.HandleFunc("POST /v1/sandbox/items", s.handleSandboxItem)
	}
	v1.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such route")
	})

	root := http.NewServeMux()
	root.Handle("GET /healthz", HealthHandler())
	root.Handle("GET /readyz", ReadyHandler(s.store.Ping))
	root.HandleFunc("POST /v1/webhooks/plaid", s.handleWebhook)
	root.Handle("/v1/", bearerAuth(s.cfg.APIToken, v1))
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such route")
	})
	return withRequestID(withRecover(s.log, withAccessLog(s.log, root)))
}
