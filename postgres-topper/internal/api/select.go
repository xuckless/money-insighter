package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"postgres-topper/internal/cache"
	"postgres-topper/internal/catalog"
	"postgres-topper/internal/query"
)

// serveRead answers GET/HEAD for a table or view. Read-only relations go
// through the response cache (keyed by path and query, shared by every
// caller since all callers see the same data); writable tables are always
// read live and marked no-store.
func (s *Server) serveRead(w http.ResponseWriter, r *http.Request, rel *catalog.Relation) {
	p, err := query.Parse(r.URL.Query(), rel, query.DefaultOptions)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}

	label := "table"
	if rel.Kind == catalog.KindView {
		label = "view"
	}
	fill := func(ctx context.Context) ([]byte, error) {
		return s.readBody(ctx, rel, p, label)
	}

	var entry *cache.Entry
	if rel.Cacheable && s.cache.Enabled() {
		key := r.URL.Path + "?" + r.URL.RawQuery
		entry, err = s.cache.Do(key, func() ([]byte, error) {
			// Detached from the request so one client hanging up does
			// not fail the fill every concurrent caller is waiting on.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), s.cfg.QueryTimeout)
			defer cancel()
			return fill(ctx)
		})
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), s.cfg.QueryTimeout)
		defer cancel()
		var body []byte
		body, err = fill(ctx)
		if err == nil {
			entry = &cache.Entry{Body: body, ETag: cache.ETag(body)}
		}
	}
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}

	h := w.Header()
	h.Set("ETag", entry.ETag)
	if entry.Expires.IsZero() {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("Cache-Control", "private, max-age="+strconv.Itoa(max(0, int(time.Until(entry.Expires).Seconds()))))
	}
	if cache.Matches(r.Header.Get("If-None-Match"), entry.ETag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(entry.Body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(entry.Body)
	}
}

// readBody runs the SELECT (and the COUNT when asked) and renders the
// envelope: {"table":..,"count":N,"limit":L,"offset":O,"total":T,"data":[..]}.
func (s *Server) readBody(ctx context.Context, rel *catalog.Relation, p *query.Params, label string) ([]byte, error) {
	st, proj := query.BuildSelect(rel, p)
	rows, err := s.pool.Query(ctx, st.SQL, st.Args...)
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	n, err := query.EncodeRows(&data, rows, proj)
	if err != nil {
		return nil, err
	}

	var total int64 = -1
	if p.Count {
		c := query.BuildCount(rel, p)
		if err := s.pool.QueryRow(ctx, c.SQL, c.Args...).Scan(&total); err != nil {
			return nil, err
		}
	}

	var out bytes.Buffer
	name, _ := json.Marshal(rel.Name)
	fmt.Fprintf(&out, `{"%s":%s,"count":%d,"limit":%d,"offset":%d`, label, name, n, p.Limit, p.Offset)
	if p.Count {
		fmt.Fprintf(&out, `,"total":%d`, total)
	}
	out.WriteString(`,"data":`)
	out.Write(data.Bytes())
	out.WriteString("}\n")
	return out.Bytes(), nil
}
