package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"postgres-topper/internal/catalog"
)

// fixture builds a transactions-like relation without a database.
func fixture(t *testing.T) *catalog.Relation {
	t.Helper()
	cols := []catalog.Column{
		{Name: "transaction_id", UDT: "text", UDTSchema: "pg_catalog", Ordinal: 1, Writable: true},
		{Name: "account_id", UDT: "text", UDTSchema: "pg_catalog", Ordinal: 2, Writable: true},
		{Name: "amount", UDT: "numeric", UDTSchema: "pg_catalog", Ordinal: 3, Writable: true},
		{Name: "date", UDT: "date", UDTSchema: "pg_catalog", Ordinal: 4, Writable: true},
		{Name: "datetime", UDT: "timestamptz", UDTSchema: "pg_catalog", Ordinal: 5, Nullable: true, Writable: true},
		{Name: "pending", UDT: "bool", UDTSchema: "pg_catalog", Ordinal: 6, Writable: true},
		{Name: "raw", UDT: "jsonb", UDTSchema: "pg_catalog", Ordinal: 7, Writable: true},
		{Name: "labels", UDT: "_text", UDTSchema: "pg_catalog", Ordinal: 8, Nullable: true, Writable: true},
		{Name: "job_id", UDT: "uuid", UDTSchema: "pg_catalog", Ordinal: 9, Nullable: true, Writable: true},
		{Name: "seq", UDT: "int8", UDTSchema: "pg_catalog", Ordinal: 10, Writable: false},
		{Name: "secret", UDT: "bytea", UDTSchema: "pg_catalog", Ordinal: 11, Denied: true},
		{Name: "removed_at", UDT: "timestamptz", UDTSchema: "pg_catalog", Ordinal: 12, Nullable: true, Writable: true},
	}
	for i := range cols {
		classifyForTest(&cols[i])
	}
	r, err := catalog.NewRelation(catalog.Relation{
		Name: "transactions", Kind: catalog.KindTable, Mode: catalog.ModeReadWrite,
		Schema: "topper", Table: "transactions", Source: `"topper"."transactions"`,
		Columns: cols, PK: []string{"transaction_id"}, Tiebreak: []string{"transaction_id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// classifyForTest mirrors catalog's type classification for the fixture.
func classifyForTest(c *catalog.Column) {
	switch c.UDT {
	case "text":
		c.TextLike = true
		c.Value = catalog.ValueNative
	case "bool", "int8", "timestamptz", "bytea":
		c.Value = catalog.ValueNative
	case "jsonb":
		c.Value = catalog.ValueRawJSON
	case "_text":
		c.Value = catalog.ValueRawJSON
	default:
		c.Value = catalog.ValueText
	}
}

func view(t *testing.T) *catalog.Relation {
	t.Helper()
	base := fixture(t)
	r, err := catalog.NewRelation(catalog.Relation{
		Name: "transactions/live", Kind: catalog.KindView, Mode: catalog.ModeRead,
		Source: base.Source, Columns: base.Columns, PK: base.PK, Tiebreak: []string{"transaction_id"},
		DefaultOrder: []catalog.Order{{Col: "date", Desc: true}},
		Forced:       []string{`"removed_at" IS NULL`},
		Toggles:      []catalog.Toggle{{Key: "include_pending", WhenOff: `"pending" = false`}},
		Cacheable:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func parse(t *testing.T, rel *catalog.Relation, raw string) *Params {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(q, rel, DefaultOptions)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return p
}

func parseErr(t *testing.T, rel *catalog.Relation, raw string) string {
	t.Helper()
	q, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse(q, rel, DefaultOptions)
	var qe *Error
	if !errors.As(err, &qe) {
		t.Fatalf("Parse(%q) = %v, want *Error", raw, err)
	}
	if qe.Status != 400 {
		t.Errorf("Parse(%q) status = %d", raw, qe.Status)
	}
	return qe.Msg
}

const projection = `"transaction_id", "account_id", "amount"::text AS "amount", "date"::text AS "date", "datetime", "pending", "raw"::text AS "raw", to_jsonb("labels")::text AS "labels", "job_id"::text AS "job_id", "seq", "removed_at"`

func TestBuildSelectShapes(t *testing.T) {
	rel := fixture(t)
	cases := []struct {
		name  string
		query string
		sql   string
		args  []any
	}{
		{"bare", "",
			`SELECT ` + projection + ` FROM "topper"."transactions" ORDER BY "transaction_id" ASC LIMIT $1 OFFSET $2`,
			[]any{100, 0}},
		{"eq and ops", "account_id=acc&amount=gte.10&date=lte.2026-09-09&pending=neq.true&limit=5&offset=10",
			`SELECT ` + projection + ` FROM "topper"."transactions" WHERE "account_id" = $1 AND "amount" >= $2 AND "date" <= $3 AND "pending" <> $4 ORDER BY "transaction_id" ASC LIMIT $5 OFFSET $6`,
			[]any{"acc", "10", "2026-09-09", "true", 5, 10}},
		{"repeated key", "date=gte.2026-01-01&date=lt.2026-02-01",
			`SELECT ` + projection + ` FROM "topper"."transactions" WHERE "date" >= $1 AND "date" < $2 ORDER BY "transaction_id" ASC LIMIT $3 OFFSET $4`,
			[]any{"2026-01-01", "2026-02-01", 100, 0}},
		{"in and is", "account_id=in.(a, b,c)&removed_at=is.null&datetime=is.notnull",
			`SELECT ` + projection + ` FROM "topper"."transactions" WHERE "account_id" IN ($1, $2, $3) AND "datetime" IS NOT NULL AND "removed_at" IS NULL ORDER BY "transaction_id" ASC LIMIT $4 OFFSET $5`,
			[]any{"a", "b", "c", 100, 0}},
		{"like and value with dots", "account_id=like.%25x%25&transaction_id=eq.a.b.c",
			`SELECT ` + projection + ` FROM "topper"."transactions" WHERE "account_id" LIKE $1 AND "transaction_id" = $2 ORDER BY "transaction_id" ASC LIMIT $3 OFFSET $4`,
			[]any{"%x%", "a.b.c", 100, 0}},
		{"or group", "pending=true&or=(amount.gt.100,account_id.in.(a,b),removed_at.is.null)",
			`SELECT ` + projection + ` FROM "topper"."transactions" WHERE "pending" = $1 AND ("amount" > $2 OR "account_id" IN ($3, $4) OR "removed_at" IS NULL) ORDER BY "transaction_id" ASC LIMIT $5 OFFSET $6`,
			[]any{"true", "100", "a", "b", 100, 0}},
		{"select and order", "select=amount,date&order_by=amount&order=DESC",
			`SELECT "amount"::text AS "amount", "date"::text AS "date" FROM "topper"."transactions" ORDER BY "amount" DESC, "transaction_id" ASC LIMIT $1 OFFSET $2`,
			[]any{100, 0}},
		{"order by pk", "order_by=transaction_id&order=desc",
			`SELECT ` + projection + ` FROM "topper"."transactions" ORDER BY "transaction_id" DESC LIMIT $1 OFFSET $2`,
			[]any{100, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := parse(t, rel, tc.query)
			st, proj := BuildSelect(rel, p)
			if st.SQL != tc.sql {
				t.Errorf("SQL:\n got %s\nwant %s", st.SQL, tc.sql)
			}
			if got, want := jsonStr(t, st.Args), jsonStr(t, tc.args); got != want {
				t.Errorf("args = %s, want %s", got, want)
			}
			if p.Select == nil && len(proj) != 11 {
				t.Errorf("projection has %d columns, want 11 (denied excluded)", len(proj))
			}
			for _, pr := range proj {
				if pr.Name == "secret" {
					t.Error("denied column projected")
				}
			}
		})
	}
}

func TestBuildSelectView(t *testing.T) {
	v := view(t)
	p := parse(t, v, "amount=gt.0")
	st, _ := BuildSelect(v, p)
	want := `SELECT ` + projection + ` FROM "topper"."transactions" WHERE "amount" > $1 AND "removed_at" IS NULL AND "pending" = false ORDER BY "date" DESC, "transaction_id" ASC LIMIT $2 OFFSET $3`
	if st.SQL != want {
		t.Errorf("SQL:\n got %s\nwant %s", st.SQL, want)
	}

	p = parse(t, v, "include_pending=1&order_by=amount")
	st, _ = BuildSelect(v, p)
	want = `SELECT ` + projection + ` FROM "topper"."transactions" WHERE "removed_at" IS NULL ORDER BY "amount" ASC, "transaction_id" ASC LIMIT $1 OFFSET $2`
	if st.SQL != want {
		t.Errorf("toggle on SQL:\n got %s\nwant %s", st.SQL, want)
	}

	c := BuildCount(v, parse(t, v, "amount=gt.0&limit=1&offset=5"))
	want = `SELECT count(*) FROM "topper"."transactions" WHERE "amount" > $1 AND "removed_at" IS NULL AND "pending" = false`
	if c.SQL != want || len(c.Args) != 1 {
		t.Errorf("count = %q %v", c.SQL, c.Args)
	}
}

func TestParseErrors(t *testing.T) {
	rel := fixture(t)
	v := view(t)
	cases := []struct {
		rel   *catalog.Relation
		query string
		want  string
	}{
		{rel, "nope=1", `unknown query parameter or column "nope"`},
		{rel, "secret=x", `column "secret" is not accessible`},
		{rel, "select=secret", `column "secret" is not accessible`},
		{rel, "order_by=secret", `not accessible`},
		{rel, "select=amount,amount", "more than once"},
		{rel, "select=", "at least one column"},
		{rel, "amount=like.1", "like requires a text column"},
		{rel, "amount=ilike.1", "ilike requires a text column"},
		{rel, "account_id=in.()", "at least one value"},
		{rel, "account_id=in.(" + strings.Repeat("x,", 200) + "x)", "at most 200 values"},
		{rel, "account_id=in.x", "malformed in filter"},
		{rel, "account_id=is.maybe", "malformed is filter"},
		{rel, "limit=0", "between 1 and 1000"},
		{rel, "limit=1001", "between 1 and 1000"},
		{rel, "limit=abc", "between 1 and 1000"},
		{rel, "offset=-1", "non-negative"},
		{rel, "limit=1&limit=2", "more than once"},
		{rel, "order=desc", "order requires order_by"},
		{rel, "order_by=amount&order=sideways", "asc or desc"},
		{rel, "count=maybe", "exact or none"},
		{rel, "or=amount.gt.1", "parenthesised"},
		{rel, "or=()", "at least one term"},
		{rel, "or=(amount.1)", "amount: expected column.operator.value"},
		{rel, "or=(amount)", "not column.operator.value"},
		{rel, "or=(nope.eq.1)", `unknown query parameter or column "nope"`},
		{rel, "or=(secret.eq.1)", "not accessible"},
		{rel, "or=(amount.like.1)", "like requires a text column"},
		{rel, "include_pending=1", `unknown query parameter or column "include_pending"`},
		{v, "include_pending=maybe", "must be 1 or 0"},
		{v, "include_pending=1&include_pending=0", "more than once"},
	}
	for _, tc := range cases {
		msg := parseErr(t, tc.rel, tc.query)
		if !strings.Contains(msg, tc.want) {
			t.Errorf("Parse(%q) = %q, want it to mention %q", tc.query, msg, tc.want)
		}
	}
}

func TestParseValuesNeverInMessages(t *testing.T) {
	rel := fixture(t)
	msg := parseErr(t, rel, "amount=like.SECRETVALUE")
	if strings.Contains(msg, "SECRETVALUE") {
		t.Errorf("message quotes the value: %q", msg)
	}
}

func TestBuildDelete(t *testing.T) {
	rel := fixture(t)
	if _, err := BuildDelete(rel, parse(t, rel, "")); err == nil || !strings.Contains(err.Error(), "without a filter") {
		t.Errorf("unfiltered delete err = %v", err)
	}
	if _, err := BuildDelete(rel, parse(t, rel, "limit=5&order_by=amount")); err == nil {
		t.Error("delete with only paging parameters accepted")
	}
	st, err := BuildDelete(rel, parse(t, rel, "account_id=a&or=(amount.gt.1,removed_at.is.notnull)&limit=5"))
	if err != nil {
		t.Fatal(err)
	}
	want := `DELETE FROM "topper"."transactions" WHERE "account_id" = $1 AND ("amount" > $2 OR "removed_at" IS NOT NULL)`
	if st.SQL != want || jsonStr(t, st.Args) != `["a","1"]` {
		t.Errorf("delete = %q %v", st.SQL, st.Args)
	}
}

func TestBuildUpsert(t *testing.T) {
	rel := fixture(t)
	row := map[string]any{
		"transaction_id": "t1",
		"amount":         json.Number("12.34"),
		"pending":        true,
		"raw":            map[string]any{"a": json.Number("1")},
		"labels":         []any{"x", `q"uote`, nil, json.Number("3"), []any{"n"}},
		"datetime":       nil,
		"seq":            json.Number("99"), // not writable: dropped
	}
	st, proj, err := BuildUpsert(rel, row, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := `INSERT INTO "topper"."transactions" ("transaction_id", "amount", "datetime", "pending", "raw", "labels") ` +
		`VALUES ($1::"pg_catalog"."text", $2::"pg_catalog"."numeric", $3::"pg_catalog"."timestamptz", $4::"pg_catalog"."bool", $5::"pg_catalog"."jsonb", $6::"pg_catalog"."_text") ` +
		`ON CONFLICT ("transaction_id") DO UPDATE SET "amount" = EXCLUDED."amount", "datetime" = EXCLUDED."datetime", "pending" = EXCLUDED."pending", "raw" = EXCLUDED."raw", "labels" = EXCLUDED."labels" ` +
		`RETURNING ` + projection
	if st.SQL != want {
		t.Errorf("SQL:\n got %s\nwant %s", st.SQL, want)
	}
	if got := jsonStr(t, st.Args); got != `["t1","12.34",null,"true","{\"a\":1}","{\"x\",\"q\\\"uote\",NULL,3,{\"n\"}}"]` {
		t.Errorf("args = %s", got)
	}
	if len(proj) != 11 {
		t.Errorf("returning projection = %d columns", len(proj))
	}

	// Only the key: DO NOTHING.
	st, _, err = BuildUpsert(rel, map[string]any{"transaction_id": "t1"}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.SQL, `ON CONFLICT ("transaction_id") DO NOTHING RETURNING`) {
		t.Errorf("key-only SQL = %s", st.SQL)
	}

	// Key absent: plain insert.
	st, _, err = BuildUpsert(rel, map[string]any{"amount": json.Number("1")}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(st.SQL, "ON CONFLICT") {
		t.Errorf("keyless SQL has ON CONFLICT: %s", st.SQL)
	}

	// Errors.
	if _, _, err := BuildUpsert(rel, map[string]any{"nope": 1}, 7); err == nil || !strings.Contains(err.Error(), `row 7: unknown column "nope"`) {
		t.Errorf("unknown column err = %v", err)
	}
	if _, _, err := BuildUpsert(rel, map[string]any{"secret": "x"}, 0); err == nil || !strings.Contains(err.Error(), "unknown column") {
		t.Errorf("denied column err = %v", err)
	}
	if _, _, err := BuildUpsert(rel, map[string]any{"seq": json.Number("1")}, 0); err == nil || !strings.Contains(err.Error(), "no writable columns") {
		t.Errorf("only unwritable err = %v", err)
	}
	if _, _, err := BuildUpsert(rel, map[string]any{"transaction_id": "t", "labels": []any{map[string]any{}}}, 0); err == nil || !strings.Contains(err.Error(), "array elements") {
		t.Errorf("object in array err = %v", err)
	}
}

func TestEncodeValue(t *testing.T) {
	cases := []struct {
		v    any
		kind catalog.ValueKind
		want string
	}{
		{nil, catalog.ValueNative, "null"},
		{nil, catalog.ValueRawJSON, "null"},
		{"12.34", catalog.ValueText, `"12.34"`},
		{`{"a":1}`, catalog.ValueRawJSON, `{"a":1}`},
		{`["x","y"]`, catalog.ValueRawJSON, `["x","y"]`},
		{true, catalog.ValueNative, "true"},
		{int64(-5), catalog.ValueNative, "-5"},
		{int32(7), catalog.ValueNative, "7"},
		{int16(3), catalog.ValueNative, "3"},
		{float64(1.5), catalog.ValueNative, "1.5"},
		{float32(2), catalog.ValueNative, "2"},
		{"héllo \"q\"", catalog.ValueNative, `"héllo \"q\""`},
		{[]byte{1, 2}, catalog.ValueNative, `"AQI="`},
		{mustTime("2026-09-09T12:00:00+02:00"), catalog.ValueNative, `"2026-09-09T10:00:00Z"`},
		{mustTime("2026-09-09T12:00:00.5Z"), catalog.ValueNative, `"2026-09-09T12:00:00.5Z"`},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		if err := encodeValue(&buf, tc.v, tc.kind); err != nil {
			t.Errorf("encodeValue(%v): %v", tc.v, err)
			continue
		}
		if buf.String() != tc.want {
			t.Errorf("encodeValue(%#v, %v) = %s, want %s", tc.v, tc.kind, buf.String(), tc.want)
		}
	}
	var buf bytes.Buffer
	_ = writeFloat(&buf, nan(), 64)
	if buf.String() != `"NaN"` {
		t.Errorf("NaN = %s", buf.String())
	}
}

func TestSplitTopLevel(t *testing.T) {
	got := splitTopLevel("a.eq.1,b.in.(x,y),c.is.null")
	if jsonStr(t, got) != `["a.eq.1","b.in.(x,y)","c.is.null"]` {
		t.Errorf("splitTopLevel = %v", got)
	}
}

func jsonStr(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
