// Command topper is the REST access layer over the plaidsync Postgres. It
// exposes plaidsync's tables read-only and the consumer tables in schema
// topper read-write, behind static bearer tokens, for other services on the
// tailnet.
//
// The binary is stateless. Everything it needs comes from TOPPER_*
// environment variables (see internal/config and .env.example). It exits
// non-zero with a message on stderr when any startup step fails, and shuts
// down gracefully on SIGINT or SIGTERM.
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

	"postgres-topper/internal/api"
	"postgres-topper/internal/auth"
	"postgres-topper/internal/cache"
	"postgres-topper/internal/catalog"
	"postgres-topper/internal/config"
	"postgres-topper/internal/store"
	"postgres-topper/internal/views"
)

const (
	// readHeaderTimeout bounds how long a client may take to send request
	// headers; it is the server's defence against slowloris connections.
	readHeaderTimeout = 10 * time.Second

	// readTimeout bounds the whole request, body included. Bodies are
	// capped at TOPPER_MAX_BODY_BYTES, so a healthy client is far below it.
	readTimeout = 30 * time.Second

	// writeTimeout bounds writing the response, counted from the end of
	// the request headers. Every query is bounded by TOPPER_QUERY_TIMEOUT
	// (default 15s) and responses are at most 1000 rows, so a slow reader
	// is the only way to hit it.
	writeTimeout = 60 * time.Second

	// idleTimeout closes keep-alive connections that stay idle.
	idleTimeout = 2 * time.Minute

	// cacheEntries caps the response cache.
	cacheEntries = 500
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		// Once the first signal has started a graceful shutdown, restore
		// the default disposition so a second one terminates a process
		// that is stuck draining.
		<-ctx.Done()
		stop()
	}()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "topper: %v\n", err)
		os.Exit(1)
	}
}

// run wires the service together and blocks until ctx is canceled by a
// signal and the HTTP server has drained.
func run(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}

	logger := newLogger(cfg.Log)
	slog.SetDefault(logger)
	logger.Info("starting topper", "config", cfg)

	if !isLoopback(cfg.BindAddr) {
		logger.Warn("bind address is not a loopback address; the service speaks plain HTTP, so it must sit behind TLS termination such as tailscale serve",
			"bind_addr", cfg.BindAddr)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL.Expose())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()

	if err := st.WaitForPlaidSchema(ctx, cfg.StartupWait); err != nil {
		return err
	}
	if err := st.RequireTopperSchema(ctx); err != nil {
		return err
	}
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate topper schema: %w", err)
	}
	version, err := st.MigrationVersion(ctx)
	if err != nil {
		return fmt.Errorf("read migration version: %w", err)
	}
	logger.Info("database ready", "topper_migration_version", version)

	cat, err := catalog.Load(ctx, st.Pool(), catalog.Spec{
		ReadTables:  catalog.PlaidTables,
		WriteTables: cfg.WriteTables,
		Views:       views.All,
		Denied:      catalog.PlaidDenylist,
	})
	if err != nil {
		return fmt.Errorf("load catalog:\n%w", err)
	}
	logger.Info("catalog loaded", "tables", len(cat.Tables()), "views", len(cat.Views()), "write_tables", cfg.WriteTables)

	srv := api.New(api.Deps{
		Config:  cfg,
		Pool:    st.Pool(),
		Catalog: cat,
		Keyring: auth.NewKeyring(cfg.APIKeys),
		Cache:   cache.New(cfg.CacheTTL, cacheEntries),
		Logger:  logger,
	})
	return serve(ctx, logger, cfg, srv.Handler())
}

// serve listens on cfg.BindAddr and serves handler until ctx is canceled,
// then drains in-flight requests for at most cfg.ShutdownTimeout.
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
		serveErr <- srv.Serve(ln)
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutdown signal received, draining http server", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
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
// to stderr, JSON unless the format is text.
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
