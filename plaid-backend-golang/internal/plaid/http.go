package plaid

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	plaidgo "github.com/plaid/plaid-go/v47/plaid"

	"plaidsync/internal/config"
	"plaidsync/internal/secret"
)

// callTimeout bounds one Plaid call at the HTTP client level, independent
// of the caller's context. The first /transactions/sync of an item can be
// several times slower than a steady-state page, so this is generous;
// callers that want less pass a shorter context.
const callTimeout = 90 * time.Second

// errPreview caps how much of a non-Plaid error body is kept in a message.
const errPreview = 200

// HTTPClient is the Client that talks to Plaid over HTTPS through
// plaid-go. Build it with NewHTTPClient.
type HTTPClient struct {
	api  *plaidgo.PlaidApiService
	env  config.PlaidEnv
	link config.PlaidConfig
	log  *slog.Logger
}

// NewHTTPClient builds a client for the environment and credentials in
// cfg. The Plaid credentials are sent as headers on every request. Link
// settings in cfg (products, country codes, webhook, redirect URI, history
// depth, client name, language) are used by CreateLinkToken. logger may be
// nil, in which case slog.Default is used.
func NewHTTPClient(cfg config.PlaidConfig, logger *slog.Logger) *HTTPClient {
	return newHTTPClient(cfg, logger, "")
}

// newHTTPClient is NewHTTPClient with an optional base URL override for
// tests that run against an httptest server.
func newHTTPClient(cfg config.PlaidConfig, logger *slog.Logger, baseURL string) *HTTPClient {
	if logger == nil {
		logger = slog.Default()
	}
	pc := plaidgo.NewConfiguration()
	pc.AddDefaultHeader("PLAID-CLIENT-ID", cfg.ClientID)
	pc.AddDefaultHeader("PLAID-SECRET", cfg.Secret.Expose())
	pc.UserAgent = "plaidsync (" + pc.UserAgent + ")"
	switch {
	case baseURL != "":
		pc.UseEnvironment(plaidgo.Environment(baseURL))
	case cfg.Env == config.PlaidEnvProduction:
		pc.UseEnvironment(plaidgo.Production)
	default:
		pc.UseEnvironment(plaidgo.Sandbox)
	}
	pc.HTTPClient = &http.Client{Timeout: callTimeout}
	return &HTTPClient{
		api:  plaidgo.NewAPIClient(pc).PlaidApi,
		env:  cfg.Env,
		link: cfg,
		log:  logger.With("component", "plaid"),
	}
}

// products converts configured product names to plaid-go's enum. Names
// are already validated lowercase identifiers.
func products(names []string) []plaidgo.Products {
	out := make([]plaidgo.Products, len(names))
	for i, n := range names {
		out[i] = plaidgo.Products(n)
	}
	return out
}

// countryCodes converts configured country codes to plaid-go's enum.
func countryCodes(codes []string) []plaidgo.CountryCode {
	out := make([]plaidgo.CountryCode, len(codes))
	for i, c := range codes {
		out[i] = plaidgo.CountryCode(c)
	}
	return out
}

