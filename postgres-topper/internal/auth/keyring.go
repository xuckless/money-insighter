// Package auth authenticates /v1 callers with static bearer tokens and
// authorises them by scope.
//
// Tokens arrive as "Authorization: Bearer <token>". Each is hashed with
// SHA-256 and compared in constant time against every configured key, so
// the comparison time reveals neither which key matched nor how much of a
// token was right. Tokens are never logged; the caller's configured name
// is what reaches the access log.
package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"

	"postgres-topper/internal/config"
)

// Scope is config.Scope: read or readwrite.
type Scope = config.Scope

// Caller is an authenticated key.
type Caller struct {
	Name  string
	Scope Scope
}

// Allows reports whether the scope permits an HTTP method. read covers the
// safe methods only; readwrite covers everything, leaving methods the API
// does not implement to be answered 405 by the handler rather than 403
// here. An unknown scope permits nothing.
func Allows(s Scope, method string) bool {
	switch s {
	case config.ScopeReadWrite:
		return true
	case config.ScopeRead:
		return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
	}
	return false
}

// Keyring holds the configured keys.
type Keyring struct {
	keys []config.APIKey
}

// NewKeyring wraps keys. The slice is not copied; config does not mutate
// it after Load.
func NewKeyring(keys []config.APIKey) *Keyring {
	return &Keyring{keys: keys}
}

// Lookup returns the caller for token. Every key is compared even after a
// match so the work done does not depend on the token.
func (k *Keyring) Lookup(token string) (Caller, bool) {
	if token == "" {
		return Caller{}, false
	}
	h := sha256.Sum256([]byte(token))
	var found Caller
	matched := 0
	for i := range k.keys {
		if subtle.ConstantTimeCompare(h[:], k.keys[i].Hash[:]) == 1 {
			found = Caller{Name: k.keys[i].Name, Scope: k.keys[i].Scope}
			matched++
		}
	}
	return found, matched > 0
}
