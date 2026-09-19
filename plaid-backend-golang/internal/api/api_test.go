package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/jobs"
	"plaidsync/internal/plaid"
	"plaidsync/internal/plaid/plaidtest"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
	"plaidsync/internal/store/storetest"
	"plaidsync/internal/sync"
)

const testToken = "test-api-token-0123456789abcdef0123456789abcdef"

// apiHarness runs the whole service against a throwaway database and a
// fake Plaid: the real router, auth, engine and job runner.
type apiHarness struct {
	t      *testing.T
	ctx    context.Context
	store  *store.Store
	fake   *plaidtest.Fake
	cfg    *config.Config
	server *Server
	http   *httptest.Server
}

func newAPIHarness(t *testing.T) *apiHarness {
	t.Helper()
	st := storetest.New(t)
	ctx := storetest.Ctx(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 3)
	}
	kr, err := crypto.NewKeyring(1, map[uint32]secret.Bytes{1: secret.NewBytes(key)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		APIToken: secret.NewToken(testToken),
		Plaid: config.PlaidConfig{
			Env: config.PlaidEnvSandbox, WebhookURL: "https://hooks.example/v1/webhooks/plaid",
			Products: []string{"transactions"}, LinkClientUserID: "owner",
		},
		Sync: config.SyncConfig{MinInterval: 0, Interval: time.Hour, SchedulerEnabled: false,
			MaxAttempts: 2, RetryBase: time.Millisecond, RetryMax: time.Millisecond, Concurrency: 2},
	}
	fake := plaidtest.New()
	fake.SandboxTemplate = plaidtest.CIBCItem()
	quiet := slog.New(slog.DiscardHandler)
	engine := sync.New(st, fake, kr, cfg.Sync, quiet)
	runner := jobs.New(st, engine, cfg.Sync, quiet)
	srv := NewServer(cfg, st, fake, kr, runner, quiet)

	rctx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		_ = runner.Run(rctx)
		close(stopped)
	}()
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		hs.Close()
		cancel()
		<-stopped
	})
	return &apiHarness{t: t, ctx: ctx, store: st, fake: fake, cfg: cfg, server: srv, http: hs}
}

// call performs one request. body may be nil, a string or any value to
// encode as JSON. With auth the bearer token is attached.
func (h *apiHarness) call(method, path string, body any, auth bool) (*http.Response, map[string]any) {
	h.t.Helper()
	var rdr io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rdr = strings.NewReader(b)
	default:
		enc, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rdr = bytes.NewReader(enc)
	}
	req, err := http.NewRequest(method, h.http.URL+path, rdr)
	if err != nil {
		h.t.Fatal(err)
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			h.t.Fatalf("%s %s: non-JSON body %q", method, path, raw)
		}
	}
	return resp, out
}

// waitJob polls GET /v1/jobs/{id} until the job is terminal.
func (h *apiHarness) waitJob(jobID string) map[string]any {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, body := h.call("GET", "/v1/jobs/"+jobID, nil, true)
		if resp.StatusCode != 200 {
			h.t.Fatalf("GET job = %d %v", resp.StatusCode, body)
		}
		job := body["job"].(map[string]any)
		switch job["state"] {
		case "succeeded", "failed", "skipped":
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatalf("job %s did not finish", jobID)
	return nil
}

// linkSandboxItem links a sandbox item and waits for its initial sync.
func (h *apiHarness) linkSandboxItem() (itemID string) {
	h.t.Helper()
	resp, body := h.call("POST", "/v1/sandbox/items", nil, true)
	if resp.StatusCode != 201 {
		h.t.Fatalf("POST /v1/sandbox/items = %d %v", resp.StatusCode, body)
	}
	item := body["item"].(map[string]any)
	job := body["job"].(map[string]any)
	if got := h.waitJob(job["job_id"].(string)); got["state"] != "succeeded" {
		h.t.Fatalf("initial job = %v", got)
	}
	return item["item_id"].(string)
}

// POST /v1/sandbox/items passes a custom test user through to Plaid.
// user_config is accepted both as an inline object, which is what a script
// generating a dataset naturally sends, and as the escaped string Plaid's
// own API takes; both must reach the client as the same string.
func TestSandboxItemCustomUser(t *testing.T) {
	const inline = `{"override_accounts":[{"type":"depository","subtype":"checking"}]}`
	for _, tc := range []struct {
		name string
		body string
	}{
		{"object", `{"override_username":"user_custom","user_config":` + inline + `}`},
		{"string", `{"override_username":"user_custom","user_config":` + strconv.Quote(inline) + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAPIHarness(t)
			if resp, body := h.call("POST", "/v1/sandbox/items", tc.body, true); resp.StatusCode != 201 {
				t.Fatalf("POST /v1/sandbox/items = %d %v", resp.StatusCode, body)
			}
			calls := h.fake.CallsTo(plaidtest.OpSandboxCreatePublicToken)
			if len(calls) != 1 || calls[0].Sandbox == nil {
				t.Fatalf("calls = %v", calls)
			}
			user := calls[0].Sandbox.User
			if user.Username != "user_custom" {
				t.Errorf("username = %q", user.Username)
			}
			if user.Config != inline {
				t.Errorf("config = %q, want %q", user.Config, inline)
			}
		})
	}
}

