// Package plaidtest provides Fake, an in-memory plaid.Client scripted from
// Plaid-shaped JSON fixtures, for tests of the sync engine and the API.
// The fixtures are embedded and decoded through the same functions the
// real client uses, so a test exercises the translation code too.
package plaidtest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"plaidsync/internal/config"
	"plaidsync/internal/plaid"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
)

// Op names accepted by Fake.FailNext, one per Client method.
const (
	OpCreateLinkToken          = "CreateLinkToken"
	OpGetLinkSession           = "GetLinkSession"
	OpExchangePublicToken      = "ExchangePublicToken"
	OpGetItem                  = "GetItem"
	OpGetAccounts              = "GetAccounts"
	OpSyncTransactions         = "SyncTransactions"
	OpGetRecurring             = "GetRecurringTransactions"
	OpRemoveItem               = "RemoveItem"
	OpUpdateWebhook            = "UpdateWebhook"
	OpWebhookVerificationKey   = "WebhookVerificationKey"
	OpSandboxCreatePublicToken = "SandboxCreatePublicToken"
	OpSandboxFireWebhook       = "SandboxFireWebhook"
	OpSandboxResetLogin        = "SandboxResetLogin"
)

// Item is one scripted Plaid item. Pages maps the cursor a caller sends to
// the page the fake answers with; the engine sends "" first and then each
// NextCursor, so a chain of pages is a chain of cursors (see SetPages).
type Item struct {
	AccessToken secret.Token
	// Info is what GetItem returns. Info.ItemID is required. Info.Error,
	// when set, is also returned as the error of SyncTransactions, which
	// is how Plaid behaves for an item in ITEM_LOGIN_REQUIRED.
	Info plaid.ItemInfo
	// Accounts is what GetAccounts returns.
	Accounts []store.Account
	// Pages by request cursor.
	Pages map[string]*plaid.SyncPage
	// Webhook is the current webhook URL, updated by UpdateWebhook.
	Webhook string
	// Removed is set by RemoveItem; every later call fails with
	// ITEM_NOT_FOUND, as Plaid does.
	Removed bool
	// SyncCalls counts SyncTransactions calls for this item.
	SyncCalls int
	// Streams is what GetRecurringTransactions returns.
	Streams []store.RecurringStream
}

// SetPages installs pages as a chain: pages[0] answers cursor "", and each
// page's NextCursor (assigned when empty) is the cursor the next page
// answers. Every page but the last gets HasMore true; the last gets false
// and, when its NextCursor is empty, "cursor-final". Passing no pages
// installs a single NOT_READY page.
func (it *Item) SetPages(pages ...*plaid.SyncPage) {
	if len(pages) == 0 {
		// Plaid's not-ready answer: no rows, has_more false and an empty
		// cursor, which the engine must not persist.
		it.Pages = map[string]*plaid.SyncPage{"": {UpdateStatus: plaid.UpdateStatusNotReady}}
		return
	}
	it.Pages = make(map[string]*plaid.SyncPage, len(pages))
	cursor := ""
	for i, p := range pages {
		last := i == len(pages)-1
		p.HasMore = !last
		if p.NextCursor == "" {
			if last {
				p.NextCursor = "cursor-final"
			} else {
				p.NextCursor = fmt.Sprintf("cursor-%d", i+1)
			}
		}
		it.Pages[cursor] = p
		cursor = p.NextCursor
	}
}

// Call is one recorded Client call.
type Call struct {
	Op     string
	ItemID string
	// Cursor is set for SyncTransactions.
	Cursor string
	// Link is set for CreateLinkToken.
	Link *plaid.LinkTokenParams
	// WebhookCode is set for SandboxFireWebhook.
	WebhookCode string
}

// Fake is a scripted plaid.Client. It is safe for concurrent use. The zero
// value is not usable; call New.
type Fake struct {
	// Env decides whether the Sandbox methods work; New sets sandbox.
	Env config.PlaidEnv
	// LinkTokenTTL is the expiry CreateLinkToken reports; New sets 4h.
	LinkTokenTTL time.Duration
	// SandboxTemplate, when set, is copied for every item created through
	// SandboxCreatePublicToken: its Accounts and Pages become the new
	// item's script. Info.InstitutionID is taken from the call.
	SandboxTemplate *Item
	// WebhookSink, when set, is invoked by SandboxFireWebhook with the
	// item id and code, so a test can deliver the webhook to a handler.
	WebhookSink func(ctx context.Context, itemID, webhookType, webhookCode string) error

	mu     sync.Mutex
	items  map[string]*Item          // by access token
	byID   map[string]*Item          // by item id
	public map[string]*Item          // by public token
	hosted map[string]*hostedSession // by link token
	keys   map[string]*plaid.VerificationKey
	fails  map[string][]error
	calls  []Call
	seq    int
}

