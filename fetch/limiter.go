package fetch

import (
	"context"
	"math"
	"slices"
	"sync"
)

// limiter bounds the number of in-flight requests to what the local uplink
// actually sustains, instead of a fixed worker count that oversubscribes a thin
// pipe.
//
// It is an additive-increase/multiplicative-decrease controller in the spirit
// of TCP congestion control. Each successful request grows the limit by one;
// each timeout (the loss signal) shrinks it by a gentle factor. Latency never
// forces the limit down, because a seed full of genuinely slow hosts has high
// latency with no local congestion, and a latency-shy controller would throttle
// it for no reason. The only signal that we have oversubscribed our own uplink
// is that requests begin timing out before they complete, and that is exactly
// what the decrease responds to.
//
// The effect is that the same binary finds a high in-flight limit on a fat
// datacenter pipe and a modest one on a laptop's uplink, in both cases sitting
// near the maximum throughput the link allows without collapsing into a timeout
// storm. That collapse is what used to trip the dead-domain breaker on live
// hosts, so keeping the link out of congestion is also what keeps the breaker
// honest. See congested, which the engine consults before blaming a host for a
// timeout.
type limiter struct {
	mu      sync.Mutex
	closed  bool
	waiters []chan struct{} // FIFO of parked acquirers, each granted via send

	limit    float64 // current concurrency limit
	inflight int
	min      float64
	max      float64
	dropRate float64 // EWMA of the recent timeout fraction, 0..1
}

// newLimiter returns a limiter operating in [min, max], starting at start. The
// engine derives these from the worker count: max is the worker ceiling, min a
// small floor, and start a moderate value so a run ramps up rather than opening
// thousands of connections in the first instant.
func newLimiter(min, start, max int) *limiter {
	if min < 1 {
		min = 1
	}
	if max < min {
		max = min
	}
	if start < min {
		start = min
	}
	if start > max {
		start = max
	}
	return &limiter{
		limit: float64(start),
		min:   float64(min),
		max:   float64(max),
	}
}

// acquire blocks until an in-flight slot is free or ctx is done. It reports
// whether a slot was taken; a false return means the caller should not proceed
// (ctx cancelled or the limiter closed at shutdown).
func (l *limiter) acquire(ctx context.Context) bool {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	if l.inflight < int(l.limit) {
		l.inflight++
		l.mu.Unlock()
		return true
	}
	// No slot now: park on a FIFO channel a releaser (or close) will signal.
	w := make(chan struct{}, 1)
	l.waiters = append(l.waiters, w)
	l.mu.Unlock()

	select {
	case <-w:
		// Woken either by a grant (which already incremented inflight) or by
		// shutdown. closed distinguishes the two.
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		return !closed
	case <-ctx.Done():
		l.mu.Lock()
		if i := indexOf(l.waiters, w); i >= 0 {
			// Still parked: cancel cleanly.
			l.waiters = slices.Delete(l.waiters, i, i+1)
			l.mu.Unlock()
			return false
		}
		// Granted concurrently with the cancel: hand the slot back.
		l.mu.Unlock()
		l.release(outNeutral)
		return false
	}
}

// grantLocked hands freed capacity to parked acquirers in FIFO order. Caller
// holds the lock. Each grant increments inflight on the waiter's behalf, so the
// slot cannot be taken by anyone else between the wake and the resume.
func (l *limiter) grantLocked() {
	for l.inflight < int(l.limit) && len(l.waiters) > 0 {
		w := l.waiters[0]
		l.waiters = l.waiters[1:]
		l.inflight++
		w <- struct{}{}
	}
}

// outcome classifies a finished request for the controller.
type outcome int

const (
	// outOK is a completed response: the link carried it, so grow the limit.
	outOK outcome = iota
	// outLoss is a timeout on a host that had answered before. A known-good
	// host failing now is the signal that we have oversubscribed our own
	// uplink, so shrink the limit.
	outLoss
	// outNeutral is a timeout on a host that never answered (likely just a dead
	// host), or a non-timeout error. It says nothing about the link, so the
	// limit is left unchanged: a dead tail must not throttle live throughput.
	outNeutral
)

