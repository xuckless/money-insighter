package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
)

// Hosted Link.
//
// A desktop or headless deployment cannot run Plaid Link itself: Link's
// OAuth redirect must land on an HTTPS URL registered in the dashboard,
// which a program on someone's laptop does not have. Hosted Link moves
// the whole Link UI onto Plaid's domain: this service asks for a hosted
// session, the caller opens the returned URL in any browser, and this
// service polls /link/token/get until the user is done. No redirect URI,
// no Link SDK, no webhook.
//
// Sessions are tracked in memory, keyed by link token, so that a caller
// polling the status endpoint can be answered idempotently: a public token
// is exchanged exactly once, on the poll that first sees it, and every
// later poll returns the same item. The table does not survive a restart;
// a link token created by a previous process answers 404 and the caller
// starts over, which costs the user one more trip through Link and
// nothing else (the item, once exchanged, is in the database).

// hostedStatus values of hostedStatusResponse.Status.
const (
	hostedStatusPending   = "pending"   // not opened yet, or Link is open
	hostedStatusCompleted = "completed" // linked; Item (and Job) are set
	hostedStatusExited    = "exited"    // the user left Link; Exit may explain why
	hostedStatusExpired   = "expired"   // the link token has expired
)

// hostedSession is one Hosted Link token issued by this process.
type hostedSession struct {
	// itemID is set for an update-mode session.
	itemID     string
	expiration time.Time

	mu   sync.Mutex
	done *hostedStatusResponse // set once the session reached a final status
}

// hostedSessions is the table of live sessions.
type hostedSessions struct {
	mu   sync.Mutex
	byTk map[string]*hostedSession
}

func newHostedSessions() *hostedSessions {
	return &hostedSessions{byTk: make(map[string]*hostedSession)}
}

// add registers a session and drops any whose token expired long enough
// ago that Plaid has forgotten it too (six hours after expiry).
func (h *hostedSessions) add(token string, s *hostedSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cutoff := time.Now().Add(-6 * time.Hour)
	for tk, old := range h.byTk {
		if old.expiration.Before(cutoff) {
			delete(h.byTk, tk)
		}
	}
	h.byTk[token] = s
}

func (h *hostedSessions) get(token string) *hostedSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.byTk[token]
}

// hostedLinkResponse is the body of both hosted session endpoints.
type hostedLinkResponse struct {
	LinkToken     string    `json:"link_token"`
	HostedLinkURL string    `json:"hosted_link_url"`
	Expiration    time.Time `json:"expiration"`
	RequestID     string    `json:"request_id"`
}

// handleHostedLink is POST /v1/link/hosted: a Hosted Link session for a
// new item. The body is optional and empty today.
func (s *Server) handleHostedLink(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readJSON(w, r, &body, true) {
		return
	}
	s.createHosted(w, r, "", plaid.LinkTokenParams{ClientUserID: s.cfg.Plaid.LinkClientUserID, Hosted: true})
}

// handleUpdateHostedLink is POST /v1/items/{id}/link/hosted: a Hosted Link
// session in update mode for an existing item. Same body as
// POST /v1/items/{id}/link/token.
func (s *Server) handleUpdateHostedLink(w http.ResponseWriter, r *http.Request) {
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
	s.createHosted(w, r, itemID, plaid.LinkTokenParams{
		ClientUserID:                s.cfg.Plaid.LinkClientUserID,
		AccessToken:                 token,
		AccountSelectionEnabled:     body.AccountSelection,
		AdditionalConsentedProducts: body.AdditionalConsentedProducts,
		Hosted:                      true,
	})
}

func (s *Server) createHosted(w http.ResponseWriter, r *http.Request, itemID string, p plaid.LinkTokenParams) {
	lt, err := s.plaid.CreateLinkToken(r.Context(), p)
	if err != nil {
		writeFailure(w, r, err, "create hosted link session")
		return
	}
	s.hosted.add(lt.Token, &hostedSession{itemID: itemID, expiration: lt.Expiration})
	s.log.InfoContext(r.Context(), "hosted link session created", "update_mode", itemID != "", "item_id", itemID, "expires", lt.Expiration, "request_id", lt.RequestID)
	writeJSON(w, http.StatusCreated, hostedLinkResponse{LinkToken: lt.Token, HostedLinkURL: lt.HostedURL, Expiration: lt.Expiration, RequestID: lt.RequestID})
}

// hostedStatusRequest is the body of POST /v1/link/hosted/status. The
// token travels in the body rather than the path so it never reaches the
// access log.
type hostedStatusRequest struct {
	LinkToken string `json:"link_token"`
}

