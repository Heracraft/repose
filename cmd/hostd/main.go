// Command hostd runs on every repose host: it is the only program that
// touches tenants. See docs/workstreams/03-hostd.md for the design and
// docs/interfaces/grpc-hostd.md for the contract it implements.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/heracraft/repose/internal/hostd/app"
	"github.com/heracraft/repose/internal/hostd/control"
	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/hostinfo"
	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/register"
	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/state"
	"github.com/heracraft/repose/internal/hostd/storeview"
)

var version = "dev" // set by -ldflags at release

const usage = `usage: hostd [flags] [command]

commands:
  run            run the daemon (default)
  register       register with the join token and exit
  status         daemon status
  guests         list guests with unit states (--json for every field)
  snapshot-all   snapshot every running guest locally (--reason scheduled|manual)
  snapshot       alias: snapshot --all --reason <reason>
  drain          stop accepting placements
  undrain        accept placements again
  reconcile      re-read units and volumes; --rebuild rebuilds a lost state.db
  freeze         freeze every running guest to its encrypted image (--shutdown: from the shutdown unit)
  thaw           restore every frozen guest now
  state export   print the state as JSON (daemon running or not)
  state import   load a JSON export into an empty state.db (daemon stopped)
  audit-login    PAM hook: record an operator login
  version        print the version
`

func options(fs *flag.FlagSet) *app.Options {
	o := &app.Options{Version: version}
	fs.StringVar(&o.StateDir, "state", "/var/lib/repose/hostd", "state directory (cert, key, host.json, state.db)")
	fs.StringVar(&o.GuestsDir, "guests-dir", "/var/lib/repose/guests", "per-guest directories")
	fs.StringVar(&o.BuildsDir, "builds-dir", "/var/lib/repose/builds", "fragment build directories")
	fs.StringVar(&o.BaseDir, "base-dir", "/var/lib/repose/base", "platform checkouts by base_ref")
	fs.StringVar(&o.BaseRepoURL, "base-repo-url", "", "git URL to clone a missing base checkout from")
	fs.StringVar(&o.BaseSSHKey, "base-repo-ssh-key", "", "private key file for cloning --base-repo-url over SSH")
	fs.StringVar(&o.BuildUser, "build-user", "nixbuild", "unprivileged user fragment evaluation and builds run as (empty: hostd's own)")
	fs.StringVar(&o.GCRootsDir, "gcroots", "/nix/var/nix/gcroots/repose", "GC roots directory")
	fs.StringVar(&o.APIAddr, "api-addr", "api.repose.herakraft.co:443", "api gRPC address")
	fs.StringVar(&o.APIServerName, "api-server-name", "", "TLS server name when it differs from the address")
	fs.StringVar(&o.APICA, "api-ca", "", "PEM bundle to trust instead of system roots (hostdev)")
	fs.StringVar(&o.TokenPath, "join-token", "/run/repose/join-token", "one-shot join token")
	fs.StringVar(&o.MetricsAddr, "metrics-addr", "", "Prometheus listen address (default: WireGuard address:9101)")
	fs.StringVar(&o.ControlSock, "control-sock", "/run/repose/hostd.sock", "operator control socket")
	fs.BoolVar(&o.GuestdUnix, "guestd-unix", false, "reach guestd over <guest dir>/guestd.sock instead of vsock (dev)")
	fs.StringVar(&o.SnapshotDir, "snapshot-dir", "", "store snapshots under a directory instead of Blob")
	fs.StringVar(&o.BlobURL, "blob-url", "", "Azure Blob service URL")
	fs.StringVar(&o.BlobContainer, "blob-container", "repose-snapshots", "Azure Blob container")
	fs.StringVar(&o.BlobIdentity, "blob-identity", "", "managed identity client id (empty: default credential)")
	fs.StringVar(&o.StoreExport, "store-export", storeview.Dir, "what each guest's virtiofsd shares: "+storeview.Dir+" is a per-guest view of its own closure (DECISIONS I-463); /run/repose/store-export shares the whole store as before")
	fs.StringVar(&o.VirtiofsUser, "virtiofsd-user", "virtiofsd", "user virtiofsd runs as")
	fs.StringVar(&o.GuestUser, "guest-user", "hostd", "unprivileged user the guest@ (Cloud Hypervisor) units run as")
	fs.BoolVar(&o.ClaudeLoginShare, "claude-login-share", true, "share each user's Claude Code login into all their guests (DECISIONS I-278)")
	fs.StringVar(&o.VG, "vg", "vg-guests", "volume group")
	fs.StringVar(&o.Pool, "pool", "thin", "thin pool")
	fs.IntVar(&o.MaxOps, "max-ops", 8, "concurrent guest operations")
	fs.IntVar(&o.MaxBuilds, "max-builds", 2, "concurrent builds")
	fs.IntVar(&o.FailAtStep, "fail-at-step", 0, "inject a CreateGuest failure at this step (REPOSE_HOSTD_TESTING=1 only)")
	fs.BoolVar(&o.NoWG, "no-wg", false, "accepted for one release and ignored: hostd writes host.json only and the host renders wg0.conf from it (DECISIONS I-137)")
	fs.StringVar(&o.Substituters, "substituters", "", "nix substituters for builds, space separated (default cache.nixos.org; the host module adds the overlay cache)")
	fs.StringVar(&o.LogLevel, "log-level", "info", "log level: debug, info, notice, warn, error")
	fs.BoolVar(&o.Freeze.Enabled, "freeze", false, "freeze running guests to an encrypted image when the host shuts down, and restore them at the next start (DECISIONS I-652)")
	fs.StringVar(&o.Freeze.KeyVaultURL, "freeze-key-vault", "", "Key Vault URL holding the RSA key that wraps image keys")
	fs.StringVar(&o.Freeze.KeyName, "freeze-key-name", "", "name of that key")
	fs.StringVar(&o.Freeze.KeyFile, "freeze-key-file", "", "RSA private key (PEM) to wrap image keys with instead of Key Vault (staging, tests)")
	fs.DurationVar(&o.Freeze.Deadline, "freeze-deadline", 240*time.Second, "longest a freeze of all guests may take; with the write rate it is how much guest memory a host restart can save")
	fs.Uint64Var(&o.Freeze.WriteRateMBs, "freeze-write-rate", 100, "MB/s the snapshots are assumed to write guest memory at, until this freeze's first finished one shows the real rate; measure the disk the guest thin pool is on (a scratch thin volume, dd oflag=direct)")
	fs.DurationVar(&o.Freeze.RestoreTimeout, "freeze-restore-timeout", 180*time.Second, "longest one restore's surroundings may take at the next start; the image's own read is added from the write rate")
	fs.BoolVar(&o.Freeze.FreePageReporting, "free-page-reporting", false, "start guests with a balloon that returns freed pages to the host")
	fs.BoolVar(&o.Freeze.ScheduledEvents, "scheduled-events", false, "freeze guests on Azure Scheduled Events reboot notices and approve them")
	fs.StringVar(&o.Freeze.VMName, "vm-name", "", "this VM's name in Scheduled Events (default: hostname)")
	return o
}

