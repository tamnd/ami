package fetch

import (
	"net/http"

	"github.com/tamnd/ami/config"
)

// setHeaders writes the request header profile for the configured mode. Fast
// mode sends the minimum to look like a generic client; polite mode sends a
// full browser-like set that bot-detection WAFs are less likely to challenge.
func (f *Fetcher) setHeaders(req *http.Request) {
	ua := f.cfg.UserAgent
	if ua == "" {
		ua = "ami/dev"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Encoding", "identity")

	if f.cfg.Mode == config.ModePolite {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.9")
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Upgrade-Insecure-Requests", "1")
	} else {
		req.Header.Set("Accept", "*/*")
	}
}