// A body with no user options links Plaid's default test user, exactly as
// before the custom-user option existed.
func TestSandboxItemDefaultUser(t *testing.T) {
	h := newAPIHarness(t)
	h.linkSandboxItem()
	calls := h.fake.CallsTo(plaidtest.OpSandboxCreatePublicToken)
	if len(calls) != 1 || calls[0].Sandbox == nil {
		t.Fatalf("calls = %v", calls)
	}
	if got := calls[0].Sandbox.User; got != (plaid.SandboxUser{}) {
		t.Errorf("user = %+v, want zero", got)
	}
	if got := calls[0].Sandbox.InstitutionID; got != defaultSandboxInstitution {
		t.Errorf("institution = %q", got)
	}
}

func TestAuth(t *testing.T) {
	h := newAPIHarness(t)
	if resp, _ := h.call("GET", "/healthz", nil, false); resp.StatusCode != 200 {
		t.Errorf("healthz = %d", resp.StatusCode)
	}
	if resp, body := h.call("GET", "/v1/items", nil, false); resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" || body["error"] == nil {
		t.Errorf("no token = %d %v", resp.StatusCode, body)
	}
	req, _ := http.NewRequest("GET", h.http.URL+"/v1/items", nil)
	req.Header.Set("Authorization", "Bearer "+testToken[:len(testToken)-1]+"x")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 401 {
		t.Errorf("wrong token = %v %v", resp, err)
	}
	if resp, _ := h.call("GET", "/v1/items?access_token="+testToken, nil, false); resp.StatusCode != 401 {
		t.Errorf("token in query = %d", resp.StatusCode)
	}
	resp, body := h.call("GET", "/v1/items", nil, true)
	if resp.StatusCode != 200 || body["items"] == nil {
		t.Errorf("with token = %d %v", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("no X-Request-Id header")
	}
	if resp, _ := h.call("GET", "/v1/nope", nil, true); resp.StatusCode != 404 {
		t.Errorf("unknown route = %d", resp.StatusCode)
	}
	if resp, _ := h.call("GET", "/nope", nil, false); resp.StatusCode != 404 {
		t.Errorf("unknown root route = %d", resp.StatusCode)
	}
}

func TestSandboxItemEndToEnd(t *testing.T) {
	h := newAPIHarness(t)
	itemID := h.linkSandboxItem()

	resp, body := h.call("GET", "/v1/items/"+itemID, nil, true)
	if resp.StatusCode != 200 {
		t.Fatalf("GET item = %d %v", resp.StatusCode, body)
	}
	item := body["item"].(map[string]any)
	if item["status"] != "active" || item["has_cursor"] != true || item["last_successful_sync_at"] == nil || item["institution_id"] != "ins_109508" {
		t.Errorf("item = %v", item)
	}
	if item["last_error"] != nil {
		t.Errorf("last_error = %v", item["last_error"])
	}
	jobsList := body["jobs"].([]any)
	runs := body["runs"].([]any)
	if len(jobsList) != 1 || jobsList[0].(map[string]any)["kind"] != "initial" || len(runs) != 1 || runs[0].(map[string]any)["outcome"] != "success" {
		t.Errorf("jobs = %v runs = %v", jobsList, runs)
	}
	if run := runs[0].(map[string]any); run["pages"] != float64(2) || run["inserted"] != float64(4) || run["accounts_seen"] != float64(2) {
		t.Errorf("run = %v", run)
	}

	// The item's webhook was pointed at this deployment.
	fi := h.fake.Item(itemID)
	if fi == nil || fi.Webhook != h.cfg.Plaid.WebhookURL {
		t.Errorf("fake item webhook = %q", fi.Webhook)
	}
	txs, _ := h.store.ListTransactions(h.ctx, itemID, 100, 0)
	if len(txs) != 4 {
		t.Errorf("transactions = %d", len(txs))
	}

	resp, body = h.call("GET", "/v1/items", nil, true)
	if items := body["items"].([]any); resp.StatusCode != 200 || len(items) != 1 {
		t.Errorf("list = %d %v", resp.StatusCode, body)
	}
}

