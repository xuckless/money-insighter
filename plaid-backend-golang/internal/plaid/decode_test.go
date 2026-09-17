package plaid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"plaidsync/internal/money"
)

// fixture reads a plaidtest fixture from disk. In-package tests cannot
// import plaidtest (it imports this package), so they read the files.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("plaidtest", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestDecodeSyncPageExactAmountsAndRaw(t *testing.T) {
	p, err := DecodeSyncPage(fixture(t, "sync_page_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.UpdateStatus != UpdateStatusHistoricalComplete || !p.HasMore || p.NextCursor != "cursor-page-2" || p.RequestID != "req-sync-page-1" {
		t.Errorf("page header = %+v", *p)
	}
	if len(p.Accounts) != 2 || len(p.Added) != 2 || len(p.Modified) != 0 || len(p.Removed) != 0 {
		t.Fatalf("counts: accounts %d added %d modified %d removed %d", len(p.Accounts), len(p.Added), len(p.Modified), len(p.Removed))
	}

	chq := p.Accounts[0]
	if chq.AccountID != "acc-chequing" || chq.Type != "depository" || *chq.Subtype != "checking" || *chq.Mask != "0001" {
		t.Errorf("chequing = %+v", chq)
	}
	if chq.CurrentBalance.String() != "1300.5" || chq.AvailableBalance.String() != "1250.5" || chq.CreditLimit != nil {
		t.Errorf("chequing balances = %v %v %v", chq.CurrentBalance, chq.AvailableBalance, chq.CreditLimit)
	}
	if chq.BalanceLastUpdatedAt != nil || chq.ItemID != "" {
		t.Errorf("chequing last updated = %v, item id %q", chq.BalanceLastUpdatedAt, chq.ItemID)
	}
	credit := p.Accounts[1]
	if credit.CreditLimit.String() != "5000" || credit.CurrentBalance.String() != "850.25" {
		t.Errorf("credit balances = %v %v", credit.CreditLimit, credit.CurrentBalance)
	}
	if credit.BalanceLastUpdatedAt == nil || !credit.BalanceLastUpdatedAt.Equal(time.Date(2026, 9, 15, 3, 12, 44, 0, time.UTC)) {
		t.Errorf("credit last updated = %v", credit.BalanceLastUpdatedAt)
	}

	g := p.Added[0]
	if g.TransactionID != "txn-groceries-1" || g.AccountID != "acc-chequing" || g.Amount != money.MustParse("12.34") {
		t.Errorf("groceries = %+v", g)
	}
	if g.Date.String() != "2026-09-02" || g.AuthorizedDate == nil || g.AuthorizedDate.String() != "2026-09-01" {
		t.Errorf("groceries dates = %v %v", g.Date, g.AuthorizedDate)
	}
	if g.Pending || g.PendingTransactionID != nil || *g.MerchantName != "Loblaws" || *g.MerchantEntityID != "ent-loblaws" {
		t.Errorf("groceries merchant = %+v", g)
	}
	if *g.PFCPrimary != "FOOD_AND_DRINK" || *g.PFCDetailed != "FOOD_AND_DRINK_GROCERIES" || *g.PFCConfidence != "VERY_HIGH" {
		t.Errorf("groceries pfc = %v %v %v", g.PFCPrimary, g.PFCDetailed, g.PFCConfidence)
	}
	if *g.PaymentChannel != "in store" || g.TransactionCode != nil || *g.ISOCurrencyCode != "CAD" || g.UnofficialCurrencyCode != nil {
		t.Errorf("groceries misc = %+v", g)
	}
	// Raw keeps everything, compacted, including fields not in columns.
	var raw map[string]any
	if err := json.Unmarshal(g.Raw, &raw); err != nil {
		t.Fatalf("raw is not JSON: %v", err)
	}
	if _, ok := raw["counterparties"]; !ok {
		t.Error("raw lost counterparties")
	}
	if string(g.Raw)[0] != '{' || len(g.Raw) >= len(fixture(t, "sync_page_1.json")) {
		t.Error("raw should be a compact object")
	}

	u := p.Added[1]
	if !u.Pending || u.Amount != money.MustParse("48.7") || u.DateTime == nil || u.AuthorizedDateTime == nil {
		t.Errorf("uber = %+v", u)
	}
	if !u.DateTime.Equal(time.Date(2026, 9, 10, 18, 5, 0, 0, time.UTC)) {
		t.Errorf("uber datetime = %v", u.DateTime)
	}
}