// hostedSession is the scripted state of one Hosted Link token: what
// GetLinkSession reports until a test advances it with StartHostedSession,
// FinishHostedSession or ExitHostedSession.
type hostedSession struct {
	expiration  time.Time
	started     bool
	finished    bool
	publicToken string
	exit        *plaid.LinkExit
}

// New returns an empty Fake in Sandbox mode.
func New() *Fake {
	return &Fake{
		Env:          config.PlaidEnvSandbox,
		LinkTokenTTL: 4 * time.Hour,
		items:        make(map[string]*Item),
		byID:         make(map[string]*Item),
		public:       make(map[string]*Item),
		hosted:       make(map[string]*hostedSession),
		keys:         make(map[string]*plaid.VerificationKey),
		fails:        make(map[string][]error),
	}
}

// AddItem registers it and returns it. An empty AccessToken is assigned
// "access-sandbox-<n>"; Info.ItemID must be set; nil Pages become a
// single NOT_READY page.
func (f *Fake) AddItem(it *Item) *Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it.Info.ItemID == "" {
		panic("plaidtest: AddItem: Info.ItemID is empty")
	}
	if it.AccessToken.IsZero() {
		f.seq++
		it.AccessToken = secret.NewToken(fmt.Sprintf("access-sandbox-%d", f.seq))
	}
	if it.Pages == nil {
		it.SetPages()
	}
	if it.Info.Webhook != nil && it.Webhook == "" {
		it.Webhook = *it.Info.Webhook
	}
	f.items[it.AccessToken.Expose()] = it
	f.byID[it.Info.ItemID] = it
	return it
}

// AddPublicToken makes publicToken exchangeable for it (which must have
// been added). Plaid public tokens are single-use; so are these.
func (f *Fake) AddPublicToken(publicToken string, it *Item) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.public[publicToken] = it
}

// AddKey registers a webhook verification key.
func (f *Fake) AddKey(k *plaid.VerificationKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys[k.KeyID] = k
}

// FailNext queues err to be returned by the next call of op (one of the
// Op constants), before any other behaviour. Several queued errors are
// returned in order.
func (f *Fake) FailNext(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fails[op] = append(f.fails[op], err)
}

// Calls returns a copy of every recorded call, in order.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// CallsTo returns the recorded calls of one op.
func (f *Fake) CallsTo(op string) []Call {
	var out []Call
	for _, c := range f.Calls() {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
}

// Item returns the scripted item with the given id, or nil.
func (f *Fake) Item(itemID string) *Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byID[itemID]
}

// ItemByToken returns the scripted item behind an access token, or nil.
func (f *Fake) ItemByToken(accessToken secret.Token) *Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[accessToken.Expose()]
}

// begin records the call and pops a queued failure. It must be called
// with the mutex held.
func (f *Fake) begin(c Call) error {
	f.calls = append(f.calls, c)
	if q := f.fails[c.Op]; len(q) > 0 {
		f.fails[c.Op] = q[1:]
		return q[0]
	}
	return nil
}

// lookup resolves an access token to a live item. It must be called with
// the mutex held.
func (f *Fake) lookup(endpoint string, accessToken secret.Token) (*Item, error) {
	it, ok := f.items[accessToken.Expose()]
	if !ok {
		return nil, &plaid.Error{Endpoint: endpoint, Type: "INVALID_INPUT", Code: "INVALID_ACCESS_TOKEN",
			Message: "could not find matching access token", RequestID: f.requestID(), HTTPStatus: 400}
	}
	if it.Removed {
		return nil, &plaid.Error{Endpoint: endpoint, Type: "ITEM_ERROR", Code: "ITEM_NOT_FOUND",
			Message:   "The Item you requested cannot be found. This Item does not exist, has been previously removed via /item/remove, or has had access removed by the user.",
			RequestID: f.requestID(), HTTPStatus: 400}
	}
	return it, nil
}

