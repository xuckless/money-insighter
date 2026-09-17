// Package catalog is the registry of everything the API may touch: which
// tables and views exist, in which mode, with which columns, primary keys
// and types. It is built once at startup by [Load] from information_schema
// and the pg_catalog, so every later request validates its column names
// against an in-memory description and never asks Postgres about metadata.
//
// The catalog is the first of the three layers that keep user input out of
// SQL: a table name must be a key of the catalog, a column name must be a
// column of that relation, and a value is always a bound parameter.
package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// Mode says what the API may do with a relation.
type Mode string

const (
	// ModeRead exposes GET only.
	ModeRead Mode = "read"
	// ModeReadWrite additionally exposes POST (upsert) and DELETE.
	ModeReadWrite Mode = "readwrite"
)

// Kind distinguishes tables from the SQL views declared in Go.
type Kind int

const (
	// KindTable is a real table.
	KindTable Kind = iota
	// KindView is a SELECT declared in internal/views, wrapped as a
	// subquery. It is never a database view.
	KindView
)

// ValueKind says how a column travels to JSON. It is decided from the
// column's Postgres type so money and dates never pass through float64.
type ValueKind int

const (
	// ValueNative is scanned by pgx into a Go value that encoding/json
	// renders faithfully: bool, integers, floats, text, bytea (base64),
	// timestamptz (RFC 3339 in UTC).
	ValueNative ValueKind = iota
	// ValueText is selected with ::text and rendered as a JSON string:
	// numeric, date, timestamp, uuid, enums and every other type.
	ValueText
	// ValueRawJSON is selected as JSON text and embedded verbatim: json,
	// jsonb and arrays (through to_jsonb).
	ValueRawJSON
)

// Column describes one column of a relation.
type Column struct {
	// Name is the column name as the API exposes it.
	Name string
	// UDT is the Postgres type name (pg_type.typname): "numeric",
	// "timestamptz", "_text" for text[]. Empty when unknown.
	UDT string
	// UDTSchema is the schema of UDT: "pg_catalog" for built-ins, or the
	// schema of a user-defined type such as an enum.
	UDTSchema string
	// Ordinal is the 1-based position in the table.
	Ordinal int
	// Nullable reports whether the column accepts NULL.
	Nullable bool
	// Writable reports whether an INSERT may supply the column. Generated
	// columns and identity-ALWAYS columns are not writable.
	Writable bool
	// Denied marks a column the API must never select, filter on or
	// order by. It is still listed so writes can reject it by name.
	Denied bool
	// TextLike reports whether LIKE and ILIKE apply.
	TextLike bool
	// Value is how the column travels to JSON.
	Value ValueKind
}

// IsArray reports whether the column is a Postgres array type.
func (c Column) IsArray() bool {
	return strings.HasPrefix(c.UDT, "_")
}

// Order is one ORDER BY term.
type Order struct {
	Col  string
	Desc bool
}

// Toggle is a boolean query parameter of a view that, when off (the
// default), adds a predicate. include_missing on the accounts view is one:
// off means WHERE missing_since IS NULL.
type Toggle struct {
	// Key is the query parameter name.
	Key string
	// WhenOff is a SQL predicate over the relation's output columns,
	// applied while the toggle is off.
	WhenOff string
}

// Relation is one table or view as the API sees it.
type Relation struct {
	// Name is the API name: "transactions" or "transactions/live".
	Name string
	Kind Kind
	Mode Mode

	// Schema and Table locate a table. Empty for SQL views.
	Schema, Table string
	// Source is the FROM clause: a quoted table name, or a parenthesised
	// subquery aliased as v for SQL views.
	Source string

	// Columns lists every column in ordinal order, denied ones included
	// but flagged.
	Columns []Column
	// PK lists the primary key columns of a table, in key order. Empty
	// for views and keyless tables.
	PK []string
	// Tiebreak lists columns appended to every ORDER BY that does not
	// already include them, so paging is stable. Usually the PK.
	Tiebreak []string
	// DefaultOrder applies when the request names no order. Empty means
	// PK ascending.
	DefaultOrder []Order
	// Forced predicates are ANDed into every WHERE.
	Forced []string
	// Toggles are the relation's toggle parameters.
	Toggles []Toggle
	// Cacheable reports whether GET responses may be cached: true for
	// read-only tables and views, false for writable tables.
	Cacheable bool

	byName map[string]int
}

// Column looks a column up by name, denied ones included.
func (r *Relation) Column(name string) (*Column, bool) {
	i, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return &r.Columns[i], true
}

