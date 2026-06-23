// Package markdown turns an HTML response body into Markdown, so a crawl can
// store a clean text rendering of each page alongside the raw bytes. The heavy
// lifting is reused from yomi: extract.FromHTML isolates the article with
// go-readability and sanitises it, and mdconv.Convert renders that subtree to
// GitHub-Flavored Markdown and tidies the result (tables, strikethrough, entity
// decoding, dropped share widgets and duplicate captions, cleaned headings).
//
// What this package adds on top is what a web-scale crawl needs and a reader of
// a single modern article does not: a Content-Type gate so only HTML is touched,
// and a charset transcode so a page served as GBK, Shift-JIS, or Latin-1 reaches
// the parser as UTF-8 instead of mojibake. yomi parses bodies as UTF-8 directly;
// across the open web that assumption does not hold.
//
// Conversion is CPU work, a few milliseconds a page, so callers run it on the
// fetch worker pool rather than the single pack consumer that would otherwise
// serialise it.
package markdown

import (
	"bytes"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/tamnd/yomi/extract"
	"github.com/tamnd/yomi/mdconv"
	"golang.org/x/net/html/charset"
)

// IsHTML reports whether a Content-Type names an HTML document. Conversion is
// only attempted for these; everything else (images, JSON, PDFs, plain text) is
// left alone.
func IsHTML(contentType string) bool {
	ct := contentType
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	switch strings.ToLower(strings.TrimSpace(ct)) {
	case "text/html", "application/xhtml+xml":
		return true
	default:
		return false
	}
}

// Convert renders the main content of an HTML body to Markdown. contentType is
// the response Content-Type (used both for the HTML check and to seed charset
// detection) and pageURL is the absolute URL the body came from (used to resolve
// relative links and images to absolute ones). It returns the Markdown and true
// on success, or "" and false when the body is not HTML, is empty, fails to
// parse, or yields no extractable article.
func Convert(body []byte, contentType, pageURL string) (string, bool) {
	if len(body) == 0 || !IsHTML(contentType) {
		return "", false
	}

	// Transcode to UTF-8 before extraction. charset.NewReader honours a charset
	// named in the Content-Type, a leading BOM, or a <meta charset> declaration,
	// so the article is read correctly whatever encoding the page used.
	utf8Body, ok := toUTF8(body, contentType)
	if !ok {
		return "", false
	}

	art, err := extract.FromHTML(utf8Body, pageURL)
	if err != nil || art.Node == nil {
		return "", false
	}

	base, _ := url.Parse(pageURL)
	md, err := mdconv.Convert(art.Node, mdconv.Options{Base: base})
	if err != nil {
		return "", false
	}

	out := strings.TrimSpace(sanitizeUTF8(md))
	if out == "" {
		return "", false
	}
	return out, true
}

// toUTF8 decodes body to UTF-8 using the charset hinted by contentType (and any
// BOM or <meta charset> in the body). A body already valid UTF-8 is returned as
// is, so the common case costs nothing.
func toUTF8(body []byte, contentType string) ([]byte, bool) {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return nil, false
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return nil, false
	}
	return out, true
}

// sanitizeUTF8 replaces any invalid UTF-8 with the replacement rune, so the
// Markdown column is always valid UTF-8 text even when a page declares one
// charset and serves another.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "�")
}
