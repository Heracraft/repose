//go:build windows

package cli

import "context"

// lockFile is a no-op on Windows, which the CLI does not support.
func lockFile(string) (func(), error) { return func() {}, nil }

// lockFileCtx is lockFile.
func lockFileCtx(context.Context, string) (func(), error) { return func() {}, nil }
