package fetch

import (
	"context"
	"net"
	"sync"
	"time"
)

// resolver wraps a small pool of pure-Go DNS resolvers and caches answers for
// the life of a run. The system resolver is tried first; public resolvers act
// as fallbacks when the local one is slow or refuses an answer. Results are
// cached so a host with thousands of URLs costs one lookup.
type resolver struct {
	timeout   time.Duration
	resolvers []*net.Resolver

	mu    sync.RWMutex
	cache map[string][]net.IP
	dead  map[string]struct{}
}

// newResolver builds the resolver pool. Each entry is a PreferGo resolver, so
// no cgo and no thread-per-lookup blow-up under load.
func newResolver(timeout time.Duration) *resolver {
	return &resolver{
		timeout: timeout,
		resolvers: []*net.Resolver{
			{PreferGo: true},
			udpResolver("8.8.8.8:53"),
			udpResolver("1.1.1.1:53"),
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
// The second return is false when the host is known-dead (NXDOMAIN-ish across
// every resolver), so callers can skip it without retrying.
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

	for _, res := range r.resolvers {
		c, cancel := context.WithTimeout(ctx, r.timeout)
		addrs, err := res.LookupIPAddr(c, host)
		cancel()
		if err != nil || len(addrs) == 0 {
			continue
		}
		ips := orderIPs(addrs)
		r.mu.Lock()
		r.cache[host] = ips
		r.mu.Unlock()
		return ips, true
	}

	r.mu.Lock()
	r.dead[host] = struct{}{}
	r.mu.Unlock()
	return nil, false
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
