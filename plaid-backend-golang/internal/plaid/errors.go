package plaid

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	"plaidsync/internal/store"
)

// Error is a Plaid API error: the error_type / error_code pair Plaid
// documents, the human-readable message, and the request id for support.
// Message is Plaid's error_message; it never contains a token. The HTTP
// status is kept because a 5xx without a parseable body is still worth
// retrying.
type Error struct {
	// Endpoint is the Plaid path that failed, for example
	// "/transactions/sync".
	Endpoint string
	// Type is Plaid's error_type, for example "ITEM_ERROR".
	Type string
	// Code is Plaid's error_code, for example "ITEM_LOGIN_REQUIRED".
	Code string
	// Message is Plaid's error_message.
	Message string
	// DisplayMessage is Plaid's display_message, suitable for an end user;
	// often empty.
	DisplayMessage string
	// RequestID identifies the failing call to Plaid support.
	RequestID string
	// HTTPStatus is the response status, 0 when unknown.
	HTTPStatus int
}

// Error formats the error for logs: endpoint, type/code, message and
// request id.
func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("plaid: ")
	if e.Endpoint != "" {
		b.WriteString(e.Endpoint)
		b.WriteString(": ")
	}
	switch {
	case e.Type != "" && e.Code != "":
		b.WriteString(e.Type + "/" + e.Code)
	case e.Code != "":
		b.WriteString(e.Code)
	case e.Type != "":
		b.WriteString(e.Type)
	default:
		fmt.Fprintf(&b, "HTTP %d", e.HTTPStatus)
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.RequestID != "" {
		b.WriteString(" (request_id ")
		b.WriteString(e.RequestID)
		b.WriteString(")")
	}
	return b.String()
}

