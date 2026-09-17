package api

import (
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
	public, err := s.plaid.SandboxCreatePublicToken(r.Context(), body.InstitutionID, body.Products)
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
