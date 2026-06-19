package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/metrics"
	"github.com/tamnd/ami/run"
	"github.com/tamnd/ami/seed"
)

// newCrawlCmd builds `ami crawl`, the main verb.
func newCrawlCmd() *cobra.Command {
	cfg := config.Default()
	var format string

	cmd := &cobra.Command{
		Use:   "crawl [flags] <seed>",
		Short: "Fetch every URL in a seed and pack the results",
		Long: "crawl reads a seed (a list of URLs), re-fetches each one concurrently,\n" +
			"and writes WARC files plus a captures.parquet index under --out.\n\n" +
			"The seed format is inferred from the path, or set it with --from:\n" +
			"  lines    one URL per line (default; - means stdin)\n" +
			"  jsonl    newline-delimited JSON objects with a \"url\" field\n" +
			"  parquet  a Parquet file with a \"url\" column\n" +
			"  sitemap  an XML sitemap or sitemap index URL",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := seed.Open(format, args[0])
			if err != nil {
				return err
			}

			runner := run.New(cfg)
			start := time.Now()
			quiet := false

			onTick := func(s metrics.Snapshot) {
				if quiet {
					return
				}
				printProgress(s)
			}

			fmt.Fprintf(os.Stderr, "ami: crawling %s -> %s\n", src.Name(), cfg.OutDir)
			if err := runner.Run(cmd.Context(), src, onTick); err != nil {
				return err
			}

			final := runner.Stats().Snapshot()
			fmt.Fprintln(os.Stderr)
			fmt.Fprintf(os.Stderr,
				"ami: done in %s | %d ok, %d unchanged, %d redirect, %d 4xx, %d 5xx, %d failed, %d skipped | %.0f pages/s, %.1f MiB/s\n",
				final.Elapsed.Round(time.Second), final.OK, final.NotMod, final.Redirect,
				final.ClientErr, final.ServerErr, final.Failed, final.Skipped,
				rate(final.Done, start), final.MiBPerSec)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&format, "from", "", "seed format: lines, jsonl, parquet, sitemap (default: infer from path)")
	f.StringVarP(&cfg.OutDir, "out", "o", cfg.OutDir, "output directory for WARC and the capture index")
	f.StringVar(&cfg.RunID, "run-id", "", "subdirectory under --out for this run")
	f.IntVar(&cfg.Workers, "workers", cfg.Workers, "worker pool size and the ceiling on adaptive in-flight requests")
	f.IntVar(&cfg.MinInflight, "min-inflight", cfg.MinInflight, "floor on the adaptive in-flight request limit")
	f.IntVar(&cfg.StartInflight, "start-inflight", cfg.StartInflight, "initial in-flight request limit before the controller adapts")
	f.IntVar(&cfg.TransportShards, "transport-shards", cfg.TransportShards, "number of keep-alive transport pools")
	f.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "per-request ceiling timeout")
	f.IntVar(&cfg.MaxConnsPerHost, "per-host", cfg.MaxConnsPerHost, "max concurrent connections per host")
	f.IntVar(&cfg.DomainFailThreshold, "domain-fail-threshold", cfg.DomainFailThreshold, "host-attributable failures before a domain is skipped")
	f.IntVar(&cfg.MaxRetries, "max-retries", cfg.MaxRetries, "retries for a fetch the engine attributes to local congestion")
	f.BoolVar(&cfg.StoreUnchanged, "store-unchanged", cfg.StoreUnchanged, "store the full body even when the digest is unchanged")
	f.Int64Var(&cfg.MaxBodyBytes, "max-body", cfg.MaxBodyBytes, "maximum response body bytes to store")
	f.Int64Var(&cfg.WARCTargetSize, "warc-size", cfg.WARCTargetSize, "target size per WARC file in bytes")
	f.Var(modeValue{&cfg.Mode}, "mode", "header profile: fast or polite")
	f.IntVar(&cfg.Shard, "shard", cfg.Shard, "this process's partition index (0-based)")
	f.IntVar(&cfg.ShardCount, "shards", cfg.ShardCount, "total number of partitions for distributed runs")
	return cmd
}

// printProgress writes a one-line live status to stderr.
func printProgress(s metrics.Snapshot) {
	fmt.Fprintf(os.Stderr,
		"\rami: %d/%d done | %.0f pages/s | %.1f MiB/s | ok=%d unchanged=%d fail=%d skip=%d   ",
		s.Done, s.Seeded, s.RatePerSec, s.MiBPerSec, s.OK, s.NotMod, s.Failed, s.Skipped)
}

// rate returns pages per second over the elapsed window.
func rate(done int64, start time.Time) float64 {
	sec := time.Since(start).Seconds()
	if sec <= 0 {
		return 0
	}
	return float64(done) / sec
}

// modeValue adapts config.Mode to pflag.Value.
type modeValue struct{ m *config.Mode }

func (v modeValue) String() string { return string(*v.m) }
func (v modeValue) Type() string   { return "mode" }
func (v modeValue) Set(s string) error {
	switch config.Mode(s) {
	case config.ModeFast, config.ModePolite:
		*v.m = config.Mode(s)
		return nil
	default:
		return fmt.Errorf("invalid mode %q (want fast or polite)", s)
	}
}