// CreateLinkToken implements Client.
func (c *HTTPClient) CreateLinkToken(ctx context.Context, p LinkTokenParams) (*LinkToken, error) {
	const endpoint = "/link/token/create"
	if p.ClientUserID == "" {
		return nil, errors.New("plaid: create link token: client user id is empty")
	}
	req := plaidgo.NewLinkTokenCreateRequest(c.link.LinkClientName, c.link.LinkLanguage, countryCodes(c.link.CountryCodes))
	req.SetUser(*plaidgo.NewLinkTokenCreateRequestUser(p.ClientUserID))
	if c.link.RedirectURI != "" {
		req.SetRedirectUri(c.link.RedirectURI)
	}

	additional := c.link.AdditionalConsentedProducts
	if p.AdditionalConsentedProducts != nil {
		additional = p.AdditionalConsentedProducts
	}
	if len(additional) > 0 {
		req.SetAdditionalConsentedProducts(products(additional))
	}

	if p.AccessToken.IsZero() {
		req.SetProducts(products(c.link.Products))
		if len(c.link.RequiredIfSupportedProducts) > 0 {
			req.SetRequiredIfSupportedProducts(products(c.link.RequiredIfSupportedProducts))
		}
		if len(c.link.OptionalProducts) > 0 {
			req.SetOptionalProducts(products(c.link.OptionalProducts))
		}
		if c.link.WebhookURL != "" {
			req.SetWebhook(c.link.WebhookURL)
		}
		tx := plaidgo.NewLinkTokenTransactions()
		tx.SetDaysRequested(int32(c.link.TransactionsDaysRequested))
		req.SetTransactions(*tx)
	} else {
		// Update mode: the webhook is ignored by Plaid and products must be
		// absent, so neither is sent.
		req.SetAccessToken(p.AccessToken.Expose())
		upd := plaidgo.NewLinkTokenCreateRequestUpdate()
		upd.SetAccountSelectionEnabled(p.AccountSelectionEnabled)
		req.SetUpdate(*upd)
	}
	if p.Hosted {
		// An empty hosted_link object is enough: Plaid then returns
		// hosted_link_url, and handles OAuth on its own page.
		req.SetHostedLink(*plaidgo.NewLinkTokenCreateHostedLink())
	}

	start := time.Now()
	resp, httpResp, err := c.api.LinkTokenCreate(ctx).LinkTokenCreateRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil, "hosted", p.Hosted)
	lt := &LinkToken{Token: resp.LinkToken, Expiration: resp.Expiration.UTC(), HostedURL: resp.GetHostedLinkUrl(), RequestID: resp.RequestId}
	if p.Hosted && lt.HostedURL == "" {
		return nil, &Error{Endpoint: endpoint, Message: "response is missing hosted_link_url", RequestID: resp.RequestId}
	}
	return lt, nil
}

// GetLinkSession implements Client.
func (c *HTTPClient) GetLinkSession(ctx context.Context, linkToken string) (*LinkSession, error) {
	const endpoint = "/link/token/get"
	if linkToken == "" {
		return nil, errors.New("plaid: get link session: link token is empty")
	}
	start := time.Now()
	req := plaidgo.NewLinkTokenGetRequest(linkToken)
	resp, httpResp, err := c.api.LinkTokenGet(ctx).LinkTokenGetRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)

	ls := &LinkSession{RequestID: resp.RequestId}
	if t := resp.Expiration.Get(); t != nil {
		ls.Expiration = t.UTC()
	}
	if resp.LinkSessions == nil {
		return ls, nil
	}
	var (
		inProgress bool
		exit       *LinkExit
	)
	for _, s := range *resp.LinkSessions {
		ls.Started = true
		if s.FinishedAt.Get() == nil {
			inProgress = true
			continue
		}
		if r := s.Results.Get(); r != nil {
			for _, add := range r.ItemAddResults {
				if add.PublicToken != "" {
					ls.PublicToken = secret.NewToken(add.PublicToken)
					break
				}
			}
		}
		if ls.PublicToken.IsZero() {
			if os := s.OnSuccess.Get(); os != nil && os.PublicToken != "" {
				ls.PublicToken = secret.NewToken(os.PublicToken)
			}
		}
		if !ls.PublicToken.IsZero() {
			ls.Finished = true
			ls.Exit = nil
			return ls, nil
		}
		if ex := s.Exit.Get(); ex != nil {
			exit = &LinkExit{}
			if pe := ex.Error.Get(); pe != nil {
				exit.Error = &Error{Endpoint: endpoint, Type: string(pe.ErrorType), Code: pe.ErrorCode, Message: pe.ErrorMessage, RequestID: pe.GetRequestId()}
			}
		} else if exit == nil {
			// Finished, no public token, no exit: an update-mode session
			// Plaid reports without a token. Treated as a plain finish.
			exit = nil
			ls.Finished = true
		}
	}
	if inProgress {
		ls.Finished = false
		return ls, nil
	}
	if exit != nil {
		ls.Finished = true
		ls.Exit = exit
	}
	return ls, nil
}

