package query

import (
	"encoding/json"

	"postgres-topper/internal/catalog"
)

// Projected describes one output column of a statement: its name, how to
// encode the scanned value, and its name pre-encoded as a JSON key.
type Projected struct {
	Name string
	Kind catalog.ValueKind
	key  []byte
}

// QuoteIdent is catalog.QuoteIdent, re-exported for the builders.
var QuoteIdent = catalog.QuoteIdent

// Projection builds the SELECT list for rel restricted to sel (nil means
// every exposed column). Each column is rendered by projectionExpr so its
// JSON form is exact, and denied columns are never included even if named.
func Projection(rel *catalog.Relation, sel []string) (exprs []string, proj []Projected) {
	var cols []catalog.Column
	if sel == nil {
		cols = rel.Exposed()
	} else {
		for _, name := range sel {
			if c, ok := rel.Column(name); ok && !c.Denied {
				cols = append(cols, *c)
			}
		}
	}
	exprs = make([]string, 0, len(cols))
	proj = make([]Projected, 0, len(cols))
	for _, c := range cols {
		exprs = append(exprs, projectionExpr(c))
		proj = append(proj, newProjected(c))
	}
	return exprs, proj
}

// newProjected builds the Projected entry for a column.
func newProjected(c catalog.Column) Projected {
	key, _ := json.Marshal(c.Name)
	return Projected{Name: c.Name, Kind: c.Value, key: key}
}

// projectionExpr renders one column for a SELECT list:
//
//	native types            "c"
//	text-rendered types     "c"::text AS "c"      numeric, date, uuid, enums, ...
//	json and jsonb          "c"::text AS "c"      embedded verbatim
//	arrays                  to_jsonb("c")::text AS "c"
func projectionExpr(c catalog.Column) string {
	q := QuoteIdent(c.Name)
	switch c.Value {
	case catalog.ValueNative:
		return q
	case catalog.ValueRawJSON:
		if c.IsArray() {
			return "to_jsonb(" + q + ")::text AS " + q
		}
		return q + "::text AS " + q
	default:
		return q + "::text AS " + q
	}
}
