//go:build !darwin

package main

import (
	"flag"

	"github.com/heracraft/repose/internal/hostd/app"
)

// platformFlags adds no flags on Linux.
func platformFlags(*flag.FlagSet, *app.Options) {}
