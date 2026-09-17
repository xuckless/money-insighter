// Package query turns a validated request into SQL. It is the second and
// third of the three layers that keep request input out of SQL: every
// column name is checked against the catalog, every identifier is quoted,
// and every value is a bound parameter. The package never runs a query; it
// returns statements for internal/api to execute and an encoder for the
// rows that come back.
package query

import "fmt"

// Error is a request problem the HTTP layer should report with Status and
// Msg verbatim. Msg may quote request input (a parameter name, an operator)
// but never a value.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

// badRequest builds a 400 Error.
func badRequest(format string, args ...any) *Error {
	return &Error{Status: 400, Msg: fmt.Sprintf(format, args...)}
}
