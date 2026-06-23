package cli

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
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
	var pprofAddr string

	cmd := &cobra.Command{
		Use:   "crawl [flags] <seed>",
		Short: "Fetch every URL in a seed and pack the results",
		Long: "crawl reads a seed (a list of URLs), re-fetches each one concurrently,\n" +
			"and writes the captures under --out. The default --format parquet stores\n" +
			"bodies and headers in rotated zstd Parquet files; --format warc writes\n" +
			"classic WARC files plus a metadata-only Parquet index.\n\n" +
			"The seed format is inferred from the path, or set it with --from:\n" +
			"  lines    one URL per line (default; - means stdin)\n" +
			"  jsonl    newline-delimited JSON objects with a \"url\" field\n" +
			"  parquet  a Parquet file with a \"url\" column\n" +
			"  sitemap  an XML sitemap or sitemap index URL",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if pprofAddr != "" {
				// Enable the contended-lock and blocking-event profiles, then serve
				// the standard pprof endpoints so a run can be profiled live with
				// `go tool pprof http://addr/debug/pprof/...`. The goroutine profile
				// shows how many workers are parked waiting for an in-flight slot
				// versus stuck in the transport, which is what pins the bottleneck.
				runtime.SetBlockProfileRate(1)
				runtime.SetMutexProfileFraction(1)
				mux := http.NewServeMux()
				mux.HandleFunc("/debug/pprof/", pprof.Index)
				mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
				mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
				mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
				mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
				srv := &http.Server{Addr: pprofAddr, Handler: mux}
				go func() { _ = srv.ListenAndServe() }()
				fmt.Fprintf(os.Stderr, "ami: pprof on http://%s/debug/pprof/\n", pprofAddr)
			}

			fdLimit := raiseFDLimit()

			src, err := seed.Open(format, args[0])
			if err != nil {
				return err
			}

			if fdLimit > 0 && uint64(cfg.Workers) > fdLimit/2 {
				fmt.Fprintf(os.Stderr,
					"ami: warning: --workers %d is high for the open-file limit (%d); some connections may be refused\n",
					cfg.Workers, fdLimit)
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
	f.StringVarP(&cfg.OutDir, "out", "o", cfg.OutDir, "output directory for the captures")
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
	f.StringVar(&cfg.Format, "format", cfg.Format, "capture format: parquet (compact zstd body store) or warc")
	f.Int64Var(&cfg.WARCTargetSize, "warc-size", cfg.WARCTargetSize, "target size per WARC file in bytes (warc format)")
	f.Int64Var(&cfg.CaptureTargetSize, "capture-size", cfg.CaptureTargetSize, "uncompressed payload bytes per rotated parquet capture file")
	f.Var(modeValue{&cfg.Mode}, "mode", "header profile: fast or polite")
	f.BoolVar(&cfg.Reorder, "reorder", cfg.Reorder, "spread the seed across hosts so throughput does not depend on input order")
	f.IntVar(&cfg.ReorderWindow, "reorder-window", cfg.ReorderWindow, "seeds buffered for the host spread (0 = auto from --workers)")
	f.IntVar(&cfg.Shard, "shard", cfg.Shard, "this process's partition index (0-based)")
	f.IntVar(&cfg.ShardCount, "shards", cfg.ShardCount, "total number of partitions for distributed runs")
	f.StringVar(&pprofAddr, "pprof", "", "serve net/http/pprof on this address (e.g. localhost:6060) for live profiling")
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
