package run

import (
	"fmt"
	"sort"
	"testing"

	"github.com/tamnd/ami/urlx"
)

// TestSpreadURLsByHostDeclusters checks that a host-clustered list comes out
// with consecutive entries on different hosts, so the worker pool does not pin
// against the per-host cap, while the set of URLs is preserved exactly.
func TestSpreadURLsByHostDeclusters(t *testing.T) {
	// Three hosts, each a run of consecutive URLs, as a raw CC shard arrives.
	var in []string
	for _, host := range []string{"a.com", "b.com", "c.com"} {
		for i := 0; i < 4; i++ {
			in = append(in, fmt.Sprintf("http://%s/%d", host, i))
		}
	}

	out := spreadURLsByHost(in)

	if len(out) != len(in) {
		t.Fatalf("length changed: got %d want %d", len(out), len(in))
	}
	// Same multiset of URLs.
	si, so := append([]string(nil), in...), append([]string(nil), out...)
	sort.Strings(si)
	sort.Strings(so)
	for i := range si {
		if si[i] != so[i] {
			t.Fatalf("set changed at %d: got %q want %q", i, so[i], si[i])
		}
	}
	// No two consecutive URLs share a host while more than one host still has
	// URLs left to emit. With three balanced hosts the round-robin makes every
	// adjacent pair distinct.
	for i := 1; i < len(out); i++ {
		if urlx.Host(out[i]) == urlx.Host(out[i-1]) {
			t.Fatalf("adjacent same-host at %d: %q then %q", i, out[i-1], out[i])
		}
	}
}

// TestSpreadURLsByHostSingleHost leaves a single-host list untouched: there is
// nothing to spread, and the early return avoids rebuilding the slice.
func TestSpreadURLsByHostSingleHost(t *testing.T) {
	in := []string{"http://a.com/1", "http://a.com/2", "http://a.com/3"}
	out := spreadURLsByHost(in)
	if len(out) != len(in) {
		t.Fatalf("length changed: got %d want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Fatalf("order changed at %d: got %q want %q", i, out[i], in[i])
		}
	}
}

// TestSpreadURLsByHostUnevenDrains checks that when one host has far more URLs
// than the others, the spread still emits every URL (the long tail keeps coming
// out after the short hosts are exhausted) and preserves the count.
func TestSpreadURLsByHostUnevenDrains(t *testing.T) {
	in := []string{
		"http://big.com/1", "http://big.com/2", "http://big.com/3",
		"http://big.com/4", "http://big.com/5",
		"http://small.com/1",
	}
	out := spreadURLsByHost(in)
	if len(out) != len(in) {
		t.Fatalf("length changed: got %d want %d", len(out), len(in))
	}
	// First two out should be one big and one small (round-robin), then the rest
	// of big drains.
	bigs := 0
	for _, u := range out {
		if urlx.Host(u) == "big.com" {
			bigs++
		}
	}
	if bigs != 5 {
		t.Fatalf("lost big.com URLs: got %d want 5", bigs)
	}
}
