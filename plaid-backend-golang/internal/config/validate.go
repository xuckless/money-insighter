package config

import (
	"net"
	"net/netip"
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

// parseHTTPURL returns the parsed URL when raw is an absolute http or https
// URL with a host.
func parseHTTPURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, false
	}
	if u.Hostname() == "" {
		return nil, false
	}
	return u, true
}

// isLoopbackHost reports whether host names the local machine: "localhost",
// any name under ".localhost" (RFC 6761), or a loopback IP literal.
func isLoopbackHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}

// checkRedirectURI requires an absolute https URL; http is accepted only
// for loopback hosts, which is where a local Link test page lives.
func checkRedirectURI(v string) string {
	u, ok := parseHTTPURL(v)
	if !ok {
		return "must be an absolute http(s) URL"
	}
	if u.Scheme != "https" && !isLoopbackHost(u.Hostname()) {
		return "must use https (http is allowed only for localhost)"
	}
	return ""
}

// checkWebhookURL requires an absolute http(s) URL, and https when the
// service talks to Plaid production.
func checkWebhookURL(v string, env PlaidEnv) string {
	u, ok := parseHTTPURL(v)
	if !ok {
		return "must be an absolute http(s) URL"
	}
	if env == PlaidEnvProduction && u.Scheme != "https" {
		return "must use https when " + envPlaidEnv + " is production"
	}
	return ""
}

// isASCIILetters reports whether s is n ASCII letters of either case.
func isASCIILetters(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// isProductName reports whether s looks like a Plaid product identifier:
// lowercase letters, digits and underscores, starting with a letter
// ("transactions", "liabilities", "payment_initiation").
func isProductName(s string) bool {
	if s == "" || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}
