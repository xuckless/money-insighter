package query

import (
	"strings"

	"postgres-topper/internal/catalog"
)

// BuildDelete renders DELETE FROM rel WHERE <request conditions>. A request
// without any condition is refused: a bare DELETE on a table is never what
// a caller meant, and the cost of being wrong is the whole table.
func BuildDelete(rel *catalog.Relation, p *Params) (Statement, error) {
	if len(p.Conds) == 0 && len(p.OrGroups) == 0 {
		return Statement{}, badRequest("refusing to delete without a filter")
	}
	var args []any
	preds := userPredicates(p, &args)
	sql := "DELETE FROM " + rel.Source + " WHERE " + strings.Join(preds, " AND ")
	return Statement{SQL: sql, Args: args}, nil
}
