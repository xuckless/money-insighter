package query

import (
	"strconv"
	"strings"

	"postgres-topper/internal/catalog"
)

// Statement is SQL with its bound arguments.
type Statement struct {
	SQL  string
	Args []any
}

// BuildSelect renders the paginated SELECT for p over rel and returns it
// with the projection needed to encode its rows.
func BuildSelect(rel *catalog.Relation, p *Params) (Statement, []Projected) {
	exprs, proj := Projection(rel, p.Select)
	var b strings.Builder
	var args []any

	b.WriteString("SELECT ")
	b.WriteString(strings.Join(exprs, ", "))
	b.WriteString(" FROM ")
	b.WriteString(rel.Source)
	writeWhere(&b, rel, p, &args)
	writeOrderBy(&b, rel, p)
	b.WriteString(" LIMIT $")
	args = append(args, p.Limit)
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString(" OFFSET $")
	args = append(args, p.Offset)
	b.WriteString(strconv.Itoa(len(args)))

	return Statement{SQL: b.String(), Args: args}, proj
}

// BuildCount renders SELECT count(*) with the same WHERE as BuildSelect.
func BuildCount(rel *catalog.Relation, p *Params) Statement {
	var b strings.Builder
	var args []any
	b.WriteString("SELECT count(*) FROM ")
	b.WriteString(rel.Source)
	writeWhere(&b, rel, p, &args)
	return Statement{SQL: b.String(), Args: args}
}

// writeWhere appends the WHERE clause: the request conditions, the OR
// groups, the relation's forced predicates and the predicates of every
// toggle that is off. Nothing is written when there is no predicate.
func writeWhere(b *strings.Builder, rel *catalog.Relation, p *Params, args *[]any) {
	preds := userPredicates(p, args)
	preds = append(preds, rel.Forced...)
	for _, t := range rel.Toggles {
		if !p.Toggles[t.Key] {
			preds = append(preds, t.WhenOff)
		}
	}
	if len(preds) == 0 {
		return
	}
	b.WriteString(" WHERE ")
	b.WriteString(strings.Join(preds, " AND "))
}

// userPredicates renders the request's own conditions, binding values into
// args. BuildDelete uses it alone: forced predicates and toggles belong to
// views, which are never deleted from.
func userPredicates(p *Params, args *[]any) []string {
	var preds []string
	for _, c := range p.Conds {
		preds = append(preds, predicate(c, args))
	}
	for _, group := range p.OrGroups {
		parts := make([]string, 0, len(group))
		for _, c := range group {
			parts = append(parts, predicate(c, args))
		}
		preds = append(preds, "("+strings.Join(parts, " OR ")+")")
	}
	return preds
}

// predicate renders one condition, appending its values to args.
func predicate(c Cond, args *[]any) string {
	col := QuoteIdent(c.Col)
	switch c.Op {
	case OpIs:
		if c.NotNull {
			return col + " IS NOT NULL"
		}
		return col + " IS NULL"
	case OpIn:
		ph := make([]string, len(c.Values))
		for i, v := range c.Values {
			*args = append(*args, v)
			ph[i] = "$" + strconv.Itoa(len(*args))
		}
		return col + " IN (" + strings.Join(ph, ", ") + ")"
	default:
		*args = append(*args, c.Values[0])
		return col + " " + sqlOps[c.Op] + " $" + strconv.Itoa(len(*args))
	}
}

// writeOrderBy appends ORDER BY: the requested term, else the relation's
// default order, else the primary key ascending; then every tiebreak
// column not already present, ascending, so paging is stable.
func writeOrderBy(b *strings.Builder, rel *catalog.Relation, p *Params) {
	var terms []catalog.Order
	switch {
	case p.Order != nil:
		terms = []catalog.Order{*p.Order}
	case len(rel.DefaultOrder) > 0:
		terms = append(terms, rel.DefaultOrder...)
	default:
		for _, col := range rel.PK {
			terms = append(terms, catalog.Order{Col: col})
		}
	}
	for _, tb := range rel.Tiebreak {
		present := false
		for _, t := range terms {
			if t.Col == tb {
				present = true
				break
			}
		}
		if !present {
			terms = append(terms, catalog.Order{Col: tb})
		}
	}
	if len(terms) == 0 {
		return
	}
	b.WriteString(" ORDER BY ")
	for i, t := range terms {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(QuoteIdent(t.Col))
		if t.Desc {
			b.WriteString(" DESC")
		} else {
			b.WriteString(" ASC")
		}
	}
}
