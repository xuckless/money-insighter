package catalog

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Load introspects every relation in spec and returns the catalog. Every
// problem is fatal: a missing plaidsync table means plaidsync has not
// migrated, a missing write table means TOPPER_WRITE_TABLES is wrong, and
// starting with a partial allowlist would silently hide the mistake.
func Load(ctx context.Context, pool *pgxpool.Pool, spec Spec) (*Catalog, error) {
	c := &Catalog{tables: map[string]*Relation{}, views: map[string]*Relation{}}
	var errs []error

	for _, name := range spec.ReadTables {
		r, err := loadTable(ctx, pool, SchemaPublic, name, ModeRead, spec.Denied[name])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.tables[name] = r
	}
	for _, name := range spec.WriteTables {
		if _, dup := c.tables[name]; dup {
			errs = append(errs, fmt.Errorf("catalog: write table %q collides with a read-only table", name))
			continue
		}
		r, err := loadTable(ctx, pool, SchemaTopper, name, ModeReadWrite, nil)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		c.tables[name] = r
	}

	denied := map[string]bool{}
	for _, cols := range spec.Denied {
		for _, col := range cols {
			denied[col] = true
		}
	}
	for _, v := range spec.Views {
		r, err := loadView(ctx, pool, c, v)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, col := range r.Columns {
			if denied[col.Name] && !col.Denied {
				errs = append(errs, fmt.Errorf("catalog: view %q exposes denied column %q", v.Name, col.Name))
			}
		}
		if _, dup := c.views[v.Name]; dup {
			errs = append(errs, fmt.Errorf("catalog: view %q declared twice", v.Name))
			continue
		}
		c.views[v.Name] = r
	}

	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

// loadTable introspects one table.
func loadTable(ctx context.Context, pool *pgxpool.Pool, schema, table string, mode Mode, denied []string) (*Relation, error) {
	var tableType string
	err := pool.QueryRow(ctx,
		`SELECT table_type FROM information_schema.tables WHERE table_schema = $1 AND table_name = $2`,
		schema, table).Scan(&tableType)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if mode == ModeReadWrite {
			return nil, fmt.Errorf("catalog: TOPPER_WRITE_TABLES: table %s.%s does not exist (or the topper role cannot see it)", schema, table)
		}
		return nil, fmt.Errorf("catalog: plaidsync table %s.%s does not exist (or the topper role cannot see it)", schema, table)
	case err != nil:
		return nil, fmt.Errorf("catalog: look up %s.%s: %w", schema, table, err)
	case tableType != "BASE TABLE":
		return nil, fmt.Errorf("catalog: %s.%s is a %s, not a table", schema, table, tableType)
	}

	cols, err := loadColumns(ctx, pool, schema, table)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("catalog: %s.%s has no visible columns", schema, table)
	}
	for _, d := range denied {
		found := false
		for i := range cols {
			if cols[i].Name == d {
				cols[i].Denied = true
				found = true
			}
		}
		if !found {
			// Schema drift, not a security problem: the column the
			// denylist protects is not there to expose.
			continue
		}
	}

	pk, err := loadPrimaryKey(ctx, pool, schema, table)
	if err != nil {
		return nil, err
	}

	return NewRelation(Relation{
		Name:      table,
		Kind:      KindTable,
		Mode:      mode,
		Schema:    schema,
		Table:     table,
		Source:    QualifiedName(schema, table),
		Columns:   cols,
		PK:        pk,
		Tiebreak:  slices.Clone(pk),
		Cacheable: mode == ModeRead,
	})
}

// loadColumns reads the column list of a table from information_schema,
// which a role holding SELECT on the table can see.
func loadColumns(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]Column, error) {
	rows, err := pool.Query(ctx, `
		SELECT column_name, ordinal_position, udt_schema, udt_name,
		       is_nullable = 'YES',
		       NOT (is_generated = 'ALWAYS' OR COALESCE(identity_generation, '') = 'ALWAYS')
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("catalog: columns of %s.%s: %w", schema, table, err)
	}
	cols, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Column, error) {
		var c Column
		err := row.Scan(&c.Name, &c.Ordinal, &c.UDTSchema, &c.UDT, &c.Nullable, &c.Writable)
		classify(&c)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("catalog: columns of %s.%s: %w", schema, table, err)
	}
	return cols, nil
}

// loadPrimaryKey reads the primary key columns in key order from pg_index.
// information_schema.table_constraints is not used: it hides tables the
// role holds only SELECT on, which is every plaidsync table.
func loadPrimaryKey(ctx context.Context, pool *pgxpool.Pool, schema, table string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = to_regclass($1) AND i.indisprimary
		ORDER BY array_position(i.indkey::int2[], a.attnum)`,
		QualifiedName(schema, table))
	if err != nil {
		return nil, fmt.Errorf("catalog: primary key of %s.%s: %w", schema, table, err)
	}
	pk, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("catalog: primary key of %s.%s: %w", schema, table, err)
	}
	return pk, nil
}
