package config

import (
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Pure validators for individual values. Each returns a reason string
// describing the violated constraint, or "" when the value is acceptable.
// Reasons never include the value.

// checkBindAddr requires a host:port with a numeric port in 1..65535. The
// host may be empty (":8080" listens on every interface) or a bracketed
// IPv6 literal.
func checkBindAddr(v string) string {
	_, port, err := net.SplitHostPort(v)
	if err != nil {
		return "must be host:port such as 127.0.0.1:8080"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "port must be a number between 1 and 65535"
	}
	return ""
}

// isIdentifier reports whether s is a plain lowercase SQL identifier:
// a letter or underscore followed by letters, digits and underscores, at
// most 63 bytes. Table names in TOPPER_WRITE_TABLES must satisfy it so a
// name is always safe to look up and to quote.
func isIdentifier(s string) bool {
	if s == "" || len(s) > maxIdentifierLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// isKeyName reports whether s is an acceptable API key name: 1..64 ASCII
// letters, digits, hyphens or underscores. Names appear in logs, so they
// are kept to characters that cannot confuse a log line.
func isKeyName(s string) bool {
	if s == "" || len(s) > maxKeyNameLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// checkOrigin requires an origin of the form scheme://host[:port] with an
// http or https scheme and nothing after the host. "*" is rejected: the
// API is authenticated, and a wildcard origin with credentials is not
// something a browser will honour anyway.
func checkOrigin(v string) string {
	if v == "*" {
		return "wildcard origins are not allowed; list each origin"
	}
	u, err := url.Parse(v)
	if err != nil {
		return "must be an origin such as https://app.example.com"
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "must use the http or https scheme"
	}
	if u.Hostname() == "" || u.User != nil {
		return "must be an origin such as https://app.example.com"
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(v, "/") {
		return "must not have a path, query or fragment"
	}
	return ""
}
