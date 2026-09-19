// Package plaid is plaidsync's view of the Plaid API: the handful of calls
// the service makes, behind the [Client] interface, with responses
// translated into the store's row types and errors classified into the
// sync engine's outcome buckets.
//
// [HTTPClient] is the real implementation over plaid-go. It uses plaid-go
// for request construction, authentication and transport, but decodes the
// bodies that carry money a second time from the raw JSON with json.Number,
// so that every amount reaches money.Amount as the exact decimal text Plaid
// sent and never passes through float64. Each account and transaction also
// keeps its complete original object in Raw. The decoders are exported
// ([DecodeSyncPage] and friends) so that the fake in package plaidtest and
// any test can build responses from Plaid-shaped JSON fixtures through the
// same code path.
//
// Nothing in this package logs an access token, a public token or a Link
// token. Access tokens travel as secret.Token and are exposed only at the
// point where they are placed in a request.
package plaid

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"plaidsync/internal/secret"
	"plaidsync/internal/store"
)

// ErrNotSandbox is returned by the Sandbox-only methods when the client is
// configured for Production. The Sandbox endpoints do not exist there, and
// the service must never try them against real credentials.
var ErrNotSandbox = errors.New("plaid: sandbox endpoints are available only when PLAID_ENV=sandbox")

// Client is the subset of the Plaid API plaidsync uses. Every method is
// safe for concurrent use. Errors from Plaid itself are returned as *Error
// (test with [AsError]); transport failures are returned as they are, and
// [Classify] handles both.
type Client interface {
	// CreateLinkToken calls /link/token/create. With p.AccessToken zero it
	// creates a token for linking a new item using the configured products,
	// country codes, webhook and redirect URI; with it set it creates an
	// update-mode token for that item.
	CreateLinkToken(ctx context.Context, p LinkTokenParams) (*LinkToken, error)

	// GetLinkSession calls /link/token/get and reports the outcome of the
	// Link sessions run with a link token. Hosted Link has no frontend
	// callback, so polling this is how the service learns that the user
	// finished (or gave up). Plaid keeps session data for six hours after
	// a session ends.
	GetLinkSession(ctx context.Context, linkToken string) (*LinkSession, error)

	// ExchangePublicToken calls /item/public_token/exchange.
	ExchangePublicToken(ctx context.Context, publicToken secret.Token) (*Exchange, error)

	// GetItem calls /item/get.
	GetItem(ctx context.Context, accessToken secret.Token) (*ItemInfo, error)

	// GetAccounts calls /accounts/get, the canonical account list of an item.
	GetAccounts(ctx context.Context, accessToken secret.Token) (*Accounts, error)

	// SyncTransactions calls /transactions/sync once for the given cursor
	// ("" for the first call of an item or a full rebuild) and returns that
	// page. Draining the pagination is the caller's job.
	SyncTransactions(ctx context.Context, accessToken secret.Token, cursor string) (*SyncPage, error)

	// GetRecurringTransactions calls /transactions/recurring/get and returns
	// every inflow and outflow stream of the item. It needs the Transactions
	// product and, in Production, the Recurring Transactions add-on; Plaid
	// answers PRODUCT_NOT_READY until the item's history has been pulled.
	GetRecurringTransactions(ctx context.Context, accessToken secret.Token) (*RecurringStreams, error)

	// RemoveItem calls /item/remove. On the Trial plan this does not free
	// an item slot; the caller decides whether that is acceptable.
	RemoveItem(ctx context.Context, accessToken secret.Token) error

	// UpdateWebhook calls /item/webhook/update.
	UpdateWebhook(ctx context.Context, accessToken secret.Token, webhookURL string) error

	// WebhookVerificationKey calls /webhook_verification_key/get for the
	// key named in a webhook's Plaid-Verification header.
	WebhookVerificationKey(ctx context.Context, keyID string) (*VerificationKey, error)

	// SandboxCreatePublicToken calls /sandbox/public_token/create, which
	// stands in for Link in Sandbox. ErrNotSandbox outside Sandbox.
	SandboxCreatePublicToken(ctx context.Context, params SandboxItemParams) (secret.Token, error)

	// SandboxFireWebhook calls /sandbox/item/fire_webhook. ErrNotSandbox
	// outside Sandbox.
	SandboxFireWebhook(ctx context.Context, accessToken secret.Token, webhookType, webhookCode string) error

	// SandboxResetLogin calls /sandbox/item/reset_login, which forces the
	// item into ITEM_LOGIN_REQUIRED. ErrNotSandbox outside Sandbox.
	SandboxResetLogin(ctx context.Context, accessToken secret.Token) error
}

