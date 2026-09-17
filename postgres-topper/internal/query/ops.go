package query

// Op is a filter operator from the PostgREST-style grammar.
type Op string

const (
	OpEq    Op = "eq"
	OpNeq   Op = "neq"
	OpGt    Op = "gt"
	OpGte   Op = "gte"
	OpLt    Op = "lt"
	OpLte   Op = "lte"
	OpLike  Op = "like"
	OpILike Op = "ilike"
	// OpIn compares against a list: col=in.(a,b,c).
	OpIn Op = "in"
	// OpIs tests for NULL: col=is.null or col=is.notnull. It binds nothing.
	OpIs Op = "is"
)

// sqlOps maps the binary operators to SQL. OpIn and OpIs are rendered
// separately.
var sqlOps = map[Op]string{
	OpEq:    "=",
	OpNeq:   "<>",
	OpGt:    ">",
	OpGte:   ">=",
	OpLt:    "<",
	OpLte:   "<=",
	OpLike:  "LIKE",
	OpILike: "ILIKE",
}

// isTextOp reports whether op only applies to text-like columns.
func isTextOp(op Op) bool {
	return op == OpLike || op == OpILike
}
