// Package cache is the in-process response cache for GET requests on
// read-only relations. Entries are keyed by path and query, live for a
// fixed TTL, and are filled through singleflight so concurrent misses for
// the same key cost one database query. Bodies are cached uncompressed;
// the ETag is computed over that body and marked weak, since the bytes on
// the wire may be gzip-encoded.
package cache

import (
	"encoding/hex"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Entry is one cached response body.
type Entry struct {
	Body    []byte
	ETag    string
	Expires time.Time
}

// Cache is safe for concurrent use.
type Cache struct {
	ttl time.Duration
	cap int
	mu  sync.Mutex
	m   map[string]*Entry
	sf  singleflight.Group
	now func() time.Time
}

// New returns a cache with the given TTL and entry cap. A TTL of zero or
// less disables caching: Do always calls fill and stores nothing.
func New(ttl time.Duration, capacity int) *Cache {
	if capacity < 1 {
		capacity = 1
	}
	return &Cache{ttl: ttl, cap: capacity, m: map[string]*Entry{}, now: time.Now}
}

// Enabled reports whether entries are ever stored.
func (c *Cache) Enabled() bool {
	return c != nil && c.ttl > 0
}

// TTL returns the configured lifetime.
func (c *Cache) TTL() time.Duration {
	if c == nil {
		return 0
	}
	return c.ttl
}

// Do returns the entry for key, filling it with fill on a miss. Concurrent
// callers for the same key share one fill. When the cache is disabled the
// returned entry is not stored and Expires is the zero time.
func (c *Cache) Do(key string, fill func() ([]byte, error)) (*Entry, error) {
	if !c.Enabled() {
		body, err := fill()
		if err != nil {
			return nil, err
		}
		return &Entry{Body: body, ETag: ETag(body)}, nil
	}
	if e := c.load(key); e != nil {
		return e, nil
	}
	v, err, _ := c.sf.Do(key, func() (any, error) {
		if e := c.load(key); e != nil {
			return e, nil
		}
		body, err := fill()
		if err != nil {
			return nil, err
		}
		e := &Entry{Body: body, ETag: ETag(body), Expires: c.now().Add(c.ttl)}
		c.store(key, e)
		return e, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*Entry), nil
}

// Len returns the number of stored entries, expired ones included.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func (c *Cache) load(key string) *Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return nil
	}
	if !c.now().Before(e.Expires) {
		delete(c.m, key)
		return nil
	}
	return e
}

// store inserts e, first dropping expired entries when the cache is full
// and then, if still full, arbitrary entries until it fits.
func (c *Cache) store(key string, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.cap {
		now := c.now()
		for k, v := range c.m {
			if !now.Before(v.Expires) {
				delete(c.m, k)
			}
		}
		for k := range c.m {
			if len(c.m) < c.cap {
				break
			}
			delete(c.m, k)
		}
	}
	c.m[key] = e
}

// ETag returns a weak validator for body: W/"<fnv-64a hex>".
func ETag(body []byte) string {
	h := fnv.New64a()
	_, _ = h.Write(body)
	return `W/"` + hex.EncodeToString(h.Sum(nil)) + `"`
}

// Matches reports whether an If-None-Match header value matches etag. It
// accepts a comma-separated list, ignores the weak prefix on either side,
// and honours "*".
func Matches(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}
		if strings.TrimPrefix(candidate, "W/") == want {
			return true
		}
	}
	return false
}
