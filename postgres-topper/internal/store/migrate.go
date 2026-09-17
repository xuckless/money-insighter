package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"postgres-topper/migrations"
)

const (
	// versionTable is where goose records the topper's applied migrations.
	// It is schema-qualified so it never collides with plaidsync's
	// public.goose_db_version in the same database.
	versionTable = "topper.goose_db_version"

	// migrationLockID is the advisory lock goose takes while migrating.
	// Advisory locks are per database and plaidsync uses goose's default
	// id, so the topper needs its own or the two services would wait on
	// each other during a simultaneous restart.
	migrationLockID int64 = 0x746f70706572 // "topper"
)

// Migrate applies every pending topper migration from the embedded
// migrations package, in version order, each in its own transaction. It is
// idempotent: a database that is already current is left untouched. A
// session-level advisory lock serialises concurrent callers.
func (s *Store) Migrate(ctx context.Context) error {
	p, db, err := s.gooseProvider()
	if err != nil {
		return err
	}
	defer closeQuietly(db)

	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	for _, r := range results {
		s.log.InfoContext(ctx, "applied migration",
			"version", r.Source.Version,
			"path", r.Source.Path,
			"duration", r.Duration,
		)
	}
	return nil
}

// MigrationVersion returns the highest topper migration version recorded in
// the database, or 0 when none has been applied.
func (s *Store) MigrationVersion(ctx context.Context) (int64, error) {
	p, db, err := s.gooseProvider()
	if err != nil {
		return 0, err
	}
	defer closeQuietly(db)

	v, err := p.GetDBVersion(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: migration version: %w", err)
	}
	return v, nil
}

// gooseProvider builds a goose Provider over the embedded migrations. goose
// speaks database/sql, so the pgx pool is adapted with OpenDBFromPool; the
// returned *sql.DB borrows connections from the pool and must be closed by
// the caller when done (closing it does not close the pool).
func (s *Store) gooseProvider() (*goose.Provider, *sql.DB, error) {
	locker, err := lock.NewPostgresSessionLocker(lock.WithLockID(migrationLockID))
	if err != nil {
		return nil, nil, fmt.Errorf("store: migration locker: %w", err)
	}
	db := stdlib.OpenDBFromPool(s.pool)
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSlog(s.log),
		goose.WithSessionLocker(locker),
		goose.WithTableName(versionTable),
	)
	if err != nil {
		closeQuietly(db)
		return nil, nil, fmt.Errorf("store: migration provider: %w", err)
	}
	return p, db, nil
}

// closeQuietly closes the database/sql adapter. Its Close only releases the
// adapter's bookkeeping (the pool stays open), so an error is not actionable.
func closeQuietly(db *sql.DB) {
	_ = db.Close()
}
