// Package metrics carries the lock-free counters for a run. Every field is an
// atomic so any worker can bump it without contention, and the periodic display
// reads a consistent-enough snapshot without stopping the world.
package metrics

import (
	"sync/atomic"
	"time"
)

// Stats holds the running totals for one crawl.
type Stats struct {
	Seeded    atomic.Int64 // URLs read from the seed
	Attempted atomic.Int64 // fetch attempts started
	OK        atomic.Int64 // 2xx responses stored
	NotMod    atomic.Int64 // 304 / unchanged revisits
	Redirect  atomic.Int64 // 3xx (other than 304)
	ClientErr atomic.Int64 // 4xx
	ServerErr atomic.Int64 // 5xx
	Failed    atomic.Int64 // transport/DNS/timeout failures
	Skipped   atomic.Int64 // dead host/domain or non-HTTP
	Bytes     atomic.Int64 // response body bytes stored

	start time.Time
}

// NewStats returns a Stats stamped with a start time for rate computation.
func NewStats(start time.Time) *Stats {
	return &Stats{start: start}
}

// Done reports the number of URLs that reached a terminal state.
func (s *Stats) Done() int64 {
	return s.OK.Load() + s.NotMod.Load() + s.Redirect.Load() +
		s.ClientErr.Load() + s.ServerErr.Load() + s.Failed.Load() + s.Skipped.Load()
}

// Snapshot is an immutable copy of Stats for display.
type Snapshot struct {
	Seeded, Attempted, OK, NotMod, Redirect            int64
	ClientErr, ServerErr, Failed, Skipped, Bytes, Done int64
	Elapsed                                            time.Duration
	RatePerSec                                         float64
	MiBPerSec                                          float64
}

// Snapshot reads the counters into a plain struct.
func (s *Stats) Snapshot() Snapshot {
	elapsed := time.Since(s.start)
	done := s.Done()
	sec := elapsed.Seconds()
	var rate, mib float64
	if sec > 0 {
		rate = float64(done) / sec
		mib = float64(s.Bytes.Load()) / (1 << 20) / sec
	}
	return Snapshot{
		Seeded:     s.Seeded.Load(),
		Attempted:  s.Attempted.Load(),
		OK:         s.OK.Load(),
		NotMod:     s.NotMod.Load(),
		Redirect:   s.Redirect.Load(),
		ClientErr:  s.ClientErr.Load(),
		ServerErr:  s.ServerErr.Load(),
		Failed:     s.Failed.Load(),
		Skipped:    s.Skipped.Load(),
		Bytes:      s.Bytes.Load(),
		Done:       done,
		Elapsed:    elapsed,
		RatePerSec: rate,
		MiBPerSec:  mib,
	}
}