func main() {
	fs := flag.NewFlagSet("hostd", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage); fs.PrintDefaults() }
	o := options(fs)
	// Flags may come before or after the command word. Global flags after
	// the command word (`hostd register --state ...`) are still global;
	// anything the global set does not know (`reconcile --rebuild`,
	// `snapshot-all --reason`) belongs to the subcommand.
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	global, sub := splitArgs(fs, args)
	if err := fs.Parse(global); err != nil {
		os.Exit(2)
	}
	if cmd == "run" && len(sub) > 0 && sub[0][0] != '-' {
		cmd, sub = sub[0], sub[1:]
	}
	os.Exit(dispatch(cmd, sub, o))
}

// splitArgs separates the flags fs defines (with their values) from
// everything else, which is the subcommand's to parse. It stops at "--".
func splitArgs(fs *flag.FlagSet, args []string) (global, sub []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			sub = append(sub, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			sub = append(sub, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		hasValue := false
		if eq := strings.Index(name, "="); eq >= 0 {
			name, hasValue = name[:eq], true
		}
		f := fs.Lookup(name)
		if f == nil {
			sub = append(sub, a)
			continue
		}
		global = append(global, a)
		if hasValue {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			global = append(global, args[i+1])
			i++
		}
	}
	return global, sub
}

func dispatch(cmd string, args []string, o *app.Options) int {
	log := app.Logger(o.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cc := control.NewClient(o.ControlSock)
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "hostd:", err)
		return 1
	}
	switch cmd {
	case "version":
		fmt.Println("hostd", version)
		return 0
	case "run":
		if err := app.Run(ctx, *o, log); err != nil {
			if errors.Is(err, register.ErrTokenUsed) {
				return register.ExitTokenUsed
			}
			if errors.Is(err, state.ErrLocked) {
				fmt.Fprintln(os.Stderr, "hostd already running")
				return 1
			}
			return fail(err)
		}
		return 0
	case "register":
		r := shell.Exec{}
		if _, err := app.EnsureIdentity(ctx, *o, log, r, &lvm.Real{VG: o.VG, Pool: o.Pool, R: r}); err != nil {
			if errors.Is(err, register.ErrTokenUsed) {
				return register.ExitTokenUsed
			}
			return fail(err)
		}
		return 0
	case "status":
		s, err := cc.Status(ctx)
		if err != nil {
			return fail(err)
		}
		return printJSON(s)
	case "guests":
		gfs := flag.NewFlagSet(cmd, flag.ExitOnError)
		asJSON := gfs.Bool("json", false, "print the guests as JSON, with each one's interrupted mark (DECISIONS I-645)")
		if err := gfs.Parse(args); err != nil {
			return 2
		}
		gs, err := cc.Guests(ctx)
		if err != nil {
			return fail(err)
		}
		if *asJSON {
			if gs == nil {
				gs = []guest.Status{}
			}
			return printJSON(gs)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "GUEST\tPROJECT\tCLASS\tSTATE\tIP\tUNIT\tVIRTIOFSD\tSTORE\tGUESTD\tREASON")
		for _, g := range gs {
			gd := "down"
			if g.GuestdOK {
				gd = "ok"
			}
			store := g.Store
			if store == "" {
				store = "-"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", g.GuestID, g.ProjectID, g.Class, g.State, g.IP, g.Unit, g.Virtiofsd, store, gd, g.Reason)
		}
		return failIf(tw.Flush())
	case "snapshot-all", "snapshot":
		sfs := flag.NewFlagSet(cmd, flag.ExitOnError)
		reason := sfs.String("reason", "manual", "scheduled|manual")
		all := sfs.Bool("all", cmd == "snapshot-all", "every running guest")
		if err := sfs.Parse(args); err != nil {
			return 2
		}
		if !*all {
			return fail(errors.New("snapshot needs --all; single-guest snapshots come from the api"))
		}
		ids, err := cc.SnapshotAll(ctx, *reason)
		if err != nil {
			return fail(err)
		}
		fmt.Printf("queued %d snapshot(s): %v\n", len(ids), ids)
		return 0
	case "drain":
		return failIf(cc.Drain(ctx, true))
	case "undrain":
		return failIf(cc.Drain(ctx, false))
	case "reconcile":
		rfs := flag.NewFlagSet(cmd, flag.ExitOnError)
		rebuild := rfs.Bool("rebuild", false, "rebuild a lost state.db from guest directories, volumes and units")
		fromAPI := rfs.Bool("from-api", false, "same as --rebuild; the api reconciles from the Hello that follows")
		if err := rfs.Parse(args); err != nil {
			return 2
		}
		ids, err := cc.Reconcile(ctx, *rebuild || *fromAPI)
		if err != nil {
			return fail(err)
		}
		if len(ids) > 0 {
			fmt.Printf("rebuilt %d guest(s): %v\n", len(ids), ids)
		}
		fmt.Println("reconciled")
		return 0
	case "state":
		if len(args) == 0 {
			return fail(errors.New("state export|import <file>"))
		}
		switch args[0] {
		case "export":
			if err := cc.ExportState(ctx, os.Stdout); err == nil {
				return 0
			} else if !errors.Is(err, control.ErrNotRunning) {
				return fail(err)
			}
			st, err := state.Open(filepath.Join(o.StateDir, "state.db"))
			if err != nil {
				return fail(err)
			}
			defer func() { _ = st.Close() }() // read only
			return failIf(st.Export(os.Stdout))
		case "import":
			if len(args) < 2 {
				return fail(errors.New("state import <file>"))
			}
			var r io.Reader = os.Stdin
			if args[1] != "-" {
				f, err := os.Open(args[1])
				if err != nil {
					return fail(err)
				}
				defer func() { _ = f.Close() }() // read only
				r = f
			}
			st, err := state.Open(filepath.Join(o.StateDir, "state.db"))
			if err != nil {
				return fail(err)
			}
			defer func() { _ = st.Close() }() // Import reported its own error
			return failIf(st.Import(r))
		}
		return fail(errors.New("state export|import <file>"))
	case "freeze", "thaw":
		var res []guest.FreezeResult
		var err error
		if cmd == "freeze" {
			ffs := flag.NewFlagSet(cmd, flag.ExitOnError)
			shutdown := ffs.Bool("shutdown", false, "the host is shutting down (repose-guest-freeze.service); quiet when the freeze is off")
			if err := ffs.Parse(args); err != nil {
				return 2
			}
			reason := guest.FreezeOperator
			if *shutdown {
				// The unit's ExecStop also runs when a switch restarts or
				// removes it; only a host on its way down freezes.
				if !systemStopping(ctx) {
					fmt.Println("the system is not shutting down; nothing frozen")
					return 0
				}
				reason = guest.FreezeShutdown
			}
			res, err = cc.Freeze(ctx, reason)
		} else {
			res, err = cc.Thaw(ctx)
		}
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "GUEST\tOUTCOME\tMS\tIMAGE\tREASON")
		for _, r := range res {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%s\n", r.GuestID, r.Outcome, r.Ms, r.ImageBytes, r.Reason)
		}
		return failIf(tw.Flush())
	case "audit-login":
		hostID := ""
		if id, err := register.Load(o.StateDir); err == nil {
			hostID = id.Host.HostID
		}
		app.AuditLogin(log, hostID, o.ControlSock)
		return 0
	case "info":
		r := shell.Exec{}
		return printJSON(hostinfo.Collect(ctx, r, &lvm.Real{VG: o.VG, Pool: o.Pool, R: r}))
	}
	fmt.Fprint(os.Stderr, usage)
	return 2
}

// systemStopping reports whether systemd is taking the system down.
func systemStopping(ctx context.Context) bool {
	out, _ := exec.CommandContext(ctx, "systemctl", "is-system-running").Output() // non-zero for every state but running
	return strings.TrimSpace(string(out)) == "stopping"
}

func printJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return failIf(enc.Encode(v))
}

func failIf(err error) int {
	if err != nil {
		fmt.Fprintln(os.Stderr, "hostd:", err)
		return 1
	}
	return 0
}
