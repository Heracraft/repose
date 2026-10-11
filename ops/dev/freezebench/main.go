// Command freezebench talks to a guest's guestd through Cloud Hypervisor's
// vsock socket, for the guest freeze experiments (docs/research/guest-freeze.md,
// DECISIONS I-652). It stands in for the parts of hostd's boot and delivery
// the experiments need, and nothing else.
//
//	freezebench wait   DIR TIMEOUT        ping until guestd answers; prints ms
//	freezebench ping   DIR                prints guestd's version, uptime, boot id
//	freezebench register DIR FILE      RegisterPaths with a nix-store --dump-db listing
//	freezebench setup  DIR SLUG           SetupProject (starts the tmux session)
//	freezebench exec   DIR USER ARGV...   runs ARGV in the guest; prints stdout
//	freezebench settime DIR               sets the guest clock to the host's
//	freezebench hostd-freeze DIR CLASS KEYPEM RECDIR VG
//	                                      freezes the guest with hostd's own code
//	                                      (internal/hostd/freeze, ch.Freezer) onto a
//	                                      thin volume of VG; the caller then removes
//	                                      virtiofsd and the tap
//	freezebench hostd-thaw DIR KEYPEM RECDIR VG
//	                                      restores it the same way; the caller has
//	                                      brought back virtiofsd and the tap
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	guestdv1 "github.com/heracraft/repose/internal/gen/guestd/v1"
	"github.com/heracraft/repose/internal/hostd/vsockclient"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: freezebench wait|ping|setup|exec|settime DIR ...")
		os.Exit(64)
	}
	if err := run(context.Background(), os.Args[1], os.Args[2], os.Args[3:]); err != nil {
		fmt.Fprintln(os.Stderr, "freezebench:", err)
		os.Exit(1)
	}
}

func dial(ctx context.Context, dir string) (vsockclient.Session, error) {
	return vsockclient.CHDialer{}.Dial(ctx, vsockclient.Target{Dir: dir})
}

func run(ctx context.Context, cmd, dir string, args []string) error {
	switch cmd {
	case "wait":
		d := 60 * time.Second
		if len(args) > 0 {
			v, err := time.ParseDuration(args[0])
			if err != nil {
				return err
			}
			d = v
		}
		start := time.Now()
		for time.Since(start) < d {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			s, err := dial(cctx, dir)
			if err == nil {
				_, err = s.Ping(cctx)
				_ = s.Close() // probe connection only
			}
			cancel()
			if err == nil {
				fmt.Println(time.Since(start).Milliseconds())
				return nil
			}
			time.Sleep(20 * time.Millisecond)
		}
		return fmt.Errorf("guestd did not answer within %s", d)
	case "ping":
		s, err := dial(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }() // read-only probe
		p, err := s.Ping(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("version=%s uptime_s=%d boot_id=%s\n", p.Version, p.UptimeS, p.BootId)
		return nil
	case "register":
		if len(args) < 1 {
			return fmt.Errorf("register needs a nix-store --dump-db file")
		}
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		s, err := dial(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }() // one request
		return s.RegisterPaths(ctx, b)
	case "setup":
		if len(args) < 1 {
			return fmt.Errorf("setup needs a slug")
		}
		s, err := dial(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }() // one request
		return s.SetupProject(ctx, &guestdv1.SetupProject{ProjectSlug: args[0], Tz: "UTC"})
	case "exec":
		if len(args) < 2 {
			return fmt.Errorf("exec needs a user and argv")
		}
		s, err := dial(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }() // one request
		user := args[0]
		if user == "root" {
			user = ""
		}
		r, err := s.Exec(ctx, args[1:], 120, user)
		if err != nil {
			return err
		}
		_, _ = os.Stdout.Write(r.Stdout) // a closed stdout has no one to tell
		_, _ = os.Stderr.Write(r.Stderr) // same
		if r.ExitCode != 0 {
			os.Exit(int(r.ExitCode))
		}
		return nil
	case "settime":
		s, err := dial(ctx, dir)
		if err != nil {
			return err
		}
		defer func() { _ = s.Close() }() // one request
		now := time.Now()
		r, err := s.Exec(ctx, []string{"date", "-u", "-s", "@" + strconv.FormatFloat(float64(now.UnixNano())/1e9, 'f', 6, 64)}, 10, "")
		if err != nil {
			return err
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("date -s: exit %d: %s", r.ExitCode, r.Stderr)
		}
		return nil
	}
	case "hostd-freeze":
		if len(args) < 4 {
			return fmt.Errorf("hostd-freeze DIR CLASS KEYPEM RECDIR VG")
		}
		return hostdFreeze(ctx, dir, args[0], args[1], args[2], args[3])
	case "hostd-thaw":
		if len(args) < 3 {
			return fmt.Errorf("hostd-thaw DIR KEYPEM RECDIR VG")
		}
		return hostdThaw(ctx, dir, args[0], args[1], args[2])
	}
	return fmt.Errorf("unknown command %q", cmd)
}
