package run

import (
	"context"
	"sync"
	"time"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/fetch"
	"github.com/tamnd/ami/urlx"
)

// FetchBatch re-fetches a list of URLs using the same engine as Run.
// Results are written to out as they complete. FetchBatch blocks until all
// URLs have been attempted or ctx is cancelled, then returns ctx.Err() (nil
// if the list drained normally).
//
// When cfg.Reorder is on (the default) the URL list is spread across hosts
// before it reaches the workers, so a host-clustered input (a raw Common Crawl
// shard arrives SURT-sorted, with long runs of same-host URLs) does not pin the
// pool against the per-host concurrency cap while most workers sit idle. This
// makes FetchBatch honor Reorder the way the full Run path does, so a caller no
// longer has to pre-spread the list itself.
func FetchBatch(ctx context.Context, cfg config.Config, urls []string, out chan<- fetch.Result) error {
	if len(urls) == 0 {
		return nil
	}

	if cfg.Workers <= 0 {
		cfg.Workers = config.Default().Workers
	}

	if cfg.Reorder {
		urls = spreadURLsByHost(urls)
	}

	fetcher := fetch.New(cfg)
	go func() {
		<-ctx.Done()
		fetcher.Stop()
	}()

	jobs := make(chan fetch.SeedURL, cfg.Workers)
	var wg sync.WaitGroup
	wg.Add(cfg.Workers)
	for range cfg.Workers {
		go func() {
			defer wg.Done()
			for su := range jobs {
				res := batchFetchWithRetry(ctx, fetcher, su, cfg.MaxRetries)
				select {
				case out <- res:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobs)
		for _, u := range urls {
			select {
			case jobs <- fetch.SeedURL{URL: u}:
			case <-ctx.Done():
				return
			}
		}
	}()

	wg.Wait()
	return ctx.Err()
}

// spreadURLsByHost reorders a URL list so consecutive entries rarely share a
// host, turning a host-clustered input into a host-spread stream. It groups the
// URLs by host preserving each host's original relative order, then emits one
// URL per host round-robin until every group is drained. The whole list is
// already in memory here, so this is a simple in-place spread rather than the
// streaming bounded-buffer spreader the Run path uses for an unbounded source.
func spreadURLsByHost(urls []string) []string {
	groups := make(map[string][]string)
	order := make([]string, 0, len(urls)) // hosts in first-seen order, for stable output
	for _, u := range urls {
		h := urlx.Host(u)
		if _, ok := groups[h]; !ok {
			order = append(order, h)
		}
		groups[h] = append(groups[h], u)
	}
	if len(order) <= 1 {
		return urls // one host (or none): nothing to spread
	}

	out := make([]string, 0, len(urls))
	for len(out) < len(urls) {
		for _, h := range order {
			g := groups[h]
			if len(g) == 0 {
				continue
			}
			out = append(out, g[0])
			groups[h] = g[1:]
		}
	}
	return out
}

// batchFetchWithRetry runs one fetch and retries while the engine signals
// congestion. The logic mirrors Runner.fetchWithRetry in run.go.
func batchFetchWithRetry(ctx context.Context, f *fetch.Fetcher, su fetch.SeedURL, maxRetries int) fetch.Result {
	res := f.Fetch(ctx, su)
	for attempt := 1; attempt <= maxRetries && fetch.IsRetry(res.Err); attempt++ {
		select {
		case <-time.After(retryBackoff(attempt)):
		case <-ctx.Done():
			res.Err = ctx.Err()
			return res
		}
		res = f.Fetch(ctx, su)
	}
	if fetch.IsRetry(res.Err) {
		res.Err = fetch.ErrCongested
	}
	return res
}
