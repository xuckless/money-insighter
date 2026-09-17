// Command plaidsync is the Plaid ingestion service: it owns the Plaid
// relationship for one deployment and writes normalised accounts and
// transactions into Postgres for other services to read.
//
// The binary is stateless. Everything it needs comes from environment
// variables (see internal/config and .env.example) and everything it knows
// lives in the database. It exits non-zero with a message on stderr when any
// startup step fails, and shuts down gracefully on SIGINT or SIGTERM.
//
// The HTTP surface is the two probes (GET /healthz, GET /readyz), the
// bearer-authenticated /v1 API (Link tokens, items, sync triggers, jobs,
// and in Sandbox a Link-less item creator) and the Plaid webhook receiver.
// A pool of workers drains the sync_jobs queue in the same process, and a
// scheduler sweeps every item on a fixed interval. See internal/api,
// internal/jobs and internal/sync.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"plaidsync/internal/api"
	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/jobs"
	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
	"plaidsync/internal/sync"
)

const (
	// readHeaderTimeout bounds how long a client may take to send request
	// headers; it is the server's defence against slowloris connections.
	readHeaderTimeout = 10 * time.Second

	// readTimeout bounds the whole request, body included, so a client
	// that trickles a POST body cannot hold a connection and a handler
	// goroutine open indefinitely. Every request this service will accept
	// is small (a public token, a webhook payload).
	readTimeout = 30 * time.Second

	// writeTimeout bounds writing the response, counted from the end of
	// the request headers. Handlers answer quickly (sync work is queued
	// and answered with 202), so a slow reader is the only way to hit it.
	writeTimeout = 30 * time.Second

	// idleTimeout closes keep-alive connections that stay idle.
	idleTimeout = 2 * time.Minute
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Once the first signal has started a graceful shutdown, restore the
		// default disposition so a second one terminates a process that is
		// stuck draining.
		<-ctx.Done()
		stop()
	}()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "plaidsync: %v\n", err)
		os.Exit(1)
	}
}

// run wires the service together and blocks until ctx is canceled by a
// signal and the HTTP server has drained. Every failure is returned rather
// than logged-and-ignored so that main can exit non-zero.
func run(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		// A joined error lists every problem on its own line; the leading
		// newline keeps the first one aligned with the rest.
		return fmt.Errorf("invalid configuration:\n%w", err)
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)
	logger.Info("starting plaidsync", "config", cfg)

	if !isLoopback(cfg.BindAddr) {
		logger.Warn("bind address is not a loopback address; the service speaks plain HTTP and holds bank credentials, so it must sit behind TLS termination and a firewall",
			"bind_addr", cfg.BindAddr)
	}

	keyring, err := crypto.NewKeyring(cfg.KEKActiveVersion, cfg.KEKs)
	if err != nil {
		return fmt.Errorf("build keyring: %w", err)
	}
	logger.Info("keyring loaded", "keyring", keyring)

	st, err := store.Open(ctx, cfg.DatabaseURL.Expose())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	version, err := st.MigrationVersion(ctx)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	logger.Info("database ready", "migration_version", version)

	plaidClient := plaid.NewHTTPClient(cfg.Plaid, logger)
	engine := sync.New(st, plaidClient, keyring, cfg.Sync, logger)
	runner := jobs.New(st, engine, cfg.Sync, logger)
	server := api.NewServer(cfg, st, plaidClient, keyring, runner, logger)

	if cfg.Plaid.WebhookURL == "" {
		logger.Warn("PLAIDSYNC_WEBHOOK_URL is unset: Plaid cannot notify this deployment; syncs run only on the scheduler and manual triggers")
	}

	// The runner stops when ctx is canceled, which cancels any sync in
	// flight; the job stays running in the table and is requeued at the
	// next start. The HTTP server drains separately, below.
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runner.Run(ctx) }()

	serveErr := serve(ctx, logger, cfg, server.Handler())
	if err := <-runnerDone; err != nil && serveErr == nil {
		serveErr = fmt.Errorf("job runner: %w", err)
	}
	return serveErr
}

// serve listens on cfg.BindAddr and serves handler until ctx is canceled,
// then drains in-flight requests for at most cfg.ShutdownTimeout. It
// returns nil after a clean shutdown and an error if the listener could not
// be opened, the server failed, or the drain timed out (in which case the
// remaining connections are closed forcibly before returning).
func serve(ctx context.Context, logger *slog.Logger, cfg *config.Config, handler http.Handler) error {
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.BindAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.BindAddr, err)
	}
	logger.Info("http server listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() {
		// Serve closes ln when it returns.
		serveErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		// Before Shutdown is called, Serve only returns on an accept failure.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received, draining http server", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// Shutdown gives up when in-flight requests outlive the timeout;
		// Close ends them so the process does not hang on exit.
		_ = srv.Close()
		return fmt.Errorf("http server shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server: %w", err)
	}
	logger.Info("http server stopped")
	return nil
}

// newLogger builds the process logger from the log configuration. Logs go
// to stderr; the format is "text" or, for anything else config accepts,
// JSON (config normalises the value, so "json" is the only other option).
func newLogger(lc config.LogConfig) *slog.Logger {
	opts := &slog.HandlerOptions{Level: lc.Level}
	var h slog.Handler
	if lc.Format == "text" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else {
		h = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(h)
}

// isLoopback reports whether addr (host:port) names a loopback interface.
// An empty host (":8080") means every interface, and a hostname other than
// localhost is treated as non-loopback without resolving it: this backs a
// startup warning, not an access control.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}
