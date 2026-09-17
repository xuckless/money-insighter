package config

// VarError reports one problem with a single environment variable: it is
// required but unset, or it is set to something [Load] cannot accept.
//
// [Load] collects every VarError it finds and returns them joined with
// errors.Join, so one failed start reports every mistake. Callers that want
// to inspect individual problems can unwrap the joined error or use
// errors.As with a *VarError target.
//
// Reason describes the problem in terms of the constraint that was violated
// and never contains the variable's value, so a VarError is safe to log and
// to print on stderr even for secrets.
type VarError struct {
	// Var is the environment variable name, for example "PLAIDSYNC_KEK".
	Var string
	// Reason says what is wrong with the variable, without quoting its value.
	Reason string
}

// Error implements error. The message names the variable and the reason.
func (e *VarError) Error() string {
	return "config: " + e.Var + ": " + e.Reason
}
