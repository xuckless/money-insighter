package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"plaidsync/migrations"
)

// Migrate applies every pending migration from the embedded migrations
// package, in version order, each in its own transaction. It is idempotent:
// a database that is already current is left untouched. A session-level
// advisory lock serialises concurrent callers (two replicas starting at
// once), so the second one waits and then finds nothing to do.
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

// MigrationVersion returns the highest migration version recorded in the
// database, or 0 when no migration has been applied. It creates goose's
// version table if it is missing (goose does that on every read), so it is
// safe to call before Migrate on an empty database.
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
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, nil, fmt.Errorf("store: migration locker: %w", err)
	}
	db := stdlib.OpenDBFromPool(s.pool)
	p, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS,
		goose.WithSlog(s.log),
		goose.WithSessionLocker(locker),
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
