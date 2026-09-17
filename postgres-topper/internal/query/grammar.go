package query

import (
	"net/url"
	"sort"
	"strconv"
	"strings"

	"postgres-topper/internal/catalog"
)

// Cond is one filter condition on a column.
type Cond struct {
	Col string
	Op  Op
	// Values holds one value for the binary operators, every value for
	// OpIn, and nothing for OpIs.
	Values []string
	// NotNull distinguishes is.notnull from is.null.
	NotNull bool
}

// Params is a parsed and validated request.
type Params struct {
	// Select lists the requested columns, or nil for every exposed column.
	Select []string
	// Conds are ANDed together.
	Conds []Cond
	// OrGroups are ANDed with Conds; the conditions inside a group are
	// ORed.
	OrGroups [][]Cond
	// Order is the requested ORDER BY term, or nil for the relation's
	// default.
	Order  *catalog.Order
	Limit  int
	Offset int
	// Count requests the total row count (count=exact).
	Count bool
	// Toggles holds the value of each toggle parameter, false when absent.
	Toggles map[string]bool
}

// Options bounds what a request may ask for.
type Options struct {
	DefaultLimit int
	MaxLimit     int
	MaxInValues  int
}

// DefaultOptions are the documented limits.
var DefaultOptions = Options{DefaultLimit: 100, MaxLimit: 1000, MaxInValues: 200}

// Reserved query parameter names, which are never column filters.
const (
	keySelect  = "select"
	keyOrderBy = "order_by"
	keyOrder   = "order"
	keyLimit   = "limit"
	keyOffset  = "offset"
	keyCount   = "count"
	keyOr      = "or"
)

// Parse validates q against rel and returns the request parameters.
//
// The grammar (values are URL-decoded before parsing):
//
//	col=value            equality
//	col=op.value         op in eq neq gt gte lt lte like ilike
//	col=in.(a,b,c)       membership; values may not contain commas or parens
//	col=is.null          col=is.notnull
//	or=(col.op.value,col.in.(a,b),col.is.null)   one OR group
//	select=col1,col2     projection
//	order_by=col&order=asc|desc
//	limit=N&offset=N     1..MaxLimit, >= 0
//	count=exact          include the total
//	<toggle>=1|0         view toggles
//
// Every other key must be an exposed column of rel. A repeated column key
// yields one condition per value.
func Parse(q url.Values, rel *catalog.Relation, o Options) (*Params, error) {
	p := &Params{Limit: o.DefaultLimit, Toggles: map[string]bool{}}
	for _, t := range rel.Toggles {
		p.Toggles[t.Key] = false
	}

	single := func(key string) (string, bool, error) {
		vals, ok := q[key]
		if !ok {
			return "", false, nil
		}
		if len(vals) > 1 {
			return "", true, badRequest("query parameter %q is specified more than once", key)
		}
		return vals[0], true, nil
	}

	if v, ok, err := single(keySelect); err != nil {
		return nil, err
	} else if ok {
		if p.Select, err = parseSelect(v, rel); err != nil {
			return nil, err
		}
	}

	if v, ok, err := single(keyOrderBy); err != nil {
		return nil, err
	} else if ok {
		if _, err := exposedColumn(rel, v); err != nil {
			return nil, err
		}
		p.Order = &catalog.Order{Col: v}
	}
	if v, ok, err := single(keyOrder); err != nil {
		return nil, err
	} else if ok {
		if p.Order == nil {
			return nil, badRequest("order requires order_by")
		}
		switch strings.ToLower(v) {
		case "asc":
		case "desc":
			p.Order.Desc = true
		default:
			return nil, badRequest("order must be asc or desc")
		}
	}

	if v, ok, err := single(keyLimit); err != nil {
		return nil, err
	} else if ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > o.MaxLimit {
			return nil, badRequest("limit must be an integer between 1 and %d", o.MaxLimit)
		}
		p.Limit = n
	}
	if v, ok, err := single(keyOffset); err != nil {
		return nil, err
	} else if ok {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, badRequest("offset must be a non-negative integer")
		}
		p.Offset = n
	}
	if v, ok, err := single(keyCount); err != nil {
		return nil, err
	} else if ok {
		switch strings.ToLower(v) {
		case "exact":
			p.Count = true
		case "none", "":
		default:
			return nil, badRequest("count must be exact or none")
		}
	}

	for _, v := range q[keyOr] {
		group, err := parseOrGroup(v, rel, o)
		if err != nil {
			return nil, err
		}
		p.OrGroups = append(p.OrGroups, group)
	}

	// Everything else is a toggle or a column filter. Keys are visited in
	// sorted order so the generated SQL is deterministic and cacheable.
	keys := make([]string, 0, len(q))
	for k := range q {
		switch k {
		case keySelect, keyOrderBy, keyOrder, keyLimit, keyOffset, keyCount, keyOr:
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if rel.HasToggle(key) {
			v, _, err := single(key)
			if err != nil {
				return nil, err
			}
			b, err := parseToggle(v)
			if err != nil {
				return nil, badRequest("%s must be 1 or 0", key)
			}
			p.Toggles[key] = b
			continue
		}
		col, err := exposedColumn(rel, key)
		if err != nil {
			return nil, err
		}
		for _, v := range q[key] {
			c, perr := parseFilter(v, true)
			if perr != nil {
				return nil, badRequest("%s: %s", key, perr.Msg)
			}
			c.Col = key
			if err := checkCond(c, col, o); err != nil {
				return nil, err
			}
			p.Conds = append(p.Conds, c)
		}
	}
	return p, nil
}

