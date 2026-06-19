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
	// classGenuine is a failure the remote host is responsible for: it refused
	// the connection, reset it, or does not resolve. These count toward the
	// dead-domain threshold unconditionally.
	classGenuine failClass = iota
	// classTransient is a timeout, a reset, or a local resource exhaustion. It
	// counts toward the threshold only when the link is healthy; during a
	// timeout storm it is treated as our own congestion and retried.
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
		if dnsErr.IsTimeout {
			return classTransient
		}
		// No such host / NXDOMAIN: the name genuinely does not resolve.
		return classGenuine
	}

	// Local resource exhaustion from oversubscription: too many open files, or
	// ephemeral ports all in use. These are our fault, not the host's.
	if errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.EADDRNOTAVAIL) ||
		errors.Is(err, syscall.EAGAIN) {
		return classTransient
	}

	// Connection refused / reset / unreachable: the peer (or its network) said
	// no. Genuine, but under heavy local load a reset can be collateral, so the
	// engine still routes it through the congestion check before counting.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return classGenuine
	}

	// Anything else (TLS handshake errors, malformed responses, short reads):
	// attribute to the host, but only when the link is healthy.
	return classTransient
}
