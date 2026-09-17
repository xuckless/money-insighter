package plaid

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"plaidsync/internal/store"
)

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		typ, code string
		status    int
		class     Class
		item      store.ItemStatus
	}{
		{"RATE_LIMIT_EXCEEDED", "TRANSACTIONS_SYNC_LIMIT", 429, ClassRetryable, store.ItemStatusError},
		{"RATE_LIMIT_EXCEEDED", "TRIAL_CONNECTION_LIMIT", 429, ClassFatal, store.ItemStatusError},
		{"RATE_LIMIT_EXCEEDED", "RATE_LIMIT", 429, ClassRetryable, store.ItemStatusError},
		{"TRANSACTIONS_ERROR", "TRANSACTIONS_SYNC_MUTATION_DURING_PAGINATION", 400, ClassRetryable, store.ItemStatusError},
		{"ITEM_ERROR", "ITEM_LOGIN_REQUIRED", 400, ClassNeedsReauth, store.ItemStatusLoginRequired},
		{"ITEM_ERROR", "PENDING_DISCONNECT", 400, ClassNeedsReauth, store.ItemStatusPendingExpiration},
		{"ITEM_ERROR", "PENDING_EXPIRATION", 400, ClassNeedsReauth, store.ItemStatusPendingExpiration},
		{"ITEM_ERROR", "USER_PERMISSION_REVOKED", 400, ClassNeedsReauth, store.ItemStatusPermissionRevoked},
		{"INVALID_INPUT", "ADDITIONAL_CONSENT_REQUIRED", 400, ClassNeedsReauth, store.ItemStatusLoginRequired},
		{"INVALID_INPUT", "INVALID_ACCESS_TOKEN", 400, ClassFatal, store.ItemStatusError},
		{"INVALID_INPUT", "INVALID_API_KEYS", 400, ClassFatal, store.ItemStatusError},
		{"ITEM_ERROR", "ITEM_NOT_FOUND", 400, ClassFatal, store.ItemStatusError},
		{"ITEM_ERROR", "PRODUCTS_NOT_SUPPORTED", 400, ClassFatal, store.ItemStatusError},
		{"ITEM_ERROR", "NO_LIABILITY_ACCOUNTS", 400, ClassError, store.ItemStatusError},
		{"INSTITUTION_ERROR", "INSTITUTION_DOWN", 400, ClassRetryable, store.ItemStatusError},
		{"INSTITUTION_ERROR", "INSTITUTION_NO_LONGER_SUPPORTED", 400, ClassFatal, store.ItemStatusError},
		{"API_ERROR", "INTERNAL_SERVER_ERROR", 500, ClassRetryable, store.ItemStatusError},
		{"INVALID_REQUEST", "MISSING_FIELDS", 400, ClassFatal, store.ItemStatusError},
		{"", "", 502, ClassRetryable, store.ItemStatusError},
		{"", "", 429, ClassRetryable, store.ItemStatusError},
		{"", "", 404, ClassError, store.ItemStatusError},
		{"SOMETHING_NEW", "WHO_KNOWS", 400, ClassError, store.ItemStatusError},
	}
	for _, c := range cases {
		e := &Error{Endpoint: "/x", Type: c.typ, Code: c.code, HTTPStatus: c.status}
		if got := e.Class(); got != c.class {
			t.Errorf("%s/%s (%d): class = %v, want %v", c.typ, c.code, c.status, got, c.class)
		}
		if got := e.ItemStatus(); got != c.item {
			t.Errorf("%s/%s: item status = %v, want %v", c.typ, c.code, got, c.item)
		}
		if got := Classify(fmt.Errorf("wrapped: %w", e)); got != c.class {
			t.Errorf("%s/%s: Classify(wrapped) = %v, want %v", c.typ, c.code, got, c.class)
		}
	}
}

func TestClassOutcome(t *testing.T) {
	want := map[Class]store.SyncOutcome{
		ClassError:       store.SyncOutcomeError,
		ClassRetryable:   store.SyncOutcomeRetryableError,
		ClassNeedsReauth: store.SyncOutcomeNeedsReauth,
		ClassFatal:       store.SyncOutcomeFatal,
		ClassCanceled:    store.SyncOutcomeCanceled,
	}
	for c, o := range want {
		if c.Outcome() != o {
			t.Errorf("%v.Outcome() = %v, want %v", c, c.Outcome(), o)
		}
		if c.String() == "" {
			t.Errorf("%d has no name", c)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyTransportErrors(t *testing.T) {
	if got := Classify(context.Canceled); got != ClassCanceled {
		t.Errorf("canceled = %v", got)
	}
	if got := Classify(fmt.Errorf("plaid: /x: %w", context.DeadlineExceeded)); got != ClassRetryable {
		t.Errorf("deadline = %v", got)
	}
	var ne net.Error = timeoutErr{}
	if got := Classify(fmt.Errorf("dial: %w", ne)); got != ClassRetryable {
		t.Errorf("net error = %v", got)
	}
	if got := Classify(errors.New("something else")); got != ClassError {
		t.Errorf("plain error = %v", got)
	}
	if got := Classify(nil); got != ClassError {
		t.Errorf("nil = %v", got)
	}
}

func TestErrorStringAndAs(t *testing.T) {
	e := &Error{Endpoint: "/transactions/sync", Type: "ITEM_ERROR", Code: "ITEM_LOGIN_REQUIRED", Message: "login needed", RequestID: "r1"}
	s := e.Error()
	for _, want := range []string{"/transactions/sync", "ITEM_ERROR/ITEM_LOGIN_REQUIRED", "login needed", "request_id r1"} {
		if !strings.Contains(s, want) {
			t.Errorf("%q lacks %q", s, want)
		}
	}
	if got := (&Error{Endpoint: "/x", HTTPStatus: 503}).Error(); !strings.Contains(got, "HTTP 503") {
		t.Errorf("status-only error = %q", got)
	}
	wrapped := fmt.Errorf("sync: %w", e)
	if pe, ok := AsError(wrapped); !ok || pe != e {
		t.Error("AsError did not unwrap")
	}
	if _, ok := AsError(errors.New("x")); ok {
		t.Error("AsError matched a plain error")
	}
}
