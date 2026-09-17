package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"postgres-topper/internal/catalog"
	"postgres-topper/internal/query"
)

// handleUpsert answers POST /v1/{table} for a writable table. The body is
// one JSON object or an array of objects; every row is upserted in one
// transaction and the rows as stored are returned:
// {"table":..,"count":N,"data":[..]}. A conflict that DO NOTHING resolved
// returns no row, so count may be below the number sent.
func (s *Server) handleUpsert(w http.ResponseWriter, r *http.Request, rel *catalog.Relation) {
	rows, err := decodeRows(w, r, s.cfg.MaxBodyBytes)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}

	type prepared struct {
		st   query.Statement
		proj []query.Projected
	}
	stmts := make([]prepared, 0, len(rows))
	for i, row := range rows {
		st, proj, err := query.BuildUpsert(rel, row, i)
		if err != nil {
			s.writeDBError(w, r, err, "")
			return
		}
		stmts = append(stmts, prepared{st, proj})
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.QueryTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	var data bytes.Buffer
	enc := query.NewArrayEncoder(&data)
	for i, st := range stmts {
		res, err := tx.Query(ctx, st.st.SQL, st.st.Args...)
		if err != nil {
			s.writeDBError(w, r, err, fmt.Sprintf("row %d: ", i))
			return
		}
		if err := enc.Append(res, st.proj); err != nil {
			s.writeDBError(w, r, err, fmt.Sprintf("row %d: ", i))
			return
		}
	}
	n := enc.Close()
	if err := tx.Commit(ctx); err != nil {
		s.writeDBError(w, r, err, "")
		return
	}

	var out bytes.Buffer
	name, _ := json.Marshal(rel.Name)
	fmt.Fprintf(&out, `{"table":%s,"count":%d,"data":`, name, n)
	out.Write(data.Bytes())
	out.WriteString("}\n")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

// handleDelete answers DELETE /v1/{table}?filters for a writable table and
// reports {"table":..,"deleted":N}. An unfiltered delete is refused.
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, rel *catalog.Relation) {
	p, err := query.Parse(r.URL.Query(), rel, query.DefaultOptions)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}
	st, err := query.BuildDelete(rel, p)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.QueryTimeout)
	defer cancel()
	tag, err := s.pool.Exec(ctx, st.SQL, st.Args...)
	if err != nil {
		s.writeDBError(w, r, err, "")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"table": rel.Name, "deleted": tag.RowsAffected()})
}

// decodeRows reads the request body as one object or an array of objects,
// with numbers kept exact, bounded by maxBytes.
func decodeRows(w http.ResponseWriter, r *http.Request, maxBytes int64) ([]map[string]any, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return nil, err
		}
		return nil, &query.Error{Status: http.StatusBadRequest, Msg: "body is not valid JSON"}
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, &query.Error{Status: http.StatusBadRequest, Msg: "body has trailing data after the JSON value"}
	}
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}, nil
	case []any:
		rows := make([]map[string]any, 0, len(x))
		for i, item := range x {
			row, ok := item.(map[string]any)
			if !ok {
				return nil, &query.Error{Status: http.StatusBadRequest, Msg: fmt.Sprintf("row %d: not a JSON object", i)}
			}
			rows = append(rows, row)
		}
		if len(rows) == 0 {
			return nil, &query.Error{Status: http.StatusBadRequest, Msg: "body is an empty array"}
		}
		return rows, nil
	}
	return nil, &query.Error{Status: http.StatusBadRequest, Msg: "body must be a JSON object or an array of objects"}
}
