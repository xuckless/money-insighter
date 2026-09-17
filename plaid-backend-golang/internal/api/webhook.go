package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
)

// webhookPayload is the part of a Plaid webhook body plaidsync acts on.
// Everything else is ignored; the full body is never stored.
type webhookPayload struct {
	Type   string `json:"webhook_type"`
	Code   string `json:"webhook_code"`
	ItemID string `json:"item_id"`
	Error  *struct {
		Type    string `json:"error_type"`
		Code    string `json:"error_code"`
		Message string `json:"error_message"`
	} `json:"error"`
	// Fields of SYNC_UPDATES_AVAILABLE.
	InitialUpdateComplete    bool `json:"initial_update_complete"`
	HistoricalUpdateComplete bool `json:"historical_update_complete"`
	// Fields of NEW_ACCOUNTS_AVAILABLE and similar.
	NewAccounts []string `json:"new_accounts"`
}

// handleWebhook is POST /v1/webhooks/plaid. It is the one /v1 route
// without bearer auth: Plaid authenticates itself with the
// Plaid-Verification JWT, which is checked against the raw body before
// the body is parsed. Every verified webhook is answered 200, including
// codes plaidsync ignores and items it does not know, because Plaid
// retries anything else and a retry cannot change the answer. Work is
// queued, never done inline, so the response is immediate.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read request body")
		return
	}
	if err := s.verifier.Verify(r.Context(), r.Header.Get("Plaid-Verification"), body); err != nil {
		s.log.WarnContext(r.Context(), "webhook rejected", "error", err, "remote", r.RemoteAddr, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusUnauthorized, "webhook verification failed")
		return
	}
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	log := s.log.With("webhook_type", p.Type, "webhook_code", p.Code, "item_id", p.ItemID, "request_id", requestIDFrom(r.Context()))
	s.dispatchWebhook(r, log, &p)
	writeJSON(w, http.StatusOK, struct {
		Received bool `json:"received"`
	}{true})
}

// dispatchWebhook applies one verified webhook. Failures are logged; the
// HTTP answer is 200 regardless (see handleWebhook).
func (s *Server) dispatchWebhook(r *http.Request, log interface {
	Info(string, ...any)
	Warn(string, ...any)
}, p *webhookPayload) {
	switch p.Type + ":" + p.Code {
	case plaid.WebhookTypeTransactions + ":" + plaid.WebhookCodeSyncUpdatesAvailable:
		log.Info("webhook: transactions available", "initial_update_complete", p.InitialUpdateComplete, "historical_update_complete", p.HistoricalUpdateComplete)
		s.enqueueFromWebhook(r, log, p.ItemID)

	case plaid.WebhookTypeItem + ":" + plaid.WebhookCodeError:
		if p.Error == nil {
			log.Warn("webhook: ITEM ERROR without an error object")
			return
		}
		pe := &plaid.Error{Type: p.Error.Type, Code: p.Error.Code, Message: p.Error.Message}
		status := pe.ItemStatus()
		log.Warn("webhook: item error", "error_code", pe.Code, "new_status", string(status))
		s.setStatusFromWebhook(r, log, p.ItemID, status, &store.ItemError{Code: pe.Code, Type: pe.Type, Message: pe.Message, At: time.Now().UTC()})

	case plaid.WebhookTypeItem + ":" + plaid.WebhookCodePendingDisconnect,
		plaid.WebhookTypeItem + ":" + plaid.WebhookCodePendingExpiration:
		log.Warn("webhook: consent expiring; Link update mode is needed")
		s.setStatusFromWebhook(r, log, p.ItemID, store.ItemStatusPendingExpiration,
			&store.ItemError{Code: p.Code, Type: "ITEM_ERROR", Message: "Plaid reports the item's consent is about to expire; run Link in update mode", At: time.Now().UTC()})

	case plaid.WebhookTypeItem + ":" + plaid.WebhookCodeUserPermissionRevoked:
		log.Warn("webhook: user revoked permission")
		s.setStatusFromWebhook(r, log, p.ItemID, store.ItemStatusPermissionRevoked,
			&store.ItemError{Code: p.Code, Type: "ITEM_ERROR", Message: "the user revoked Plaid's access to the item", At: time.Now().UTC()})

	case plaid.WebhookTypeItem + ":" + plaid.WebhookCodeLoginRepaired:
		log.Info("webhook: login repaired; item is active again")
		s.setStatusFromWebhook(r, log, p.ItemID, store.ItemStatusActive, nil)
		s.enqueueFromWebhook(r, log, p.ItemID)

	case plaid.WebhookTypeItem + ":" + plaid.WebhookCodeNewAccountsAvailable:
		// Canadian non-OAuth items need an update-mode session with
		// account selection to add them; nothing to do automatically.
		log.Info("webhook: new accounts available at the institution; run Link update mode with account selection to add them", "new_accounts", p.NewAccounts)

	default:
		log.Info("webhook: ignored")
	}
}

// enqueueFromWebhook queues a webhook-triggered sync, logging rather than
// failing when the item is unknown or not syncable.
func (s *Server) enqueueFromWebhook(r *http.Request, log interface{ Warn(string, ...any) }, itemID string) {
	if itemID == "" {
		log.Warn("webhook without item_id")
		return
	}
	if _, err := s.runner.Enqueue(r.Context(), itemID, store.JobKindWebhook); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			log.Warn("webhook for an item this deployment does not have")
			return
		}
		log.Warn("webhook: could not queue sync", "error", err)
	}
}

// setStatusFromWebhook records an item status change, logging rather than
// failing when the item is unknown.
func (s *Server) setStatusFromWebhook(r *http.Request, log interface{ Warn(string, ...any) }, itemID string, status store.ItemStatus, e *store.ItemError) {
	if itemID == "" {
		log.Warn("webhook without item_id")
		return
	}
	if err := s.store.SetItemStatus(r.Context(), itemID, status, e); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			log.Warn("webhook for an item this deployment does not have")
			return
		}
		log.Warn("webhook: could not update item status", "error", err)
	}
}
