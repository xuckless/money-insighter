package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5/pgxpool"
)

// loadView builds the Relation of one ViewSpec. A FromTable view copies the
// table's columns and source; a SQL view is described by preparing it with
// LIMIT 0 and reading the result's field descriptions, so the column names
// and types come from Postgres, not from a hand-maintained list.
func loadView(ctx context.Context, pool *pgxpool.Pool, c *Catalog, v ViewSpec) (*Relation, error) {
	if v.Name == "" {
		return nil, errors.New("catalog: view with empty name")
	}
	if (v.FromTable == "") == (v.SQL == "") {
		return nil, fmt.Errorf("catalog: view %q must set exactly one of FromTable and SQL", v.Name)
	}

	r := Relation{
		Name:         v.Name,
		Kind:         KindView,
		Mode:         ModeRead,
		DefaultOrder: slices.Clone(v.DefaultOrder),
		Tiebreak:     slices.Clone(v.Tiebreak),
		Forced:       slices.Clone(v.Forced),
		Toggles:      slices.Clone(v.Toggles),
		Cacheable:    true,
	}

	if v.FromTable != "" {
		base, ok := c.tables[v.FromTable]
		if !ok {
			return nil, fmt.Errorf("catalog: view %q is based on unknown table %q", v.Name, v.FromTable)
		}
		if base.Mode != ModeRead {
			return nil, fmt.Errorf("catalog: view %q is based on writable table %q", v.Name, v.FromTable)
		}
		r.Schema, r.Table, r.Source = base.Schema, base.Table, base.Source
		r.Columns = slices.Clone(base.Columns)
		r.PK = slices.Clone(base.PK)
		if r.Tiebreak == nil {
			r.Tiebreak = slices.Clone(base.PK)
		}
		return NewRelation(r)
	}

	cols, err := describe(ctx, pool, v.SQL)
	if err != nil {
		return nil, fmt.Errorf("catalog: view %q: %w", v.Name, err)
	}
	r.Source = "(" + v.SQL + ") AS v"
	r.Columns = cols
	return NewRelation(r)
}

// describe runs sql with LIMIT 0 and returns its output columns. Types are
// resolved through the connection's type map, whose names match
// pg_type.typname (and therefore information_schema's udt_name).
func describe(ctx context.Context, pool *pgxpool.Pool, sql string) ([]Column, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	rows, err := conn.Query(ctx, "SELECT * FROM ("+sql+") AS v LIMIT 0")
	if err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	defer rows.Close()

	tm := conn.Conn().TypeMap()
	fds := rows.FieldDescriptions()
	cols := make([]Column, 0, len(fds))
	for i, fd := range fds {
		c := Column{Name: fd.Name, Ordinal: i + 1, Nullable: true}
		if t, ok := tm.TypeForOID(fd.DataTypeOID); ok {
			c.UDT = t.Name
			c.UDTSchema = "pg_catalog"
		}
		classify(&c)
		cols = append(cols, c)
	}
	// Drain so the connection is clean before Release.
	for rows.Next() {
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("describe: %w", err)
	}
	return cols, nil
}
