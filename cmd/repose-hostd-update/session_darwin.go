package main

import (
	"context"

	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/vzpark"
)

// unlocked is whether uid's session can restore a save file now.
func unlocked(ctx context.Context, uid int) bool {
	s, err := vzpark.ReadSession(ctx, shell.Exec{}, uid)
	return err == nil && s.Unlocked()
}
