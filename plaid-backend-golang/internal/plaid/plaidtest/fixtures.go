package plaidtest

import (
	"embed"
	"fmt"

	"plaidsync/internal/plaid"
)

// Fixture names. Each is a Plaid-shaped JSON body under testdata.
const (
	// FixtureSyncPage1 is the first of two /transactions/sync pages: two
	// accounts, two added transactions (one posted grocery purchase, one
	// pending ride), has_more true.
	FixtureSyncPage1 = "sync_page_1.json"
	// FixtureSyncPage2 is the final page: the ride posts (added with
	// pending_transaction_id, pending id in removed), a payroll deposit is
	// added, the grocery purchase is modified, has_more false.
	FixtureSyncPage2 = "sync_page_2.json"
	// FixtureSyncNotReady is the empty response Plaid sends right after an
	// item is linked: no rows, "" cursor, NOT_READY.
	FixtureSyncNotReady = "sync_not_ready.json"
	// FixtureAccountsGet is an /accounts/get body with the same two
	// accounts as FixtureSyncPage1.
	FixtureAccountsGet = "accounts_get.json"
	// FixtureItemGet is a healthy CIBC item.
	FixtureItemGet = "item_get.json"
	// FixtureItemGetLoginRequired is an Amex CA item in ITEM_LOGIN_REQUIRED
	// with a consent expiry.
	FixtureItemGetLoginRequired = "item_get_login_required.json"
	// FixtureWebhookKey is a /webhook_verification_key/get body.
	FixtureWebhookKey = "webhook_key.json"

	// Error bodies, as Plaid returns them with a 400 or 429.
	FixtureErrorLoginRequired = "error_item_login_required.json"
	FixtureErrorMutation      = "error_mutation.json"
	FixtureErrorRateLimit     = "error_rate_limit.json"
	FixtureErrorTrialLimit    = "error_trial_limit.json"
)

//go:embed testdata/*.json
var testdata embed.FS

// Fixture returns the raw bytes of a fixture. It panics on an unknown
// name: fixtures are test inputs and a typo is a bug in the test.
func Fixture(name string) []byte {
	b, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return b
}

// SyncPage decodes a /transactions/sync fixture. It panics on failure.
func SyncPage(name string) *plaid.SyncPage {
	p, err := plaid.DecodeSyncPage(Fixture(name))
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return p
}

// AccountsGet decodes an /accounts/get fixture. It panics on failure.
func AccountsGet(name string) *plaid.Accounts {
	a, err := plaid.DecodeAccountsGet(Fixture(name))
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return a
}

// ItemGet decodes an /item/get fixture. It panics on failure.
func ItemGet(name string) *plaid.ItemInfo {
	i, err := plaid.DecodeItemGet(Fixture(name))
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return i
}

// VerificationKey decodes a /webhook_verification_key/get fixture. It
// panics on failure.
func VerificationKey(name string) *plaid.VerificationKey {
	k, err := plaid.DecodeVerificationKey(Fixture(name))
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return k
}

// PlaidError decodes an error-body fixture into the *plaid.Error the real
// client would return for it, with the given endpoint and HTTP status.
func PlaidError(name, endpoint string, httpStatus int) *plaid.Error {
	e, err := plaid.DecodeError(Fixture(name), endpoint, httpStatus)
	if err != nil {
		panic(fmt.Sprintf("plaidtest: fixture %s: %v", name, err))
	}
	return e
}

// CIBCItem returns a healthy two-account item scripted from the fixtures:
// item_get.json for GetItem, accounts_get.json for GetAccounts, and the
// two sync pages as its pagination. Add it to a Fake with AddItem.
func CIBCItem() *Item {
	it := &Item{
		Info:     *ItemGet(FixtureItemGet),
		Accounts: AccountsGet(FixtureAccountsGet).Accounts,
	}
	it.SetPages(SyncPage(FixtureSyncPage1), SyncPage(FixtureSyncPage2))
	return it
}