// Exposed returns the non-denied columns in ordinal order.
func (r *Relation) Exposed() []Column {
	out := make([]Column, 0, len(r.Columns))
	for _, c := range r.Columns {
		if !c.Denied {
			out = append(out, c)
		}
	}
	return out
}

// HasToggle reports whether key is one of the relation's toggles.
func (r *Relation) HasToggle(key string) bool {
	for _, t := range r.Toggles {
		if t.Key == key {
			return true
		}
	}
	return false
}

// index rebuilds the name lookup after Columns changes.
func (r *Relation) index() {
	r.byName = make(map[string]int, len(r.Columns))
	for i, c := range r.Columns {
		r.byName[c.Name] = i
	}
}

// ViewSpec declares a view in Go. Exactly one of FromTable and SQL is set.
type ViewSpec struct {
	// Name is the API name under /v1/views/, for example "transactions/live".
	Name string
	// FromTable makes the view a filtered form of a read table: same
	// source, same columns, plus Forced and Toggles.
	FromTable string
	// SQL is a complete SELECT that becomes the subquery source. Its
	// output column names are the view's columns.
	SQL string
	// DefaultOrder, Tiebreak, Forced and Toggles are copied onto the
	// Relation.
	DefaultOrder []Order
	Tiebreak     []string
	Forced       []string
	Toggles      []Toggle
}

// Spec is the input to Load.
type Spec struct {
	// ReadTables are read-only tables in schema public.
	ReadTables []string
	// WriteTables are read-write tables in schema topper.
	WriteTables []string
	// Views are the SQL views to introspect.
	Views []ViewSpec
	// Denied maps a read table name to columns that must never be
	// exposed.
	Denied map[string][]string
}

// PlaidTables are plaidsync's tables, exposed read-only.
var PlaidTables = []string{"plaid_items", "plaid_accounts", "transactions", "sync_jobs", "sync_runs"}

// PlaidDenylist names the columns of plaidsync's tables the API must never
// expose: the encrypted bank credential and its key version.
var PlaidDenylist = map[string][]string{
	"plaid_items": {"encrypted_access_token", "key_version"},
}

// Schema names.
const (
	SchemaPublic = "public"
	SchemaTopper = "topper"
)

// Catalog is the loaded registry.
type Catalog struct {
	tables map[string]*Relation
	views  map[string]*Relation
}

// Table returns the table with the given API name.
func (c *Catalog) Table(name string) (*Relation, bool) {
	r, ok := c.tables[name]
	return r, ok
}

// View returns the view with the given API name.
func (c *Catalog) View(name string) (*Relation, bool) {
	r, ok := c.views[name]
	return r, ok
}

// Tables returns every table sorted by name.
func (c *Catalog) Tables() []*Relation {
	return sorted(c.tables)
}

// Views returns every view sorted by name.
func (c *Catalog) Views() []*Relation {
	return sorted(c.views)
}

func sorted(m map[string]*Relation) []*Relation {
	out := make([]*Relation, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// QuoteIdent double-quotes a SQL identifier. Identifiers reaching it always
// come from the catalog or from information_schema, never from a request,
// but quoting is applied everywhere regardless.
func QuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// QualifiedName returns "schema"."table".
func QualifiedName(schema, table string) string {
	return QuoteIdent(schema) + "." + QuoteIdent(table)
}

// classify fills the type-derived fields of a column from its UDT.
func classify(c *Column) {
	switch c.UDT {
	case "text", "varchar", "bpchar", "name", "citext":
		c.TextLike = true
		c.Value = ValueNative
	case "bool", "int2", "int4", "int8", "float4", "float8", "bytea", "timestamptz":
		c.Value = ValueNative
	case "json", "jsonb":
		c.Value = ValueRawJSON
	default:
		if c.IsArray() {
			c.Value = ValueRawJSON
		} else {
			c.Value = ValueText
		}
	}
}

// NewRelation validates and indexes r. Load uses it for every relation; tests
// use it to build fixtures without a database.
func NewRelation(r Relation) (*Relation, error) {
	r.index()
	if len(r.byName) != len(r.Columns) {
		return nil, fmt.Errorf("catalog: relation %q has duplicate column names", r.Name)
	}
	for _, o := range r.DefaultOrder {
		if c, ok := r.Column(o.Col); !ok || c.Denied {
			return nil, fmt.Errorf("catalog: relation %q: default order column %q is not exposed", r.Name, o.Col)
		}
	}
	for _, t := range r.Tiebreak {
		if c, ok := r.Column(t); !ok || c.Denied {
			return nil, fmt.Errorf("catalog: relation %q: tiebreak column %q is not exposed", r.Name, t)
		}
	}
	return &r, nil
}
