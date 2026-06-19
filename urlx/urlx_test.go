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
		"www.example.com": "example.com",
		"example.com":     "example.com",
		"a.b.c.example.io": "example.io",
		"localhost":       "localhost",
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
