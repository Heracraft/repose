//go:build !unix

package mcpreg

import "time"

// flockFile: repose-mcp runs in a Linux guest only; elsewhere sync runs
// unlocked.
func flockFile(string, time.Duration) (func(), error) { return func() {}, nil }
