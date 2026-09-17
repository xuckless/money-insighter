package cache

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDisabled(t *testing.T) {
	c := New(0, 10)
	if c.Enabled() {
		t.Fatal("ttl 0 is enabled")
	}
	calls := 0
	for i := 0; i < 3; i++ {
		e, err := c.Do("k", func() ([]byte, error) { calls++; return []byte("x"), nil })
		if err != nil || string(e.Body) != "x" || e.ETag == "" || !e.Expires.IsZero() {
			t.Errorf("entry = %+v %v", e, err)
		}
	}
	if calls != 3 || c.Len() != 0 {
		t.Errorf("calls = %d, len = %d", calls, c.Len())
	}
}

func TestHitMissExpiry(t *testing.T) {
	c := New(time.Minute, 10)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	calls := 0
	fill := func() ([]byte, error) { calls++; return []byte("body"), nil }

	e1, _ := c.Do("k", fill)
	e2, _ := c.Do("k", fill)
	if calls != 1 || e1 != e2 {
		t.Errorf("second call was not a hit: calls=%d", calls)
	}
	if e1.ETag != ETag([]byte("body")) || !e1.Expires.Equal(now.Add(time.Minute)) {
		t.Errorf("entry = %+v", e1)
	}
	now = now.Add(time.Minute)
	if _, _ = c.Do("k", fill); calls != 2 {
		t.Errorf("expired entry served: calls=%d", calls)
	}
	if _, err := c.Do("err", func() ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Error("fill error swallowed")
	}
	if c.Len() != 1 {
		t.Errorf("failed fill stored: len=%d", c.Len())
	}
}

func TestSingleflight(t *testing.T) {
	c := New(time.Minute, 10)
	var calls int32
	release := make(chan struct{})
	fill := func() ([]byte, error) {
		atomic.AddInt32(&calls, 1)
		<-release
		return []byte("v"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Do("same", fill); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Errorf("fill ran %d times", calls)
	}
}

func TestCapEviction(t *testing.T) {
	c := New(time.Minute, 3)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		_, _ = c.Do(k, func() ([]byte, error) { return []byte(k), nil })
	}
	if c.Len() > 3 {
		t.Errorf("len = %d, cap 3", c.Len())
	}
}

func TestMatches(t *testing.T) {
	et := ETag([]byte("x"))
	cases := []struct {
		hdr  string
		want bool
	}{
		{"", false},
		{et, true},
		{`"` + et[3:len(et)-1] + `"`, true},
		{`"other", ` + et, true},
		{"*", true},
		{`"nope"`, false},
	}
	for _, tc := range cases {
		if got := Matches(tc.hdr, et); got != tc.want {
			t.Errorf("Matches(%q) = %v", tc.hdr, got)
		}
	}
	if !strings0(et) {
		t.Errorf("etag %q is not weak", et)
	}
}

func strings0(s string) bool { return len(s) > 3 && s[:3] == `W/"` }
