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
	"errors"
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
	lim     *limiter
	maxBody int64

	hostSem sync.Map // host -> chan struct{}
	deadDom sync.Map // registered domain -> *domainState
}

type domainState struct {
	fails atomic.Int64
	// alive is set once a domain has returned any response. A domain that has
	// answered is never skipped for later timeouts: the breaker exists to stop
	// wasting workers on hosts that will never answer, not on live-but-slow
	// ones, so a single success immunizes it for the rest of the run.
	alive atomic.Bool
}

// New builds a Fetcher from config.
func New(cfg config.Config) *Fetcher {
	res := newResolver(cfg.DNSTimeout)
	f := &Fetcher{
		cfg:     cfg,
		res:     res,
		timeout: newAdaptiveTimeout(cfg.Timeout),
		lim:     newLimiter(cfg.MinInflight, cfg.StartInflight, cfg.Workers),
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
// should be skipped for the rest of the run. A domain that has ever answered is
// never dead: one success immunizes it, so a live-but-slow host is never
// skipped over a run of transient timeouts.
func (f *Fetcher) domainDead(domain string) bool {
	v, ok := f.deadDom.Load(domain)
	if !ok {
		return false
	}
	st := v.(*domainState)
	if st.alive.Load() {
		return false
	}
	return st.fails.Load() >= int64(f.cfg.DomainFailThreshold)
}

// noteDomainFail records a host-attributable failure for a domain.
func (f *Fetcher) noteDomainFail(domain string) {
	v, _ := f.deadDom.LoadOrStore(domain, &domainState{})
	v.(*domainState).fails.Add(1)
}

// noteDomainOK records that a domain answered, immunizing it from the breaker
// and clearing any failures it accumulated before it first responded.
func (f *Fetcher) noteDomainOK(domain string) {
	v, _ := f.deadDom.LoadOrStore(domain, &domainState{})
	st := v.(*domainState)
	if st.alive.CompareAndSwap(false, true) {
		st.fails.Store(0)
	}
}

// Stop releases the engine's blocking limiter so a cancelled run unwinds. It is
// safe to call once the run context is done.
func (f *Fetcher) Stop() { f.lim.close() }

// Limit reports the live in-flight concurrency limit, for progress display.
func (f *Fetcher) Limit() int { return f.lim.currentLimit() }

// attribute decides what a transport error means for the dead-domain breaker.
// A failure the remote host owns counts toward its death. A transient failure
// is blamed on our own oversubscription and returned as errRetry while the link
// is congested, so the run loop retries it; only on a healthy link does a
// transient failure count, because then a timeout really is the host's fault.
func (f *Fetcher) attribute(domain string, err error) error {
	switch classify(err) {
	case classCanceled:
		return err
	case classGenuine:
		f.noteDomainFail(domain)
		return err
	default: // classTransient
		if f.lim.congested() {
			return errRetry
		}
		f.noteDomainFail(domain)
		return err
	}
}

// failOutcome maps a failed request to a controller outcome. A timeout on a
// host that has answered before is a congestion loss (the link is the problem);
// a timeout on a host that never answered, or any non-timeout error, is neutral
// (a dead host, which must not throttle the limit for the live ones).
func (f *Fetcher) failOutcome(domain string, err error) outcome {
	if isTimeoutErr(err) && f.domainAlive(domain) {
		return outLoss
	}
	return outNeutral
}

// domainAlive reports whether a domain has ever returned a response.
func (f *Fetcher) domainAlive(domain string) bool {
	if v, ok := f.deadDom.Load(domain); ok {
		return v.(*domainState).alive.Load()
	}
	return false
}

// isTimeoutErr reports whether err is a timeout, the loss signal the limiter
// uses to back off.
func isTimeoutErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		return ne.Timeout()
	}
	return false
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

	// Gate on the adaptive in-flight limit before taking any connection, so a
	// thin uplink is never asked to open more sockets than it sustains. This is
	// what keeps the link out of the congestion collapse that would otherwise
	// time out live hosts and trip the breaker.
	if !f.lim.acquire(ctx) {
		r.Err = ctx.Err()
		if r.Err == nil {
			r.Err = errSkip
		}
		return r
	}

	release, ok := f.acquireHost(ctx, host)
	if !ok {
		f.lim.release(outNeutral)
		r.Err = ctx.Err()
		return r
	}

	to := f.timeout.value()
	reqCtx, cancel := context.WithTimeout(ctx, to)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL, nil)
	if err != nil {
		cancel()
		release()
		f.lim.release(outNeutral)
		r.Err = err
		return r
	}
	f.setHeaders(req)
	r.ReqHeader = req.Header.Clone()

	start := time.Now()
	resp, err := f.clientFor(host).Do(req)
	if err != nil {
		cancel()
		release()
		f.lim.release(f.failOutcome(domain, err))
		r.Err = f.attribute(domain, err)
		return r
	}

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, f.maxBody))
	if rerr == nil {
		_, _ = io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	}
	_ = resp.Body.Close()
	rtt := time.Since(start)
	cancel()
	release()
	if rerr != nil {
		f.lim.release(f.failOutcome(domain, rerr))
		r.Err = f.attribute(domain, rerr)
		return r
	}
	f.lim.release(outOK)
	f.timeout.observe(rtt)
	f.noteDomainOK(domain)

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
