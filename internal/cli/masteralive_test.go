//go:build !windows

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSSHMaster puts an ssh on PATH whose master answers `-O check` and
// whose sessions run `true` at once (healthy) or hang (a master whose
// connection died while the laptop slept). It records `-O stop`.
func fakeSSHMaster(t *testing.T, healthy bool) (stopped string) {
	t.Helper()
	bin := t.TempDir()
	stopped = filepath.Join(bin, "stopped")
	session := "exec sleep 30"
	if healthy {
		session = "exit 0"
	}
	script := `#!/bin/sh
if [ "$1" = -O ]; then
  [ "$2" = stop ] && : > ` + stopped + `
  exit 0
fi
` + session + "\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stopped
}

func TestMasterAliveHealthy(t *testing.T) {
	stopped := fakeSSHMaster(t, true)
	if !masterAlive(context.Background(), hostTarget("demo")) {
		t.Fatal("a master that runs `true` was reported down")
	}
	if _, err := os.Stat(stopped); err == nil {
		t.Fatal("a healthy master was stopped")
	}
}

// I-491: a master that answers its socket but not over the network is
// reported down within masterProbeTimeout and told to stop, so the attach
// takes the cold path instead of hanging after "Connected".
func TestMasterAliveDeadConnection(t *testing.T) {
	stopped := fakeSSHMaster(t, false)
	started := time.Now()
	if masterAlive(context.Background(), hostTarget("demo")) {
		t.Fatal("a master whose sessions hang was reported up")
	}
	if d := time.Since(started); d > masterProbeTimeout+2*time.Second {
		t.Fatalf("masterAlive took %s", d)
	}
	if _, err := os.Stat(stopped); err != nil {
		t.Fatal("the dead master was not stopped")
	}
}