func TestDecodeSyncPageRemovedAndModified(t *testing.T) {
	p, err := DecodeSyncPage(fixture(t, "sync_page_2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.HasMore || p.NextCursor != "cursor-final" {
		t.Errorf("header = %+v", *p)
	}
	if len(p.Removed) != 1 || p.Removed[0] != (Removed{TransactionID: "txn-uber-pending", AccountID: "acc-credit"}) {
		t.Errorf("removed = %+v", p.Removed)
	}
	posted := p.Added[0]
	if posted.PendingTransactionID == nil || *posted.PendingTransactionID != "txn-uber-pending" || posted.Pending {
		t.Errorf("posted = %+v", posted)
	}
	pay := p.Added[1]
	if pay.Amount != money.MustParse("-2500") || !pay.Amount.IsNegative() || pay.MerchantName != nil || *pay.TransactionCode != "direct debit" {
		t.Errorf("payroll = %+v", pay)
	}
	if len(p.Modified) != 1 || *p.Modified[0].MerchantName != "Loblaws Supermarket" {
		t.Errorf("modified = %+v", p.Modified)
	}
}

func TestDecodeSyncPageNotReady(t *testing.T) {
	p, err := DecodeSyncPage(fixture(t, "sync_not_ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if p.UpdateStatus != UpdateStatusNotReady || p.HasMore || p.NextCursor != "" {
		t.Errorf("page = %+v", *p)
	}
	if p.Accounts == nil || p.Added == nil || p.Modified == nil || p.Removed == nil {
		t.Error("empty lists should be non-nil")
	}
}

func TestDecodeAccountsGet(t *testing.T) {
	a, err := DecodeAccountsGet(fixture(t, "accounts_get.json"))
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestID != "req-accounts-get" || len(a.Accounts) != 2 {
		t.Fatalf("accounts = %+v", *a)
	}
	if a.Accounts[0].Name != "CIBC Smart Account" || a.Accounts[1].OfficialName == nil || *a.Accounts[1].OfficialName != "American Express Cobalt Card" {
		t.Errorf("accounts = %+v", a.Accounts)
	}
	var raw map[string]any
	if err := json.Unmarshal(a.Accounts[0].Raw, &raw); err != nil || raw["holder_category"] != "personal" {
		t.Errorf("raw = %s (%v)", a.Accounts[0].Raw, err)
	}
}

func TestDecodeItemGet(t *testing.T) {
	i, err := DecodeItemGet(fixture(t, "item_get.json"))
	if err != nil {
		t.Fatal(err)
	}
	if i.ItemID != "item-cibc-1" || *i.InstitutionID != "ins_37" || *i.InstitutionName != "CIBC" || i.RequestID != "req-item-get" {
		t.Errorf("item = %+v", *i)
	}
	if i.Error != nil || i.ConsentExpiresAt != nil || i.Webhook == nil {
		t.Errorf("item error/consent/webhook = %v %v %v", i.Error, i.ConsentExpiresAt, i.Webhook)
	}
	if len(i.ConsentedProducts) != 2 || i.ConsentedProducts[1] != "liabilities" || len(i.Products) != 1 {
		t.Errorf("products = %v %v", i.Products, i.ConsentedProducts)
	}
	var raw map[string]any
	if err := json.Unmarshal(i.Raw, &raw); err != nil || raw["update_type"] != "background" {
		t.Errorf("raw = %s", i.Raw)
	}
	if _, ok := raw["status"]; ok {
		t.Error("raw should be the item object only, not the whole response")
	}

	e, err := DecodeItemGet(fixture(t, "item_get_login_required.json"))
	if err != nil {
		t.Fatal(err)
	}
	if e.Error == nil || e.Error.Code != "ITEM_LOGIN_REQUIRED" || e.Error.Type != "ITEM_ERROR" || e.Error.Class() != ClassNeedsReauth {
		t.Errorf("standing error = %+v", e.Error)
	}
	if e.ConsentExpiresAt == nil || !e.ConsentExpiresAt.Equal(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("consent = %v", e.ConsentExpiresAt)
	}
}

func TestDecodeVerificationKey(t *testing.T) {
	k, err := DecodeVerificationKey(fixture(t, "webhook_key.json"))
	if err != nil {
		t.Fatal(err)
	}
	if k.KeyID != "6c5516e1-92dc-479e-a8ff-5a51992e0001" || k.Algorithm != "ES256" || k.Curve != "P-256" || k.KeyType != "EC" {
		t.Errorf("key = %+v", *k)
	}
	if k.ExpiredAt != nil || !k.CreatedAt.Equal(time.Unix(1560466143, 0)) || k.X == "" || k.Y == "" {
		t.Errorf("key times = %+v", *k)
	}
}

func TestDecodeError(t *testing.T) {
	e, err := DecodeError(fixture(t, "error_mutation.json"), "/transactions/sync", 400)
	if err != nil {
		t.Fatal(err)
	}
	if e.Code != "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION" || e.Type != "TRANSACTIONS_ERROR" || e.HTTPStatus != 400 || e.RequestID != "req-err-mutation" {
		t.Errorf("error = %+v", *e)
	}
	if e.Class() != ClassRetryable {
		t.Errorf("class = %v", e.Class())
	}
	if _, err := DecodeError([]byte(`<html>bad gateway</html>`), "/x", 502); err == nil {
		t.Error("non-JSON body should not decode as a Plaid error")
	}
	if _, err := DecodeError([]byte(`{"request_id":"x"}`), "/x", 200); err == nil {
		t.Error("an object without type or code is not a Plaid error")
	}
}

func TestDecodeRejectsBrokenRows(t *testing.T) {
	cases := map[string]string{
		"missing id":    `{"account_id":"a","amount":1,"date":"2026-01-01","name":"x"}`,
		"bad amount":    `{"transaction_id":"t","account_id":"a","amount":"abc","date":"2026-01-01","name":"x"}`,
		"bad date":      `{"transaction_id":"t","account_id":"a","amount":1,"date":"01/02/2026","name":"x"}`,
		"no date":       `{"transaction_id":"t","account_id":"a","amount":1,"name":"x"}`,
		"no amount":     `{"transaction_id":"t","account_id":"a","date":"2026-01-01","name":"x"}`,
		"bad datetime":  `{"transaction_id":"t","account_id":"a","amount":1,"date":"2026-01-01","name":"x","datetime":"yesterday"}`,
		"not an object": `[1,2]`,
	}
	for name, body := range cases {
		if _, err := DecodeTransaction(json.RawMessage(body)); err == nil {
			t.Errorf("%s: DecodeTransaction accepted %s", name, body)
		}
	}
	if _, err := DecodeAccount(json.RawMessage(`{"account_id":"a","name":"","type":"depository","balances":{}}`)); err == nil {
		t.Error("account without a name accepted")
	}
	if _, err := DecodeAccount(json.RawMessage(`{"account_id":"a","name":"n","type":"depository","balances":{"current":"abc"}}`)); err == nil {
		t.Error("string balance accepted")
	}
	if _, err := DecodeSyncPage([]byte(`{"added":[{"transaction_id":""}]}`)); err == nil {
		t.Error("page with a broken row accepted")
	}
	if _, err := DecodeItemGet([]byte(`{"request_id":"r"}`)); err == nil {
		t.Error("item response without item accepted")
	}
}

func TestDecodeAmountKeepsExactDigits(t *testing.T) {
	// A float64 round trip would turn 0.1+0.2-style values into long
	// fractions; decoding from the JSON text must not.
	body := `{"transaction_id":"t","account_id":"a","amount":1234567.89,"date":"2026-01-01","name":"x"}`
	tx, err := DecodeTransaction(json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	if tx.Amount.String() != "1234567.89" {
		t.Errorf("amount = %s", tx.Amount)
	}
	body = `{"transaction_id":"t","account_id":"a","amount":1e2,"date":"2026-01-01","name":"x"}`
	if tx, err = DecodeTransaction(json.RawMessage(body)); err != nil || tx.Amount.String() != "100" {
		t.Errorf("exponent amount = %v, %v", tx.Amount, err)
	}
}

func TestDecodeRecurringGet(t *testing.T) {
	r, err := DecodeRecurringGet(fixture(t, "recurring_get.json"))
	if err != nil {
		t.Fatal(err)
	}
	if r.RequestID != "req-recurring-1" || len(r.Streams) != 2 {
		t.Fatalf("request id %q, %d streams", r.RequestID, len(r.Streams))
	}

	pay := r.Streams[0]
	if pay.StreamID != "stream-payroll" || pay.Direction != "inflow" || pay.AccountID != "acc-chequing" || pay.ItemID != "" {
		t.Errorf("payroll = %+v", pay)
	}
	if pay.Frequency != "BIWEEKLY" || pay.Status != "MATURE" || !pay.IsActive {
		t.Errorf("payroll frequency/status = %s %s %v", pay.Frequency, pay.Status, pay.IsActive)
	}
	if *pay.AverageAmount != money.MustParse("-3050.00") || pay.ISOCurrencyCode == nil || *pay.ISOCurrencyCode != "CAD" {
		t.Errorf("payroll amount = %v %v", pay.AverageAmount, pay.ISOCurrencyCode)
	}
	if pay.FirstDate.String() != "2026-03-13" || pay.PredictedNextDate == nil || pay.PredictedNextDate.String() != "2026-09-25" {
		t.Errorf("payroll dates = %v %v", pay.FirstDate, pay.PredictedNextDate)
	}
	if pay.PFCDetailed == nil || *pay.PFCDetailed != "INCOME_WAGES" || len(pay.TransactionIDs) != 2 {
		t.Errorf("payroll pfc/txns = %v %v", pay.PFCDetailed, pay.TransactionIDs)
	}

	sp := r.Streams[1]
	if sp.Direction != "outflow" || sp.MerchantName == nil || *sp.MerchantName != "Spotify" {
		t.Errorf("spotify = %+v", sp)
	}
	if sp.AverageAmount.String() != "11.99" || sp.LastAmount.String() != "12.99" {
		t.Errorf("spotify amounts = %v %v", sp.AverageAmount, sp.LastAmount)
	}
	var raw map[string]any
	if err := json.Unmarshal(sp.Raw, &raw); err != nil || raw["stream_id"] != "stream-spotify" {
		t.Errorf("spotify raw = %s (%v)", sp.Raw, err)
	}
}

func TestDecodeRecurringStreamRoundsComputedAmounts(t *testing.T) {
	body := `{"stream_id":"s","account_id":"a","first_date":"2026-01-01","last_date":"2026-02-01",
		"average_amount":{"amount":89.40000000000002},"last_amount":{"amount":-3050.005}}`
	s, err := DecodeRecurringStream(json.RawMessage(body), "outflow")
	if err != nil {
		t.Fatal(err)
	}
	if s.AverageAmount.String() != "89.4" && s.AverageAmount.String() != "89.40" {
		t.Errorf("average = %s, want 89.40", s.AverageAmount)
	}
	if s.LastAmount.String() != "-3050.01" {
		t.Errorf("last = %s, want -3050.01 (half away from zero)", s.LastAmount)
	}
	if s.Frequency != "UNKNOWN" || s.Status != "UNKNOWN" {
		t.Errorf("defaults = %s / %s", s.Frequency, s.Status)
	}
}

func TestDecodeRecurringStreamRejectsIncomplete(t *testing.T) {
	for name, body := range map[string]string{
		"no stream id":  `{"account_id":"a","first_date":"2026-01-01","last_date":"2026-02-01"}`,
		"no account id": `{"stream_id":"s","first_date":"2026-01-01","last_date":"2026-02-01"}`,
		"no dates":      `{"stream_id":"s","account_id":"a"}`,
		"bad amount":    `{"stream_id":"s","account_id":"a","first_date":"2026-01-01","last_date":"2026-02-01","average_amount":{"amount":"x"}}`,
	} {
		if _, err := DecodeRecurringStream(json.RawMessage(body), "outflow"); err == nil {
			t.Errorf("%s: decoded without error", name)
		}
	}
}
