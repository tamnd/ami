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

// TestRespondedDomainNeverDies checks that a domain that has sent us its first
// response byte, even without a completed body, is immune to the breaker. A host
// that puts bytes on the wire is alive but slow, not dead, so it must never be
// skipped over later body-stall timeouts.
func TestRespondedDomainNeverDies(t *testing.T) {
	cfg := config.Default()
	cfg.DomainFailThreshold = 3
	f := New(cfg)
	const dom = "slow-body.example"

	f.noteDomainResponded(dom)
	for range cfg.DomainFailThreshold + 5 {
		f.noteDomainFail(dom)
	}
	if f.domainDead(dom) {
		t.Fatal("a domain that sent a response byte was skipped over later failures")
	}
}

// TestConnectButSilentDomainDies checks the property that the TCP-reachability
// immunization used to violate: a host that accepts the connection then never
// sends a byte (the dominant dead-host mode on a stale shard) must trip the
// breaker so its remaining URLs are skipped instead of each re-paying the header
// deadline. attribute counts an uncongested timeout on a never-responded domain.
func TestConnectButSilentDomainDies(t *testing.T) {
	cfg := config.Default()
	cfg.DomainFailThreshold = 2
	f := New(cfg)
	const dom = "silent.example"

	to := &timeoutErr{}
	for i := 0; i < cfg.DomainFailThreshold; i++ {
		_ = f.attribute(dom, to)
	}
	if !f.domainDead(dom) {
		t.Fatal("a connect-but-silent host was never skipped despite repeated timeouts")
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