// LinkTokenParams are the per-call inputs of CreateLinkToken. Everything
// else about a Link token (products, country codes, webhook, redirect URI,
// history depth, client name, language) is deployment configuration and is
// fixed when the client is built.
type LinkTokenParams struct {
	// ClientUserID is Plaid's user.client_user_id, a stable identifier for
	// the end user. Single-tenant deployments use one constant value.
	ClientUserID string

	// AccessToken, when set, requests update mode for that item: no
	// products, no webhook, and the item's existing access token remains
	// valid after Link completes.
	AccessToken secret.Token

	// AccountSelectionEnabled sets update.account_selection_enabled, which
	// Canadian non-OAuth items need when the user must add or remove
	// accounts. Update mode only.
	AccountSelectionEnabled bool

	// AdditionalConsentedProducts, when non-nil, replaces the configured
	// additional_consented_products list. Update mode uses it to gather
	// consent after ADDITIONAL_CONSENT_REQUIRED.
	AdditionalConsentedProducts []string

	// Hosted requests a Hosted Link session: Plaid hosts the Link UI at
	// the returned HostedURL, which the user opens in an ordinary browser.
	// Nothing in this process ever runs Link, so no redirect URI is
	// needed for OAuth institutions; the outcome is read back with
	// GetLinkSession. Works for new items and, with AccessToken set, for
	// update mode.
	Hosted bool
}

// SandboxItemParams are the per-call inputs of SandboxCreatePublicToken.
// Everything else about the item (country codes, webhook, history depth)
// is deployment configuration and is fixed when the client is built.
type SandboxItemParams struct {
	// InstitutionID is the Sandbox institution to link. Required.
	InstitutionID string

	// Products is the initial product list; nil means the configured
	// products.
	Products []string

	// User selects the Sandbox test user Plaid signs in as. The zero
	// value means Plaid's default user, which returns its own canned
	// accounts and transactions.
	User SandboxUser

	// DaysRequested is the transaction history depth to ask for. Zero
	// means the configured TransactionsDaysRequested, which is what Link
	// asks for; Plaid's own default here is only 90 days, so leaving it
	// unset would give a Sandbox item less history than a real one.
	DaysRequested int
}

// SandboxUser overrides the Sandbox test user /sandbox/public_token/create
// signs in as. Plaid's custom user is Username "user_custom" with Config
// holding the JSON that describes the accounts, balances and transactions
// the item will return; Plaid caps one config at roughly 250 transactions
// across at most 10 accounts, and rejects it at OAuth institutions. The
// config is demo data, not a credential, so it is not modelled as a secret,
// but this package never logs it either.
type SandboxUser struct {
	// Username is Plaid's options.override_username. Empty leaves the
	// default user in place.
	Username string

	// Config is Plaid's options.override_password: for "user_custom", the
	// custom-user JSON as a string. Ignored when Username is empty.
	Config string
}

// LinkToken is the result of /link/token/create. The token itself is handed
// to a browser, so it is not modelled as a secret, but it is still never
// logged by this package.
type LinkToken struct {
	Token      string
	Expiration time.Time
	// HostedURL is the Hosted Link page for this token; set only when the
	// token was created with LinkTokenParams.Hosted.
	HostedURL string
	RequestID string
}

// LinkSession is the outcome of the Link sessions run with one link token,
// as reported by /link/token/get. A token can be opened more than once
// (the user closes the page and comes back), so the fields summarise every
// session: a success anywhere wins, then a session still in progress,
// then the most recent exit.
type LinkSession struct {
	// Expiration is when the link token stops working.
	Expiration time.Time
	// Started reports whether the user has opened Link at all.
	Started bool
	// Finished reports whether a session ended, in success or by exit,
	// with no other session still in progress.
	Finished bool
	// PublicToken is set when a session ended in success. Update-mode
	// sessions can finish without one; Finished with a zero PublicToken
	// and nil Exit is that case.
	PublicToken secret.Token
	// Exit is set when the user left Link without linking. Exit.Error is
	// Plaid's error when the exit was caused by one, nil when the user
	// simply closed the page.
	Exit      *LinkExit
	RequestID string
}

// LinkExit describes a Link session the user left without linking.
type LinkExit struct {
	Error *Error
}

