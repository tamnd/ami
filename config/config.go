// Package config holds the tunables for one ami run. Defaults come from a prior
// 50K-worker recrawl engine and are sized for a 10 Gbps box; lower them for a
// laptop or a polite crawl.
package config

import "time"

// Mode selects the request header profile.
type Mode string

const (
	// ModeFast sends minimal headers for the highest throughput.
	ModeFast Mode = "fast"
	// ModePolite sends a full browser-like header set, less likely to be
	// fingerprinted and blocked by bot-detection WAFs.
	ModePolite Mode = "polite"
)

// Config is the full set of knobs for a crawl.
type Config struct {
	// Output.
	OutDir string
	RunID  string

	// Concurrency.
	//
	// Workers is the size of the goroutine pool and the ceiling on in-flight
	// requests. The actual number of concurrent requests floats between
	// MinInflight and Workers under an adaptive controller that tracks latency,
	// so a thin uplink is never asked to open more connections than it sustains
	// (which would collapse into timeouts and false-skip live hosts), while a
	// fat pipe is driven up to Workers.
	Workers         int
	MinInflight     int
	StartInflight   int
	DNSWorkers      int
	TransportShards int

	// Timeouts.
	Timeout      time.Duration
	ProbeTimeout time.Duration
	DNSTimeout   time.Duration

	// Politeness.
	MaxConnsPerHost     int
	MaxConnsPerIP       int
	DomainFailThreshold int
	MaxRetries          int
	PerHostDelay        time.Duration

	// Behaviour.
	Mode           Mode
	UserAgent      string
	StoreUnchanged bool
	MaxBodyBytes   int64

	// Output.
	//
	// Format selects how captured exchanges are stored:
	//   parquet  bodies and reconstructed headers go into rotated, zstd-compressed
	//            Parquet files (captures-NNNNN.parquet). Columnar zstd compresses
	//            thousands of similar pages together, so this is several times
	//            smaller on disk than per-record gzip WARC, and the crawl is
	//            network-bound so the extra compression is effectively free.
	//   warc     classic WARC/1.1 files (one gzip member per record) plus a
	//            metadata-only Parquet index pointing into them, for archival
	//            fidelity and interop with the web-archiving ecosystem.
	Format string

	// Output sizing.
	WARCTargetSize int64
	// CaptureTargetSize rotates the Parquet capture files once this many
	// uncompressed payload bytes have accumulated, so a long run produces a series
	// of bounded, independently readable files instead of one giant file finalized
	// only at the end. Completed files can be offloaded and deleted mid-run.
	CaptureTargetSize int64
	IndexBatchRows    int

	// Sharded distribution (process only partition Shard of ShardCount).
	Shard      int
	ShardCount int

	// Reorder spreads the seed across hosts before it reaches the workers, so
	// throughput does not depend on the order the caller listed the URLs in. A
	// raw Common Crawl shard arrives host-clustered (many consecutive URLs share
	// a host); fed in that order the worker pool stalls on the per-host
	// concurrency cap. With Reorder on, the engine buffers a window of seeds and
	// emits them round-robin across hosts, keeping a wide host set in flight
	// whatever the input order. On by default.
	Reorder bool
	// ReorderWindow is how many seeds to buffer for the round-robin spread. A
	// larger window holds more distinct hosts at once at the cost of memory.
	// Zero selects an automatic size derived from Workers.
	ReorderWindow int
}

// Default returns the standard configuration.
func Default() Config {
	return Config{
		OutDir:              "ami-out",
		Workers:             2000,
		MinInflight:         32,
		StartInflight:       64,
		DNSWorkers:          2000,
		TransportShards:     64,
		Timeout:             5 * time.Second,
		ProbeTimeout:        3 * time.Second,
		DNSTimeout:          2 * time.Second,
		MaxConnsPerHost:     8,
		MaxConnsPerIP:       24,
		DomainFailThreshold: 3,
		MaxRetries:          4,
		Mode:                ModeFast,
		UserAgent:           "ami/" + "dev" + " (+https://ami.tamnd.com/bot)",
		MaxBodyBytes:        2 << 20, // 2 MiB
		Format:              "parquet",
		WARCTargetSize:      1 << 30, // 1 GiB
		CaptureTargetSize:   1 << 30, // 1 GiB of uncompressed payload per file
		IndexBatchRows:      2000,
		ShardCount:          1,
		Reorder:             true,
	}
}
