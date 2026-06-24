package fetch

import (
	"testing"
	"time"
)

// TestAdaptiveTimeoutDefaultFloor checks that a zero floor selects the engine
// default rather than collapsing the timeout to zero.
func TestAdaptiveTimeoutDefaultFloor(t *testing.T) {
	a := newAdaptiveTimeout(5*time.Second, 0)
	if a.floor != defaultTimeoutFloor {
		t.Fatalf("floor: got %v want %v", a.floor, defaultTimeoutFloor)
	}
	if got := a.value(); got != 5*time.Second {
		t.Fatalf("seeded value: got %v want 5s", got)
	}
}

// TestAdaptiveTimeoutCustomFloor checks that a caller-supplied floor is honored,
// and that feeding only fast samples cannot pull the served timeout below it.
func TestAdaptiveTimeoutCustomFloor(t *testing.T) {
	floor := 1200 * time.Millisecond
	a := newAdaptiveTimeout(5*time.Second, floor)
	if a.floor != floor {
		t.Fatalf("floor: got %v want %v", a.floor, floor)
	}
	// Feed a pile of sub-100ms samples so P95 x 2 would land well under the
	// floor, then confirm the served value is clamped up to the floor.
	for i := 0; i < 256; i++ {
		a.observe(50 * time.Millisecond)
	}
	if got := a.value(); got < floor {
		t.Fatalf("value dropped below floor: got %v floor %v", got, floor)
	}
	if got := a.value(); got > 5*time.Second {
		t.Fatalf("value above ceiling: got %v", got)
	}
}

// TestAdaptiveTimeoutFloorAboveCeilingPins checks the dead-host-heavy idiom: a
// floor at or above the ceiling pins the served timeout at the ceiling, so a
// caller that sets a tight ceiling gets a fixed short deadline regardless of the
// observed latency distribution.
func TestAdaptiveTimeoutFloorAboveCeilingPins(t *testing.T) {
	a := newAdaptiveTimeout(2500*time.Millisecond, 3*time.Second)
	for i := 0; i < 256; i++ {
		a.observe(50 * time.Millisecond)
	}
	if got := a.value(); got != 2500*time.Millisecond {
		t.Fatalf("expected pinned ceiling 2.5s, got %v", got)
	}
}
