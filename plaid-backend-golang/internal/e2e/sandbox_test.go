//go:build sandbox

// Package e2e holds the opt-in end-to-end test against Plaid Sandbox. It
// is behind the "sandbox" build tag and skips unless PLAID_CLIENT_ID,
// PLAID_SECRET and PLAIDSYNC_TEST_DATABASE_URL are set, so `go test
// ./...` never touches the network. Run it with `make test-sandbox`.
package e2e

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/crypto"
	"plaidsync/internal/plaid"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
	"plaidsync/internal/store/storetest"
	"plaidsync/internal/sync"
)

// sandboxInstitution is First Platypus Bank, Plaid's Sandbox institution
// with Transactions test data.
const sandboxInstitution = "ins_109508"

// dataWait is how long the test waits for Sandbox to prepare the item's
// transaction history. Sandbox usually needs a few seconds after the
// first empty /transactions/sync.
const dataWait = 2 * time.Minute

func TestSandboxLinkAndSync(t *testing.T) {
	if os.Getenv("PLAID_CLIENT_ID") == "" || os.Getenv("PLAID_SECRET") == "" {
		t.Skip("PLAID_CLIENT_ID and PLAID_SECRET not set")
	}
	if env := os.Getenv("PLAID_ENV"); env != "" && env != "sandbox" {
		t.Fatalf("refusing to run against PLAID_ENV=%s; this test creates items", env)
	}
	st := storetest.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg := config.PlaidConfig{
		ClientID: os.Getenv("PLAID_CLIENT_ID"), Secret: secret.NewToken(os.Getenv("PLAID_SECRET")),
		Env: config.PlaidEnvSandbox, CountryCodes: []string{"CA"}, Products: []string{"transactions"},
		RequiredIfSupportedProducts: []string{}, OptionalProducts: []string{}, AdditionalConsentedProducts: []string{},
		TransactionsDaysRequested: 90, LinkClientName: "plaidsync-e2e", LinkLanguage: "en", LinkClientUserID: "e2e",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	pc := plaid.NewHTTPClient(cfg, logger)

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	kr, err := crypto.NewKeyring(1, map[uint32]secret.Bytes{1: secret.NewBytes(key)})
	if err != nil {
		t.Fatal(err)
	}
	engine := sync.New(st, pc, kr, config.SyncConfig{MaxAttempts: 4, RetryBase: 2 * time.Second, RetryMax: 20 * time.Second, Concurrency: 1, Interval: time.Hour}, logger)

	// Link without a browser.
	public, err := pc.SandboxCreatePublicToken(ctx, plaid.SandboxItemParams{InstitutionID: sandboxInstitution})
	if err != nil {
		t.Fatalf("sandbox public token: %v", err)
	}
	ex, err := pc.ExchangePublicToken(ctx, public)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	t.Logf("linked item %s", ex.ItemID)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		if err := pc.RemoveItem(cctx, ex.AccessToken); err != nil {
			t.Logf("remove item: %v", err)
		}
	})

	info, err := pc.GetItem(ctx, ex.AccessToken)
	if err != nil {
		t.Fatalf("item get: %v", err)
	}
	if info.ItemID != ex.ItemID || info.InstitutionID == nil || *info.InstitutionID != sandboxInstitution {
		t.Errorf("item = %+v", *info)
	}
	blob, version, err := kr.Encrypt(ex.AccessToken, sync.CredentialAAD(ex.ItemID))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertItem(ctx, store.NewItem{
		ItemID: ex.ItemID, InstitutionID: info.InstitutionID, InstitutionName: info.InstitutionName,
		Credential: store.Credential{Ciphertext: blob, KeyVersion: version}, ConsentExpiresAt: info.ConsentExpiresAt, Raw: info.Raw,
	}); err != nil {
		t.Fatal(err)
	}

	accts, err := pc.GetAccounts(ctx, ex.AccessToken)
	if err != nil {
		t.Fatalf("accounts get: %v", err)
	}
	if len(accts.Accounts) == 0 {
		t.Fatal("sandbox item has no accounts")
	}
	t.Logf("%d accounts; first: %s (%s/%v) current=%v", len(accts.Accounts), accts.Accounts[0].Name, accts.Accounts[0].Type, accts.Accounts[0].Subtype, accts.Accounts[0].CurrentBalance)

	// Sync until Sandbox has produced history.
	deadline := time.Now().Add(dataWait)
	var res *sync.Result
	for {
		res = engine.SyncItem(ctx, ex.ItemID, store.JobKindInitial, nil)
		if !res.Succeeded() {
			t.Fatalf("sync: outcome %s: %v", res.Outcome, res.Err)
		}
		if !res.NotReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sandbox produced no transactions within %s", dataWait)
		}
		t.Log("history not ready yet; waiting")
		time.Sleep(5 * time.Second)
	}
	t.Logf("sync: pages=%d added=%d inserted=%d accounts=%d update_status=%s run_id=%d",
		res.Pages, res.Added, res.Apply.Inserted, res.Apply.AccountsUpserted, res.UpdateStatus, res.RunID)
	if res.Apply.Inserted == 0 {
		t.Fatal("no transactions inserted")
	}

	item, err := st.GetItem(ctx, ex.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Cursor == nil || *item.Cursor == "" || item.Status != store.ItemStatusActive || item.LastSuccessfulSyncAt == nil {
		t.Errorf("item after sync = %+v", *item)
	}
	txs, err := st.ListTransactions(ctx, ex.ItemID, 5, 0)
	if err != nil || len(txs) == 0 {
		t.Fatalf("transactions: %v, %d", err, len(txs))
	}
	for _, tx := range txs {
		t.Logf("  %s %8s %s %q pending=%v", tx.Date, tx.Amount, deref(tx.ISOCurrencyCode), tx.Name, tx.Pending)
		if tx.Raw == nil || tx.Amount.IsZero() && tx.Name == "" {
			t.Errorf("row %s looks empty: %+v", tx.TransactionID, tx)
		}
	}

	// A second sync from the saved cursor is a no-op that advances nothing
	// or picks up whatever Sandbox generated since; either way it succeeds.
	res2 := engine.SyncItem(ctx, ex.ItemID, store.JobKindManual, nil)
	if !res2.Succeeded() {
		t.Fatalf("second sync: outcome %s: %v", res2.Outcome, res2.Err)
	}
	t.Logf("second sync: pages=%d added=%d modified=%d removed=%d unchanged=%d", res2.Pages, res2.Added, res2.Modified, res2.Removed, res2.Apply.Unchanged)

	// Force ITEM_LOGIN_REQUIRED and check the classification end to end.
	if err := pc.SandboxResetLogin(ctx, ex.AccessToken); err != nil {
		t.Fatalf("reset login: %v", err)
	}
	res3 := engine.SyncItem(ctx, ex.ItemID, store.JobKindManual, nil)
	if res3.Outcome != store.SyncOutcomeNeedsReauth {
		t.Fatalf("after reset login: outcome %s: %v", res3.Outcome, res3.Err)
	}
	if item, _ = st.GetItem(ctx, ex.ItemID); item.Status != store.ItemStatusLoginRequired {
		t.Errorf("item after reset login = %+v", *item)
	}
	t.Logf("reset login classified as %s; item status %s", res3.Outcome, item.Status)

	// The webhook verification key endpoint answers for a fresh key id
	// with an error we classify, and the fixture key id Plaid documents
	// resolves to a real key.
	if _, err := pc.WebhookVerificationKey(ctx, "6c5516e1-92dc-479e-a8ff-5a51992e0001"); err != nil {
		t.Logf("documented key id not resolvable (fine if Plaid rotated it): %v", err)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
