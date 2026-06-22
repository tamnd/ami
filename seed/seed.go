// Package seed defines what ami crawls: a stream of URLs, each optionally
// carrying a content digest and a free-form metadata map. A seed is just a list
// of URLs to ami; where the list comes from is the job of a SeedSource, and the
// engine never assumes any particular origin.
package seed

import "context"

// Seed is one unit of work: a URL plus optional hints.
//
// Digest, when present, is the content digest of a previously fetched version
// of this URL (any opaque string the producer chose, commonly a sha1). ami uses
// it to decide whether a re-fetch changed, by comparing it against the sha1 of
// the freshly fetched body; it never parses it otherwise.
//
// Meta is arbitrary key/value context the producer wants carried through to the
// capture index unchanged (for example a source partition, a language guess, or
// a fetch timestamp). ami stores it verbatim and attaches no meaning to it.
type Seed struct {
	URL    string
	Digest string
	// ETag and ModTime are response validators from a prior capture. When set,
	// ami issues a conditional request (If-None-Match / If-Modified-Since) so an
	// unchanged page returns a bodiless 304. They make a prior run's capture
	// index usable directly as a recrawl seed.
	ETag    string
	ModTime string
	Meta    map[string]string
}

// Source is a one-shot stream of seeds. Implementations push each seed to yield
// and return the first error they hit; returning a non-nil error from yield
// means the consumer is done and the source should stop and return that error.
//
// A Source must be cheap to start and must stream rather than buffer: seed files
// can hold tens of millions of rows, far more than fits comfortably in memory.
type Source interface {
	// Name identifies the adapter for logs and the run manifest.
	Name() string
	// Iterate calls yield once per seed until the input is exhausted, ctx is
	// cancelled, or yield returns an error.
	Iterate(ctx context.Context, yield func(Seed) error) error
}
