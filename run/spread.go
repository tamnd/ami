package run

import (
	"context"
	"sync"

	"github.com/tamnd/ami/seed"
	"github.com/tamnd/ami/urlx"
)

// feed streams every seed from src into the worker channel. When Reorder is on
// it spreads the seeds across hosts first, so the order the caller listed the
// URLs in does not decide throughput: a host-clustered shard (a run of URLs
// that all share one host, as a raw Common Crawl shard arrives) would otherwise
// pin the worker pool against the per-host concurrency cap while most of the
// pool sits idle. The spreader keeps a wide host set in flight regardless.
func (r *Runner) feed(ctx context.Context, src seed.Source, seeds chan<- seed.Seed) error {
	if !r.cfg.Reorder {
		return src.Iterate(ctx, func(s seed.Seed) error {
			if !r.keep(s) {
				return nil
			}
			r.stats.Seeded.Add(1)
			select {
			case seeds <- s:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}

	sp := newHostSpreader(r.reorderWindow())

	// A watcher closes the spreader on cancellation so a producer parked on a
	// full buffer or a drainer parked on an empty one unwinds promptly.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			sp.close()
		case <-stop:
		}
	}()
	defer close(stop)

	// Drainer: pop in round-robin host order and hand to the workers.
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			s, ok := sp.pop()
			if !ok {
				return
			}
			select {
			case seeds <- s:
			case <-ctx.Done():
				return
			}
		}
	}()

	feedErr := src.Iterate(ctx, func(s seed.Seed) error {
		if !r.keep(s) {
			return nil
		}
		r.stats.Seeded.Add(1)
		if !sp.push(s) {
			// Closed mid-feed, which only happens on cancellation.
			return ctx.Err()
		}
		return nil
	})

	sp.close() // no more seeds; let the drainer flush the buffer and exit
	<-drainDone
	return feedErr
}

// keep reports whether a seed should be crawled by this process: it must carry a
// URL and fall in this process's shard partition.
func (r *Runner) keep(s seed.Seed) bool {
	return s.URL != "" && r.inShard(s.URL)
}

// reorderWindow is how many seeds the spreader buffers. A configured value wins;
// zero auto-sizes from the worker count, large enough to hold many distinct
// hosts at once so the round-robin always has a wide set to rotate through, with
// a floor so a small pool still spreads well.
func (r *Runner) reorderWindow() int {
	if r.cfg.ReorderWindow > 0 {
		return r.cfg.ReorderWindow
	}
	if w := r.cfg.Workers * 16; w > 65536 {
		return w
	}
	return 65536
}

// hostSpreader is a bounded buffer of per-host FIFO queues. Pushes append to the
// queue for the seed's host; pops take one seed at a time round-robin across the
// hosts that currently have a queued seed. The effect is that consecutive pops
// touch different hosts even when consecutive pushes shared one, so the workers
// see a host-spread stream out of a host-clustered input, in constant memory.
type hostSpreader struct {
	mu       sync.Mutex
	notFull  *sync.Cond
	notEmpty *sync.Cond

	capacity int
	n        int // total seeds buffered across all host queues

	q      map[string][]seed.Seed
	ring   []string // hosts with a non-empty queue, in round-robin order
	cursor int      // index into ring of the next host to pop from
	closed bool
}

func newHostSpreader(capacity int) *hostSpreader {
	s := &hostSpreader{
		capacity: capacity,
		q:        make(map[string][]seed.Seed),
	}
	s.notFull = sync.NewCond(&s.mu)
	s.notEmpty = sync.NewCond(&s.mu)
	return s
}

// push adds one seed, blocking while the buffer is full. It returns false if the
// spreader was closed before the seed could be admitted.
func (s *hostSpreader) push(item seed.Seed) bool {
	h := urlx.Host(item.URL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.n >= s.capacity && !s.closed {
		s.notFull.Wait()
	}
	if s.closed {
		return false
	}
	if len(s.q[h]) == 0 {
		s.ring = append(s.ring, h)
	}
	s.q[h] = append(s.q[h], item)
	s.n++
	s.notEmpty.Signal()
	return true
}

// pop removes the next seed in round-robin host order, blocking while the buffer
// is empty. It returns false once the spreader is closed and fully drained.
func (s *hostSpreader) pop() (seed.Seed, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.n == 0 && !s.closed {
		s.notEmpty.Wait()
	}
	if s.n == 0 {
		return seed.Seed{}, false
	}
	if s.cursor >= len(s.ring) {
		s.cursor = 0
	}
	h := s.ring[s.cursor]
	queue := s.q[h]
	item := queue[0]
	queue = queue[1:]
	if len(queue) == 0 {
		delete(s.q, h)
		// Drop the host from the ring; the slice shifts left so the cursor now
		// points at the next host already, no advance needed.
		s.ring = append(s.ring[:s.cursor], s.ring[s.cursor+1:]...)
	} else {
		s.q[h] = queue
		s.cursor++ // move to the next host for the round-robin
	}
	s.n--
	s.notFull.Signal()
	return item, true
}

// close marks the spreader done. Pending pops drain the remaining buffer and
// then report false; pending and future pushes report false at once. Idempotent.
func (s *hostSpreader) close() {
	s.mu.Lock()
	s.closed = true
	s.notEmpty.Broadcast()
	s.notFull.Broadcast()
	s.mu.Unlock()
}