func TestLinkTokens(t *testing.T) {
	h := newAPIHarness(t)
	resp, body := h.call("POST", "/v1/link/token", nil, true)
	if resp.StatusCode != 200 || body["link_token"] == nil || body["expiration"] == nil {
		t.Fatalf("link token = %d %v", resp.StatusCode, body)
	}
	calls := h.fake.CallsTo(plaidtest.OpCreateLinkToken)
	if len(calls) != 1 || calls[0].Link.ClientUserID != "owner" || !calls[0].Link.AccessToken.IsZero() {
		t.Errorf("link calls = %+v", calls)
	}

	itemID := h.linkSandboxItem()
	resp, body = h.call("POST", "/v1/items/"+itemID+"/link/token", map[string]any{"account_selection": true, "additional_consented_products": []string{"liabilities"}}, true)
	if resp.StatusCode != 200 || body["link_token"] == nil {
		t.Fatalf("update link token = %d %v", resp.StatusCode, body)
	}
	calls = h.fake.CallsTo(plaidtest.OpCreateLinkToken)
	last := calls[len(calls)-1].Link
	if last.AccessToken.IsZero() || !last.AccountSelectionEnabled || len(last.AdditionalConsentedProducts) != 1 || calls[len(calls)-1].ItemID != itemID {
		t.Errorf("update link call = %+v", calls[len(calls)-1])
	}
	if resp, _ := h.call("POST", "/v1/items/nope/link/token", nil, true); resp.StatusCode != 404 {
		t.Errorf("unknown item update token = %d", resp.StatusCode)
	}
	if resp, body := h.call("POST", "/v1/link/token", `{"unexpected": 1}`, true); resp.StatusCode != 400 || body["error"] == nil {
		t.Errorf("unknown field = %d %v", resp.StatusCode, body)
	}
}

func TestExchange(t *testing.T) {
	h := newAPIHarness(t)
	it := h.fake.AddItem(plaidtest.CIBCItem())
	h.fake.AddPublicToken("public-good", it)

	resp, body := h.call("POST", "/v1/link/exchange", map[string]string{"public_token": "public-good"}, true)
	if resp.StatusCode != 201 {
		t.Fatalf("exchange = %d %v", resp.StatusCode, body)
	}
	item := body["item"].(map[string]any)
	if item["item_id"] != "item-cibc-1" || item["institution_name"] != "CIBC" || item["status"] != "active" {
		t.Errorf("item = %v", item)
	}
	job := body["job"].(map[string]any)
	if got := h.waitJob(job["job_id"].(string)); got["state"] != "succeeded" {
		t.Errorf("job = %v", got)
	}

	resp, body = h.call("POST", "/v1/link/exchange", map[string]string{"public_token": "public-bad"}, true)
	if resp.StatusCode != 400 || body["plaid"] == nil || body["plaid"].(map[string]any)["code"] != "INVALID_PUBLIC_TOKEN" {
		t.Errorf("bad token = %d %v", resp.StatusCode, body)
	}
	if resp, _ := h.call("POST", "/v1/link/exchange", map[string]string{}, true); resp.StatusCode != 400 {
		t.Errorf("missing token = %d", resp.StatusCode)
	}
	if resp, _ := h.call("POST", "/v1/link/exchange", nil, true); resp.StatusCode != 400 {
		t.Errorf("empty body = %d", resp.StatusCode)
	}

	// Re-linking the same item (update mode) resets its status.
	if err := h.store.SetItemStatus(h.ctx, "item-cibc-1", store.ItemStatusLoginRequired, &store.ItemError{Code: "ITEM_LOGIN_REQUIRED", Message: "x"}); err != nil {
		t.Fatal(err)
	}
	h.fake.AddPublicToken("public-again", it)
	it.Pages["cursor-final"] = plaidtest.SyncPage(plaidtest.FixtureSyncNotReady)
	it.Pages["cursor-final"].NextCursor = "cursor-final"
	resp, body = h.call("POST", "/v1/link/exchange", map[string]string{"public_token": "public-again"}, true)
	if resp.StatusCode != 201 || body["item"].(map[string]any)["status"] != "active" || body["item"].(map[string]any)["last_error"] != nil {
		t.Errorf("relink = %d %v", resp.StatusCode, body)
	}
}

