// Package urlx holds the small URL helpers the engine needs: host extraction,
// a stable per-host grouping key, and a cheap canonical form for dedup. It is
// deliberately minimal; ami treats a URL as opaque bytes everywhere it can.
package urlx

import (
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Host returns the lowercase hostname of a URL without the port, or "" if the
// URL does not parse or carries no host.
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// RegisteredDomain returns the registered domain (eTLD+1) for host, the key the
// engine uses to group work per site and to track a dead domain. It is a true
// public-suffix lookup, not a last-two-labels heuristic: under a multi-level
// suffix like go.jp, gov.br, lg.jp, edu.cn, or co.uk, "kantei.go.jp" and
// "ndl.go.jp" are distinct registered domains, not a single "go.jp" group.
// That distinction is not cosmetic for the dead-domain breaker: collapsing a
// whole public suffix into one key lets three failures anywhere under go.jp
// condemn every live government host beneath it, a false-dead at the scale of
// an entire suffix. A host with no eTLD+1 (a bare suffix, an IP, "localhost")
// has no registrable domain, so it is returned unchanged as its own key.
func RegisteredDomain(host string) string {
	host = strings.TrimSuffix(host, ".")
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// Scheme reports the URL scheme in lowercase, defaulting to "http" when absent.
func Scheme(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return "http"
	}
	return strings.ToLower(u.Scheme)
}

// IsHTTP reports whether the URL uses http or https.
func IsHTTP(raw string) bool {
	s := Scheme(raw)
	return s == "http" || s == "https"
}
