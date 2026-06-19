// Package fetch is the network engine: it turns a stream of seeds into a stream
// of results as fast as one machine sustains. It owns DNS, a sharded pool of
// keep-alive transports, per-host and per-IP concurrency limits, an adaptive
// timeout, and dead-host/dead-domain tracking so a run does not waste workers on
// targets that will never answer.
package fetch

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tamnd/ami/config"
	"github.com/tamnd/ami/urlx"
)

// Result is the outcome of fetching one seed.
type Result struct {
	URL       string
	FetchedAt time.Time

	// Set on a completed HTTP exchange.
	Status    int
	Header    http.Header
	ReqHeader http.Header
	Body      []byte
	Digest    string // sha1 hex of Body

	// Unchanged is true when Digest matches the seed's prior digest, so the
	// content did not change since it was last captured.
	Unchanged bool

	// Meta is carried verbatim from the seed.
	Meta map[string]string

	// Err is non-nil for transport/DNS/timeout failures (no HTTP response).
	Err error
}

// Fetcher performs concurrent re-fetches with post-fetch digest comparison.
type Fetcher struct {
	cfg     config.Config
	res     *resolver
	clients []*http.Client
	timeout *adaptiveTimeout
	maxBody int64

	hostSem sync.Map // host -> chan struct{}
	deadDom sync.Map // registered domain -> *domainState
}

type domainState struct {
	fails atomic.Int64
}

// New builds a Fetcher from config.
func New(cfg config.Config) *Fetcher {
	res := newResolver(cfg.DNSTimeout)
	f := &Fetcher{
		cfg:     cfg,
		res:     res,
		timeout: newAdaptiveTimeout(cfg.Timeout),
		maxBody: cfg.MaxBodyBytes,
	}
	f.clients = make([]*http.Client, cfg.TransportShards)
	for i := range f.clients {
		f.clients[i] = &http.Client{
			Transport:     f.newTransport(res),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}
	return f
}

// newTransport builds one transport shard. Each shard keeps its own idle pool,
// so spreading hosts across shards multiplies the effective keep-alive budget.
func (f *Fetcher) newTransport(res *resolver) *http.Transport {
	dialer := &net.Dialer{Timeout: f.cfg.ProbeTimeout, KeepAlive: 30 * time.Second}
	return &http.Transport{
		// Resolve through our cached resolver and dial an IP directly, so a
		// host with many URLs pays for one lookup and connects fast.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return dialer.DialContext(ctx, network, addr)
			}
			ips, ok := res.lookup(ctx, host)
			if !ok || len(ips) == 0 {
				return dialer.DialContext(ctx, network, addr)
			}
			var lastErr error
			for _, ip := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		// InsecureSkipVerify avoids cgo cert-verification thread exhaustion at
		// high concurrency; ami archives bytes, it does not authenticate peers.
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2:     false,
		DisableCompression:    true,
		MaxIdleConns:          0,
		MaxIdleConnsPerHost:   50,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   f.cfg.ProbeTimeout,
		ExpectContinueTimeout: time.Second,
		WriteBufferSize:       8 * 1024,
		ReadBufferSize:        16 * 1024,
	}
}

// clientFor returns the transport shard for a host, chosen by hash so a host is
// pinned to one shard and reuses its keep-alive pool.
func (f *Fetcher) clientFor(host string) *http.Client {
	h := fnv.New32a()
	_, _ = h.Write([]byte(host))
	return f.clients[int(h.Sum32())%len(f.clients)]
}

// acquireHost returns a release func after taking a per-host slot, bounding how
// many connections a single host sees at once.
func (f *Fetcher) acquireHost(ctx context.Context, host string) (func(), bool) {
	v, _ := f.hostSem.LoadOrStore(host, make(chan struct{}, f.cfg.MaxConnsPerHost))
	sem := v.(chan struct{})
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, true
	case <-ctx.Done():
		return func() {}, false
	}
}

// domainDead reports whether a domain has exceeded the failure threshold and
// should be skipped for the rest of the run.
func (f *Fetcher) domainDead(domain string) bool {
	v, ok := f.deadDom.Load(domain)
	if !ok {
		return false
	}
	return v.(*domainState).fails.Load() >= int64(f.cfg.DomainFailThreshold)
}

// noteDomainFail records a transport failure for a domain.
func (f *Fetcher) noteDomainFail(domain string) {
	v, _ := f.deadDom.LoadOrStore(domain, &domainState{})
	v.(*domainState).fails.Add(1)
}

// Fetch performs one re-fetch and returns its Result, setting Unchanged when the
// fetched body's sha1 matches the seed's prior digest. It never returns an error
// itself; failures are reported in Result.Err.
func (f *Fetcher) Fetch(ctx context.Context, s SeedURL) Result {
	r := Result{URL: s.URL, Meta: s.Meta, FetchedAt: time.Now()}

	host := urlx.Host(s.URL)
	if host == "" || !urlx.IsHTTP(s.URL) {
		r.Err = errSkip
		return r
	}
	domain := urlx.RegisteredDomain(host)
	if f.domainDead(domain) {
		r.Err = errSkip
		return r
	}

	release, ok := f.acquireHost(ctx, host)
	if !ok {
		r.Err = ctx.Err()
		return r
	}
	defer release()

	to := f.timeout.value()
	reqCtx, cancel := context.WithTimeout(ctx, to)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL, nil)
	if err != nil {
		r.Err = err
		return r
	}
	f.setHeaders(req)
	r.ReqHeader = req.Header.Clone()

	start := time.Now()
	resp, err := f.clientFor(host).Do(req)
	if err != nil {
		f.noteDomainFail(domain)
		r.Err = err
		return r
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBody))
	if err != nil {
		r.Err = err
		return r
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	f.timeout.observe(time.Since(start))

	sum := sha1.Sum(body)
	r.Status = resp.StatusCode
	r.Header = resp.Header
	r.Body = body
	r.Digest = hex.EncodeToString(sum[:])
	r.Unchanged = s.Digest != "" && s.Digest == r.Digest
	return r
}

// SeedURL is the subset of a seed the fetcher needs, decoupling fetch from the
// seed package's concrete type.
type SeedURL struct {
	URL    string
	Digest string
	Meta   map[string]string
}
