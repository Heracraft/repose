//go:build !darwin

package app

// platformFor is the Linux host: nothing replaced.
func platformFor(Options) platform { return platform{} }
