package api

import (
	"errors"
	"net/http"

	"postgres-topper/internal/query"
	"postgres-topper/internal/store"
)

// clientClosedRequest is the non-standard status (nginx's convention)
// recorded when the client went away before the response was written.
const clientClosedRequest = 499

// writeDBError maps a failure from the query layer or Postgres to a
// response. prefix, when non-empty, is prepended to client-facing messages
// ("row 3: "). Internal failures are logged with the SQLSTATE and never
// echoed; data errors are echoed because Postgres's message only ever
// quotes the caller's own input.
func (s *Server) writeDBError(w http.ResponseWriter, r *http.Request, err error, prefix string) {
	var qe *query.Error
	if errors.As(err, &qe) {
		writeError(w, qe.Status, prefix+qe.Msg)
		return
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}

	kind, pgErr := store.Classify(err)
	detail := ""
	if pgErr != nil {
		detail = pgErr.Message
		if pgErr.Detail != "" {
			detail += ": " + pgErr.Detail
		}
	}
	switch kind {
	case store.KindCanceled:
		w.WriteHeader(clientClosedRequest)
	case store.KindTimeout:
		writeError(w, http.StatusGatewayTimeout, prefix+"query timed out")
	case store.KindUnique:
		writeError(w, http.StatusConflict, prefix+"conflict: "+detail)
	case store.KindForeignKey:
		code := http.StatusUnprocessableEntity
		if r.Method == http.MethodDelete {
			code = http.StatusConflict
		}
		writeError(w, code, prefix+"foreign key violation: "+detail)
	case store.KindNotNull, store.KindCheck, store.KindIntegrity:
		writeError(w, http.StatusUnprocessableEntity, prefix+"constraint violation: "+detail)
	case store.KindData:
		writeError(w, http.StatusBadRequest, prefix+"invalid value: "+detail)
	case store.KindUndefinedOp:
		writeError(w, http.StatusBadRequest, prefix+"filter not applicable to the column type: "+detail)
	default:
		attrs := []any{"error", err, "method", r.Method, "path", r.URL.Path}
		if pgErr != nil {
			attrs = append(attrs, "sqlstate", pgErr.Code)
		}
		if kind == store.KindPrivilege {
			attrs = append(attrs, "hint", "the database grants and the topper allowlist disagree")
		}
		s.log.ErrorContext(r.Context(), "database error", attrs...)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
