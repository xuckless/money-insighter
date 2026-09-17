package query

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"postgres-topper/internal/catalog"
)

// BuildUpsert renders INSERT ... ON CONFLICT ... RETURNING for one decoded
// JSON row. rowIdx is only used in error messages.
//
//   - Every key must be a column of rel (400 otherwise). Columns that
//     cannot be written (generated, identity ALWAYS) are dropped.
//   - Each value is bound as text and cast to the column's type in SQL
//     ($n::"pg_catalog"."numeric"), so Postgres's own input functions
//     parse it and a bad value is a class 22 error, not a Go guess.
//   - When every primary key column is supplied, a conflict updates the
//     other supplied columns (DO UPDATE), or does nothing when only the
//     key was supplied. Otherwise it is a plain INSERT.
//   - RETURNING uses the same projection as a SELECT, so the caller gets
//     the row exactly as a later GET would render it.
func BuildUpsert(rel *catalog.Relation, row map[string]any, rowIdx int) (Statement, []Projected, error) {
	for key := range row {
		c, ok := rel.Column(key)
		if !ok || c.Denied {
			return Statement{}, nil, badRequest("row %d: unknown column %q", rowIdx, key)
		}
	}

	var cols []catalog.Column
	for _, c := range rel.Columns {
		if _, present := row[c.Name]; present && c.Writable {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return Statement{}, nil, badRequest("row %d: no writable columns supplied", rowIdx)
	}

	names := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	args := make([]any, len(cols))
	for i, c := range cols {
		v, err := normalizeArg(row[c.Name], c)
		if err != nil {
			return Statement{}, nil, badRequest("row %d: column %q: %s", rowIdx, c.Name, err.Msg)
		}
		names[i] = QuoteIdent(c.Name)
		placeholders[i] = "$" + strconv.Itoa(i+1) + "::" + typeName(c)
		args[i] = v
	}

	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (%s)", rel.Source, strings.Join(names, ", "), strings.Join(placeholders, ", "))

	if len(rel.PK) > 0 && hasAll(cols, rel.PK) {
		pk := make([]string, len(rel.PK))
		for i, k := range rel.PK {
			pk[i] = QuoteIdent(k)
		}
		var sets []string
		for _, c := range cols {
			if !contains(rel.PK, c.Name) {
				q := QuoteIdent(c.Name)
				sets = append(sets, q+" = EXCLUDED."+q)
			}
		}
		if len(sets) > 0 {
			fmt.Fprintf(&b, " ON CONFLICT (%s) DO UPDATE SET %s", strings.Join(pk, ", "), strings.Join(sets, ", "))
		} else {
			fmt.Fprintf(&b, " ON CONFLICT (%s) DO NOTHING", strings.Join(pk, ", "))
		}
	}

	exprs, proj := Projection(rel, nil)
	b.WriteString(" RETURNING ")
	b.WriteString(strings.Join(exprs, ", "))
	return Statement{SQL: b.String(), Args: args}, proj, nil
}

// typeName renders the cast target for a column: "schema"."udt". For an
// array the udt is the internal array type name ("_text"), which is valid
// in a cast when quoted.
func typeName(c catalog.Column) string {
	schema := c.UDTSchema
	if schema == "" {
		schema = "pg_catalog"
	}
	return QuoteIdent(schema) + "." + QuoteIdent(c.UDT)
}

// normalizeArg renders a decoded JSON value as a text-format bound
// parameter: scalars as their text, objects as JSON text, arrays as JSON
// text for json/jsonb columns and as a Postgres array literal for array
// columns. Numbers must have been decoded with json.Decoder.UseNumber so
// they stay exact.
func normalizeArg(v any, c catalog.Column) (any, *Error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return x, nil
	case bool:
		return strconv.FormatBool(x), nil
	case json.Number:
		return x.String(), nil
	case float64:
		// Only reachable if a caller decodes without UseNumber.
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case []any:
		if c.IsArray() {
			lit, err := arrayLiteral(x)
			if err != nil {
				return nil, err
			}
			return lit, nil
		}
		return marshalJSON(x)
	case map[string]any:
		return marshalJSON(x)
	}
	return nil, badRequest("unsupported JSON value")
}

func marshalJSON(v any) (any, *Error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, badRequest("value is not encodable")
	}
	return string(b), nil
}

// arrayLiteral renders a JSON array as a Postgres array literal: strings
// are double-quoted with backslash escapes, null is NULL, numbers and
// booleans are their text, nested arrays recurse. Objects are refused.
func arrayLiteral(items []any) (string, *Error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		switch x := it.(type) {
		case nil:
			b.WriteString("NULL")
		case string:
			b.WriteByte('"')
			for _, r := range x {
				if r == '"' || r == '\\' {
					b.WriteByte('\\')
				}
				b.WriteRune(r)
			}
			b.WriteByte('"')
		case bool:
			b.WriteString(strconv.FormatBool(x))
		case json.Number:
			b.WriteString(x.String())
		case float64:
			b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
		case []any:
			inner, err := arrayLiteral(x)
			if err != nil {
				return "", err
			}
			b.WriteString(inner)
		default:
			return "", badRequest("array elements must be scalars or arrays")
		}
	}
	b.WriteByte('}')
	return b.String(), nil
}

func hasAll(cols []catalog.Column, names []string) bool {
	for _, n := range names {
		found := false
		for _, c := range cols {
			if c.Name == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
