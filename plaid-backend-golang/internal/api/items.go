package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
)

// recentLimit is how many jobs and runs GET /v1/items/{id} includes.
const recentLimit = 10

// handleListItems is GET /v1/items: every item, removed ones included,
// oldest first.
func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListItems(r.Context())
	if err != nil {
		writeFailure(w, r, err, "list items")
		return
	}
	out := make([]itemJSON, 0, len(items))
	for i := range items {
		out = append(out, toItemJSON(&items[i]))
	}
	writeJSON(w, http.StatusOK, struct {
		Items []itemJSON `json:"items"`
	}{out})
}

// itemDetail is the body of GET /v1/items/{id}: the item plus its recent
// jobs and runs, which together are its health.
type itemDetail struct {
	Item itemJSON  `json:"item"`
	Jobs []jobJSON `json:"jobs"`
	Runs []runJSON `json:"runs"`
}

// handleGetItem is GET /v1/items/{id}.
func (s *Server) handleGetItem(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		writeFailure(w, r, err, "get item")
		return
	}
	jobs, err := s.store.ListJobs(r.Context(), itemID, recentLimit)
	if err != nil {
		writeFailure(w, r, err, "list jobs")
		return
	}
	runs, err := s.store.ListSyncRuns(r.Context(), itemID, recentLimit)
	if err != nil {
		writeFailure(w, r, err, "list runs")
		return
	}
	d := itemDetail{Item: toItemJSON(item), Jobs: make([]jobJSON, 0, len(jobs)), Runs: make([]runJSON, 0, len(runs))}
	for i := range jobs {
		d.Jobs = append(d.Jobs, toJobJSON(&jobs[i]))
	}
	for i := range runs {
		d.Runs = append(d.Runs, toRunJSON(&runs[i]))
	}
	writeJSON(w, http.StatusOK, d)
}

// handleSync is POST /v1/items/{id}/sync: queues a manual sync and answers
// 202 with the job to poll. A sync already queued for the item is returned
// instead of a duplicate. A manual sync within PLAIDSYNC_SYNC_MIN_INTERVAL
// of the last successful one is accepted and then finishes as skipped
// with error_code "debounced".
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	job, err := s.runner.Enqueue(r.Context(), r.PathValue("id"), store.JobKindManual)
	if err != nil {
		writeFailure(w, r, err, "queue sync")
		return
	}
	w.Header().Set("Location", "/v1/jobs/"+job.JobID)
	writeJSON(w, http.StatusAccepted, struct {
		Job jobJSON `json:"job"`
	}{toJobJSON(job)})
}

// handleRemoveItem is DELETE /v1/items/{id}: calls /item/remove and purges
// the credential. Rows stay for history. On the Trial plan a removed item
// does not free a slot, which is why this is a deliberate DELETE and not a
// side effect of anything else. Removing an already removed item is a
// no-op 200.
func (s *Server) handleRemoveItem(w http.ResponseWriter, r *http.Request) {
	itemID := r.PathValue("id")
	item, err := s.store.GetItem(r.Context(), itemID)
	if err != nil {
		writeFailure(w, r, err, "get item")
		return
	}
	if item.Status != store.ItemStatusRemoved {
		token, err := s.accessToken(r.Context(), itemID)
		if err != nil {
			writeFailure(w, r, err, "read item credential")
			return
		}
		if err := s.plaid.RemoveItem(r.Context(), token); err != nil {
			if pe, ok := plaid.AsError(err); !ok || pe.Code != "ITEM_NOT_FOUND" {
				writeFailure(w, r, err, "remove item at Plaid")
				return
			}
			// Plaid no longer knows the item: it is gone either way.
		}
		if err := s.store.MarkItemRemoved(r.Context(), itemID); err != nil {
			writeFailure(w, r, err, "mark item removed")
			return
		}
		s.log.InfoContext(r.Context(), "item removed", "item_id", itemID)
		if item, err = s.store.GetItem(r.Context(), itemID); err != nil {
			writeFailure(w, r, err, "get item")
			return
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Item itemJSON `json:"item"`
	}{toItemJSON(item)})
}

