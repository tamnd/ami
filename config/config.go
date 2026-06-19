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
	Workers         int
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
	PerHostDelay        time.Duration

	// Behaviour.
	Mode           Mode
	UserAgent      string
	StoreUnchanged bool
	MaxBodyBytes   int64

	// Output sizing.
	WARCTargetSize int64
	IndexBatchRows int

	// Sharded distribution (process only partition Shard of ShardCount).
	Shard      int
	ShardCount int
}

// Default returns the standard configuration.
func Default() Config {
	return Config{
		OutDir:              "ami-out",
		Workers:             2000,
		DNSWorkers:          2000,
		TransportShards:     64,
		Timeout:             5 * time.Second,
		ProbeTimeout:        1500 * time.Millisecond,
		DNSTimeout:          2 * time.Second,
		MaxConnsPerHost:     8,
		MaxConnsPerIP:       24,
		DomainFailThreshold: 3,
		Mode:                ModeFast,
		UserAgent:           "ami/" + "dev" + " (+https://ami.tamnd.com/bot)",
		MaxBodyBytes:        2 << 20, // 2 MiB
		WARCTargetSize:      1 << 30, // 1 GiB
		IndexBatchRows:      2000,
		ShardCount:          1,
	}
}