func TestSyncTriggerAndJobs(t *testing.T) {
	h := newAPIHarness(t)
	itemID := h.linkSandboxItem()
	fi := h.fake.Item(itemID)
	fi.Pages["cursor-final"] = plaidtest.SyncPage(plaidtest.FixtureSyncNotReady)
	fi.Pages["cursor-final"].NextCursor = "cursor-final"

	resp, body := h.call("POST", "/v1/items/"+itemID+"/sync", nil, true)
	if resp.StatusCode != 202 || !strings.HasPrefix(resp.Header.Get("Location"), "/v1/jobs/") {
		t.Fatalf("sync = %d %v %q", resp.StatusCode, body, resp.Header.Get("Location"))
	}
	job := body["job"].(map[string]any)
	if job["kind"] != "manual" || job["state"] != "queued" {
		t.Errorf("job = %v", job)
	}
	if got := h.waitJob(job["job_id"].(string)); got["state"] != "succeeded" {
		t.Errorf("job = %v", got)
	}

	if resp, _ := h.call("POST", "/v1/items/nope/sync", nil, true); resp.StatusCode != 404 {
		t.Errorf("unknown item sync = %d", resp.StatusCode)
	}
	if resp, _ := h.call("GET", "/v1/jobs/not-a-uuid", nil, true); resp.StatusCode != 404 {
		t.Errorf("bad job id = %d", resp.StatusCode)
	}
	if err := h.store.SetItemStatus(h.ctx, itemID, store.ItemStatusLoginRequired, nil); err != nil {
		t.Fatal(err)
	}
	if resp, body := h.call("POST", "/v1/items/"+itemID+"/sync", nil, true); resp.StatusCode != 409 {
		t.Errorf("sync of login_required item = %d %v", resp.StatusCode, body)
	}
}

func TestRemoveItem(t *testing.T) {
	h := newAPIHarness(t)
	itemID := h.linkSandboxItem()

	resp, body := h.call("DELETE", "/v1/items/"+itemID, nil, true)
	if resp.StatusCode != 200 || body["item"].(map[string]any)["status"] != "removed" {
		t.Fatalf("delete = %d %v", resp.StatusCode, body)
	}
	if fi := h.fake.Item(itemID); !fi.Removed {
		t.Error("Plaid /item/remove was not called")
	}
	if _, err := h.store.GetCredential(h.ctx, itemID); err == nil {
		t.Error("credential still present after removal")
	}
	if resp, _ := h.call("DELETE", "/v1/items/"+itemID, nil, true); resp.StatusCode != 200 {
		t.Errorf("second delete = %d", resp.StatusCode)
	}
	if resp, _ := h.call("POST", "/v1/items/"+itemID+"/sync", nil, true); resp.StatusCode != 409 {
		t.Errorf("sync after removal = %d", resp.StatusCode)
	}
	if resp, _ := h.call("DELETE", "/v1/items/nope", nil, true); resp.StatusCode != 404 {
		t.Errorf("delete unknown = %d", resp.StatusCode)
	}
}

