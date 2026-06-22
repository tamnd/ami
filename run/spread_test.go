package run

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/metrics"
	"github.com/tamnd/ami/seed"
	"github.com/tamnd/ami/urlx"
)

// drainSpreader pops every seed the spreader yields into a slice.
func drainSpreader(sp *hostSpreader) []seed.Seed {
	var got []seed.Seed
	for {
		s, ok := sp.pop()
		if !ok {
			return got
		}
		got = append(got, s)
	}
}

// urls extracts the URL of each seed, in order.
func urls(seeds []seed.Seed) []string {
	out := make([]string, len(seeds))
	for i, s := range seeds {
		out[i] = s.URL
	}
	return out
}

func TestHostSpreaderPreservesSet(t *testing.T) {
	// A host-clustered input: three hosts, each with a run of consecutive URLs.
	var in []seed.Seed
	for _, h := range []string{"a.com", "b.com", "c.com"} {
		for i := 0; i < 5; i++ {
			in = append(in, seed.Seed{URL: fmt.Sprintf("http://%s/%d", h, i)})
		}
	}

	sp := newHostSpreader(64)
	go func() {
		for _, s := range in {
			if !sp.push(s) {
				t.Errorf("push returned false before close")
			}
		}
		sp.close()
	}()
	got := drainSpreader(sp)

	if len(got) != len(in) {
		t.Fatalf("got %d seeds, want %d", len(got), len(in))
	}
	wantSet := urls(in)
	gotSet := urls(got)
	sort.Strings(wantSet)
	sort.Strings(gotSet)
	for i := range wantSet {
		if wantSet[i] != gotSet[i] {
			t.Fatalf("set mismatch at %d: got %q want %q", i, gotSet[i], wantSet[i])
		}
	}
}

func TestHostSpreaderInterleavesHosts(t *testing.T) {
	// Same host-clustered input as above: every URL of a host is contiguous.
	var in []seed.Seed
	hosts := []string{"a.com", "b.com", "c.com"}
	for _, h := range hosts {
		for i := 0; i < 5; i++ {
			in = append(in, seed.Seed{URL: fmt.Sprintf("http://%s/%d", h, i)})
		}
	}

	// Window larger than the input so the spreader holds every host at once and
	// the round-robin can interleave all three.
	sp := newHostSpreader(64)
	for _, s := range in {
		if !sp.push(s) {
			t.Fatal("push returned false before close")
		}
	}
	sp.close()
	got := drainSpreader(sp)

	// The first three pops should each touch a different host, which the
	// clustered input never would on its own.
	seen := map[string]bool{}
	for _, s := range got[:len(hosts)] {
		seen[urlx.Host(s.URL)] = true
	}
	if len(seen) != len(hosts) {
		t.Fatalf("first %d pops covered %d hosts, want %d (no spread): %v",
			len(hosts), len(seen), len(hosts), urls(got))
	}

	// Within a host, order is still FIFO.
	perHost := map[string][]string{}
	for _, s := range got {
		perHost[urlx.Host(s.URL)] = append(perHost[urlx.Host(s.URL)], s.URL)
	}
	for h, list := range perHost {
		for i := 0; i < len(list); i++ {
			want := fmt.Sprintf("http://%s/%d", h, i)
			if list[i] != want {
				t.Fatalf("host %s out of FIFO order: got %q want %q", h, list[i], want)
			}
		}
	}
}

func TestFeedReorderSpreadsClusteredSeed(t *testing.T) {
	var in []seed.Seed
	hosts := []string{"a.com", "b.com", "c.com", "d.com"}
	for _, h := range hosts {
		for i := 0; i < 8; i++ {
			in = append(in, seed.Seed{URL: fmt.Sprintf("http://%s/%d", h, i)})
		}
	}

	cfg := config.Default()
	cfg.Reorder = true
	r := New(cfg)
	r.stats = metrics.NewStats(time.Now())

	seeds := make(chan seed.Seed, 4)
	var got []seed.Seed
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for s := range seeds {
			got = append(got, s)
		}
	}()

	if err := r.feed(context.Background(), sliceSource{seeds: in}, seeds); err != nil {
		t.Fatalf("feed: %v", err)
	}
	close(seeds)
	wg.Wait()

	if len(got) != len(in) {
		t.Fatalf("got %d seeds, want %d", len(got), len(in))
	}
	if int(r.stats.Seeded.Load()) != len(in) {
		t.Fatalf("Seeded = %d, want %d", r.stats.Seeded.Load(), len(in))
	}
	// First four emitted seeds should span all four hosts with a buffered feed.
	seen := map[string]bool{}
	for _, s := range got[:len(hosts)] {
		seen[urlx.Host(s.URL)] = true
	}
	if len(seen) != len(hosts) {
		t.Fatalf("first %d feed emissions covered %d hosts, want %d: %v",
			len(hosts), len(seen), len(hosts), urls(got))
	}
}

func TestFeedNoReorderKeepsOrder(t *testing.T) {
	in := []seed.Seed{
		{URL: "http://a.com/1"},
		{URL: "http://a.com/2"},
		{URL: "http://b.com/1"},
	}

	cfg := config.Default()
	cfg.Reorder = false
	r := New(cfg)
	r.stats = metrics.NewStats(time.Now())

	seeds := make(chan seed.Seed, len(in))
	if err := r.feed(context.Background(), sliceSource{seeds: in}, seeds); err != nil {
		t.Fatalf("feed: %v", err)
	}
	close(seeds)

	var got []string
	for s := range seeds {
		got = append(got, s.URL)
	}
	want := urls(in)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order changed at %d: got %q want %q", i, got[i], want[i])
		}
	}
}
