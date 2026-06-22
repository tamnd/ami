//go:build unix

package cli

import "syscall"

// raiseFDLimit lifts the process's soft open-file limit to its hard ceiling.
// A crawl holds one socket per in-flight request plus DNS sockets, so the
// common 1024 soft default silently caps concurrency well below the worker
// count the operator asked for. Raising the soft limit to the hard limit (on a
// typical Linux box, 1048576) removes that hidden cap; it is best-effort, since
// a process without privilege to raise it should still run at the lower limit.
// It returns the soft limit now in effect.
func raiseFDLimit() uint64 {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return 0
	}
	if lim.Cur < lim.Max {
		lim.Cur = lim.Max
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			// Re-read whatever stuck.
			_ = syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim)
		}
	}
	return uint64(lim.Cur)
}
