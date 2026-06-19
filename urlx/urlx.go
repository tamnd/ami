// Package urlx holds the small URL helpers the engine needs: host extraction,
// a stable per-host grouping key, and a cheap canonical form for dedup. It is
// deliberately minimal; ami treats a URL as opaque bytes everywhere it can.
package urlx

import (
	"net/url"
	"strings"
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

// RegisteredDomain returns a coarse eTLD-style key used only to spread load and
// to track dead domains. It is not a true public-suffix lookup: it takes the
// last two labels, which is good enough for fanning work across a worker pool
// and wrong only for multi-label suffixes like co.uk, where it merely groups a
// little more aggressively. That is acceptable for load shaping.
func RegisteredDomain(host string) string {
	host = strings.TrimSuffix(host, ".")
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	return strings.Join(labels[len(labels)-2:], ".")
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