// release returns a slot and updates the limit from this request's outcome. It
// then hands any freed capacity to parked acquirers.
func (l *limiter) release(o outcome) {
	l.mu.Lock()
	l.inflight--
	l.update(o)
	l.grantLocked()
	l.mu.Unlock()
}

// indexOf returns the position of w in ws, or -1.
func indexOf(ws []chan struct{}, w chan struct{}) int {
	for i, x := range ws {
		if x == w {
			return i
		}
	}
	return -1
}

// congestThresh is the share of recent known-good-host completions that must be
// timing out before the controller reads the link as congested and backs off.
// It is what separates a thin uplink in collapse (a broad storm, most good
// hosts failing at once) from a fat pipe with a few flaky hosts (isolated
// losses). Below it, the limit is free to climb to the ceiling, so flaky hosts
// never throttle a link that is nowhere near saturated.
const congestThresh = 0.25

// update recomputes the limit from one request's outcome. Caller holds the
// lock.
//
// The controller is additive-increase/multiplicative-decrease in the spirit of
// TCP congestion control, with one change for crawling: it backs off only on
// broad congestion, not on a single loss. A completed response grows the limit
// by one. A congestion loss (a known-good host timing out) raises a decaying
// drop-rate estimate, and only when that estimate shows many good hosts failing
// at once does the limit shrink. Two failure modes are deliberately ignored: a
// timeout on a host that never answered (a dead-tail host, which says nothing
// about our uplink) and an isolated timeout on an otherwise-good host (a flaky
// server, not local congestion). Latency is never a signal, because genuinely
// slow hosts run at high latency with no congestion at all. Only a broad storm
// of known-good hosts failing proves we have asked the link for more than it
// carries, and that alone pulls the limit down.
func (l *limiter) update(o outcome) {
	// Decay the drop-rate EWMA toward the latest outcome. Only a congestion
	// loss pushes it up; everything else lets it fall, so it reads as "what
	// share of good-host requests are failing right now".
	const dropAlpha = 0.02
	target := 0.0
	if o == outLoss {
		target = 1.0
	}
	l.dropRate = l.dropRate*(1-dropAlpha) + target*dropAlpha

	switch o {
	case outLoss:
		// Multiplicative decrease, but only under broad congestion. A gentle
		// factor keeps the oscillation around the operating point small, so the
		// limit hovers near the link's capacity rather than sawtoothing hard.
		if l.dropRate > congestThresh {
			l.limit = math.Max(l.min, l.limit*0.85)
		}
	case outOK:
		// Additive increase: climb one slot per success. On a fat pipe this
		// walks to the ceiling and stays there (losses never get broad); on a
		// thin pipe it climbs until oversubscription makes good hosts fail in
		// bulk, then the decrease pulls it back to what the link sustains.
		l.limit = math.Min(l.max, l.limit+1)
	case outNeutral:
		// A dead host or a non-timeout error: leave the limit untouched.
	}
}

// congested reports whether the link is currently in a timeout storm, in which
// case a request that timed out is more likely our own oversubscription than a
// dead host. The engine uses this to decide whether a timeout should count
// against a domain or be retried.
func (l *limiter) congested() bool {
	l.mu.Lock()
	d := l.dropRate
	l.mu.Unlock()
	return d > 0.10
}

// currentLimit returns the live in-flight limit, for diagnostics.
func (l *limiter) currentLimit() int {
	l.mu.Lock()
	v := int(l.limit)
	l.mu.Unlock()
	return v
}

// close wakes every blocked acquirer so a cancelled run unwinds promptly. A
// waiter woken this way sees closed and reports failure.
func (l *limiter) close() {
	l.mu.Lock()
	l.closed = true
	for _, w := range l.waiters {
		close(w)
	}
	l.waiters = nil
	l.mu.Unlock()
}
