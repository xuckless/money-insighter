package catalog_test

import (
	"strings"
	"testing"

	"postgres-topper/internal/catalog"
	"postgres-topper/internal/testdb"
)

func column(t *testing.T, r *catalog.Relation, name string) catalog.Column {
	t.Helper()
	c, ok := r.Column(name)
	if !ok {
		t.Fatalf("%s: column %q missing", r.Name, name)
	}
	return *c
}

func TestLoadPlaidTables(t *testing.T) {
	db := testdb.New(t)
	db.CreateConsumerTables(t)

	cat, err := catalog.Load(testdb.Ctx(t), db.Pool, catalog.Spec{
		ReadTables:  catalog.PlaidTables,
		WriteTables: []string{"tx_notes", "events"},
		Denied:      catalog.PlaidDenylist,
		Views: []catalog.ViewSpec{
			{Name: "transactions/live", FromTable: "transactions", Forced: []string{`"removed_at" IS NULL`}},
			{Name: "sums", SQL: `SELECT item_id, sum(amount) AS total, count(*) AS n, array_agg(transaction_id) AS ids, jsonb_agg(raw) AS raws, gen_random_uuid() AS u FROM public.transactions GROUP BY item_id`, Tiebreak: []string{"item_id"}},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := len(cat.Tables()); got != 7 {
		t.Errorf("tables = %d, want 7", got)
	}

	tx, _ := cat.Table("transactions")
	if tx.Mode != catalog.ModeRead || !tx.Cacheable || tx.Source != `"public"."transactions"` {
		t.Errorf("transactions relation = %+v", tx)
	}
	if got := strings.Join(tx.PK, ","); got != "transaction_id" {
		t.Errorf("transactions PK = %q", got)
	}
	cases := map[string]struct {
		udt   string
		value catalog.ValueKind
		text  bool
	}{
		"amount":        {"numeric", catalog.ValueText, false},
		"date":          {"date", catalog.ValueText, false},
		"datetime":      {"timestamptz", catalog.ValueNative, false},
		"raw":           {"jsonb", catalog.ValueRawJSON, false},
		"pending":       {"bool", catalog.ValueNative, false},
		"name":          {"text", catalog.ValueNative, true},
		"removed_at":    {"timestamptz", catalog.ValueNative, false},
		"merchant_name": {"text", catalog.ValueNative, true},
	}
	for name, want := range cases {
		c := column(t, tx, name)
		if c.UDT != want.udt || c.Value != want.value || c.TextLike != want.text || c.Denied {
			t.Errorf("transactions.%s = %+v, want udt %s value %v textlike %v", name, c, want.udt, want.value, want.text)
		}
	}

	items, _ := cat.Table("plaid_items")
	for _, name := range []string{"encrypted_access_token", "key_version"} {
		if c := column(t, items, name); !c.Denied {
			t.Errorf("plaid_items.%s is not denied", name)
		}
	}
	for _, c := range items.Exposed() {
		if c.Name == "encrypted_access_token" || c.Name == "key_version" {
			t.Errorf("Exposed lists %s", c.Name)
		}
	}
	if got := len(items.Exposed()); got != len(items.Columns)-2 {
		t.Errorf("exposed = %d, columns = %d", got, len(items.Columns))
	}

	jobs, _ := cat.Table("sync_jobs")
	if c := column(t, jobs, "job_id"); c.UDT != "uuid" || c.Value != catalog.ValueText {
		t.Errorf("sync_jobs.job_id = %+v", c)
	}
	runs, _ := cat.Table("sync_runs")
	if c := column(t, runs, "run_id"); c.UDT != "int8" || c.Value != catalog.ValueNative || !c.Writable {
		t.Errorf("sync_runs.run_id = %+v", c)
	}

	notes, _ := cat.Table("tx_notes")
	if notes.Mode != catalog.ModeReadWrite || notes.Cacheable || notes.Schema != "topper" {
		t.Errorf("tx_notes = %+v", notes)
	}
	if c := column(t, notes, "labels"); c.UDT != "_text" || c.Value != catalog.ValueRawJSON || !c.IsArray() {
		t.Errorf("tx_notes.labels = %+v", c)
	}
	if c := column(t, notes, "note"); !c.Nullable || !c.Writable {
		t.Errorf("tx_notes.note = %+v", c)
	}
	if c := column(t, notes, "tags"); c.Nullable {
		t.Errorf("tx_notes.tags nullable = %+v", c)
	}
	events, _ := cat.Table("events")
	if c := column(t, events, "event_id"); c.Writable {
		t.Errorf("identity ALWAYS column is writable: %+v", c)
	}
	if got := strings.Join(events.PK, ","); got != "event_id" {
		t.Errorf("events PK = %q", got)
	}

	live, ok := cat.View("transactions/live")
	if !ok {
		t.Fatal("view transactions/live missing")
	}
	if live.Kind != catalog.KindView || live.Source != tx.Source || len(live.Columns) != len(tx.Columns) || strings.Join(live.Tiebreak, ",") != "transaction_id" {
		t.Errorf("live view = %+v", live)
	}

	sums, ok := cat.View("sums")
	if !ok {
		t.Fatal("view sums missing")
	}
	if !strings.HasPrefix(sums.Source, "(SELECT") || !strings.HasSuffix(sums.Source, ") AS v") {
		t.Errorf("sums source = %q", sums.Source)
	}
	want := map[string]struct {
		udt   string
		value catalog.ValueKind
	}{
		"item_id": {"text", catalog.ValueNative},
		"total":   {"numeric", catalog.ValueText},
		"n":       {"int8", catalog.ValueNative},
		"ids":     {"_text", catalog.ValueRawJSON},
		"raws":    {"jsonb", catalog.ValueRawJSON},
		"u":       {"uuid", catalog.ValueText},
	}
	for name, w := range want {
		c := column(t, sums, name)
		if c.UDT != w.udt || c.Value != w.value {
			t.Errorf("sums.%s = %+v, want %s %v", name, c, w.udt, w.value)
		}
	}
	if got := len(cat.Views()); got != 2 {
		t.Errorf("views = %d", got)
	}
}

func TestLoadErrors(t *testing.T) {
	db := testdb.New(t)
	ctx := testdb.Ctx(t)

	_, err := catalog.Load(ctx, db.Pool, catalog.Spec{
		ReadTables:  []string{"transactions", "nope"},
		WriteTables: []string{"missing", "transactions"},
		Views: []catalog.ViewSpec{
			{Name: "bad", SQL: `SELECT * FROM public.nowhere`},
			{Name: "leak", SQL: `SELECT encrypted_access_token FROM public.plaid_items`},
			{Name: "both", FromTable: "transactions", SQL: "SELECT 1"},
			{Name: "orphan", FromTable: "plaid_accounts"},
		},
		Denied: catalog.PlaidDenylist,
	})
	if err == nil {
		t.Fatal("Load succeeded with a broken spec")
	}
	msg := err.Error()
	for _, want := range []string{
		"plaidsync table public.nope does not exist",
		"TOPPER_WRITE_TABLES: table topper.missing does not exist",
		`write table "transactions" collides`,
		`view "bad"`,
		`view "leak" exposes denied column "encrypted_access_token"`,
		`view "both" must set exactly one`,
		`view "orphan" is based on unknown table`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}