// requestID returns a fresh fake request id. Mutex held.
func (f *Fake) requestID() string {
	f.seq++
	return fmt.Sprintf("req-fake-%d", f.seq)
}

// CreateLinkToken implements plaid.Client.
func (f *Fake) CreateLinkToken(ctx context.Context, p plaid.LinkTokenParams) (*plaid.LinkToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	params := p
	c := Call{Op: OpCreateLinkToken, Link: &params}
	if !p.AccessToken.IsZero() {
		if it, ok := f.items[p.AccessToken.Expose()]; ok {
			c.ItemID = it.Info.ItemID
		}
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	if !p.AccessToken.IsZero() {
		if _, err := f.lookup("/link/token/create", p.AccessToken); err != nil {
			return nil, err
		}
	}
	f.seq++
	lt := &plaid.LinkToken{
		Token:      fmt.Sprintf("link-sandbox-%d", f.seq),
		Expiration: time.Now().Add(f.LinkTokenTTL).UTC(),
		RequestID:  f.requestID(),
	}
	if p.Hosted {
		lt.HostedURL = "https://hosted.plaid.test/" + lt.Token
		f.hosted[lt.Token] = &hostedSession{expiration: lt.Expiration}
	}
	return lt, nil
}

// GetLinkSession implements plaid.Client. Only tokens created with
// LinkTokenParams.Hosted are known; anything else is INVALID_LINK_TOKEN,
// as is an expired token.
func (f *Fake) GetLinkSession(ctx context.Context, linkToken string) (*plaid.LinkSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(Call{Op: OpGetLinkSession}); err != nil {
		return nil, err
	}
	hs := f.hosted[linkToken]
	if hs == nil || time.Now().After(hs.expiration) {
		return nil, &plaid.Error{Endpoint: "/link/token/get", Type: "INVALID_INPUT", Code: "INVALID_LINK_TOKEN",
			Message: "the provided link token is invalid or expired", RequestID: f.requestID(), HTTPStatus: 400}
	}
	ls := &plaid.LinkSession{Expiration: hs.expiration, Started: hs.started, Finished: hs.finished, RequestID: f.requestID()}
	if hs.publicToken != "" {
		ls.PublicToken = secret.NewToken(hs.publicToken)
	}
	if hs.exit != nil {
		e := *hs.exit
		ls.Exit = &e
	}
	return ls, nil
}

// StartHostedSession marks a Hosted Link token as opened by the user but
// not yet finished. It panics for a token CreateLinkToken did not issue
// with Hosted set.
func (f *Fake) StartHostedSession(linkToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostedSession(linkToken).started = true
}

// FinishHostedSession ends a Hosted Link session in success with the
// given public token, which the test must have registered with
// AddPublicToken. An empty publicToken is an update-mode finish without a
// token.
func (f *Fake) FinishHostedSession(linkToken, publicToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hs := f.hostedSession(linkToken)
	hs.started, hs.finished, hs.publicToken, hs.exit = true, true, publicToken, nil
}

// ExitHostedSession ends a Hosted Link session with the user leaving; err
// may be nil for a plain close.
func (f *Fake) ExitHostedSession(linkToken string, err *plaid.Error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	hs := f.hostedSession(linkToken)
	hs.started, hs.finished, hs.publicToken = true, true, ""
	hs.exit = &plaid.LinkExit{Error: err}
}

// ExpireHostedSession makes the token expired for GetLinkSession.
func (f *Fake) ExpireHostedSession(linkToken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hostedSession(linkToken).expiration = time.Now().Add(-time.Second)
}

func (f *Fake) hostedSession(linkToken string) *hostedSession {
	hs := f.hosted[linkToken]
	if hs == nil {
		panic("plaidtest: no hosted session for link token " + linkToken)
	}
	return hs
}

// ExchangePublicToken implements plaid.Client.
func (f *Fake) ExchangePublicToken(ctx context.Context, publicToken secret.Token) (*plaid.Exchange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it := f.public[publicToken.Expose()]
	c := Call{Op: OpExchangePublicToken}
	if it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	if it == nil {
		return nil, &plaid.Error{Endpoint: "/item/public_token/exchange", Type: "INVALID_INPUT", Code: "INVALID_PUBLIC_TOKEN",
			Message: "could not find matching public token", RequestID: f.requestID(), HTTPStatus: 400}
	}
	delete(f.public, publicToken.Expose())
	return &plaid.Exchange{AccessToken: it.AccessToken, ItemID: it.Info.ItemID, RequestID: f.requestID()}, nil
}

// GetItem implements plaid.Client.
func (f *Fake) GetItem(ctx context.Context, accessToken secret.Token) (*plaid.ItemInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpGetItem}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	it, err := f.lookup("/item/get", accessToken)
	if err != nil {
		return nil, err
	}
	info := it.Info
	if it.Webhook != "" {
		w := it.Webhook
		info.Webhook = &w
	}
	info.RequestID = f.requestID()
	return &info, nil
}