// exposedColumn returns the column named name, or a 400 Error when it does
// not exist or is denied. The error for a denied column deliberately says
// "not accessible" rather than pretending the column does not exist, since
// the schema is documented anyway.
func exposedColumn(rel *catalog.Relation, name string) (*catalog.Column, error) {
	c, ok := rel.Column(name)
	if !ok {
		return nil, badRequest("unknown query parameter or column %q", name)
	}
	if c.Denied {
		return nil, badRequest("column %q is not accessible", name)
	}
	return c, nil
}

// parseSelect parses the select= list.
func parseSelect(v string, rel *catalog.Relation) ([]string, error) {
	if strings.TrimSpace(v) == "" {
		return nil, badRequest("select must list at least one column")
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, name := range parts {
		name = strings.TrimSpace(name)
		if _, err := exposedColumn(rel, name); err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, badRequest("select lists %q more than once", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

// parseToggle accepts 1/0, true/false, yes/no, on/off.
func parseToggle(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, badRequest("bad toggle")
}

// parseFilter parses the value side of a filter. allowBare permits a plain
// value (meaning eq); inside or=(...) an operator is mandatory.
func parseFilter(raw string, allowBare bool) (Cond, *Error) {
	if strings.HasPrefix(raw, "in.(") && strings.HasSuffix(raw, ")") {
		inner := raw[len("in.(") : len(raw)-1]
		if strings.TrimSpace(inner) == "" {
			return Cond{}, badRequest("in.() needs at least one value")
		}
		vals := strings.Split(inner, ",")
		for i := range vals {
			vals[i] = strings.TrimSpace(vals[i])
		}
		return Cond{Op: OpIn, Values: vals}, nil
	}
	if raw == "is.null" {
		return Cond{Op: OpIs}, nil
	}
	if raw == "is.notnull" {
		return Cond{Op: OpIs, NotNull: true}, nil
	}
	if op, val, ok := strings.Cut(raw, "."); ok {
		if _, known := sqlOps[Op(op)]; known {
			return Cond{Op: Op(op), Values: []string{val}}, nil
		}
		if op == string(OpIn) || op == string(OpIs) {
			return Cond{}, badRequest("malformed %s filter", op)
		}
	}
	if !allowBare {
		return Cond{}, badRequest("expected column.operator.value")
	}
	return Cond{Op: OpEq, Values: []string{raw}}, nil
}

// checkCond applies the type rules to a parsed condition.
func checkCond(c Cond, col *catalog.Column, o Options) error {
	if isTextOp(c.Op) && !col.TextLike {
		return badRequest("%s: %s requires a text column", c.Col, c.Op)
	}
	if c.Op == OpIn && len(c.Values) > o.MaxInValues {
		return badRequest("%s: in.() may list at most %d values", c.Col, o.MaxInValues)
	}
	return nil
}

// parseOrGroup parses or=(term,term,...). Terms are split on commas at
// parenthesis depth zero so in.(a,b) inside a group survives.
func parseOrGroup(raw string, rel *catalog.Relation, o Options) ([]Cond, error) {
	if !strings.HasPrefix(raw, "(") || !strings.HasSuffix(raw, ")") {
		return nil, badRequest("or must be a parenthesised list such as or=(a.eq.1,b.is.null)")
	}
	var group []Cond
	for _, term := range splitTopLevel(raw[1 : len(raw)-1]) {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		colName, rest, ok := strings.Cut(term, ".")
		if !ok || colName == "" {
			return nil, badRequest("or: term %q is not column.operator.value", term)
		}
		col, err := exposedColumn(rel, colName)
		if err != nil {
			return nil, badRequest("or: %s", err.(*Error).Msg)
		}
		c, perr := parseFilter(rest, false)
		if perr != nil {
			return nil, badRequest("or: %s: %s", colName, perr.Msg)
		}
		c.Col = colName
		if err := checkCond(c, col, o); err != nil {
			return nil, badRequest("or: %s", err.(*Error).Msg)
		}
		group = append(group, c)
	}
	if len(group) == 0 {
		return nil, badRequest("or=() needs at least one term")
	}
	return group, nil
}

// splitTopLevel splits s on commas that are not inside parentheses.
func splitTopLevel(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if start <= len(s) {
		parts = append(parts, s[start:])
	}
	return parts
}
