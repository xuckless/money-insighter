package store

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Kind classifies a database error for the HTTP layer, which maps each
// kind to a status code without inspecting SQLSTATEs itself.
type Kind int

const (
	// KindOther is any error not listed below, including connection
	// failures; it is reported as an internal error.
	KindOther Kind = iota
	// KindUnique is a unique or primary key violation (23505).
	KindUnique
	// KindForeignKey is a foreign key violation (23503).
	KindForeignKey
	// KindNotNull is a NOT NULL violation (23502).
	KindNotNull
	// KindCheck is a CHECK constraint violation (23514).
	KindCheck
	// KindIntegrity is any other class 23 violation.
	KindIntegrity
	// KindData is a class 22 data exception: a value the column type
	// cannot parse or hold ("abc" for a numeric, an out-of-range date).
	KindData
	// KindUndefinedOp is a type mismatch the planner rejected: an operator
	// or cast that does not exist for the column's type (42883, 42804,
	// 42P18, 42846).
	KindUndefinedOp
	// KindPrivilege is a permission failure (42501), which for the topper
	// means its allowlist and the database grants disagree.
	KindPrivilege
	// KindTimeout is a statement the server or the context cancelled for
	// time (57014, context.DeadlineExceeded).
	KindTimeout
	// KindCanceled is a request the client abandoned (context.Canceled).
	KindCanceled
)

// Classify inspects err and returns its Kind plus the underlying
// *pgconn.PgError when there is one. A nil err is KindOther, nil.
func Classify(err error) (Kind, *pgconn.PgError) {
	if err == nil {
		return KindOther, nil
	}
	if errors.Is(err, context.Canceled) {
		return KindCanceled, nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return KindTimeout, nil
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return KindOther, nil
	}
	switch code := pgErr.Code; {
	case code == "23505":
		return KindUnique, pgErr
	case code == "23503":
		return KindForeignKey, pgErr
	case code == "23502":
		return KindNotNull, pgErr
	case code == "23514":
		return KindCheck, pgErr
	case strings.HasPrefix(code, "23"):
		return KindIntegrity, pgErr
	case strings.HasPrefix(code, "22"):
		return KindData, pgErr
	case code == "42883", code == "42804", code == "42P18", code == "42846":
		return KindUndefinedOp, pgErr
	case code == "42501":
		return KindPrivilege, pgErr
	case code == "57014":
		return KindTimeout, pgErr
	}
	return KindOther, pgErr
}