// GetAccounts implements plaid.Client.
func (f *Fake) GetAccounts(ctx context.Context, accessToken secret.Token) (*plaid.Accounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpGetAccounts}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	it, err := f.lookup("/accounts/get", accessToken)
	if err != nil {
		return nil, err
	}
	if it.Info.Error != nil {
		e := *it.Info.Error
		e.Endpoint = "/accounts/get"
		return nil, &e
	}
	return &plaid.Accounts{Accounts: append([]store.Account(nil), it.Accounts...), RequestID: f.requestID()}, nil
}

// SyncTransactions implements plaid.Client.
func (f *Fake) SyncTransactions(ctx context.Context, accessToken secret.Token, cursor string) (*plaid.SyncPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpSyncTransactions, Cursor: cursor}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
		it.SyncCalls++
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	it, err := f.lookup("/transactions/sync", accessToken)
	if err != nil {
		return nil, err
	}
	if it.Info.Error != nil {
		e := *it.Info.Error
		e.Endpoint = "/transactions/sync"
		e.RequestID = f.requestID()
		return nil, &e
	}
	p, ok := it.Pages[cursor]
	if !ok {
		return nil, &plaid.Error{Endpoint: "/transactions/sync", Type: "INVALID_INPUT", Code: "INVALID_FIELD",
			Message: fmt.Sprintf("plaidtest: no page scripted for cursor %q", cursor), RequestID: f.requestID(), HTTPStatus: 400}
	}
	out := *p
	out.Accounts = append([]store.Account(nil), p.Accounts...)
	out.Added = append([]store.Transaction(nil), p.Added...)
	out.Modified = append([]store.Transaction(nil), p.Modified...)
	out.Removed = append([]plaid.Removed(nil), p.Removed...)
	out.RequestID = f.requestID()
	return &out, nil
}

// GetRecurringTransactions implements plaid.Client.
func (f *Fake) GetRecurringTransactions(ctx context.Context, accessToken secret.Token) (*plaid.RecurringStreams, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpGetRecurring}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return nil, err
	}
	it, err := f.lookup("/transactions/recurring/get", accessToken)
	if err != nil {
		return nil, err
	}
	if it.Info.Error != nil {
		e := *it.Info.Error
		e.Endpoint = "/transactions/recurring/get"
		return nil, &e
	}
	return &plaid.RecurringStreams{Streams: append([]store.RecurringStream(nil), it.Streams...), RequestID: f.requestID()}, nil
}

// RemoveItem implements plaid.Client.
func (f *Fake) RemoveItem(ctx context.Context, accessToken secret.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpRemoveItem}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return err
	}
	it, err := f.lookup("/item/remove", accessToken)
	if err != nil {
		return err
	}
	it.Removed = true
	return nil
}

// UpdateWebhook implements plaid.Client.
func (f *Fake) UpdateWebhook(ctx context.Context, accessToken secret.Token, webhookURL string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpUpdateWebhook}
	if it := f.items[accessToken.Expose()]; it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return err
	}
	it, err := f.lookup("/item/webhook/update", accessToken)
	if err != nil {
		return err
	}
	it.Webhook = webhookURL
	return nil
}

