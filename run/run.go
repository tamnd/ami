// Package run wires the stages together: it streams a seed source through the
// fetch engine on a large worker pool, then funnels every result to a single
// pack consumer that writes WARC and the capture index. Reading the network is
// the bottleneck, so one writer keeps the output files simple and still keeps
// up with thousands of concurrent fetches.
package run

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/fetch"
	"github.com/tamnd/ami/metrics"
	"github.com/tamnd/ami/pack"
	"github.com/tamnd/ami/seed"
	"github.com/tamnd/ami/urlx"
)

// Runner executes one crawl end to end.
type Runner struct {
	cfg   config.Config
	stats *metrics.Stats
}

// New returns a Runner for cfg.
func New(cfg config.Config) *Runner {
	return &Runner{cfg: cfg}
}

// Stats exposes the live counters (for the display loop).
func (r *Runner) Stats() *metrics.Stats { return r.stats }

// Run crawls every seed from src and returns when the source is drained or ctx
// is cancelled. The OnTick callback, if set, is invoked about once a second
// with a fresh snapshot for progress display.
func (r *Runner) Run(ctx context.Context, src seed.Source, onTick func(metrics.Snapshot)) error {
	start := time.Now()
	r.stats = metrics.NewStats(start)

	outDir := r.cfg.OutDir
	if r.cfg.RunID != "" {
		outDir = filepath.Join(outDir, r.cfg.RunID)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	warc, err := pack.NewWARCWriter(outDir, "ami", r.cfg.WARCTargetSize)
	if err != nil {
		return err
	}
	idx, err := pack.NewIndexWriter(outDir, "captures.parquet")
	if err != nil {
		_ = warc.Close()
		return err
	}

	fetcher := fetch.New(r.cfg)

	seeds := make(chan seed.Seed, r.cfg.Workers*2)
	results := make(chan fetch.Result, r.cfg.Workers*2)

	// Periodic display.
	stopTick := make(chan struct{})
	if onTick != nil {
		go r.tick(onTick, stopTick)
	}

	// Single pack consumer.
	packDone := make(chan error, 1)
	go func() {
		packDone <- r.consume(warc, idx, results)
	}()

	// Unblock the engine's adaptive limiter when the run is cancelled, so
	// workers parked waiting for an in-flight slot unwind promptly.
	go func() {
		<-ctx.Done()
		fetcher.Stop()
	}()

	// Worker pool.
	var wg sync.WaitGroup
	wg.Add(r.cfg.Workers)
	for i := 0; i < r.cfg.Workers; i++ {
		go func() {
			defer wg.Done()
			for s := range seeds {
				res := r.fetchWithRetry(ctx, fetcher, toSeedURL(s))
				r.record(res)
				select {
				case results <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Feed seeds.
	feedErr := src.Iterate(ctx, func(s seed.Seed) error {
		if s.URL == "" || !r.inShard(s.URL) {
			return nil
		}
		r.stats.Seeded.Add(1)
		select {
		case seeds <- s:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	close(seeds)
	wg.Wait()
	close(results)
	packErr := <-packDone
	close(stopTick)

	if cerr := warc.Close(); cerr != nil && packErr == nil {
		packErr = cerr
	}
	if cerr := idx.Close(); cerr != nil && packErr == nil {
		packErr = cerr
	}

	if feedErr != nil && feedErr != context.Canceled {
		return feedErr
	}
	return packErr
}

// fetchWithRetry runs one fetch and retries it while the engine reports the
// failure as its own congestion rather than the host's fault. The adaptive
// limiter shrinks the in-flight count after a timeout storm, so each backoff
// gives the link a moment to drain before the next attempt; a URL that would
// once have been false-skipped as a dead domain now gets through. After the
// retry budget is spent it is recorded as a plain failure, never a skip, so the
// rest of its domain is still attempted.
func (r *Runner) fetchWithRetry(ctx context.Context, fetcher *fetch.Fetcher, su fetch.SeedURL) fetch.Result {
	res := fetcher.Fetch(ctx, su)
	for attempt := 1; attempt <= r.cfg.MaxRetries && fetch.IsRetry(res.Err); attempt++ {
		select {
		case <-time.After(retryBackoff(attempt)):
		case <-ctx.Done():
			res.Err = ctx.Err()
			return res
		}
		res = fetcher.Fetch(ctx, su)
	}
	if fetch.IsRetry(res.Err) {
		// Still congested after the budget: count it honestly as a failure,
		// without ever marking the domain dead.
		res.Err = fetch.ErrCongested
	}
	return res
}

// retryBackoff returns the pause before a congestion retry. It is deliberately
// short: the limiter does the real work of draining the link, the backoff just
// staggers the retries so they do not resynchronize into a fresh burst.
func retryBackoff(attempt int) time.Duration {
	return min(time.Duration(attempt)*75*time.Millisecond, 500*time.Millisecond)
}

// consume drains results, writing each to the WARC and the index.
func (r *Runner) consume(warc *pack.WARCWriter, idx *pack.IndexWriter, results <-chan fetch.Result) error {
	for res := range results {
		cap := pack.Capture{
			URL:          res.URL,
			Host:         urlx.Host(res.URL),
			Status:       int32(res.Status),
			FetchedAt:    res.FetchedAt.UnixMilli(),
			Digest:       res.Digest,
			Unchanged:    res.Unchanged,
			ETag:         res.ETag,
			LastModified: res.LastModified,
			MetaJSON:     pack.MetaToJSON(res.Meta),
		}
		if res.Err != nil {
			cap.Error = res.Err.Error()
			if err := idx.Write(cap); err != nil {
				return err
			}
			continue
		}
		cap.ContentType = res.Header.Get("Content-Type")
		cap.BodyLength = int64(len(res.Body))

		revisit := res.Unchanged && !r.cfg.StoreUnchanged
		loc, err := warc.WriteResponse(res.URL, res.Status, res.ReqHeader, res.Header, res.Body, revisit, res.Digest)
		if err != nil {
			return err
		}
		cap.WARCFile = loc.File
		cap.WARCOffset = loc.Offset
		cap.WARCLength = loc.Length
		if err := idx.Write(cap); err != nil {
			return err
		}
	}
	return nil
}

// record bumps the stats counters for one result.
func (r *Runner) record(res fetch.Result) {
	r.stats.Attempted.Add(1)
	switch {
	case res.Err != nil && fetch.IsSkip(res.Err):
		r.stats.Skipped.Add(1)
	case res.Err != nil:
		r.stats.Failed.Add(1)
	case res.Unchanged:
		r.stats.NotMod.Add(1)
		r.stats.Bytes.Add(int64(len(res.Body)))
	case res.Status >= 200 && res.Status < 300:
		r.stats.OK.Add(1)
		r.stats.Bytes.Add(int64(len(res.Body)))
	case res.Status >= 300 && res.Status < 400:
		r.stats.Redirect.Add(1)
	case res.Status >= 400 && res.Status < 500:
		r.stats.ClientErr.Add(1)
	case res.Status >= 500:
		r.stats.ServerErr.Add(1)
	}
}

// tick emits a snapshot about once a second until stop is closed.
func (r *Runner) tick(onTick func(metrics.Snapshot), stop <-chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			onTick(r.stats.Snapshot())
		case <-stop:
			onTick(r.stats.Snapshot())
			return
		}
	}
}

// inShard reports whether a URL belongs to this process's partition.
func (r *Runner) inShard(url string) bool {
	if r.cfg.ShardCount <= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(url))
	return int(h.Sum64()%uint64(r.cfg.ShardCount)) == r.cfg.Shard
}

// toSeedURL narrows a seed for the fetch engine.
func toSeedURL(s seed.Seed) fetch.SeedURL {
	return fetch.SeedURL{URL: s.URL, Digest: s.Digest, ETag: s.ETag, ModTime: s.ModTime, Meta: s.Meta}
}
