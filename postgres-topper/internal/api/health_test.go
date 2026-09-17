package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// probe performs one request against h and returns the recorded response
// and its body as a string.
func probe(t *testing.T, h http.Handler, req *http.Request) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return rec, string(body)
}

// assertStatusBody checks the status code, the JSON content type, the
// no-store cache directive and the exact {"status":...} body.
func assertStatusBody(t *testing.T, rec *httptest.ResponseRecorder, body string, wantCode int, wantStatus string) {
	t.Helper()
	if rec.Code != wantCode {
		t.Errorf("status code = %d, want %d", rec.Code, wantCode)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	want := `{"status":"` + wantStatus + `"}`
	if strings.TrimSpace(body) != want {
		t.Errorf("body = %q, want %q", body, want)
	}
	var decoded struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Errorf("body is not JSON: %v", err)
	} else if decoded.Status != wantStatus {
		t.Errorf("decoded status = %q, want %q", decoded.Status, wantStatus)
	}
}

func TestHealthHandler(t *testing.T) {
	rec, body := probe(t, HealthHandler(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assertStatusBody(t, rec, body, http.StatusOK, "ok")
}

func TestReadyHandlerOK(t *testing.T) {
	calls := 0
	h := ReadyHandler(func(context.Context) error {
		calls++
		return nil
	})
	rec, body := probe(t, h, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assertStatusBody(t, rec, body, http.StatusOK, "ok")
	if calls != 1 {
		t.Errorf("ping called %d times, want 1", calls)
	}
}

func TestReadyHandlerUnavailable(t *testing.T) {
	// A distinctive failure reason that must reach the log but never the
	// response body.
	const reason = "ping-failure-reason-7f3a9c"

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := ReadyHandler(func(context.Context) error {
		return errors.New(reason)
	})
	rec, body := probe(t, h, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assertStatusBody(t, rec, body, http.StatusServiceUnavailable, "unavailable")

	if strings.Contains(body, reason) {
		t.Errorf("response body leaks the ping error: %q", body)
	}
	if !strings.Contains(logs.String(), reason) {
		t.Errorf("ping error was not logged; log output:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("readiness failure not logged at WARN; log output:\n%s", logs.String())
	}
}

func TestReadyHandlerPingContextHasDeadline(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	h := ReadyHandler(func(ctx context.Context) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	})
	before := time.Now()
	probe(t, h, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if !hasDeadline {
		t.Fatal("ping context has no deadline")
	}
	if remaining := deadline.Sub(before); remaining <= 0 || remaining > readyTimeout+time.Second {
		t.Errorf("ping deadline %v from now, want about %v", remaining, readyTimeout)
	}
}

func TestReadyHandlerPingContextFollowsRequest(t *testing.T) {
	reqCtx, cancelReq := context.WithCancel(context.Background())
	defer cancelReq()

	entered := make(chan struct{})
	h := ReadyHandler(func(ctx context.Context) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("ping context was not canceled with the request")
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(reqCtx)
	done := make(chan struct{})
	rec := httptest.NewRecorder()
	go func() {
		defer close(done)
		h.ServeHTTP(rec, req)
	}()

	<-entered
	cancelReq()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the request context was canceled")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status code = %d, want %d after cancellation", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestReadyHandlerNilPingPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ReadyHandler(nil) did not panic")
		}
	}()
	ReadyHandler(nil)
}

// TestProbesOnServeMuxMethodPatterns mounts the probes the way
// cmd/topper does, with method patterns on a stdlib ServeMux, and checks
// that only GET (and HEAD, which GET patterns also match) is accepted.
func TestProbesOnServeMuxMethodPatterns(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", HealthHandler())
	mux.Handle("GET /readyz", ReadyHandler(func(context.Context) error { return nil }))

	for _, path := range []string{"/healthz", "/readyz"} {
		for _, tc := range []struct {
			method string
			want   int
		}{
			{http.MethodGet, http.StatusOK},
			{http.MethodHead, http.StatusOK},
			{http.MethodPost, http.StatusMethodNotAllowed},
			{http.MethodDelete, http.StatusMethodNotAllowed},
		} {
			rec, _ := probe(t, mux, httptest.NewRequest(tc.method, path, nil))
			if rec.Code != tc.want {
				t.Errorf("%s %s = %d, want %d", tc.method, path, rec.Code, tc.want)
			}
		}
	}

	rec, _ := probe(t, mux, httptest.NewRequest(http.MethodGet, "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