// hostedStatusResponse is the body of POST /v1/link/hosted/status.
type hostedStatusResponse struct {
	Status string `json:"status"`
	// Started reports whether the user has opened the hosted page; only
	// meaningful while Status is pending.
	Started bool `json:"started"`
	// Item and Job are set when Status is completed. Job is nil when the
	// initial sync could not be queued (the item's status says why).
	Item *itemJSON `json:"item,omitempty"`
	Job  *jobJSON  `json:"job,omitempty"`
	// Exit is Plaid's error when Status is exited because of one.
	Exit *plaidError `json:"exit,omitempty"`
}

// handleHostedStatus is POST /v1/link/hosted/status: where a hosted
// session stands. Callers poll it (every couple of seconds is fine) until
// the status is no longer pending. The first poll that finds a finished
// session exchanges its public token and stores the item; the result is
// remembered so every later poll for the same token is answered from
// memory.
func (s *Server) handleHostedStatus(w http.ResponseWriter, r *http.Request) {
	var body hostedStatusRequest
	if !readJSON(w, r, &body, false) {
		return
	}
	if body.LinkToken == "" {
		writeError(w, http.StatusBadRequest, "link_token is required")
		return
	}
	hs := s.hosted.get(body.LinkToken)
	if hs == nil {
		writeError(w, http.StatusNotFound, "unknown hosted link session; create a new one")
		return
	}

	// One poll at a time per session, so two concurrent polls cannot both
	// see the public token and both try to exchange it.
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.done != nil {
		writeJSON(w, http.StatusOK, *hs.done)
		return
	}
	if time.Now().After(hs.expiration) {
		hs.done = &hostedStatusResponse{Status: hostedStatusExpired}
		writeJSON(w, http.StatusOK, *hs.done)
		return
	}

	ls, err := s.plaid.GetLinkSession(r.Context(), body.LinkToken)
	if err != nil {
		if pe, ok := plaid.AsError(err); ok && pe.Code == "INVALID_LINK_TOKEN" {
			hs.done = &hostedStatusResponse{Status: hostedStatusExpired}
			writeJSON(w, http.StatusOK, *hs.done)
			return
		}
		writeFailure(w, r, err, "read hosted link session")
		return
	}
	if !ls.Finished {
		writeJSON(w, http.StatusOK, hostedStatusResponse{Status: hostedStatusPending, Started: ls.Started})
		return
	}

	resp, err := s.completeHosted(r.Context(), hs, ls)
	if err != nil {
		// Not remembered: the next poll retries, which is right for a
		// transient failure and harmless otherwise (the public token is
		// still unexchanged when the exchange itself failed).
		writeFailure(w, r, err, "complete hosted link session")
		return
	}
	hs.done = resp
	writeJSON(w, http.StatusOK, *resp)
}

// completeHosted turns a finished Link session into the final status:
// an exit is reported as is; a public token goes through the same path as
// POST /v1/link/exchange; an update-mode finish without a token (Plaid
// reports some that way) re-activates the item and queues a sync.
func (s *Server) completeHosted(ctx context.Context, hs *hostedSession, ls *plaid.LinkSession) (*hostedStatusResponse, error) {
	if ls.Exit != nil {
		out := &hostedStatusResponse{Status: hostedStatusExited}
		if e := ls.Exit.Error; e != nil {
			out.Exit = &plaidError{Type: e.Type, Code: e.Code, Message: e.Message, RequestID: e.RequestID}
		}
		s.log.Info("hosted link session exited", "item_id", hs.itemID, "has_error", ls.Exit.Error != nil)
		return out, nil
	}

	var (
		item *store.Item
		job  *store.Job
		err  error
	)
	switch {
	case !ls.PublicToken.IsZero():
		item, job, err = s.linkItem(ctx, ls.PublicToken)
	case hs.itemID != "":
		// Re-linked with the credential it already has: the upsert resets
		// the status to active and clears the last error, exactly as a
		// re-exchange would, and keeps the cursor.
		var cred *store.Credential
		if cred, err = s.store.GetCredential(ctx, hs.itemID); err != nil {
			break
		}
		if err = s.store.UpsertItem(ctx, store.NewItem{ItemID: hs.itemID, Credential: *cred}); err != nil {
			break
		}
		if item, err = s.store.GetItem(ctx, hs.itemID); err != nil {
			break
		}
		if job, err = s.runner.Enqueue(ctx, hs.itemID, store.JobKindManual); err != nil {
			s.log.Warn("hosted link: could not queue the sync after update mode", "item_id", hs.itemID, "error", err)
			job, err = nil, nil
		}
	default:
		err = errors.New("link session finished without a public token")
	}
	if err != nil {
		return nil, err
	}
	out := &hostedStatusResponse{Status: hostedStatusCompleted}
	ij := toItemJSON(item)
	out.Item = &ij
	if job != nil {
		jj := toJobJSON(job)
		out.Job = &jj
	}
	s.log.Info("hosted link session completed", "item_id", item.ItemID, "update_mode", hs.itemID != "")
	return out, nil
}