// handleGetJob is GET /v1/jobs/{id}.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.GetJob(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, r, err, "get job")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Job jobJSON `json:"job"`
	}{toJobJSON(job)})
}

// sandboxItemRequest is the body of POST /v1/sandbox/items.
type sandboxItemRequest struct {
	// InstitutionID defaults to ins_109508 (First Platypus Bank).
	InstitutionID string `json:"institution_id"`
	// Products defaults to the configured PLAIDSYNC_PRODUCTS.
	Products []string `json:"products"`
	// OverrideUsername selects a Sandbox test user other than the
	// default. "user_custom" reads the dataset from UserConfig.
	OverrideUsername string `json:"override_username"`
	// UserConfig is Plaid's custom-user JSON, passed through untouched.
	// An object is accepted as well as a string, so a caller can send the
	// dataset inline rather than escaping it. Ignored without
	// OverrideUsername.
	UserConfig json.RawMessage `json:"user_config"`
	// DaysRequested overrides the configured history depth for this item.
	// 0 means the configured PLAIDSYNC_TRANSACTIONS_DAYS_REQUESTED.
	DaysRequested int `json:"days_requested"`
}

// maxUserConfig bounds the custom-user dataset. Plaid's own limit is far
// smaller (roughly 250 transactions); this only stops an oversized body
// from reaching the Plaid client.
const maxUserConfig = 1 << 20

// userConfigString renders UserConfig as the string Plaid expects in
// options.override_password: a JSON string body is unquoted, anything else
// is sent as it was written.
func (b sandboxItemRequest) userConfigString() (string, error) {
	if len(b.UserConfig) == 0 {
		return "", nil
	}
	if b.UserConfig[0] == '"' {
		var s string
		if err := json.Unmarshal(b.UserConfig, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	return string(b.UserConfig), nil
}

// handleSandboxItem is POST /v1/sandbox/items, mounted only when
// PLAID_ENV=sandbox: creates a Sandbox item through
// /sandbox/public_token/create and runs the same exchange path as Link,
// so the whole backend can be exercised without a browser.
func (s *Server) handleSandboxItem(w http.ResponseWriter, r *http.Request) {
	var body sandboxItemRequest
	if !readJSON(w, r, &body, true) {
		return
	}
	if body.InstitutionID == "" {
		body.InstitutionID = defaultSandboxInstitution
	}
	if len(body.UserConfig) > maxUserConfig {
		writeError(w, http.StatusBadRequest, "user_config is too large")
		return
	}
	if body.DaysRequested < 0 || body.DaysRequested > 730 {
		writeError(w, http.StatusBadRequest, "days_requested must be between 0 and 730")
		return
	}
	userConfig, err := body.userConfigString()
	if err != nil {
		writeError(w, http.StatusBadRequest, "user_config is not valid JSON")
		return
	}
	if userConfig != "" && !json.Valid([]byte(userConfig)) {
		writeError(w, http.StatusBadRequest, "user_config is not valid JSON")
		return
	}
	public, err := s.plaid.SandboxCreatePublicToken(r.Context(), plaid.SandboxItemParams{
		InstitutionID: body.InstitutionID,
		Products:      body.Products,
		User: plaid.SandboxUser{
			Username: body.OverrideUsername,
			Config:   userConfig,
		},
		DaysRequested: body.DaysRequested,
	})
	if err != nil {
		if errors.Is(err, plaid.ErrNotSandbox) {
			writeError(w, http.StatusNotFound, "sandbox endpoints are disabled outside PLAID_ENV=sandbox")
			return
		}
		writeFailure(w, r, err, "create sandbox public token")
		return
	}
	item, job, err := s.linkItem(r.Context(), public)
	if err != nil {
		writeFailure(w, r, err, "exchange sandbox public token")
		return
	}
	resp := linkedResponse{Item: toItemJSON(item)}
	if job != nil {
		j := toJobJSON(job)
		resp.Job = &j
	}
	writeJSON(w, http.StatusCreated, resp)
}
