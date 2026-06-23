package markdown

import (
	"strings"
	"testing"
)

func TestIsHTML(t *testing.T) {
	cases := map[string]bool{
		"text/html":                       true,
		"text/html; charset=utf-8":        true,
		"  TEXT/HTML ;charset=iso-8859-1": true,
		"application/xhtml+xml":           true,
		"application/json":                false,
		"image/png":                       false,
		"text/plain":                      false,
		"":                                false,
	}
	for ct, want := range cases {
		if got := IsHTML(ct); got != want {
			t.Errorf("IsHTML(%q) = %v, want %v", ct, got, want)
		}
	}
}

func TestConvertArticle(t *testing.T) {
	body := []byte(`<!doctype html>
<html><head><title>Example</title></head>
<body>
<nav><a href="/elsewhere">menu noise that should be dropped</a></nav>
<article>
<h1>The Headline</h1>
<p>First paragraph with a <a href="/post/2">relative link</a> and some text long enough to read as an article rather than chrome, so readability keeps it.</p>
<p>Second paragraph that also carries enough words to look like genuine article body content worth extracting into Markdown for the column.</p>
</article>
<footer>copyright boilerplate</footer>
</body></html>`)

	md, ok := Convert(body, "text/html; charset=utf-8", "https://example.com/post/1")
	if !ok {
		t.Fatal("Convert returned ok=false for a normal article")
	}
	if !strings.Contains(md, "The Headline") {
		t.Errorf("headline missing from markdown:\n%s", md)
	}
	if !strings.Contains(md, "First paragraph") {
		t.Errorf("body text missing from markdown:\n%s", md)
	}
	// The relative link should be resolved against the page URL.
	if !strings.Contains(md, "https://example.com/post/2") {
		t.Errorf("relative link not made absolute:\n%s", md)
	}
}

func TestConvertNonHTML(t *testing.T) {
	if md, ok := Convert([]byte(`{"a":1}`), "application/json", "https://example.com/x.json"); ok || md != "" {
		t.Errorf("expected no conversion for JSON, got ok=%v md=%q", ok, md)
	}
	if md, ok := Convert(nil, "text/html", "https://example.com/"); ok || md != "" {
		t.Errorf("expected no conversion for empty body, got ok=%v md=%q", ok, md)
	}
}

func TestConvertNonUTF8(t *testing.T) {
	// "Café" with the é encoded as ISO-8859-1 (0xE9), declared via Content-Type.
	body := []byte("<html><head><title>t</title></head><body><article><h1>Caf\xe9 story</h1>" +
		"<p>A paragraph about a caf\xe9 with enough words to be treated as real article content by the extractor pass.</p>" +
		"<p>Another paragraph of body text so the article is clearly more than a stub and survives extraction cleanly.</p>" +
		"</article></body></html>")
	md, ok := Convert(body, "text/html; charset=iso-8859-1", "https://example.com/cafe")
	if !ok {
		t.Fatal("Convert returned ok=false for a latin-1 article")
	}
	if !strings.Contains(md, "Café") {
		t.Errorf("latin-1 byte not transcoded to UTF-8 é:\n%s", md)
	}
}