// AsError unwraps err to a *Error, reporting false for transport and
// other non-Plaid errors.
func AsError(err error) (*Error, bool) {
	var pe *Error
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// Class is how the sync engine must react to an error. It is the spec's
// error buckets: retry with backoff, wait for a human to run Link again,
// stop because the deployment is misconfigured, or record and move on.
type Class int

// Error classes, in the order the engine cares about them.
const (
	// ClassError is any failure that is neither retryable nor a known
	// item or configuration condition: unknown Plaid codes, decode
	// failures, database errors. The run is recorded and the item is
	// marked error.
	ClassError Class = iota
	// ClassRetryable is a transient failure: rate limits, Plaid or
	// institution outages, network timeouts, and a mutation during
	// pagination (which restarts the loop). Retry with backoff, bounded.
	ClassRetryable
	// ClassNeedsReauth means the item cannot be synced until a human runs
	// Link in update mode. The item status tells which state it is in;
	// see ItemStatus.
	ClassNeedsReauth
	// ClassFatal is a configuration or plan problem: bad API keys, an
	// invalid or removed access token, the Trial connection cap, a product
	// the item does not support. Retrying cannot help.
	ClassFatal
	// ClassCanceled means the call's context was canceled, normally by
	// shutdown. Nothing is wrong with the item.
	ClassCanceled
)

// String names the class for logs.
func (c Class) String() string {
	switch c {
	case ClassRetryable:
		return "retryable"
	case ClassNeedsReauth:
		return "needs_reauth"
	case ClassFatal:
		return "fatal"
	case ClassCanceled:
		return "canceled"
	default:
		return "error"
	}
}

// Outcome maps the class onto the sync_runs outcome column.
func (c Class) Outcome() store.SyncOutcome {
	switch c {
	case ClassRetryable:
		return store.SyncOutcomeRetryableError
	case ClassNeedsReauth:
		return store.SyncOutcomeNeedsReauth
	case ClassFatal:
		return store.SyncOutcomeFatal
	case ClassCanceled:
		return store.SyncOutcomeCanceled
	default:
		return store.SyncOutcomeError
	}
}

// Error codes with a classification of their own, regardless of type. The
// lists follow docs/plaid-canada-research.md section 3.4 plus Plaid's
// documented item and API error codes.
var (
	retryableCodes = map[string]bool{
		"TRANSACTIONS_SYNC_LIMIT":                      true,
		"TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION": true,
		"PRODUCT_NOT_READY":                            true,
		"INSTITUTION_DOWN":                             true,
		"INSTITUTION_NOT_RESPONDING":                   true,
		"INSTITUTION_NOT_AVAILABLE":                    true,
		"INTERNAL_SERVER_ERROR":                        true,
		"PLANNED_MAINTENANCE":                          true,
	}
	reauthCodes = map[string]bool{
		"ITEM_LOGIN_REQUIRED":         true,
		"PENDING_EXPIRATION":          true,
		"PENDING_DISCONNECT":          true,
		"USER_PERMISSION_REVOKED":     true,
		"ACCESS_NOT_GRANTED":          true,
		"ADDITIONAL_CONSENT_REQUIRED": true,
		"INVALID_CREDENTIALS":         true,
		"INVALID_MFA":                 true,
		"INSUFFICIENT_CREDENTIALS":    true,
		"ITEM_LOCKED":                 true,
		"USER_SETUP_REQUIRED":         true,
		"MFA_NOT_SUPPORTED":           true,
		"USER_INPUT_TIMEOUT":          true,
	}
	fatalCodes = map[string]bool{
		"TRIAL_CONNECTION_LIMIT":            true,
		"ITEM_NOT_FOUND":                    true,
		"INVALID_ACCESS_TOKEN":              true,
		"INVALID_PUBLIC_TOKEN":              true,
		"INVALID_LINK_TOKEN":                true,
		"INVALID_API_KEYS":                  true,
		"INVALID_PRODUCT":                   true,
		"INVALID_PRODUCTS":                  true,
		"UNAUTHORIZED_ENVIRONMENT":          true,
		"INSTITUTION_NO_LONGER_SUPPORTED":   true,
		"PRODUCTS_NOT_SUPPORTED":            true,
		"ITEM_NOT_SUPPORTED":                true,
		"NO_ACCOUNTS":                       true,
		"INSTITUTION_REGISTRATION_REQUIRED": true,
	}
)

// Class classifies the Plaid error. Codes are checked before types
// because Plaid reuses types across very different conditions
// (ADDITIONAL_CONSENT_REQUIRED is INVALID_INPUT, TRIAL_CONNECTION_LIMIT is
// RATE_LIMIT_EXCEEDED).
func (e *Error) Class() Class {
	switch {
	case retryableCodes[e.Code]:
		return ClassRetryable
	case reauthCodes[e.Code]:
		return ClassNeedsReauth
	case fatalCodes[e.Code]:
		return ClassFatal
	}
	switch e.Type {
	case "RATE_LIMIT_EXCEEDED", "API_ERROR", "INSTITUTION_ERROR":
		return ClassRetryable
	case "INVALID_REQUEST", "INVALID_INPUT", "INVALID_RESULT":
		return ClassFatal
	case "":
		if e.HTTPStatus >= 500 || e.HTTPStatus == http.StatusTooManyRequests {
			return ClassRetryable
		}
	}
	return ClassError
}

// ItemStatus is the plaid_items status the error puts an item in. It is
// meaningful for ClassNeedsReauth (which of the three re-auth states) and
// returns ItemStatusError for everything else. Callers decide whether a
// retryable or canceled error should touch the item at all.
func (e *Error) ItemStatus() store.ItemStatus {
	switch e.Code {
	case "PENDING_EXPIRATION", "PENDING_DISCONNECT":
		return store.ItemStatusPendingExpiration
	case "USER_PERMISSION_REVOKED", "ACCESS_NOT_GRANTED":
		return store.ItemStatusPermissionRevoked
	}
	if e.Class() == ClassNeedsReauth {
		return store.ItemStatusLoginRequired
	}
	return store.ItemStatusError
}

// Classify classifies any error a Client method can return: a *Error by
// its Class, a canceled context as ClassCanceled, a deadline or network
// failure as ClassRetryable, and anything else as ClassError. nil is
// ClassError; callers do not classify success.
func Classify(err error) Class {
	if err == nil {
		return ClassError
	}
	if pe, ok := AsError(err); ok {
		return pe.Class()
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassRetryable
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return ClassRetryable
	}
	return ClassError
}
