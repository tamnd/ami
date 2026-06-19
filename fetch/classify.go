package fetch

import (
	"context"
	"errors"
	"net"
	"syscall"
)

// errRetry marks a fetch that failed in a way the engine attributes to its own
// congestion rather than to the remote host: a timeout, a reset, or a local
// resource exhaustion seen while the link is in a timeout storm. The run loop
// retries such a fetch after a brief backoff instead of blaming the host, so an
// alive domain is never skipped because we briefly overwhelmed our own uplink.
var errRetry = retryError{}

type retryError struct{}

func (retryError) Error() string { return "retry: transient congestion" }

// IsRetry reports whether err is the sentinel retry error.
func IsRetry(err error) bool {
	_, ok := err.(retryError)
	return ok
}

// ErrCongested is recorded when a fetch still fails after exhausting its
// congestion retries. It is counted as a failure, not a skip, and it never
// trips the dead-domain breaker: the host may be fine, we simply could not get
// through within the retry budget.
var ErrCongested = errors.New("gave up after congestion retries")

// failClass labels why a request failed, so the breaker counts only failures it
// can attribute to the remote host.
type failClass int

const (
	// classGenuine is a failure that is unambiguous proof the host cannot be
	// reached at all: its name does not resolve (NXDOMAIN), or there is no
	// network route to it. Nothing we do under our own load can manufacture
	// these, so they count toward the dead-domain threshold. The set is
	// deliberately narrow: a timeout, a reset, and even a refused connection are
	// NOT here, because all three can be produced by our own concurrency against
	// a perfectly live host (a slow or saturated link, a load balancer resetting
	// under burst, a full accept backlog refusing the connection).
	classGenuine failClass = iota
	// classTransient is everything that does not prove the host is dead:
	// timeouts, resets, refusals, TLS errors, DNS hiccups, local resource
	// exhaustion. It never counts toward the threshold; during a timeout storm it
	// is retried, and otherwise it is recorded as an honest failure, but it never
	// skips a host. This is what guarantees a live-but-slow, resetting, or
	// backlog-refusing host is never falsely declared dead.
	classTransient
	// classCanceled is shutdown: the run context was cancelled. It is neither
	// counted nor retried.
	classCanceled
)

// classify maps a transport error to a failClass.
func classify(err error) failClass {
	if err == nil {
		return classGenuine
	}
	if errors.Is(err, context.Canceled) {
		return classCanceled
	}
	// A request deadline we set ourselves: ambiguous, treat as transient.
	if errors.Is(err, context.DeadlineExceeded) {
		return classTransient
	}

	if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
		return classTransient
	}

	if dnsErr, ok := errors.AsType[*net.DNSError](err); ok {
		// A DNS timeout or a temporary resolver failure is transient: under a
		// burst of tens of thousands of lookups our own resolver can stumble on a
		// name that resolves fine in isolation.
		if dnsErr.IsTimeout || dnsErr.IsTemporary {
			return classTransient
		}
		// No such host / NXDOMAIN: the name genuinely does not resolve. This is a
		// clean negative answer from DNS, not a load artifact.
		if dnsErr.IsNotFound {
			return classGenuine
		}
		// Any other DNS error (server misbehaving, malformed) is ambiguous under
		// load: do not condemn the host for it.
		return classTransient
	}

	// No route to the host or its network: a routing-layer verdict we cannot
	// manufacture by sending too many requests. Genuinely unreachable.
	if errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return classGenuine
	}

	// Everything else (refused connections, resets, TLS handshake errors,
	// malformed responses, short reads, local resource exhaustion) is transient:
	// each can be collateral from our own concurrency against a live host, so
	// none of it counts toward declaring the host dead.
	return classTransient
}
