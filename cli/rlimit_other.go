//go:build !unix

package cli

// raiseFDLimit is a no-op on platforms without a POSIX open-file limit to
// raise. It reports 0, meaning "unknown / not adjusted".
func raiseFDLimit() uint64 { return 0 }
