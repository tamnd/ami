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

// defaultTimeoutFloor is the adaptive timeout's lower clamp when the caller does
// not set one. It is high enough that a host which is merely slow to connect or
// answer (a distant government or university server can take two to three
// seconds to complete a TLS handshake) is given time to respond rather than
// aborted and recorded as a failure. A fast host never reaches the floor, so it
// costs nothing on a healthy run; it only widens the window for the slow tail.
const defaultTimeoutFloor = 3 * time.Second

// newAdaptiveTimeout returns a timeout seeded at the ceiling and clamped below
// by floor. A zero floor selects defaultTimeoutFloor. A floor at or above the
// ceiling pins the timeout at the ceiling, which is how a caller that wants a
// fixed short deadline (a dead-host-heavy crawl that values proving a host dead
// fast over waiting on the slow tail) gets one: set the ceiling tight and leave
// the floor at its default.
func newAdaptiveTimeout(ceiling, floor time.Duration) *adaptiveTimeout {
	if floor <= 0 {
		floor = defaultTimeoutFloor
	}
	a := &adaptiveTimeout{ceiling: ceiling, floor: floor}
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
	v := min(max(time.Duration(p95ms)*time.Millisecond*2, a.floor), a.ceiling)
	a.current.Store(int64(v))
}

// value returns the current request timeout.
func (a *adaptiveTimeout) value() time.Duration {
	return time.Duration(a.current.Load())
}
