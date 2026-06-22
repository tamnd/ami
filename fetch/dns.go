package fetch

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// resolver wraps a small pool of pure-Go DNS resolvers and caches answers for
// the life of a run. Every resolver in the pool is raced in parallel and the
// first usable answer wins, so a slow or overloaded local stub never gates a
// lookup: on a crawl that resolves tens of thousands of distinct hosts, the
// systemd-resolved stub at 127.0.0.53 both caps the rate (a few hundred
// lookups a second) and silently drops a large fraction of perfectly
// resolvable hosts under load, which a sequential system-first resolver turns
// into spurious fetch failures. Racing public resolvers alongside it removes
// both problems. Results are cached so a host with thousands of URLs costs one
// lookup.
type resolver struct {
	timeout   time.Duration
	resolvers []*net.Resolver

	mu    sync.RWMutex
	cache map[string][]net.IP
	dead  map[string]struct{}
}

// newResolver builds the resolver pool. Each entry is a PreferGo resolver, so
// no cgo and no thread-per-lookup blow-up under load. The system resolver is
// one racer among several rather than the first one tried, so the local stub's
// rate cap and load-shedding cannot bound the crawl; the public resolvers
// answer in parallel and the fastest correct reply is taken.
func newResolver(timeout time.Duration) *resolver {
	return &resolver{
		timeout: timeout,
		resolvers: []*net.Resolver{
			{PreferGo: true}, // the system stub, for split-horizon names; not privileged
			udpResolver("8.8.8.8:53"),
			udpResolver("1.1.1.1:53"),
			udpResolver("9.9.9.9:53"),
		},
		cache: make(map[string][]net.IP),
		dead:  make(map[string]struct{}),
	}
}

// udpResolver returns a Go resolver that always dials the given DNS server.
func udpResolver(server string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", server)
		},
	}
}

// lookup returns cached or freshly resolved IPs for host, ordered IPv4-first.
// The second return is false when the host could not be resolved. It is only
// recorded as known-dead (so a later URL on the same host skips the lookup)
// when every resolver in the pool authoritatively reports NXDOMAIN; a lookup
// that merely timed out or was dropped under load is not proof the name does
// not exist, so it is not negative-cached and the breaker is never fed a
// manufactured death.
func (r *resolver) lookup(ctx context.Context, host string) ([]net.IP, bool) {
	r.mu.RLock()
	if _, dead := r.dead[host]; dead {
		r.mu.RUnlock()
		return nil, false
	}
	if ips, ok := r.cache[host]; ok {
		r.mu.RUnlock()
		return ips, true
	}
	r.mu.RUnlock()

	ips, nxdomain := r.race(ctx, host)
	if len(ips) > 0 {
		r.mu.Lock()
		r.cache[host] = ips
		r.mu.Unlock()
		return ips, true
	}
	if nxdomain {
		r.mu.Lock()
		r.dead[host] = struct{}{}
		r.mu.Unlock()
	}
	return nil, false
}

// race queries every resolver in the pool concurrently and returns the first
// usable answer, cancelling the losers. If no resolver returns addresses, the
// nxdomain return reports whether every resolver agreed the name does not
// exist (a definitive NXDOMAIN); if any resolver instead timed out or failed
// transiently, nxdomain is false, because a transient miss is not proof of a
// dead name.
func (r *resolver) race(ctx context.Context, host string) (ips []net.IP, nxdomain bool) {
	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	type answer struct {
		ips []net.IP
		nx  bool
	}
	ch := make(chan answer, len(r.resolvers))
	for _, res := range r.resolvers {
		go func(res *net.Resolver) {
			addrs, err := res.LookupIPAddr(cctx, host)
			if err == nil && len(addrs) > 0 {
				ch <- answer{ips: orderIPs(addrs)}
				return
			}
			var de *net.DNSError
			ch <- answer{nx: errors.As(err, &de) && de.IsNotFound}
		}(res)
	}

	allNX := true
	for range r.resolvers {
		a := <-ch
		if len(a.ips) > 0 {
			return a.ips, false
		}
		if !a.nx {
			allNX = false
		}
	}
	return nil, allNX
}

// orderIPs returns the addresses IPv4-first; v4 connects succeed more often on
// crawl targets and avoids stalls on broken v6 paths.
func orderIPs(addrs []net.IPAddr) []net.IP {
	v4 := make([]net.IP, 0, len(addrs))
	v6 := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if a.IP.To4() != nil {
			v4 = append(v4, a.IP)
		} else {
			v6 = append(v6, a.IP)
		}
	}
	return append(v4, v6...)
}
