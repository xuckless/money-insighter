package api

import (
	"testing"

	"plaidsync/internal/plaid"
	"plaidsync/internal/plaid/plaidtest"
	"plaidsync/internal/store"
)

func TestHostedLinkNewItem(t *testing.T) {
	h := newAPIHarness(t)
	it := h.fake.AddItem(plaidtest.CIBCItem())
	h.fake.AddPublicToken("public-hosted", it)

	resp, body := h.call("POST", "/v1/link/hosted", nil, true)
	if resp.StatusCode != 201 || body["hosted_link_url"] == nil || body["link_token"] == nil {
		t.Fatalf("create hosted = %d %v", resp.StatusCode, body)
	}
	token := body["link_token"].(string)
	calls := h.fake.CallsTo(plaidtest.OpCreateLinkToken)
	if len(calls) != 1 || !calls[0].Link.Hosted || !calls[0].Link.AccessToken.IsZero() {
		t.Errorf("link calls = %+v", calls)
	}

	poll := func() map[string]any {
		t.Helper()
		resp, body := h.call("POST", "/v1/link/hosted/status", map[string]string{"link_token": token}, true)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d %v", resp.StatusCode, body)
		}
		return body
	}
	if b := poll(); b["status"] != "pending" || b["started"] != false {
		t.Errorf("before open = %v", b)
	}
	h.fake.StartHostedSession(token)
	if b := poll(); b["status"] != "pending" || b["started"] != true {
		t.Errorf("while open = %v", b)
	}

	h.fake.FinishHostedSession(token, "public-hosted")
	b := poll()
	if b["status"] != "completed" {
		t.Fatalf("after finish = %v", b)
	}
	item := b["item"].(map[string]any)
	if item["item_id"] != "item-cibc-1" || item["status"] != "active" {
		t.Errorf("item = %v", item)
	}
	if got := h.waitJob(b["job"].(map[string]any)["job_id"].(string)); got["state"] != "succeeded" {
		t.Errorf("job = %v", got)
	}

	// Later polls are answered from memory: no second exchange, same body.
	exchanges := len(h.fake.CallsTo(plaidtest.OpExchangePublicToken))
	if b2 := poll(); b2["status"] != "completed" || b2["item"].(map[string]any)["item_id"] != "item-cibc-1" {
		t.Errorf("repeat poll = %v", b2)
	}
	if n := len(h.fake.CallsTo(plaidtest.OpExchangePublicToken)); n != exchanges || exchanges != 1 {
		t.Errorf("exchanges = %d then %d, want 1", exchanges, n)
	}

	if resp, _ := h.call("POST", "/v1/link/hosted/status", map[string]string{"link_token": "link-from-a-previous-process"}, true); resp.StatusCode != 404 {
		t.Errorf("unknown token = %d", resp.StatusCode)
	}
	if resp, _ := h.call("POST", "/v1/link/hosted/status", map[string]string{}, true); resp.StatusCode != 400 {
		t.Errorf("missing token = %d", resp.StatusCode)
	}
	if resp, _ := h.call("POST", "/v1/link/hosted/status", nil, false); resp.StatusCode != 401 {
		t.Errorf("no auth = %d", resp.StatusCode)
	}
}

func TestHostedLinkExitAndExpiry(t *testing.T) {
	h := newAPIHarness(t)

	create := func() string {
		t.Helper()
		resp, body := h.call("POST", "/v1/link/hosted", nil, true)
		if resp.StatusCode != 201 {
			t.Fatalf("create hosted = %d %v", resp.StatusCode, body)
		}
		return body["link_token"].(string)
	}
	status := func(token string) map[string]any {
		t.Helper()
		resp, body := h.call("POST", "/v1/link/hosted/status", map[string]string{"link_token": token}, true)
		if resp.StatusCode != 200 {
			t.Fatalf("status = %d %v", resp.StatusCode, body)
		}
		return body
	}

	closed := create()
	h.fake.ExitHostedSession(closed, nil)
	if b := status(closed); b["status"] != "exited" || b["exit"] != nil {
		t.Errorf("plain exit = %v", b)
	}

	failed := create()
	h.fake.ExitHostedSession(failed, &plaid.Error{Type: "INSTITUTION_ERROR", Code: "INSTITUTION_DOWN", Message: "down", RequestID: "req-x"})
	b := status(failed)
	if b["status"] != "exited" || b["exit"] == nil || b["exit"].(map[string]any)["code"] != "INSTITUTION_DOWN" {
		t.Errorf("error exit = %v", b)
	}

	expired := create()
	h.fake.ExpireHostedSession(expired)
	if b := status(expired); b["status"] != "expired" {
		t.Errorf("expired = %v", b)
	}
	// Remembered: Plaid is not asked again.
	n := len(h.fake.CallsTo(plaidtest.OpGetLinkSession))
	status(expired)
	if len(h.fake.CallsTo(plaidtest.OpGetLinkSession)) != n {
		t.Errorf("expired status asked Plaid again")
	}
}

func TestHostedLinkUpdateMode(t *testing.T) {
	h := newAPIHarness(t)
	itemID := h.linkSandboxItem()
	if err := h.store.SetItemStatus(h.ctx, itemID, store.ItemStatusLoginRequired, &store.ItemError{Code: "ITEM_LOGIN_REQUIRED", Message: "x"}); err != nil {
		t.Fatal(err)
	}

	resp, body := h.call("POST", "/v1/items/"+itemID+"/link/hosted", map[string]any{"account_selection": true}, true)
	if resp.StatusCode != 201 || body["hosted_link_url"] == nil {
		t.Fatalf("update hosted = %d %v", resp.StatusCode, body)
	}
	token := body["link_token"].(string)
	calls := h.fake.CallsTo(plaidtest.OpCreateLinkToken)
	last := calls[len(calls)-1]
	if !last.Link.Hosted || last.Link.AccessToken.IsZero() || !last.Link.AccountSelectionEnabled || last.ItemID != itemID {
		t.Errorf("update hosted call = %+v", last)
	}

	// Plaid reports the update-mode session finished without a public
	// token: the item is re-activated and a sync queued.
	h.fake.FinishHostedSession(token, "")
	resp, body = h.call("POST", "/v1/link/hosted/status", map[string]string{"link_token": token}, true)
	if resp.StatusCode != 200 || body["status"] != "completed" {
		t.Fatalf("update status = %d %v", resp.StatusCode, body)
	}
	item := body["item"].(map[string]any)
	if item["item_id"] != itemID || item["status"] != "active" || item["last_error"] != nil {
		t.Errorf("relinked item = %v", item)
	}
	if body["job"] == nil {
		t.Errorf("no job queued after update mode: %v", body)
	}

	if resp, _ := h.call("POST", "/v1/items/nope/link/hosted", nil, true); resp.StatusCode != 404 {
		t.Errorf("unknown item = %d", resp.StatusCode)
	}
}
