package seed

import (
	"compress/gzip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Sitemap reads <loc> URLs from an XML sitemap or sitemap index at a URL. A
// sitemap index is followed one level deep so a single entry point expands into
// every child sitemap's URLs. Gzip-encoded sitemaps (.xml.gz) are decoded
// transparently.
type Sitemap struct {
	URL    string
	Client *http.Client
}

// Name implements Source.
func (s Sitemap) Name() string { return "sitemap:" + s.URL }

type sitemapDoc struct {
	XMLName  xml.Name      `xml:"urlset"`
	URLs     []sitemapLoc  `xml:"url"`
	IdxName  xml.Name      `xml:"sitemapindex"`
	Sitemaps []sitemapLoc  `xml:"sitemap"`
}

type sitemapLoc struct {
	Loc string `xml:"loc"`
}

// Iterate implements Source.
func (s Sitemap) Iterate(ctx context.Context, yield func(Seed) error) error {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return s.fetch(ctx, client, s.URL, true, yield)
}

// fetch downloads one sitemap document and yields its URLs. When followIndex is
// true and the document is a sitemap index, each child sitemap is fetched once
// (followIndex false) so recursion stops at one level.
func (s Sitemap) fetch(ctx context.Context, client *http.Client, loc string, followIndex bool, yield func(Seed) error) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sitemap %q: status %d", loc, resp.StatusCode)
	}

	var r io.Reader = resp.Body
	if strings.HasSuffix(loc, ".gz") || resp.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}

	var doc sitemapDoc
	if err := xml.NewDecoder(r).Decode(&doc); err != nil {
		return err
	}

	for _, u := range doc.URLs {
		u.Loc = strings.TrimSpace(u.Loc)
		if u.Loc == "" {
			continue
		}
		if err := yield(Seed{URL: u.Loc}); err != nil {
			return err
		}
	}
	if followIndex {
		for _, child := range doc.Sitemaps {
			child.Loc = strings.TrimSpace(child.Loc)
			if child.Loc == "" {
				continue
			}
			if err := s.fetch(ctx, client, child.Loc, false, yield); err != nil {
				return err
			}
		}
	}
	return nil
}
