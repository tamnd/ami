package fetch

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want failClass
	}{
		{"deadline", context.DeadlineExceeded, classTransient},
		{"canceled", context.Canceled, classCanceled},
		// Refused and reset are transient, not genuine: our own concurrency can
		// make a live host's accept backlog overflow (refused) or its load
		// balancer reset us, so neither proves the host is dead.
		{"refused", syscall.ECONNREFUSED, classTransient},
		{"reset", syscall.ECONNRESET, classTransient},
		// No route to host or network is a genuine, unmanufacturable verdict.
		{"unreachable", syscall.EHOSTUNREACH, classGenuine},
		{"net-unreachable", syscall.ENETUNREACH, classGenuine},
		{"too-many-files", syscall.EMFILE, classTransient},
		{"no-ports", syscall.EADDRNOTAVAIL, classTransient},
		{"nxdomain", &net.DNSError{Err: "no such host", IsNotFound: true}, classGenuine},
		{"dns-timeout", &net.DNSError{Err: "timeout", IsTimeout: true}, classTransient},
		{"dns-temporary", &net.DNSError{Err: "temp", IsTemporary: true}, classTransient},
		{"tls-garbage", errors.New("tls: handshake failure"), classTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); got != c.want {
				t.Fatalf("classify(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

// TestClassifyTimeoutError checks a net.Error that reports Timeout is transient.
func TestClassifyTimeoutError(t *testing.T) {
	to := &net.OpError{Op: "dial", Err: timeoutErr{}}
	if got := classify(to); got != classTransient {
		t.Fatalf("timeout OpError classified %d, want transient", got)
	}
	if !isTimeoutErr(to) {
		t.Fatal("isTimeoutErr should detect a net.Error timeout")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
