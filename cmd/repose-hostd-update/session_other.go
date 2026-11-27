//go:build !darwin

package main

import "context"

// unlocked: off a Mac there is no session to restore in.
func unlocked(context.Context, int) bool { return false }
