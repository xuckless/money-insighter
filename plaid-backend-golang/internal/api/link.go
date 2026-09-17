package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"plaidsync/internal/plaid"
	"plaidsync/internal/secret"
	"plaidsync/internal/store"
	"plaidsync/internal/sync"
)

// linkTokenResponse is the body of both Link token endpoints.
type linkTokenResponse struct {
	LinkToken  string    `json:"link_token"`
	Expiration time.Time `json:"expiration"`
	RequestID  string    `json:"request_id"`
}

// handleLinkToken is POST /v1/link/token: a Link token for linking a new
// item with the configured products, country codes, webhook and redirect
// URI. The body is optional and empty today; it exists so future options
// have a place without a breaking change.
func (s *Server) handleLinkToken(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readJSON(w, r, &body, true) {
		return
	}
	lt, err := s.plaid.CreateLinkToken(r.Context(), plaid.LinkTokenParams{ClientUserID: s.cfg.Plaid.LinkClientUserID})
	if err != nil {
		writeFailure(w, r, err, "create link token")
		return
	}
	writeJSON(w, http.StatusOK, linkTokenResponse{LinkToken: lt.Token, Expiration: lt.Expiration, RequestID: lt.RequestID})
}

// updateLinkTokenRequest is the body of POST /v1/items/{id}/link/token.
type updateLinkTokenRequest struct {
	// AccountSelection sets update.account_selection_enabled, needed when
	// the user must add or remove accounts (NEW_ACCOUNTS_AVAILABLE).
	AccountSelection bool `json:"account_selection"`
	// AdditionalConsentedProducts, when present, replaces the configured
	// list for this session (after ADDITIONAL_CONSENT_REQUIRED).
	AdditionalConsentedProducts []string `json:"additional_consented_products"`
}

// handleUpdateLinkToken is POST /v1/items/{id}/link/token: an update-mode
// Link token for an existing item. On success the item's access token
// stays valid; the client then calls POST /v1/link/exchange with the new
// public token, which resets the item's status to active.
func (s *Server) handleUpdateLinkToken(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	var body updateLinkTokenRequest
	if !readJSON(w, r, &body, true) {
		return
	}
	token, err := s.accessToken(r.Context(), itemID)
	if err != nil {
		writeFailure(w, r, err, "read item credential")
		return
	}
	lt, err := s.plaid.CreateLinkToken(r.Context(), plaid.LinkTokenParams{
		ClientUserID:                s.cfg.Plaid.LinkClientUserID,
		AccessToken:                 token,
		AccountSelectionEnabled:     body.AccountSelection,
		AdditionalConsentedProducts: body.AdditionalConsentedProducts,
	})
	if err != nil {
		writeFailure(w, r, err, "create update-mode link token")
		return
	}
	writeJSON(w, http.StatusOK, linkTokenResponse{LinkToken: lt.Token, Expiration: lt.Expiration, RequestID: lt.RequestID})
}

// exchangeRequest is the body of POST /v1/link/exchange.
type exchangeRequest struct {
	PublicToken string `json:"public_token"`
}

// linkedResponse is the body of a successful exchange: the item as stored
// and the initial sync job (nil when no job could be queued, which the
// item's status explains).
type linkedResponse struct {
	Item itemJSON `json:"item"`
	Job  *jobJSON `json:"job"`
}

// handleExchange is POST /v1/link/exchange: turns the public token Link
// produced into a stored item and queues its first sync. For an update
// mode session the same call re-activates the existing item.
func (s *Server) handleExchange(w http.ResponseWriter, r *http.Request) {
	var body exchangeRequest
	if !readJSON(w, r, &body, false) {
		return
	}
	if body.PublicToken == "" {
		writeError(w, http.StatusBadRequest, "public_token is required")
		return
	}
	item, job, err := s.linkItem(r.Context(), secret.NewToken(body.PublicToken))
	if err != nil {
		writeFailure(w, r, err, "exchange public token")
		return
	}
	resp := linkedResponse{Item: toItemJSON(item)}
	if job != nil {
		j := toJobJSON(job)
		resp.Job = &j
	}
	writeJSON(w, http.StatusCreated, resp)
}

// linkItem is the shared tail of Link: exchange the public token, store
// the credential at once (so a failure in any later step never loses an
// access token that counts against the Trial cap), enrich the row from
// /item/get, point the item's webhook at this deployment when it differs,
// and queue the initial sync.
func (s *Server) linkItem(ctx context.Context, publicToken secret.Token) (*store.Item, *store.Job, error) {
	ex, err := s.plaid.ExchangePublicToken(ctx, publicToken)
	if err != nil {
		return nil, nil, err
	}
	log := s.log.With("item_id", ex.ItemID)

	blob, version, err := s.keys.Encrypt(ex.AccessToken, sync.CredentialAAD(ex.ItemID))
	if err != nil {
		return nil, nil, fmt.Errorf("encrypt access token: %w", err)
	}
	cred := store.Credential{Ciphertext: blob, KeyVersion: version}
	if err := s.store.UpsertItem(ctx, store.NewItem{ItemID: ex.ItemID, Credential: cred}); err != nil {
		return nil, nil, err
	}
	log.Info("item linked", "request_id", ex.RequestID)

	info, err := s.plaid.GetItem(ctx, ex.AccessToken)
	if err != nil {
		// The credential is stored; the item can be enriched by the next
		// sync. Report the failure so the caller knows the link is partial.
		log.Warn("item stored but /item/get failed", "error", err)
		return nil, nil, err
	}
	if err := s.store.UpsertItem(ctx, store.NewItem{
		ItemID: ex.ItemID, InstitutionID: info.InstitutionID, InstitutionName: info.InstitutionName,
		Credential: cred, ConsentExpiresAt: info.ConsentExpiresAt, Raw: info.Raw,
	}); err != nil {
		return nil, nil, err
	}

	if want := s.cfg.Plaid.WebhookURL; want != "" && (info.Webhook == nil || *info.Webhook != want) {
		if err := s.plaid.UpdateWebhook(ctx, ex.AccessToken, want); err != nil {
			log.Warn("could not point the item's webhook at this deployment", "error", err)
		} else {
			log.Info("item webhook updated")
		}
	}

	item, err := s.store.GetItem(ctx, ex.ItemID)
	if err != nil {
		return nil, nil, err
	}
	job, err := s.runner.Enqueue(ctx, ex.ItemID, store.JobKindInitial)
	if err != nil {
		log.Warn("could not queue the initial sync", "error", err)
		return item, nil, nil
	}
	return item, job, nil
}

// accessToken decrypts an item's stored access token. It returns
// store.ErrNotFound for an unknown or removed item.
func (s *Server) accessToken(ctx context.Context, itemID string) (secret.Token, error) {
	cred, err := s.store.GetCredential(ctx, itemID)
	if err != nil {
		return secret.Token{}, err
	}
	token, err := s.keys.Decrypt(cred.Ciphertext, cred.KeyVersion, sync.CredentialAAD(itemID))
	if err != nil {
		return secret.Token{}, fmt.Errorf("decrypt access token (key version %d): %w", cred.KeyVersion, err)
	}
	return token, nil
}
