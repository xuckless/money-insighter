package api

import (
	"net/http"

	"postgres-topper/internal/catalog"
)

// listColumn is one column in the listing.
type listColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Writable bool   `json:"writable,omitempty"`
}

// listTable describes one table in the listing.
type listTable struct {
	Name       string       `json:"name"`
	Mode       catalog.Mode `json:"mode"`
	Schema     string       `json:"schema"`
	PrimaryKey []string     `json:"primary_key"`
	Columns    []listColumn `json:"columns"`
}

// listView describes one view in the listing.
type listView struct {
	Name    string       `json:"name"`
	Path    string       `json:"path"`
	Toggles []string     `json:"toggles"`
	Columns []listColumn `json:"columns"`
}

// listResponse is the body of GET /v1/.
type listResponse struct {
	Tables []listTable `json:"tables"`
	Views  []listView  `json:"views"`
}

// handleList answers GET /v1/ with every table and view the caller may
// use, their modes, keys and exposed columns.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeMethodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	resp := listResponse{Tables: []listTable{}, Views: []listView{}}
	for _, t := range s.cat.Tables() {
		resp.Tables = append(resp.Tables, listTable{
			Name:       t.Name,
			Mode:       t.Mode,
			Schema:     t.Schema,
			PrimaryKey: nonNil(t.PK),
			Columns:    listColumns(t, t.Mode == catalog.ModeReadWrite),
		})
	}
	for _, v := range s.cat.Views() {
		toggles := make([]string, 0, len(v.Toggles))
		for _, t := range v.Toggles {
			toggles = append(toggles, t.Key)
		}
		resp.Views = append(resp.Views, listView{
			Name:    v.Name,
			Path:    "/v1/views/" + v.Name,
			Toggles: toggles,
			Columns: listColumns(v, false),
		})
	}
	w.Header().Set("Cache-Control", "private, max-age=60")
	writeJSON(w, http.StatusOK, resp)
}

func listColumns(rel *catalog.Relation, writable bool) []listColumn {
	cols := rel.Exposed()
	out := make([]listColumn, 0, len(cols))
	for _, c := range cols {
		lc := listColumn{Name: c.Name, Type: c.UDT, Nullable: c.Nullable}
		if writable {
			lc.Writable = c.Writable
		}
		out = append(out, lc)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