func TestWebhooks(t *testing.T) {
	h := newAPIHarness(t)
	itemID := h.linkSandboxItem()
	fi := h.fake.Item(itemID)
	fi.Pages["cursor-final"] = plaidtest.SyncPage(plaidtest.FixtureSyncNotReady)
	fi.Pages["cursor-final"].NextCursor = "cursor-final"
	signer := newSigner(t, h.fake, "kid-live", false)

	post := func(payload string, signed bool) (*http.Response, map[string]any) {
		req, _ := http.NewRequest("POST", h.http.URL+"/v1/webhooks/plaid", strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if signed {
			req.Header.Set("Plaid-Verification", signer.sign(t, []byte(payload), time.Now()))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp, body
	}
	countJobs := func() int {
		jobsList, _ := h.store.ListJobs(h.ctx, itemID, 100)
		return len(jobsList)
	}

	sync := `{"webhook_type":"TRANSACTIONS","webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"` + itemID + `","initial_update_complete":true,"historical_update_complete":true,"environment":"sandbox"}`
	if resp, _ := post(sync, false); resp.StatusCode != 401 {
		t.Errorf("unsigned webhook = %d", resp.StatusCode)
	}
	before := countJobs()
	if resp, body := post(sync, true); resp.StatusCode != 200 || body["received"] != true {
		t.Errorf("signed webhook = %d %v", resp.StatusCode, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for countJobs() == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	jobsList, _ := h.store.ListJobs(h.ctx, itemID, 100)
	if len(jobsList) != before+1 || jobsList[0].Kind != store.JobKindWebhook {
		t.Fatalf("jobs after webhook = %+v", jobsList)
	}
	if got := h.waitJob(jobsList[0].JobID); got["state"] != "succeeded" {
		t.Errorf("webhook job = %v", got)
	}

	errWebhook := `{"webhook_type":"ITEM","webhook_code":"ERROR","item_id":"` + itemID + `","error":{"error_type":"ITEM_ERROR","error_code":"ITEM_LOGIN_REQUIRED","error_message":"login required","display_message":null}}`
	if resp, _ := post(errWebhook, true); resp.StatusCode != 200 {
		t.Errorf("error webhook = %d", resp.StatusCode)
	}
	item, _ := h.store.GetItem(h.ctx, itemID)
	if item.Status != store.ItemStatusLoginRequired || item.LastErrorCode == nil || *item.LastErrorCode != "ITEM_LOGIN_REQUIRED" {
		t.Errorf("item after error webhook = %+v", *item)
	}

	before = countJobs()
	repaired := `{"webhook_type":"ITEM","webhook_code":"LOGIN_REPAIRED","item_id":"` + itemID + `"}`
	if resp, _ := post(repaired, true); resp.StatusCode != 200 {
		t.Errorf("repaired webhook = %d", resp.StatusCode)
	}
	item, _ = h.store.GetItem(h.ctx, itemID)
	if item.Status != store.ItemStatusActive {
		t.Errorf("item after repair = %+v", *item)
	}
	if countJobs() != before+1 {
		t.Error("LOGIN_REPAIRED did not queue a sync")
	}

	pending := `{"webhook_type":"ITEM","webhook_code":"PENDING_DISCONNECT","item_id":"` + itemID + `","reason":"INSTITUTION_MIGRATION"}`
	if resp, _ := post(pending, true); resp.StatusCode != 200 {
		t.Errorf("pending webhook = %d", resp.StatusCode)
	}
	if item, _ = h.store.GetItem(h.ctx, itemID); item.Status != store.ItemStatusPendingExpiration {
		t.Errorf("item after pending = %+v", *item)
	}

	// Unknown items and ignored codes are still 200.
	if resp, _ := post(`{"webhook_type":"TRANSACTIONS","webhook_code":"SYNC_UPDATES_AVAILABLE","item_id":"someone-elses"}`, true); resp.StatusCode != 200 {
		t.Errorf("unknown item webhook = %d", resp.StatusCode)
	}
	if resp, _ := post(`{"webhook_type":"TRANSACTIONS","webhook_code":"DEFAULT_UPDATE","item_id":"`+itemID+`"}`, true); resp.StatusCode != 200 {
		t.Errorf("ignored webhook = %d", resp.StatusCode)
	}
	if resp, _ := post(`not json`, true); resp.StatusCode != 400 {
		t.Errorf("non-JSON webhook = %d", resp.StatusCode)
	}
}

func TestSandboxRouteHiddenInProduction(t *testing.T) {
	h := newAPIHarness(t)
	h.cfg.Plaid.Env = config.PlaidEnvProduction
	prod := httptest.NewServer(h.server.Handler())
	defer prod.Close()
	req, _ := http.NewRequest("POST", prod.URL+"/v1/sandbox/items", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 404 {
		t.Errorf("sandbox route in production = %v %v", resp, err)
	}
}
