// repose-hostd-update keeps repose current on a Mac (DECISIONS I-1496,
// I-1686): launchd runs it every 30 minutes as root
// (run.repose.hostd-update). It finds the newest hostd-<version> release
// with a manifest-darwin.json, checks the manifest's signature against
// the release key and the bundle against the manifest, unpacks it beside
// the running version, makes it current, restarts hostd (no machine
// stops) and rolls back if the new hostd does not answer. A changed
// repose-vm reaches running machines by a park and a restore, only in an
// unlocked session; otherwise at each machine's next start.
//
//	repose-hostd-update [--manifest-url URL] [--pubkey FILE] [--force]
//
// Log lines carry events and versions only.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/hostd/control"
	"github.com/heracraft/repose/internal/machost"
)

func main() {
	fs := flag.NewFlagSet("repose-hostd-update", flag.ExitOnError)
	manifest := fs.String("manifest-url", "", "a manifest to install from instead of the newest hostd release's")
	pubkey := fs.String("pubkey", "", "a PEM public key to check the manifest with instead of the release key (a lab's)")
	force := fs.Bool("force", false, "install even when the manifest is not newer than the running version")
	root := fs.String("root", machost.Root, "the install's root")
	_ = fs.Parse(os.Args[1:])

	logf := func(event string, kv ...any) {
		var b strings.Builder
		fmt.Fprintf(&b, "%s repose-hostd-update: event=%s", time.Now().UTC().Format(time.RFC3339), event)
		for i := 0; i+1 < len(kv); i += 2 {
			fmt.Fprintf(&b, " %v=%v", kv[i], kv[i+1])
		}
		fmt.Println(b.String())
	}
	p := machost.Paths{Root: *root}
	pub := ""
	if *pubkey != "" {
		b, err := os.ReadFile(*pubkey)
		if err != nil {
			logf("failed", "err", err.Error())
			os.Exit(1)
		}
		pub = string(b)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	cc := control.NewClient(p.ControlSock())
	u := &machost.Updater{
		Paths: p, ManifestURL: *manifest, PubKeyPEM: pub, Force: *force,
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		Healthy: func(ctx context.Context) error {
			_, err := cc.Status(ctx)
			return err
		},
		CycleLaunchers: func(ctx context.Context) (bool, string, error) {
			return cycleLaunchers(ctx, p, cc)
		},
		Log: logf,
	}
	if err := u.Run(ctx); err != nil {
		logf("failed", "err", err.Error())
		os.Exit(1)
	}
}

// cycleLaunchers parks every running machine and restores it, so each
// runs the new repose-vm (proposal 2.7: a 3.4 s gap, memory kept). A
// restore needs the Mac user's unlocked session (I-1662), so with the
// screen locked or nobody logged in nothing is parked.
func cycleLaunchers(ctx context.Context, p machost.Paths, cc *control.Client) (bool, string, error) {
	rec, err := readRecord(p)
	if err != nil {
		return false, "no install record", nil
	}
	if !unlocked(ctx, rec.UID) {
		return false, "session_locked", nil
	}
	if _, err := cc.Park(ctx, control.ParkOperatorReason); err != nil {
		return false, "", err
	}
	_, err = cc.Unpark(ctx)
	return err == nil, "", err
}

func readRecord(p machost.Paths) (machost.Record, error) {
	var r machost.Record
	b, err := os.ReadFile(p.Record())
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}