// ExchangePublicToken implements Client.
func (c *HTTPClient) ExchangePublicToken(ctx context.Context, publicToken secret.Token) (*Exchange, error) {
	const endpoint = "/item/public_token/exchange"
	if publicToken.IsZero() {
		return nil, errors.New("plaid: exchange public token: public token is empty")
	}
	start := time.Now()
	req := plaidgo.NewItemPublicTokenExchangeRequest(publicToken.Expose())
	resp, httpResp, err := c.api.ItemPublicTokenExchange(ctx).ItemPublicTokenExchangeRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)
	if resp.AccessToken == "" || resp.ItemId == "" {
		return nil, &Error{Endpoint: endpoint, Message: "response is missing access_token or item_id", RequestID: resp.RequestId}
	}
	return &Exchange{AccessToken: secret.NewToken(resp.AccessToken), ItemID: resp.ItemId, RequestID: resp.RequestId}, nil
}

// GetItem implements Client.
func (c *HTTPClient) GetItem(ctx context.Context, accessToken secret.Token) (*ItemInfo, error) {
	const endpoint = "/item/get"
	if accessToken.IsZero() {
		return nil, errors.New("plaid: get item: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewItemGetRequest(accessToken.Expose())
	_, httpResp, err := c.api.ItemGet(ctx).ItemGetRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	body, err := readBody(httpResp)
	if err != nil {
		return nil, fmt.Errorf("plaid: %s: %w", endpoint, err)
	}
	info, err := DecodeItemGet(body)
	if err != nil {
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, info.RequestID, nil)
	return info, nil
}

// GetAccounts implements Client.
func (c *HTTPClient) GetAccounts(ctx context.Context, accessToken secret.Token) (*Accounts, error) {
	const endpoint = "/accounts/get"
	if accessToken.IsZero() {
		return nil, errors.New("plaid: get accounts: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewAccountsGetRequest(accessToken.Expose())
	_, httpResp, err := c.api.AccountsGet(ctx).AccountsGetRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	body, err := readBody(httpResp)
	if err != nil {
		return nil, fmt.Errorf("plaid: %s: %w", endpoint, err)
	}
	accts, err := DecodeAccountsGet(body)
	if err != nil {
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, accts.RequestID, nil)
	return accts, nil
}

// syncPageSize is the count sent to /transactions/sync: Plaid's maximum,
// recommended to reduce the number of pages and with it the chance of a
// mutation during pagination.
const syncPageSize = 500

// SyncTransactions implements Client.
func (c *HTTPClient) SyncTransactions(ctx context.Context, accessToken secret.Token, cursor string) (*SyncPage, error) {
	const endpoint = "/transactions/sync"
	if accessToken.IsZero() {
		return nil, errors.New("plaid: sync transactions: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewTransactionsSyncRequest(accessToken.Expose())
	if cursor != "" {
		req.SetCursor(cursor)
	}
	req.SetCount(syncPageSize)
	opts := plaidgo.NewTransactionsSyncRequestOptions()
	opts.SetPersonalFinanceCategoryVersion(plaidgo.PERSONALFINANCECATEGORYVERSION_V2)
	req.SetOptions(*opts)

	_, httpResp, err := c.api.TransactionsSync(ctx).TransactionsSyncRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	body, err := readBody(httpResp)
	if err != nil {
		return nil, fmt.Errorf("plaid: %s: %w", endpoint, err)
	}
	page, err := DecodeSyncPage(body)
	if err != nil {
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, page.RequestID, nil,
		"added", len(page.Added), "modified", len(page.Modified), "removed", len(page.Removed), "has_more", page.HasMore)
	return page, nil
}

// RemoveItem implements Client.
func (c *HTTPClient) RemoveItem(ctx context.Context, accessToken secret.Token) error {
	const endpoint = "/item/remove"
	if accessToken.IsZero() {
		return errors.New("plaid: remove item: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewItemRemoveRequest(accessToken.Expose())
	resp, httpResp, err := c.api.ItemRemove(ctx).ItemRemoveRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)
	return nil
}

// UpdateWebhook implements Client.
func (c *HTTPClient) UpdateWebhook(ctx context.Context, accessToken secret.Token, webhookURL string) error {
	const endpoint = "/item/webhook/update"
	if accessToken.IsZero() {
		return errors.New("plaid: update webhook: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewItemWebhookUpdateRequest(accessToken.Expose())
	req.SetWebhook(webhookURL)
	resp, httpResp, err := c.api.ItemWebhookUpdate(ctx).ItemWebhookUpdateRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)
	return nil
}

// WebhookVerificationKey implements Client.
func (c *HTTPClient) WebhookVerificationKey(ctx context.Context, keyID string) (*VerificationKey, error) {
	const endpoint = "/webhook_verification_key/get"
	if keyID == "" {
		return nil, errors.New("plaid: webhook verification key: key id is empty")
	}
	start := time.Now()
	req := plaidgo.NewWebhookVerificationKeyGetRequest(keyID)
	_, httpResp, err := c.api.WebhookVerificationKeyGet(ctx).WebhookVerificationKeyGetRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return nil, err
	}
	body, err := readBody(httpResp)
	if err != nil {
		return nil, fmt.Errorf("plaid: %s: %w", endpoint, err)
	}
	key, err := DecodeVerificationKey(body)
	if err != nil {
		return nil, err
	}
	c.logCall(ctx, endpoint, start, httpResp, key.RequestID, nil, "key_id", key.KeyID)
	return key, nil
}

// SandboxCreatePublicToken implements Client.
func (c *HTTPClient) SandboxCreatePublicToken(ctx context.Context, institutionID string, prods []string) (secret.Token, error) {
	const endpoint = "/sandbox/public_token/create"
	if c.env != config.PlaidEnvSandbox {
		return secret.Token{}, ErrNotSandbox
	}
	if institutionID == "" {
		return secret.Token{}, errors.New("plaid: sandbox create public token: institution id is empty")
	}
	if prods == nil {
		prods = c.link.Products
	}
	start := time.Now()
	req := plaidgo.NewSandboxPublicTokenCreateRequest(institutionID, products(prods))
	if c.link.WebhookURL != "" {
		opts := plaidgo.NewSandboxPublicTokenCreateRequestOptions()
		opts.SetWebhook(c.link.WebhookURL)
		req.SetOptions(*opts)
	}
	resp, httpResp, err := c.api.SandboxPublicTokenCreate(ctx).SandboxPublicTokenCreateRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return secret.Token{}, err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)
	if resp.PublicToken == "" {
		return secret.Token{}, &Error{Endpoint: endpoint, Message: "response is missing public_token", RequestID: resp.RequestId}
	}
	return secret.NewToken(resp.PublicToken), nil
}

// SandboxFireWebhook implements Client.
func (c *HTTPClient) SandboxFireWebhook(ctx context.Context, accessToken secret.Token, webhookType, webhookCode string) error {
	const endpoint = "/sandbox/item/fire_webhook"
	if c.env != config.PlaidEnvSandbox {
		return ErrNotSandbox
	}
	if accessToken.IsZero() {
		return errors.New("plaid: sandbox fire webhook: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewSandboxItemFireWebhookRequest(accessToken.Expose(), webhookCode)
	if webhookType != "" {
		req.SetWebhookType(plaidgo.WebhookType(webhookType))
	}
	resp, httpResp, err := c.api.SandboxItemFireWebhook(ctx).SandboxItemFireWebhookRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil, "webhook_code", webhookCode)
	return nil
}

// SandboxResetLogin implements Client.
func (c *HTTPClient) SandboxResetLogin(ctx context.Context, accessToken secret.Token) error {
	const endpoint = "/sandbox/item/reset_login"
	if c.env != config.PlaidEnvSandbox {
		return ErrNotSandbox
	}
	if accessToken.IsZero() {
		return errors.New("plaid: sandbox reset login: access token is empty")
	}
	start := time.Now()
	req := plaidgo.NewSandboxItemResetLoginRequest(accessToken.Expose())
	resp, httpResp, err := c.api.SandboxItemResetLogin(ctx).SandboxItemResetLoginRequest(*req).Execute()
	if err := c.wrap(endpoint, httpResp, err); err != nil {
		c.logCall(ctx, endpoint, start, httpResp, "", err)
		return err
	}
	c.logCall(ctx, endpoint, start, httpResp, resp.RequestId, nil)
	return nil
}

// readBody returns the response body. plaid-go re-buffers the body it
// decoded so it can be read again here; a nil response cannot happen on
// the success path but is guarded anyway.
func readBody(httpResp *http.Response) ([]byte, error) {
	if httpResp == nil || httpResp.Body == nil {
		return nil, errors.New("response body is missing")
	}
	defer httpResp.Body.Close()
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	return body, nil
}

// wrap converts a plaid-go error into a *Error when Plaid answered with
// an error object, an *Error carrying only the HTTP status when it did
// not, and leaves transport errors as they are (wrapped with the
// endpoint) so Classify can inspect them.
func (c *HTTPClient) wrap(endpoint string, httpResp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	status := 0
	if httpResp != nil {
		status = httpResp.StatusCode
	}
	var ge plaidgo.GenericOpenAPIError
	if !errors.As(err, &ge) {
		if ctxErr := context.Cause(ctxOf(err)); ctxErr != nil {
			return fmt.Errorf("plaid: %s: %w", endpoint, ctxErr)
		}
		return fmt.Errorf("plaid: %s: %w", endpoint, err)
	}
	if pe, err := DecodeError(ge.Body(), endpoint, status); err == nil {
		return pe
	}
	msg := ge.Error()
	if len(msg) > errPreview {
		msg = msg[:errPreview]
	}
	return &Error{Endpoint: endpoint, HTTPStatus: status, Message: msg}
}

// ctxOf returns a context whose Cause is the context error wrapped in err,
// or a background context when err is not a context error. It exists so
// wrap can preserve context.Canceled and context.DeadlineExceeded for
// errors.Is without depending on how plaid-go wrapped them.
func ctxOf(err error) context.Context {
	switch {
	case errors.Is(err, context.Canceled):
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(context.Canceled)
		return ctx
	case errors.Is(err, context.DeadlineExceeded):
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(context.DeadlineExceeded)
		return ctx
	}
	return context.Background()
}

// logCall writes one debug line per Plaid call with everything useful for
// support and nothing secret: endpoint, status, duration, request id and
// any extra attributes.
func (c *HTTPClient) logCall(ctx context.Context, endpoint string, start time.Time, httpResp *http.Response, requestID string, err error, extra ...any) {
	attrs := []any{"endpoint", endpoint, "duration", time.Since(start).Round(time.Millisecond)}
	if httpResp != nil {
		attrs = append(attrs, "status", httpResp.StatusCode)
	}
	if requestID != "" {
		attrs = append(attrs, "request_id", requestID)
	}
	attrs = append(attrs, extra...)
	if err != nil {
		attrs = append(attrs, "error", err)
		c.log.WarnContext(ctx, "plaid call failed", attrs...)
		return
	}
	c.log.DebugContext(ctx, "plaid call", attrs...)
}
