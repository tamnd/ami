package fetch

import (
	"sync/atomic"
	"time"
)

// errSkip marks a seed the fetcher chose not to attempt (non-HTTP, dead host,
// or a dead domain). It is reported in Result.Err and counted as skipped, not
// failed.
var errSkip = skipError{}

type skipError struct{}

func (skipError) Error() string { return "skipped" }

// IsSkip reports whether err is the sentinel skip error.
func IsSkip(err error) bool {
	_, ok := err.(skipError)
	return ok
}

// timeoutEdges bucket observed latencies for the P95 estimate, in milliseconds.
var timeoutEdges = [8]int64{100, 250, 500, 1000, 2000, 3500, 5000, 10000}

// adaptiveTimeout tracks a latency histogram and serves a request timeout of
// roughly P95 x 2, clamped to a floor and the configured ceiling. It starts at
// the ceiling and only tightens once it has enough samples, so a run does not
// time out healthy-but-slow hosts before it has learned the distribution.
type adaptiveTimeout struct {
	ceiling time.Duration
	floor   time.Duration
	buckets [8]atomic.Int64
	count   atomic.Int64
	current atomic.Int64 // nanoseconds
}

// newAdaptiveTimeout returns a timeout seeded at the ceiling.
func newAdaptiveTimeout(ceiling time.Duration) *adaptiveTimeout {
	a := &adaptiveTimeout{ceiling: ceiling, floor: 500 * time.Millisecond}
	a.current.Store(int64(ceiling))
	return a
}

// observe records one latency sample and recomputes the timeout once enough
// samples have accumulated.
func (a *adaptiveTimeout) observe(d time.Duration) {
	ms := d.Milliseconds()
	idx := len(timeoutEdges) - 1
	for i, e := range timeoutEdges {
		if ms <= e {
			idx = i
			break
		}
	}
	a.buckets[idx].Add(1)
	n := a.count.Add(1)
	if n < 5 || n%64 != 0 {
		return
	}
	a.recompute(n)
}

// recompute sets current to P95 x 2, clamped to [floor, ceiling].
func (a *adaptiveTimeout) recompute(total int64) {
	target := total * 95 / 100
	var cum int64
	p95ms := timeoutEdges[len(timeoutEdges)-1]
	for i := range a.buckets {
		cum += a.buckets[i].Load()
		if cum >= target {
			p95ms = timeoutEdges[i]
			break
		}
	}
	v := time.Duration(p95ms) * time.Millisecond * 2
	if v < a.floor {
		v = a.floor
	}
	if v > a.ceiling {
		v = a.ceiling
	}
	a.current.Store(int64(v))
}

// value returns the current request timeout.
func (a *adaptiveTimeout) value() time.Duration {
	return time.Duration(a.current.Load())
}
