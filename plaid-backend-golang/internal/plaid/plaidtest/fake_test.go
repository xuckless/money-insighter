package plaidtest

import (
	"context"
	"errors"
	"testing"

	"plaidsync/internal/config"
	"plaidsync/internal/plaid"
	"plaidsync/internal/secret"
)

func TestFakePaginatesFixtures(t *testing.T) {
	f := New()
	it := f.AddItem(CIBCItem())
	ctx := context.Background()

	p1, err := f.SyncTransactions(ctx, it.AccessToken, "")
	if err != nil {
		t.Fatal(err)
	}
	if !p1.HasMore || p1.NextCursor != "cursor-page-2" || len(p1.Added) != 2 {
		t.Errorf("page 1 = %+v", *p1)
	}
	p2, err := f.SyncTransactions(ctx, it.AccessToken, p1.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if p2.HasMore || p2.NextCursor != "cursor-final" || len(p2.Removed) != 1 {
		t.Errorf("page 2 = %+v", *p2)
	}
	if _, err := f.SyncTransactions(ctx, it.AccessToken, "cursor-final"); err == nil {
		t.Error("unscripted cursor should fail")
	}
	if it.SyncCalls != 3 || len(f.CallsTo(OpSyncTransactions)) != 3 {
		t.Errorf("sync calls = %d / %d", it.SyncCalls, len(f.CallsTo(OpSyncTransactions)))
	}

	// Pages are returned as copies: mutating one must not leak.
	p1.Added[0].Name = "mutated"
	again, _ := f.SyncTransactions(ctx, it.AccessToken, "")
	if again.Added[0].Name == "mutated" {
		t.Error("page slice aliased between calls")
	}
}

func TestFakeItemLifecycle(t *testing.T) {
	f := New()
	it := f.AddItem(CIBCItem())
	f.AddPublicToken("public-1", it)
	ctx := context.Background()

	ex, err := f.ExchangePublicToken(ctx, secret.NewToken("public-1"))
	if err != nil || ex.ItemID != "item-cibc-1" || !ex.AccessToken.Equal(it.AccessToken) {
		t.Fatalf("exchange = %v, %v", ex, err)
	}
	if _, err := f.ExchangePublicToken(ctx, secret.NewToken("public-1")); err == nil {
		t.Error("public token reused")
	}
	info, err := f.GetItem(ctx, it.AccessToken)
	if err != nil || info.ItemID != "item-cibc-1" || info.Error != nil {
		t.Fatalf("item = %v, %v", info, err)
	}
	accts, err := f.GetAccounts(ctx, it.AccessToken)
	if err != nil || len(accts.Accounts) != 2 {
		t.Fatalf("accounts = %v, %v", accts, err)
	}

	if err := f.SandboxResetLogin(ctx, it.AccessToken); err != nil {
		t.Fatal(err)
	}
	_, err = f.SyncTransactions(ctx, it.AccessToken, "")
	if pe, ok := plaid.AsError(err); !ok || pe.Code != "ITEM_LOGIN_REQUIRED" || pe.Endpoint != "/transactions/sync" {
		t.Errorf("after reset login: %v", err)
	}
	f.RepairLogin("item-cibc-1")
	if _, err := f.SyncTransactions(ctx, it.AccessToken, ""); err != nil {
		t.Errorf("after repair: %v", err)
	}

	if err := f.UpdateWebhook(ctx, it.AccessToken, "https://x/hook"); err != nil || it.Webhook != "https://x/hook" {
		t.Errorf("update webhook: %v, %q", err, it.Webhook)
	}
	if err := f.RemoveItem(ctx, it.AccessToken); err != nil {
		t.Fatal(err)
	}
	_, err = f.GetItem(ctx, it.AccessToken)
	if pe, ok := plaid.AsError(err); !ok || pe.Code != "ITEM_NOT_FOUND" || pe.Class() != plaid.ClassFatal {
		t.Errorf("removed item: %v", err)
	}
	_, err = f.GetItem(ctx, secret.NewToken("nope"))
	if pe, ok := plaid.AsError(err); !ok || pe.Code != "INVALID_ACCESS_TOKEN" {
		t.Errorf("unknown token: %v", err)
	}
}

func TestFakeFailNextAndSandboxCreate(t *testing.T) {
	f := New()
	f.SandboxTemplate = CIBCItem()
	ctx := context.Background()

	boom := errors.New("boom")
	f.FailNext(OpSyncTransactions, boom)
	f.FailNext(OpSyncTransactions, PlaidError(FixtureErrorMutation, "/transactions/sync", 400))

	pub, err := f.SandboxCreatePublicToken(ctx, "ins_37", nil)
	if err != nil {
		t.Fatal(err)
	}
	ex, err := f.ExchangePublicToken(ctx, pub)
	if err != nil {
		t.Fatal(err)
	}
	it := f.Item(ex.ItemID)
	if it == nil || *it.Info.InstitutionID != "ins_37" || len(it.Accounts) != 2 {
		t.Fatalf("sandbox item = %+v", it)
	}
	if _, err := f.SyncTransactions(ctx, it.AccessToken, ""); !errors.Is(err, boom) {
		t.Errorf("first failure = %v", err)
	}
	_, err = f.SyncTransactions(ctx, it.AccessToken, "")
	if pe, ok := plaid.AsError(err); !ok || pe.Code != "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION" {
		t.Errorf("second failure = %v", err)
	}
	if p, err := f.SyncTransactions(ctx, it.AccessToken, ""); err != nil || len(p.Added) != 2 {
		t.Errorf("third call = %v, %v", p, err)
	}

	lt, err := f.CreateLinkToken(ctx, plaid.LinkTokenParams{ClientUserID: "owner"})
	if err != nil || lt.Token == "" {
		t.Errorf("link token = %v, %v", lt, err)
	}
	if calls := f.CallsTo(OpCreateLinkToken); len(calls) != 1 || calls[0].Link.ClientUserID != "owner" {
		t.Errorf("link calls = %+v", calls)
	}

	fired := ""
	f.WebhookSink = func(ctx context.Context, itemID, typ, code string) error {
		fired = itemID + ":" + typ + ":" + code
		return nil
	}
	if err := f.SandboxFireWebhook(ctx, it.AccessToken, plaid.WebhookTypeTransactions, plaid.WebhookCodeSyncUpdatesAvailable); err != nil {
		t.Fatal(err)
	}
	if fired != ex.ItemID+":TRANSACTIONS:SYNC_UPDATES_AVAILABLE" {
		t.Errorf("fired = %q", fired)
	}

	f.Env = config.PlaidEnvProduction
	if _, err := f.SandboxCreatePublicToken(ctx, "ins_37", nil); err != plaid.ErrNotSandbox {
		t.Errorf("production = %v", err)
	}
}

func TestFakeKeys(t *testing.T) {
	f := New()
	f.AddKey(VerificationKey(FixtureWebhookKey))
	k, err := f.WebhookVerificationKey(context.Background(), "6c5516e1-92dc-479e-a8ff-5a51992e0001")
	if err != nil || k.Curve != "P-256" {
		t.Errorf("key = %v, %v", k, err)
	}
	if _, err := f.WebhookVerificationKey(context.Background(), "other"); err == nil {
		t.Error("unknown key id succeeded")
	}
}
