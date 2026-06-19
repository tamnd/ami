package fetch

import (
	"context"
	"testing"
	"time"
)

// TestLimiterGrowsWhenHealthy checks that steady low-latency traffic walks the
// limit up toward the ceiling.
func TestLimiterGrowsWhenHealthy(t *testing.T) {
	l := newLimiter(4, 16, 1000)
	for i := 0; i < 2000; i++ {
		if !l.acquire(context.Background()) {
			t.Fatal("acquire failed on an open limiter")
		}
		l.release(outOK)
	}
	if got := l.currentLimit(); got <= 16 {
		t.Fatalf("limit did not grow on healthy traffic: %d", got)
	}
}

// TestLimiterBacksOffOnDrops checks that a burst of timeouts shrinks the limit
// and raises the congestion signal.
func TestLimiterBacksOffOnDrops(t *testing.T) {
	l := newLimiter(4, 500, 1000)
	for i := 0; i < 200; i++ {
		_ = l.acquire(context.Background())
		l.release(outLoss) // every request times out
	}
	if got := l.currentLimit(); got >= 500 {
		t.Fatalf("limit did not back off under drops: %d", got)
	}
	if !l.congested() {
		t.Fatal("congested() should be true during a timeout storm")
	}
}

// TestLimiterIgnoresIsolatedLosses is the fat-pipe case: a steady stream of
// successes sprinkled with the odd timeout from a flaky-but-alive host must keep
// climbing to the ceiling, not get pinned low. A handful of isolated losses are
// not link congestion, and throttling on them leaves bandwidth on the table.
func TestLimiterIgnoresIsolatedLosses(t *testing.T) {
	l := newLimiter(4, 64, 5000)
	for i := 0; i < 5000; i++ {
		_ = l.acquire(context.Background())
		if i%20 == 0 { // ~5% of completions time out, scattered among the good ones
			l.release(outLoss)
		} else {
			l.release(outOK)
		}
	}
	if l.congested() {
		t.Fatal("a ~5% scattered loss rate must not read as congestion")
	}
	if got := l.currentLimit(); got <= 64 {
		t.Fatalf("isolated losses pinned the limit instead of climbing: %d", got)
	}
}

// TestLimiterClearsCongestion checks that the congestion signal falls once the
// link recovers, so healthy timeouts are attributed to the host again.
func TestLimiterClearsCongestion(t *testing.T) {
	l := newLimiter(4, 200, 1000)
	for i := 0; i < 50; i++ {
		_ = l.acquire(context.Background())
		l.release(outLoss)
	}
	if !l.congested() {
		t.Fatal("expected congestion after a drop burst")
	}
	for i := 0; i < 2000; i++ {
		_ = l.acquire(context.Background())
		l.release(outOK)
	}
	if l.congested() {
		t.Fatal("congestion should clear after a long healthy run")
	}
}

// TestLimiterAcquireCancels checks that acquire unblocks when the context is
// cancelled while the limiter is saturated.
func TestLimiterAcquireCancels(t *testing.T) {
	l := newLimiter(1, 1, 1)
	if !l.acquire(context.Background()) {
		t.Fatal("first acquire should succeed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- l.acquire(ctx) }()
	select {
	case <-done:
		t.Fatal("acquire returned while the only slot was held")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("acquire should report failure after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("acquire did not unblock after cancel")
	}
}
