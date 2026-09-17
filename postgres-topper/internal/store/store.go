// Package store owns the database connection of the topper: the pgx pool,
// the startup checks that the topper schema and the plaidsync tables exist,
// the topper's own goose migrations, and the classification of Postgres
// errors. It runs no application queries; those are built in internal/query
// and executed by internal/api against [Store.Pool].
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidDatabaseURL is returned by [Open] when the URL cannot be parsed.
var ErrInvalidDatabaseURL = errors.New("store: database url is not a valid connection string")

const (
	// defaultMaxConns is the pool size applied unless the URL asks for
	// more (pool_max_conns). Every request holds at most one connection
	// for at most the query timeout, so a small pool goes a long way.
	defaultMaxConns = 10

	// applicationName is reported to Postgres (pg_stat_activity) unless the
	// URL sets its own application_name.
	applicationName = "topper"
)

// Store is an open connection pool.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

// Open parses databaseURL, applies the pool defaults and returns a Store.
// It does not ping: in the Compose stack Postgres may still be starting,
// and [Store.WaitForPlaidSchema] retries connection errors anyway.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		var pce *pgconn.ParseConfigError
		if errors.As(err, &pce) {
			return nil, ErrInvalidDatabaseURL
		}
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	if cfg.MaxConns < defaultMaxConns {
		cfg.MaxConns = defaultMaxConns
	}
	cfg.MinConns = 1
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	return &Store{
		pool: pool,
		log:  slog.Default().With("component", "store"),
	}, nil
}

// Pool returns the underlying pool for the query layer.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// Close closes the pool. It blocks until every acquired connection has
// been released.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping checks that a connection can be acquired and the server answers.
func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}
