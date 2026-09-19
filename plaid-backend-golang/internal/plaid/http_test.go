package plaid

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/secret"
)

// plaidServer is an httptest server that plays Plaid: it records every
// request and answers each path from a scripted response.
type plaidServer struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	responses map[string][]scripted // by path, consumed in order
	requests  []recorded
}

type scripted struct {
	status int
	body   []byte
}

type recorded struct {
	path   string
	header http.Header
	body   map[string]any
}

func newPlaidServer(t *testing.T) *plaidServer {
	t.Helper()
	ps := &plaidServer{t: t, responses: make(map[string][]scripted)}
	ps.srv = httptest.NewServer(http.HandlerFunc(ps.handle))
	t.Cleanup(ps.srv.Close)
	return ps
}

func (ps *plaidServer) on(path string, status int, body []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.responses[path] = append(ps.responses[path], scripted{status, body})
}

func (ps *plaidServer) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	ps.mu.Lock()
	ps.requests = append(ps.requests, recorded{path: r.URL.Path, header: r.Header.Clone(), body: body})
	q := ps.responses[r.URL.Path]
	if len(q) == 0 {
		ps.mu.Unlock()
		http.Error(w, `{"error_type":"INVALID_REQUEST","error_code":"UNSCRIPTED","error_message":"no response scripted for `+r.URL.Path+`"}`, http.StatusBadRequest)
		return
	}
	ps.responses[r.URL.Path] = q[1:]
	ps.mu.Unlock()

	if r.Method != http.MethodPost {
		ps.t.Errorf("%s: method %s, want POST", r.URL.Path, r.Method)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(q[0].status)
	_, _ = w.Write(q[0].body)
}

func (ps *plaidServer) last() recorded {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if len(ps.requests) == 0 {
		ps.t.Fatal("no requests recorded")
	}
	return ps.requests[len(ps.requests)-1]
}

func testPlaidConfig(env config.PlaidEnv) config.PlaidConfig {
	return config.PlaidConfig{
		ClientID:                    "client-id-123",
		Secret:                      secret.NewToken("secret-456"),
		Env:                         env,
		RedirectURI:                 "https://app.example/oauth",
		WebhookURL:                  "https://hooks.example/v1/webhooks/plaid",
		CountryCodes:                []string{"CA"},
		Products:                    []string{"transactions"},
		RequiredIfSupportedProducts: []string{},
		OptionalProducts:            []string{},
		AdditionalConsentedProducts: []string{"liabilities"},
		TransactionsDaysRequested:   730,
		LinkClientName:              "plaidsync-test",
		LinkLanguage:                "en",
		LinkClientUserID:            "owner",
	}
}

func newTestClient(t *testing.T, ps *plaidServer, env config.PlaidEnv) *HTTPClient {
	t.Helper()
	return newHTTPClient(testPlaidConfig(env), slog.New(slog.DiscardHandler), ps.srv.URL)
}

