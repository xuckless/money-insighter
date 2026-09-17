package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"plaidsync/internal/jobs"
	"plaidsync/internal/plaid"
	"plaidsync/internal/store"
)

// maxBodyBytes caps every request body this API reads. The largest
// legitimate body is a webhook payload of a few kilobytes.
const maxBodyBytes = 1 << 20

// errorResponse is the body of every error: a message, and for failures
// that came from Plaid, the error's type, code and request id so the
// caller (and Plaid support) can act on them.
type errorResponse struct {
	Error string      `json:"error"`
	Plaid *plaidError `json:"plaid,omitempty"`
}

type plaidError struct {
	Type      string `json:"type,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// writeJSON writes v as the response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		slog.Warn("write response", "error", err)
	}
}

// writeError writes an error body. Messages are for API callers and
// must never carry a token, a database URL or a request body.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeFailure maps an error from the store, the runner or Plaid onto a
// status and body. Anything unrecognised is a 500 whose detail goes to
// the log only.
func writeFailure(w http.ResponseWriter, r *http.Request, err error, what string) {
	if pe, ok := plaid.AsError(err); ok {
		status := http.StatusBadGateway
		switch {
		case pe.Code == "INVALID_PUBLIC_TOKEN" || pe.Code == "INVALID_LINK_TOKEN":
			status = http.StatusBadRequest
		case pe.Code == "TRIAL_CONNECTION_LIMIT":
			status = http.StatusTooManyRequests
		case pe.Class() == plaid.ClassRetryable:
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, errorResponse{
			Error: what + ": Plaid returned " + pe.Type + "/" + pe.Code,
			Plaid: &plaidError{Type: pe.Type, Code: pe.Code, Message: pe.Message, RequestID: pe.RequestID},
		})
		return
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, what+": not found")
	case errors.Is(err, jobs.ErrItemRemoved):
		writeError(w, http.StatusConflict, what+": the item has been removed")
	case errors.Is(err, jobs.ErrNotSyncable):
		writeError(w, http.StatusConflict, what+": the item needs Link update mode before it can sync")
	case errors.Is(err, store.ErrItemLocked):
		writeError(w, http.StatusConflict, what+": a sync of this item is in progress")
	case errors.Is(err, plaid.ErrNotSandbox):
		writeError(w, http.StatusNotFound, what+": sandbox endpoints are disabled outside PLAID_ENV=sandbox")
	default:
		slog.ErrorContext(r.Context(), what, "error", err, "request_id", requestIDFrom(r.Context()))
		writeError(w, http.StatusInternalServerError, what+": internal error")
	}
}

// readJSON decodes the request body into v, rejecting unknown fields and
// trailing data. An empty body is accepted as an empty object when
// allowEmpty is set, so endpoints with all-optional fields work with a
// bare POST.
func readJSON(w http.ResponseWriter, r *http.Request, v any, allowEmpty bool) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeError(w, http.StatusBadRequest, "read request body: "+err.Error())
		}
		return false
	}
	if len(body) == 0 {
		if allowEmpty {
			return true
		}
		writeError(w, http.StatusBadRequest, "request body is required")
		return false
	}
	dec := json.NewDecoder(bytesReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	if dec.More() {
		writeError(w, http.StatusBadRequest, "invalid JSON body: trailing data")
		return false
	}
	return true
}

// JSON shapes of the store's rows. They are the API's contract; the store
// types are not exposed directly so a column rename never leaks.

type itemJSON struct {
	ItemID               string     `json:"item_id"`
	InstitutionID        *string    `json:"institution_id"`
	InstitutionName      *string    `json:"institution_name"`
	Status               string     `json:"status"`
	HasCursor            bool       `json:"has_cursor"`
	LastError            *itemError `json:"last_error"`
	LastSuccessfulSyncAt *time.Time `json:"last_successful_sync_at"`
	ConsentExpiresAt     *time.Time `json:"consent_expires_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

type itemError struct {
	Code    *string    `json:"code"`
	Type    *string    `json:"type"`
	Message *string    `json:"message"`
	At      *time.Time `json:"at"`
}

func toItemJSON(it *store.Item) itemJSON {
	out := itemJSON{
		ItemID: it.ItemID, InstitutionID: it.InstitutionID, InstitutionName: it.InstitutionName,
		Status: string(it.Status), HasCursor: it.Cursor != nil && *it.Cursor != "",
		LastSuccessfulSyncAt: it.LastSuccessfulSyncAt, ConsentExpiresAt: it.ConsentExpiresAt,
		CreatedAt: it.CreatedAt, UpdatedAt: it.UpdatedAt,
	}
	if it.LastErrorAt != nil || it.LastErrorCode != nil || it.LastErrorMessage != nil {
		out.LastError = &itemError{Code: it.LastErrorCode, Type: it.LastErrorType, Message: it.LastErrorMessage, At: it.LastErrorAt}
	}
	return out
}

type jobJSON struct {
	JobID        string     `json:"job_id"`
	ItemID       string     `json:"item_id"`
	Kind         string     `json:"kind"`
	State        string     `json:"state"`
	CreatedAt    time.Time  `json:"created_at"`
	StartedAt    *time.Time `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ErrorCode    *string    `json:"error_code"`
	ErrorMessage *string    `json:"error_message"`
}

func toJobJSON(j *store.Job) jobJSON {
	return jobJSON{
		JobID: j.JobID, ItemID: j.ItemID, Kind: string(j.Kind), State: string(j.State),
		CreatedAt: j.CreatedAt, StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
		ErrorCode: j.ErrorCode, ErrorMessage: j.ErrorMessage,
	}
}

type runJSON struct {
	RunID           int64     `json:"run_id"`
	ItemID          string    `json:"item_id"`
	JobID           *string   `json:"job_id"`
	Trigger         string    `json:"trigger"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Pages           int       `json:"pages"`
	Added           int       `json:"added"`
	Modified        int       `json:"modified"`
	Removed         int       `json:"removed"`
	Inserted        int       `json:"inserted"`
	Updated         int       `json:"updated"`
	Superseded      int       `json:"superseded"`
	AccountsSeen    int       `json:"accounts_seen"`
	AccountsMissing int       `json:"accounts_missing"`
	Outcome         string    `json:"outcome"`
	ErrorCode       *string   `json:"error_code"`
	ErrorType       *string   `json:"error_type"`
	ErrorMessage    *string   `json:"error_message"`
	RequestID       *string   `json:"request_id"`
}

func toRunJSON(r *store.SyncRun) runJSON {
	return runJSON{
		RunID: r.RunID, ItemID: r.ItemID, JobID: r.JobID, Trigger: string(r.Trigger),
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
		Pages: r.Pages, Added: r.Added, Modified: r.Modified, Removed: r.Removed,
		Inserted: r.Inserted, Updated: r.Updated, Superseded: r.Superseded,
		AccountsSeen: r.AccountsSeen, AccountsMissing: r.AccountsMissing,
		Outcome: string(r.Outcome), ErrorCode: r.ErrorCode, ErrorType: r.ErrorType,
		ErrorMessage: r.ErrorMessage, RequestID: r.RequestID,
	}
}
