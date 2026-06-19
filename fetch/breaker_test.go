package fetch

import (
	"testing"

	"github.com/tamnd/ami/config"
)

// TestBreakerTripsOnGenuineFailures checks that a domain is skipped only after
// it accumulates the threshold of genuine, host-attributable failures.
func TestBreakerTripsOnGenuineFailures(t *testing.T) {
	cfg := config.Default()
	cfg.DomainFailThreshold = 3
	f := New(cfg)
	const dom = "dead.example"

	for i := range cfg.DomainFailThreshold - 1 {
		f.noteDomainFail(dom)
		if f.domainDead(dom) {
			t.Fatalf("domain dead after %d of %d failures", i+1, cfg.DomainFailThreshold)
		}
	}
	f.noteDomainFail(dom)
	if !f.domainDead(dom) {
		t.Fatalf("domain not dead after %d failures", cfg.DomainFailThreshold)
	}
}

// TestAnsweredDomainNeverDies checks that a domain that has returned a response
// is immune to the breaker no matter how many failures follow.
func TestAnsweredDomainNeverDies(t *testing.T) {
	cfg := config.Default()
	cfg.DomainFailThreshold = 3
	f := New(cfg)
	const dom = "slow.example"

	f.noteDomainOK(dom)
	for range cfg.DomainFailThreshold + 5 {
		f.noteDomainFail(dom)
	}
	if f.domainDead(dom) {
		t.Fatal("a domain that answered was skipped over later failures")
	}
}

// TestReachableDomainNeverDies checks that a domain we have merely connected to,
// even without a completed response, is immune to the breaker. This is the
// property the resolver-socket reachability bug violated: marking reachability
// from the wrong signal silently immunised every domain; marking it from no
// signal would let a slow-but-connectable host be skipped.
func TestReachableDomainNeverDies(t *testing.T) {
	cfg := config.Default()
	cfg.DomainFailThreshold = 3
	f := New(cfg)
	const dom = "reachable.example"

	f.noteDomainReachable(dom)
	for range cfg.DomainFailThreshold + 5 {
		f.noteDomainFail(dom)
	}
	if f.domainDead(dom) {
		t.Fatal("a domain we connected to was skipped over later failures")
	}
}

// TestUntouchedDomainIsAlive checks that a domain with no recorded failures is
// never reported dead.
func TestUntouchedDomainIsAlive(t *testing.T) {
	f := New(config.Default())
	if f.domainDead("fresh.example") {
		t.Fatal("a domain with no failures was reported dead")
	}
}
