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
	"net/http/httptrace"
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

	// ETag and LastModified are the response validators, carried into the
	// capture index so a later recrawl can issue a conditional request. On a
	// 304 they fall back to the validators the request sent.
	ETag         string
	LastModified string

	// Unchanged is true when Digest matches the seed's prior digest, so the
	// content did not change since it was last captured.
	Unchanged bool

	// Meta is carried verbatim from the seed.
	Meta map[string]string

	// Err is non-nil for transport/DNS/timeout failures (no HTTP response).
	Err error

	// Timing fields (zero if not captured).
	TTFB          time.Duration // time from request send to first byte of response
	FetchDuration time.Duration // total wall-clock from request start to body read done
	FinalURL      string        // URL after following redirects; equals URL if no redirect
	IP            string        // IP address of the server that responded
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
	// reachable is set once we have ever established a TCP connection to the
	// domain, even if that exchange then timed out or failed before a response.
	// A host we have connected to is by definition not dead, so it is never
	// skipped: this catches a host that accepts connections but is too slow to
	// answer within the request deadline, which a response timeout alone would
	// otherwise look like silence.
	reachable atomic.Bool
}

// New builds a Fetcher from config.
func New(cfg config.Config) *Fetcher {
	res := newResolver(cfg.DNSTimeout, cfg.DNSWorkers)
	f := &Fetcher{
		cfg:     cfg,
		res:     res,
		timeout: newAdaptiveTimeout(cfg.Timeout, cfg.TimeoutFloor),
		lim:     newLimiter(cfg.MinInflight, cfg.StartInflight, cfg.Workers),
		maxBody: cfg.MaxBodyBytes,
	}
	f.clients = make([]*http.Client, cfg.TransportShards)
	for i := range f.clients {
		f.clients[i] = &http.Client{
			Transport: f.newTransport(res),
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if cfg.MaxRedirects > 0 && len(via) >= cfg.MaxRedirects {
					return http.ErrUseLastResponse
				}
				if cfg.MaxRedirects == 0 {
					return http.ErrUseLastResponse
				}
				return nil
			},
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
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2:   false,
		DisableCompression:  true,
		MaxIdleConns:        0,
		MaxIdleConnsPerHost: 50,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: f.cfg.ProbeTimeout,
		// ResponseHeaderTimeout abandons a host that has connected but not sent
		// response headers in time, which is the dominant dead-host cost on a raw
		// crawl: a host that accepts the TCP connection then blackholes the request
		// pays only this instead of the full request deadline. A host already
		// streaming a body is unaffected, its headers having arrived. Zero leaves
		// it off, falling back to the request context deadline.
		ResponseHeaderTimeout: f.cfg.HeaderTimeout,
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
// should be skipped for the rest of the run. A domain that has ever answered or
// that we have ever connected to is never dead: a single success or a single
// established connection immunizes it, so a live-but-slow host is never skipped
// over a run of transient timeouts, and only failures that prove the host
// genuinely cannot be reached (its name does not resolve, or there is no route)
// ever count toward the threshold in the first place.
func (f *Fetcher) domainDead(domain string) bool {
	v, ok := f.deadDom.Load(domain)
	if !ok {
		return false
	}
	st := v.(*domainState)
	if st.alive.Load() || st.reachable.Load() {
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

// noteDomainReachable records that we established a TCP connection to a domain,
// immunizing it from the breaker even if the request that opened the connection
// then timed out. A host that accepts connections is reachable, not dead, so it
// must never be skipped.
func (f *Fetcher) noteDomainReachable(domain string) {
	v, _ := f.deadDom.LoadOrStore(domain, &domainState{})
	st := v.(*domainState)
	if st.reachable.CompareAndSwap(false, true) {
		st.fails.Store(0)
	}
}

// Stop releases the engine's blocking limiter so a cancelled run unwinds. It is
// safe to call once the run context is done.
func (f *Fetcher) Stop() { f.lim.close() }

// Limit reports the live in-flight concurrency limit, for progress display.
func (f *Fetcher) Limit() int { return f.lim.currentLimit() }

// attribute decides what a transport error means for the dead-domain breaker.
// Only a genuine failure (a name that does not resolve, or a host with no
// network route) counts toward a domain's death, because only those prove the
// host cannot be reached at all. A transient failure (a timeout, a reset, a
// refused connection, a TLS error) never counts: it is retried as errRetry
// while the link is congested, and otherwise recorded as an honest failure, so
// a live-but-slow, resetting, or backlog-refusing host is never skipped.
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

	// Take the per-host slot first, then the in-flight limiter slot. The order
	// matters: a raw shard arrives host-clustered, so many workers contend for a
	// single host's slots at once, and if the limiter slot were taken first every
	// one of those workers would hold a slice of the global in-flight budget while
	// parked on the host semaphore, doing nothing on the wire. The limiter would
	// then read the link as saturated when almost nothing is in flight, and the
	// effective concurrency would collapse far below the configured limit. By
	// gating on the host first, a limiter slot is only ever held by a request that
	// is actually about to open a connection, so inflight reflects real link load
	// and the AIMD controller climbs to the link's true capacity.
	release, ok := f.acquireHost(ctx, host)
	if !ok {
		r.Err = ctx.Err()
		if r.Err == nil {
			r.Err = errSkip
		}
		return r
	}

	// Gate on the adaptive in-flight limit before taking any connection, so a
	// thin uplink is never asked to open more sockets than it sustains. This is
	// what keeps the link out of the congestion collapse that would otherwise
	// time out live hosts and trip the breaker.
	if !f.lim.acquire(ctx) {
		release()
		r.Err = ctx.Err()
		if r.Err == nil {
			r.Err = errSkip
		}
		return r
	}

	to := f.timeout.value()
	reqCtx, cancel := context.WithTimeout(ctx, to)

	// Mark the domain reachable the moment we have a connection to the host
	// itself, so a host we can reach is never skipped even if the request that
	// opened the connection later times out. Both hooks are request-scoped: they
	// fire only for the connection the HTTP round trip uses, never for the UDP
	// sockets our pure-Go resolver opens to its DNS servers (those would
	// otherwise look like a successful connect to every domain, including ones
	// that do not resolve, and silently disable the dead-domain breaker).
	// GotConn covers a freshly dialed or pooled connection; TLSHandshakeStart
	// additionally covers an https host whose TCP connects but whose TLS
	// handshake is too slow to finish within the deadline.
	var ttfbOnce sync.Once
	var ttfbAt time.Time
	reqCtx = httptrace.WithClientTrace(reqCtx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) {
			f.noteDomainReachable(domain)
		},
		TLSHandshakeStart: func() {
			f.noteDomainReachable(domain)
		},
		GotFirstResponseByte: func() {
			ttfbOnce.Do(func() { ttfbAt = time.Now() })
		},
		ConnectDone: func(network, addr string, err error) {
			if err == nil && r.IP == "" {
				if host, _, herr := net.SplitHostPort(addr); herr == nil {
					r.IP = host
				}
			}
		},
	})

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, s.URL, nil)
	if err != nil {
		cancel()
		release()
		f.lim.release(outNeutral)
		r.Err = err
		return r
	}
	f.setHeaders(req)
	// Conditional re-fetch: when the seed carries validators from a prior
	// capture, ask the origin to answer 304 Not Modified if nothing changed.
	// On a recrawl of a stable seed this turns most responses into a bodiless
	// round trip, so throughput stops being bounded by egress bandwidth (the
	// body never crosses the link) and becomes bounded by request rate and
	// latency, which is the engine's own ceiling.
	if s.ETag != "" {
		req.Header.Set("If-None-Match", s.ETag)
	}
	if s.ModTime != "" {
		req.Header.Set("If-Modified-Since", s.ModTime)
	}
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
	r.FetchDuration = rtt
	if !ttfbAt.IsZero() {
		r.TTFB = ttfbAt.Sub(start)
	}
	r.FinalURL = resp.Request.URL.String()
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

	r.Status = resp.StatusCode
	r.Header = resp.Header
	// Carry the response validators forward so the next recrawl can issue a
	// conditional request for this URL. A 304 echoes them in most servers, but
	// not all, so fall back to the validators we sent.
	r.ETag = resp.Header.Get("ETag")
	if r.ETag == "" {
		r.ETag = s.ETag
	}
	r.LastModified = resp.Header.Get("Last-Modified")
	if r.LastModified == "" {
		r.LastModified = s.ModTime
	}
	if resp.StatusCode == http.StatusNotModified {
		// No body crossed the link; the content is the prior capture's, so its
		// digest carries forward unchanged.
		r.Unchanged = true
		r.Digest = s.Digest
		return r
	}
	sum := sha1.Sum(body)
	r.Body = body
	r.Digest = hex.EncodeToString(sum[:])
	r.Unchanged = s.Digest != "" && s.Digest == r.Digest
	return r
}

// SeedURL is the subset of a seed the fetcher needs, decoupling fetch from the
// seed package's concrete type.
//
// Digest, ETag, and ModTime are validators from a prior capture of this URL.
// Digest drives post-fetch content comparison; ETag and ModTime drive
// conditional requests (If-None-Match / If-Modified-Since) so an unchanged page
// comes back as a bodiless 304.
type SeedURL struct {
	URL     string
	Digest  string
	ETag    string
	ModTime string
	Meta    map[string]string
}
