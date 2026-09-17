package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// This file holds the small pieces of query plumbing shared by the read
// paths: collecting rows into the tagged row structs of types.go, argument
// validation for paginated lists, and Postgres error classification.

// maxListLimit caps how many rows a single list call may ask for, so an API
// handler that forwards a client-supplied limit cannot pull a whole table.
const maxListLimit = 1000

// sqlStateForeignKeyViolation is the SQLSTATE Postgres raises when an INSERT
// or UPDATE references a parent row that does not exist (class 23,
// integrity constraint violation).
const sqlStateForeignKeyViolation = "23503"

// scanAllByName runs sql on q and collects every row into a T by column
// name, using the db tags on T. The query must select exactly the columns
// T declares. No rows is not an error: the result is an empty, non-nil
// slice.
func scanAllByName[T any](ctx context.Context, q querier, sql string, args ...any) ([]T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[T])
}

// scanOneByName runs sql on q and collects the first row into a *T by
// column name. It returns ErrNotFound when the query yields no rows.
func scanOneByName[T any](ctx context.Context, q querier, sql string, args ...any) (*T, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	v, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[T])
	if err != nil {
		return nil, notFoundIfNoRows(err)
	}
	return v, nil
}

// checkLimit validates a list limit: it must be positive and at most
// maxListLimit.
func checkLimit(limit int) error {
	if limit <= 0 || limit > maxListLimit {
		return fmt.Errorf("store: limit %d out of range 1..%d", limit, maxListLimit)
	}
	return nil
}

// checkOffset validates a list offset: it must not be negative.
func checkOffset(offset int) error {
	if offset < 0 {
		return fmt.Errorf("store: offset %d must not be negative", offset)
	}
	return nil
}

// isForeignKeyViolation reports whether err is a Postgres foreign key
// violation, which the write paths translate into ErrNotFound for the
// referenced parent row.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation
}