func TestHTTPClientSendsCredentialsAsHeaders(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/item/get", 200, fixture(t, "item_get.json"))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)

	info, err := c.GetItem(context.Background(), secret.NewToken("access-1"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ItemID != "item-cibc-1" {
		t.Errorf("item = %+v", *info)
	}
	req := ps.last()
	if req.header.Get("PLAID-CLIENT-ID") != "client-id-123" || req.header.Get("PLAID-SECRET") != "secret-456" {
		t.Errorf("credential headers missing: %v", req.header)
	}
	if req.header.Get("Plaid-Version") == "" || req.header.Get("Content-Type") != "application/json" {
		t.Errorf("headers = %v", req.header)
	}
	if req.body["access_token"] != "access-1" {
		t.Errorf("body = %v", req.body)
	}
	if _, ok := req.body["client_id"]; ok {
		t.Error("client_id should travel in a header, not the body")
	}
}

func TestHTTPClientCreateLinkTokenNewItem(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/link/token/create", 200, []byte(`{"link_token":"link-sandbox-abc","expiration":"2026-09-17T00:00:00Z","request_id":"req-link"}`))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)

	lt, err := c.CreateLinkToken(context.Background(), LinkTokenParams{ClientUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if lt.Token != "link-sandbox-abc" || lt.RequestID != "req-link" || !lt.Expiration.Equal(time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("link token = %+v", *lt)
	}
	b := ps.last().body
	if b["client_name"] != "plaidsync-test" || b["language"] != "en" {
		t.Errorf("client_name/language = %v/%v", b["client_name"], b["language"])
	}
	if cc, _ := b["country_codes"].([]any); len(cc) != 1 || cc[0] != "CA" {
		t.Errorf("country_codes = %v", b["country_codes"])
	}
	if u, _ := b["user"].(map[string]any); u["client_user_id"] != "owner" {
		t.Errorf("user = %v", b["user"])
	}
	if p, _ := b["products"].([]any); len(p) != 1 || p[0] != "transactions" {
		t.Errorf("products = %v", b["products"])
	}
	if a, _ := b["additional_consented_products"].([]any); len(a) != 1 || a[0] != "liabilities" {
		t.Errorf("additional_consented_products = %v", b["additional_consented_products"])
	}
	if b["webhook"] != "https://hooks.example/v1/webhooks/plaid" || b["redirect_uri"] != "https://app.example/oauth" {
		t.Errorf("webhook/redirect = %v/%v", b["webhook"], b["redirect_uri"])
	}
	if tx, _ := b["transactions"].(map[string]any); tx["days_requested"] != float64(730) {
		t.Errorf("transactions = %v", b["transactions"])
	}
	for _, absent := range []string{"access_token", "update", "required_if_supported_products", "optional_products"} {
		if _, ok := b[absent]; ok {
			t.Errorf("%s should be absent for a new item: %v", absent, b[absent])
		}
	}
}

func TestHTTPClientCreateLinkTokenUpdateMode(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/link/token/create", 200, []byte(`{"link_token":"link-upd","expiration":"2026-09-17T00:30:00Z","request_id":"req-upd"}`))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)

	_, err := c.CreateLinkToken(context.Background(), LinkTokenParams{
		ClientUserID:                "owner",
		AccessToken:                 secret.NewToken("access-9"),
		AccountSelectionEnabled:     true,
		AdditionalConsentedProducts: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := ps.last().body
	if b["access_token"] != "access-9" {
		t.Errorf("access_token = %v", b["access_token"])
	}
	if u, _ := b["update"].(map[string]any); u["account_selection_enabled"] != true {
		t.Errorf("update = %v", b["update"])
	}
	for _, absent := range []string{"products", "webhook", "transactions", "additional_consented_products"} {
		if _, ok := b[absent]; ok {
			t.Errorf("%s should be absent in update mode: %v", absent, b[absent])
		}
	}
	if b["redirect_uri"] != "https://app.example/oauth" {
		t.Errorf("redirect_uri = %v", b["redirect_uri"])
	}
}

func TestHTTPClientExchange(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/item/public_token/exchange", 200, []byte(`{"access_token":"access-new","item_id":"item-new","request_id":"req-x"}`))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)

	ex, err := c.ExchangePublicToken(context.Background(), secret.NewToken("public-1"))
	if err != nil {
		t.Fatal(err)
	}
	if ex.AccessToken.Expose() != "access-new" || ex.ItemID != "item-new" || ex.RequestID != "req-x" {
		t.Errorf("exchange = %+v", *ex)
	}
	if ps.last().body["public_token"] != "public-1" {
		t.Errorf("body = %v", ps.last().body)
	}
	if _, err := c.ExchangePublicToken(context.Background(), secret.Token{}); err == nil {
		t.Error("empty public token accepted")
	}
}

func TestHTTPClientSyncTransactions(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/transactions/sync", 200, fixture(t, "sync_page_1.json"))
	ps.on("/transactions/sync", 200, fixture(t, "sync_page_2.json"))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)
	tok := secret.NewToken("access-1")

	p1, err := c.SyncTransactions(context.Background(), tok, "")
	if err != nil {
		t.Fatal(err)
	}
	b := ps.last().body
	if _, ok := b["cursor"]; ok {
		t.Errorf("first call must omit cursor: %v", b)
	}
	if b["count"] != float64(500) {
		t.Errorf("count = %v", b["count"])
	}
	if o, _ := b["options"].(map[string]any); o["personal_finance_category_version"] != "v2" {
		t.Errorf("options = %v", b["options"])
	}
	if !p1.HasMore || len(p1.Added) != 2 || p1.Added[0].Amount.String() != "12.34" {
		t.Errorf("page 1 = %+v", *p1)
	}

	p2, err := c.SyncTransactions(context.Background(), tok, p1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if ps.last().body["cursor"] != "cursor-page-2" {
		t.Errorf("second call cursor = %v", ps.last().body["cursor"])
	}
	if p2.HasMore || p2.NextCursor != "cursor-final" || len(p2.Removed) != 1 {
		t.Errorf("page 2 = %+v", *p2)
	}
}

func TestHTTPClientPlaidErrorsBecomeError(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/transactions/sync", 400, fixture(t, "error_item_login_required.json"))
	ps.on("/transactions/sync", 429, fixture(t, "error_rate_limit.json"))
	ps.on("/transactions/sync", 502, []byte(`<html>bad gateway</html>`))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)
	tok := secret.NewToken("access-1")

	_, err := c.SyncTransactions(context.Background(), tok, "")
	pe, ok := AsError(err)
	if !ok {
		t.Fatalf("err = %v (%T), want *Error", err, err)
	}
	if pe.Code != "ITEM_LOGIN_REQUIRED" || pe.Type != "ITEM_ERROR" || pe.HTTPStatus != 400 || pe.Endpoint != "/transactions/sync" || pe.RequestID != "req-err-login" {
		t.Errorf("error = %+v", *pe)
	}
	if pe.Class() != ClassNeedsReauth {
		t.Errorf("class = %v", pe.Class())
	}

	_, err = c.SyncTransactions(context.Background(), tok, "")
	if pe, ok = AsError(err); !ok || pe.Code != "TRANSACTIONS_SYNC_LIMIT" || pe.HTTPStatus != 429 || Classify(err) != ClassRetryable {
		t.Errorf("rate limit error = %v", err)
	}

	_, err = c.SyncTransactions(context.Background(), tok, "")
	if pe, ok = AsError(err); !ok || pe.Code != "" || pe.HTTPStatus != 502 || Classify(err) != ClassRetryable {
		t.Errorf("gateway error = %v", err)
	}
}

func TestHTTPClientCanceledContext(t *testing.T) {
	ps := newPlaidServer(t)
	c := newTestClient(t, ps, config.PlaidEnvSandbox)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.SyncTransactions(ctx, secret.NewToken("access-1"), "")
	if err == nil {
		t.Fatal("call with canceled context succeeded")
	}
	if got := Classify(err); got != ClassCanceled {
		t.Errorf("Classify = %v for %v", got, err)
	}
}

// A custom Sandbox user travels in options next to the webhook, and the
// config is sent as the string Plaid reads it back from.
func TestHTTPClientSandboxCustomUser(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/sandbox/public_token/create", 200, []byte(`{"public_token":"public-sandbox-2","request_id":"req-s"}`))

	sb := newTestClient(t, ps, config.PlaidEnvSandbox)
	const cfg = `{"override_accounts":[{"type":"depository","subtype":"checking"}]}`
	if _, err := sb.SandboxCreatePublicToken(context.Background(), SandboxItemParams{
		InstitutionID: "ins_109508",
		User:          SandboxUser{Username: "user_custom", Config: cfg},
	}); err != nil {
		t.Fatalf("sandbox create = %v", err)
	}
	o, _ := ps.last().body["options"].(map[string]any)
	if tx, _ := o["transactions"].(map[string]any); tx["days_requested"] != float64(730) {
		t.Errorf("days_requested = %v", o["transactions"])
	}
	if o["override_username"] != "user_custom" {
		t.Errorf("override_username = %v", o["override_username"])
	}
	if o["override_password"] != cfg {
		t.Errorf("override_password = %v", o["override_password"])
	}
	// The webhook the client is configured with must survive alongside it.
	if o["webhook"] != "https://hooks.example/v1/webhooks/plaid" {
		t.Errorf("webhook = %v", o["webhook"])
	}
}

// A config with no username is a caller mistake: Plaid would ignore it and
// silently link the default user, so it is refused before the call.
func TestHTTPClientSandboxConfigNeedsUsername(t *testing.T) {
	ps := newPlaidServer(t)
	sb := newTestClient(t, ps, config.PlaidEnvSandbox)
	_, err := sb.SandboxCreatePublicToken(context.Background(), SandboxItemParams{
		InstitutionID: "ins_109508",
		User:          SandboxUser{Config: `{}`},
	})
	if err == nil {
		t.Fatal("config without a username was accepted")
	}
}

func TestHTTPClientSandboxGate(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/sandbox/public_token/create", 200, []byte(`{"public_token":"public-sandbox-1","request_id":"req-s"}`))
	ps.on("/sandbox/item/fire_webhook", 200, []byte(`{"webhook_fired":true,"request_id":"req-f"}`))
	ps.on("/sandbox/item/reset_login", 200, []byte(`{"reset_login":true,"request_id":"req-r"}`))

	prod := newTestClient(t, ps, config.PlaidEnvProduction)
	ctx := context.Background()
	if _, err := prod.SandboxCreatePublicToken(ctx, SandboxItemParams{InstitutionID: "ins_109508"}); err != ErrNotSandbox {
		t.Errorf("production create = %v", err)
	}
	if err := prod.SandboxFireWebhook(ctx, secret.NewToken("a"), WebhookTypeTransactions, WebhookCodeSyncUpdatesAvailable); err != ErrNotSandbox {
		t.Errorf("production fire = %v", err)
	}
	if err := prod.SandboxResetLogin(ctx, secret.NewToken("a")); err != ErrNotSandbox {
		t.Errorf("production reset = %v", err)
	}

	sb := newTestClient(t, ps, config.PlaidEnvSandbox)
	tok, err := sb.SandboxCreatePublicToken(ctx, SandboxItemParams{InstitutionID: "ins_109508"})
	if err != nil || tok.Expose() != "public-sandbox-1" {
		t.Fatalf("sandbox create = %v, %v", tok, err)
	}
	b := ps.last().body
	if b["institution_id"] != "ins_109508" {
		t.Errorf("institution_id = %v", b["institution_id"])
	}
	if p, _ := b["initial_products"].([]any); len(p) != 1 || p[0] != "transactions" {
		t.Errorf("initial_products = %v", b["initial_products"])
	}
	o, _ := b["options"].(map[string]any)
	if o["webhook"] != "https://hooks.example/v1/webhooks/plaid" {
		t.Errorf("options = %v", b["options"])
	}
	// Plaid would otherwise default to 90 days, giving a Sandbox item
	// less history than Link asks for.
	tx, _ := o["transactions"].(map[string]any)
	if tx["days_requested"] != float64(730) {
		t.Errorf("days_requested = %v", tx["days_requested"])
	}
	if err := sb.SandboxFireWebhook(ctx, secret.NewToken("a"), WebhookTypeTransactions, WebhookCodeSyncUpdatesAvailable); err != nil {
		t.Errorf("fire = %v", err)
	}
	if b := ps.last().body; b["webhook_code"] != "SYNC_UPDATES_AVAILABLE" || b["webhook_type"] != "TRANSACTIONS" {
		t.Errorf("fire body = %v", b)
	}
	if err := sb.SandboxResetLogin(ctx, secret.NewToken("a")); err != nil {
		t.Errorf("reset = %v", err)
	}
}

func TestHTTPClientWebhookKeyAndItemOps(t *testing.T) {
	ps := newPlaidServer(t)
	ps.on("/webhook_verification_key/get", 200, fixture(t, "webhook_key.json"))
	ps.on("/item/remove", 200, []byte(`{"request_id":"req-rm"}`))
	ps.on("/item/webhook/update", 200, []byte(`{"item":{"item_id":"i"},"request_id":"req-wh"}`))
	ps.on("/accounts/get", 200, fixture(t, "accounts_get.json"))
	c := newTestClient(t, ps, config.PlaidEnvSandbox)
	ctx := context.Background()

	k, err := c.WebhookVerificationKey(ctx, "6c5516e1-92dc-479e-a8ff-5a51992e0001")
	if err != nil || k.KeyID != "6c5516e1-92dc-479e-a8ff-5a51992e0001" {
		t.Fatalf("key = %v, %v", k, err)
	}
	if ps.last().body["key_id"] != "6c5516e1-92dc-479e-a8ff-5a51992e0001" {
		t.Errorf("key body = %v", ps.last().body)
	}
	if err := c.RemoveItem(ctx, secret.NewToken("access-1")); err != nil {
		t.Errorf("remove = %v", err)
	}
	if err := c.UpdateWebhook(ctx, secret.NewToken("access-1"), "https://new.example/hook"); err != nil {
		t.Errorf("update webhook = %v", err)
	}
	if ps.last().body["webhook"] != "https://new.example/hook" {
		t.Errorf("webhook body = %v", ps.last().body)
	}
	a, err := c.GetAccounts(ctx, secret.NewToken("access-1"))
	if err != nil || len(a.Accounts) != 2 {
		t.Errorf("accounts = %v, %v", a, err)
	}
}