// WebhookVerificationKey implements plaid.Client.
func (f *Fake) WebhookVerificationKey(ctx context.Context, keyID string) (*plaid.VerificationKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(Call{Op: OpWebhookVerificationKey}); err != nil {
		return nil, err
	}
	k, ok := f.keys[keyID]
	if !ok {
		return nil, &plaid.Error{Endpoint: "/webhook_verification_key/get", Type: "INVALID_INPUT", Code: "INVALID_FIELD",
			Message: "key_id is not a known key", RequestID: f.requestID(), HTTPStatus: 400}
	}
	out := *k
	out.RequestID = f.requestID()
	return &out, nil
}

// SandboxCreatePublicToken implements plaid.Client. It creates a new item
// from SandboxTemplate (or an empty one) and returns a public token for
// it.
func (f *Fake) SandboxCreatePublicToken(ctx context.Context, institutionID string, products []string) (secret.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(Call{Op: OpSandboxCreatePublicToken}); err != nil {
		return secret.Token{}, err
	}
	if f.Env != config.PlaidEnvSandbox {
		return secret.Token{}, plaid.ErrNotSandbox
	}
	f.seq++
	n := f.seq
	it := &Item{}
	if f.SandboxTemplate != nil {
		it.Info = f.SandboxTemplate.Info
		it.Accounts = append([]store.Account(nil), f.SandboxTemplate.Accounts...)
		it.Pages = f.SandboxTemplate.Pages
	}
	it.Info.ItemID = fmt.Sprintf("item-sandbox-%d", n)
	inst := institutionID
	it.Info.InstitutionID = &inst
	if it.Info.Raw == nil {
		it.Info.Raw = []byte(fmt.Sprintf(`{"item_id":%q,"institution_id":%q}`, it.Info.ItemID, inst))
	}
	if len(products) > 0 {
		it.Info.Products = append([]string(nil), products...)
	}
	it.AccessToken = secret.NewToken(fmt.Sprintf("access-sandbox-%d", n))
	if it.Pages == nil {
		it.SetPages()
	}
	f.items[it.AccessToken.Expose()] = it
	f.byID[it.Info.ItemID] = it
	public := fmt.Sprintf("public-sandbox-%d", n)
	f.public[public] = it
	return secret.NewToken(public), nil
}

// SandboxFireWebhook implements plaid.Client. With WebhookSink set it
// delivers the webhook synchronously and returns the sink's error.
func (f *Fake) SandboxFireWebhook(ctx context.Context, accessToken secret.Token, webhookType, webhookCode string) error {
	f.mu.Lock()
	c := Call{Op: OpSandboxFireWebhook, WebhookCode: webhookCode}
	it := f.items[accessToken.Expose()]
	if it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		f.mu.Unlock()
		return err
	}
	if f.Env != config.PlaidEnvSandbox {
		f.mu.Unlock()
		return plaid.ErrNotSandbox
	}
	if _, err := f.lookup("/sandbox/item/fire_webhook", accessToken); err != nil {
		f.mu.Unlock()
		return err
	}
	sink := f.WebhookSink
	itemID := it.Info.ItemID
	f.mu.Unlock()
	if sink != nil {
		return sink(ctx, itemID, webhookType, webhookCode)
	}
	return nil
}

// SandboxResetLogin implements plaid.Client. The item's next
// SyncTransactions and GetAccounts fail with ITEM_LOGIN_REQUIRED.
func (f *Fake) SandboxResetLogin(ctx context.Context, accessToken secret.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := Call{Op: OpSandboxResetLogin}
	it := f.items[accessToken.Expose()]
	if it != nil {
		c.ItemID = it.Info.ItemID
	}
	if err := f.begin(c); err != nil {
		return err
	}
	if f.Env != config.PlaidEnvSandbox {
		return plaid.ErrNotSandbox
	}
	if _, err := f.lookup("/sandbox/item/reset_login", accessToken); err != nil {
		return err
	}
	it.Info.Error = &plaid.Error{Type: "ITEM_ERROR", Code: "ITEM_LOGIN_REQUIRED",
		Message:        "the login details of this item have changed (credentials, MFA, or required user action) and a user login is required to update this information.",
		DisplayMessage: "Please re-enter your credentials.", HTTPStatus: 400}
	return nil
}

// RepairLogin clears an item's standing error, as a completed update-mode
// Link session would.
func (f *Fake) RepairLogin(itemID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it := f.byID[itemID]; it != nil {
		it.Info.Error = nil
	}
}

var _ plaid.Client = (*Fake)(nil)
