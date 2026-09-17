// Package store is the only place that talks to Postgres. It owns the schema
// (migrations run from here), every SQL statement, and the row types the rest
// of the service sees. Callers never receive a Plaid access token from this
// package except through Credential, which carries ciphertext only.
//
// Every Store method takes explicit ids rather than reading them from
// ambient state, so threading a tenant dimension through later is mechanical.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors callers branch on with errors.Is. Every method that can
// return one wraps it, so compare with errors.Is rather than ==.
var (
	// ErrNotFound is returned when the requested row does not exist, or
	// exists but is unusable for the operation (for example GetCredential
	// on a removed item, whose credential column is NULL).
	ErrNotFound = errors.New("store: not found")

	// ErrItemLocked is returned by WithItemLock when another sync is
	// already holding the item's advisory lock. It is never blocked on.
	ErrItemLocked = errors.New("store: item sync already running")

	// ErrInvalidDatabaseURL is returned by Open when the database URL cannot
	// be parsed. It carries no detail on purpose: the URL is a secret and
	// the parser's own message would echo parts of it. Check the value of
	// PLAIDSYNC_DATABASE_URL against the pgx / libpq connection string
	// syntax.
	ErrInvalidDatabaseURL = errors.New("store: database url is not a valid connection string")
)

const (
	// minMaxConns is the floor applied to the pool size. Sync transactions
	// stay open for the whole /transactions/sync pagination, so a handful of
	// concurrent syncs plus the API plus the scheduler must not exhaust the
	// pool. A larger value in the URL (pool_max_conns) is respected.
	minMaxConns = 20

	// rollbackTimeout bounds how long a rollback issued after the caller's
	// context is already dead may take.
	rollbackTimeout = 5 * time.Second

	// applicationName is reported to Postgres (pg_stat_activity) unless the
	// URL sets its own application_name.
	applicationName = "plaidsync"
)

// Store is a handle on the database. It is safe for concurrent use and is
// normally created once per process by Open.
type Store struct {
	pool *pgxpool.Pool
	log  *slog.Logger

	// testHookBeforeCursorUpdate, when non-nil, is called by ApplySyncBatch
	// after the pending reconciliation step and before the cursor update on
	// plaid_items. Tests set it to inject a failure and prove that nothing
	// from the batch persists. It is always nil in production.
	testHookBeforeCursorUpdate func(ctx context.Context) error
}

// Open connects to Postgres at databaseURL (a postgres:// URL or keyword
// string), builds a connection pool with at least minMaxConns connections,
// and verifies the connection with one ping. It does not run migrations;
// call Migrate.
//
// The URL is a secret (it usually carries the password), so the returned
// error never echoes it. A parse failure yields ErrInvalidDatabaseURL with
// no detail: pgx's own parse error prints the connection string with the
// password masked on a best-effort basis, which still exposes user, host,
// database and query parameters, and its wrapped causes quote the
// offending fragment. Connect failures are wrapped as pgx reports them:
// user, database, host address and the network error, never the password.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		var pce *pgconn.ParseConfigError
		if errors.As(err, &pce) {
			return nil, ErrInvalidDatabaseURL
		}
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	if cfg.MaxConns < minMaxConns {
		cfg.MaxConns = minMaxConns
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["application_name"]; !ok {
		cfg.ConnConfig.RuntimeParams["application_name"] = applicationName
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}

	return &Store{
		pool: pool,
		log:  slog.Default().With("component", "store"),
	}, nil
}

// Close releases every pooled connection. It blocks until connections that
// are checked out (for example by a sync transaction) are returned. It is
// safe to call more than once.
func (s *Store) Close() {
	s.pool.Close()
}

// Ping checks that a connection can be acquired and the server answers. It
// backs the /readyz endpoint.
func (s *Store) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("store: ping: %w", err)
	}
	return nil
}

// querier is the subset of *pgxpool.Pool and pgx.Tx that queries need, so a
// statement can be written once and run either directly on the pool or
// inside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	_ querier = (*pgxpool.Pool)(nil)
	_ querier = (pgx.Tx)(nil)
)

// beginTx starts a read-committed, read-write transaction on a pooled
// connection. The caller owns it and must Commit or Rollback; withTx does
// that for the common shape. Prefer withTx unless the transaction has to
// outlive a single function (WithItemLock).
func (s *Store) beginTx(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	return tx, nil
}

// withTx runs fn inside a transaction. A nil return commits; any error (or
// a panic, which is re-raised) rolls back. fn's error is returned unwrapped
// so callers can add their own context; commit failures are wrapped.
//
// The rollback runs on a context detached from ctx's cancellation so that a
// caller whose context has expired still sends ROLLBACK and hands a clean
// connection back to the pool instead of a poisoned one.
func (s *Store) withTx(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) (err error) {
	tx, err := s.beginTx(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		s.rollback(ctx, tx)
	}()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	committed = true
	return nil
}

// rollback rolls tx back on a context that survives ctx's cancellation, and
// logs (rather than returns) a failure, since the caller already has the
// error that caused the rollback. Rolling back an already-finished
// transaction is a no-op.
func (s *Store) rollback(ctx context.Context, tx pgx.Tx) {
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if err := tx.Rollback(rbCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		s.log.WarnContext(ctx, "transaction rollback failed", "error", err)
	}
}

// notFoundIfNoRows maps pgx.ErrNoRows to ErrNotFound and leaves every other
// error alone. Use it on the result of a QueryRow(...).Scan.
func notFoundIfNoRows(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// rowsAffectedOrNotFound turns an Exec result into ErrNotFound when the
// statement matched no rows. Use it for UPDATEs keyed by a primary key.
func rowsAffectedOrNotFound(tag pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