// Exchange is the result of /item/public_token/exchange.
type Exchange struct {
	AccessToken secret.Token
	ItemID      string
	RequestID   string
}

// ItemInfo is the result of /item/get: the fields plaidsync records plus
// the complete item object. Raw contains no secrets (Plaid never returns
// the access token) and is what plaid_items.raw stores.
type ItemInfo struct {
	ItemID          string
	InstitutionID   *string
	InstitutionName *string
	// Webhook is the URL Plaid currently delivers this item's webhooks to.
	Webhook *string
	// ConsentExpiresAt is consent_expiration_time; nil when the item has no
	// consent expiry.
	ConsentExpiresAt *time.Time
	// Products, ConsentedProducts, BilledProducts and AvailableProducts are
	// Plaid's product lists for the item, in Plaid's lowercase spelling.
	Products, ConsentedProducts, BilledProducts, AvailableProducts []string
	// Error is the item's standing error (for example ITEM_LOGIN_REQUIRED),
	// nil when the item is healthy. It is not returned as the method's
	// error: /item/get succeeds on an errored item.
	Error *Error
	// Raw is the item object as Plaid sent it.
	Raw       json.RawMessage
	RequestID string
}

// Accounts is the result of /accounts/get. ItemID on each account is left
// empty; the store fills it from the locked item.
type Accounts struct {
	Accounts  []store.Account
	RequestID string
}

// RecurringStreams is the result of /transactions/recurring/get: inflow
// and outflow streams together, each with Direction set and Raw holding
// the stream object. ItemID on each stream is left empty.
type RecurringStreams struct {
	Streams   []store.RecurringStream
	RequestID string
}

// Removed is one entry of a /transactions/sync "removed" list.
type Removed struct {
	TransactionID string
	AccountID     string
}

// SyncPage is one /transactions/sync response. Added and Modified are
// already translated into store rows with Raw set to the original object;
// callers treat both as upserts by TransactionID.
type SyncPage struct {
	// Accounts lists only the accounts that have transactions in this
	// response, not the canonical account list.
	Accounts []store.Account
	Added    []store.Transaction
	Modified []store.Transaction
	Removed  []Removed
	// NextCursor is the cursor to persist once HasMore is false. It is ""
	// when the item's transactions are not yet available.
	NextCursor string
	HasMore    bool
	// UpdateStatus is transactions_update_status:
	// TRANSACTIONS_UPDATE_STATUS_UNKNOWN, NOT_READY,
	// INITIAL_UPDATE_COMPLETE or HISTORICAL_UPDATE_COMPLETE.
	UpdateStatus string
	RequestID    string
}

// Transactions update statuses reported on a SyncPage.
const (
	UpdateStatusUnknown            = "TRANSACTIONS_UPDATE_STATUS_UNKNOWN"
	UpdateStatusNotReady           = "NOT_READY"
	UpdateStatusInitialComplete    = "INITIAL_UPDATE_COMPLETE"
	UpdateStatusHistoricalComplete = "HISTORICAL_UPDATE_COMPLETE"
)

// VerificationKey is one JSON Web Key from /webhook_verification_key/get.
// Plaid's keys are ES256 (P-256) keys; X and Y are base64url coordinates.
type VerificationKey struct {
	KeyID     string
	Algorithm string
	Curve     string
	KeyType   string
	Use       string
	X, Y      string
	CreatedAt time.Time
	// ExpiredAt is nil while the key is current. Plaid rotates keys and
	// keeps an expired key resolvable so in-flight webhooks still verify.
	ExpiredAt *time.Time
	RequestID string
}

// Webhook types and codes plaidsync sends in Sandbox and handles on
// receipt. Codes not listed here are acknowledged and ignored.
const (
	WebhookTypeTransactions = "TRANSACTIONS"
	WebhookTypeItem         = "ITEM"

	WebhookCodeSyncUpdatesAvailable  = "SYNC_UPDATES_AVAILABLE"
	WebhookCodeError                 = "ERROR"
	WebhookCodePendingDisconnect     = "PENDING_DISCONNECT"
	WebhookCodePendingExpiration     = "PENDING_EXPIRATION"
	WebhookCodeLoginRepaired         = "LOGIN_REPAIRED"
	WebhookCodeNewAccountsAvailable  = "NEW_ACCOUNTS_AVAILABLE"
	WebhookCodeUserPermissionRevoked = "USER_PERMISSION_REVOKED"
)
