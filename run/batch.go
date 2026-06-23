package run

import (
	"context"
	"sync"
	"time"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/fetch"
)

// FetchBatch re-fetches a list of URLs using the same engine as Run.
// Results are written to out as they complete. FetchBatch blocks until all
// URLs have been attempted or ctx is cancelled, then returns ctx.Err() (nil
// if the list drained normally).
func FetchBatch(ctx context.Context, cfg config.Config, urls []string, out chan<- fetch.Result) error {
	if len(urls) == 0 {
		return nil
	}

	if cfg.Workers <= 0 {
		cfg.Workers = config.Default().Workers
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
