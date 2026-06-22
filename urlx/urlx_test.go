package urlx

import "testing"

func TestHost(t *testing.T) {
	cases := map[string]string{
		"https://Example.COM/path": "example.com",
		"http://sub.example.org":   "sub.example.org",
		"https://host:8443/x":      "host",
		"not a url":                "",
		"ftp://example.com":        "example.com",
	}
	for in, want := range cases {
		if got := Host(in); got != want {
			t.Errorf("Host(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegisteredDomain(t *testing.T) {
	cases := map[string]string{
		"www.example.com":  "example.com",
		"example.com":      "example.com",
		"a.b.c.example.io": "example.io",
		"localhost":        "localhost",
		// Multi-level public suffixes must not collapse distinct registered
		// domains into one key: doing so let three failures condemn an entire
		// suffix of live hosts (the .go.jp false-dead). Each of these is its own
		// registrable domain, not a shared "go.jp"/"co.uk"/"edu.cn" group.
		"kantei.go.jp":     "kantei.go.jp",
		"www.kantei.go.jp": "kantei.go.jp",
		"ndl.go.jp":        "ndl.go.jp",
		"ba.gov.br":        "ba.gov.br",
		"news.bbc.co.uk":   "bbc.co.uk",
		"whu.edu.cn":       "whu.edu.cn",
	}
	for in, want := range cases {
		if got := RegisteredDomain(in); got != want {
			t.Errorf("RegisteredDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsHTTP(t *testing.T) {
	if !IsHTTP("https://example.com") {
		t.Error("https should be HTTP")
	}
	if IsHTTP("ftp://example.com") {
		t.Error("ftp should not be HTTP")
	}
}
