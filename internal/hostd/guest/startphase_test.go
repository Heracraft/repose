package guest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/fakeguestd"
)

// A start that runs out of time in the registration phase still leaves a
// machine its owner can log in to, says which phase ran out, and carries
// the console.
//
// All three failed on the Mac appliance (ENG-80, 2026-11-19): nine starts,
// seven failures, and the ones that answered and then ran out of time gave
// the user "Permission denied (publickey)" afterwards and an empty
// `repose logs --kind console`. The cause of the first is this order. A
// guest's user CA and its principals file both live on a tmpfs and sshd
// recreates them empty on every boot (nix/guest/base/ssh.nix), so while
// the delivery sent them after the registration -- the slowest call of a
// start, 2 min 6 s there -- a start that ran out of time left a machine
// that trusted nobody.
func TestStartOutOfTimeKeepsSshAndNamesThePhase(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.PathsTimeout = 150 * time.Millisecond
	})
	// A closure whose paths the guest does not know yet, so the delivery
	// makes the RegisterPaths call at all.
	h.nix.Registration = []byte("1\n/nix/store/aaaa-nixos-system-v1\n")
	h.gopts.Delay = map[string]time.Duration{"RegisterPaths": 3 * time.Second}
	// A console this boot printed, which the failure must carry. The fake
	// guestd is held here too: the failed start tears the guest down, and
	// what it was sent before that is the point of the test.
	var fg *fakeguestd.Server
	prev := h.sd.OnRun
	h.sd.OnRun = func(unit string, argv []string) error {
		if err := prev(unit, argv); err != nil {
			return err
		}
		if id, ok := strings.CutPrefix(unit, "guest@"); ok {
			fg = h.guestd(id)
			_ = os.WriteFile(filepath.Join(h.cfg.GuestsDir, id, "console.log"),
				[]byte("[    0.000000] Linux version 6.18.57\n[    4.100000] systemd[1]: Reached target multi-user.target.\n"), 0o600)
		}
		return nil
	}

	req := createReq(gid1)
	req.SystemClosure = h.closure
	res := h.mustFail(cmd(req), CodeGuestUnresponsive)
	cerr := res.Error
	if !strings.Contains(cerr.Message, "RegisterPaths: ") || !strings.Contains(cerr.Message, "deadline exceeded") {
		t.Errorf("an api before I-1408 cannot read this message: %q", cerr.Message)
	}
	if !strings.Contains(cerr.Message, "the "+phasePaths+" phase ran out of its 150ms") {
		t.Errorf("the message does not name the phase and its limit: %q", cerr.Message)
	}
	if !strings.Contains(cerr.ConsoleTail, "multi-user.target") {
		t.Errorf("console not carried, so `repose logs --kind console` has nothing to print: %q", cerr.ConsoleTail)
	}

	// The ssh identity went first, so the machine trusts its owner even
	// though the start failed.
	if fg == nil {
		t.Fatal("no fake guestd was started")
	}
	sec := fg.Secrets()
	if string(sec[SecretUserCA]) == "" || string(sec[SecretHostKey]) == "" {
		t.Errorf("a start that ran out of time left the machine without its ssh identity: %v", sec)
	}
	if p := fg.Principals(); len(p) == 0 {
		t.Errorf("a start that ran out of time left the machine with no principals, so every login is refused")
	}
	kinds := strings.Join(fg.Kinds(), ",")
	if i, j := strings.Index(kinds, "SetPrincipals"), strings.Index(kinds, "RegisterPaths"); i < 0 || j < 0 || i > j {
		t.Errorf("the ssh identity is still sequenced behind the registration: %s", kinds)
	}
}

// A guest that is still booting is not a guest whose guestd was lost.
//
// The monitor marks a guest lost after GuestdLostAfter (60 s) of no vsock
// connection, counted from the moment the monitor starts, so on hardware
// where guestd's first answer comes later than that every healthy boot
// raised the warning and bumped repose_host_guestd_lost: on the Mac
// appliance guestd answers at 59.5 to 70 s (research §9, §10, I-1376).
// Before the first connection the boot's own ready window is what covers
// a guest that never comes, and the start's failure is what reports it
// (ENG-80, DECISIONS I-1408).
func TestABootingGuestIsNotALostGuestd(t *testing.T) {
	h := newHarness(t, func(c *Config) {
		c.GuestdLostAfter = 50 * time.Millisecond
		c.GuestdRetry = 20 * time.Millisecond
		c.GuestdBootRetry = 20 * time.Millisecond
		c.ReadyTimeout = 2 * time.Second
	})
	const id = gid1
	// A guest whose guestd takes longer to listen than the lost window,
	// as the Mac appliance's does.
	h.mu.Lock()
	h.noBoot[id] = true
	h.mu.Unlock()
	go func() {
		time.Sleep(400 * time.Millisecond)
		srv, err := fakeguestd.Listen(filepath.Join(h.sockDir, id+".sock"), h.gopts)
		if err != nil {
			t.Error(err)
			return
		}
		h.mu.Lock()
		h.guestds[id] = srv
		h.mu.Unlock()
	}()
	h.create(id)
	if g := h.guest(id); g.State != StateRunning {
		t.Fatalf("state %q, want running", g.State)
	}
	for _, w := range h.rec.warnings() {
		if w == "guestd_lost" {
			t.Errorf("a healthy boot whose guestd answered after the lost window raised guestd_lost")
		}
	}
	if strings.Contains(h.logs.String(), "guestd_lost") {
		t.Errorf("a healthy boot logged guestd_lost")
	}
}

// The guest's wait for the registration must outlast the host's deadline
// for sending it, and nothing could check that: `repose.guestd.pathsWait`
// is an option of the guest closure and `--paths-timeout` is host
// configuration, so neither side's evaluation sees the other and no Go
// package holds either number (ENG-124, DECISIONS I-1416).
//
// The failure it prevents is the worst shape found on 2026-11-20, because
// it is silent: if the guest gives up first, `repose-paths.service` prints
// "continuing without it" and home-manager activates against a store the
// guest's nix database does not know, so the start reports success into a
// broken machine. I-1408 left both numbers at 600 s, which is not
// outlasting, and the guest's clock starts first -- at `guestd.service`,
// where the host's starts later at RegisterPaths after Ready -- so the
// guest expired first by the whole ready phase.
//
// This reads the two nix files as text. It is not an evaluation and
// cannot be: the check has to see both numbers at once, and the only
// place that does is a flake evaluation, which does not fit the dev box
// (8.5 to 12 GB against 9 free). A text check catches the regression that
// matters, which is someone changing one number and not the other.
func TestTheGuestsPathsWaitOutlastsTheHostsPathsDeadline(t *testing.T) {
	root := repoRoot(t)
	guestWait := nixSeconds(t, filepath.Join(root, "nix/guest/base/guestd.nix"),
		`options.repose.guestd.pathsWait`, `default = (\d+);`)
	hostPaths := nixDuration(t, filepath.Join(root, "nix/hosts/mac.nix"),
		`guestPathsTimeout = lib.mkDefault "([^"]+)"`)
	hostReady := nixDuration(t, filepath.Join(root, "nix/hosts/mac.nix"),
		`guestReadyTimeout = lib.mkDefault "([^"]+)"`)

	// The guest starts waiting at guestd.service and the host starts its
	// paths clock after Ready, so the guest burns the ready phase first.
	// Ready is bounded by the host's own ready window, so that is the
	// head start the wait has to cover on top of the paths deadline.
	need := hostPaths + hostReady
	if guestWait <= need {
		t.Errorf("repose.guestd.pathsWait is %s, which does not outlast the host's %s paths deadline plus the %s ready window it waits through first (%s): "+
			"the guest gives up before the host does, prints \"continuing without it\", and home-manager activates against an unregistered store",
			guestWait, hostPaths, hostReady, need)
	}
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("no go.mod above the test's directory")
	return ""
}

// nixSeconds is a bare integer of seconds in a nix file, found after near.
func nixSeconds(t *testing.T, path, near, pat string) time.Duration {
	t.Helper()
	n := nixMatch(t, path, near, pat)
	d, err := time.ParseDuration(n + "s")
	if err != nil {
		t.Fatalf("%s: %q is not a number of seconds: %v", path, n, err)
	}
	return d
}

// nixDuration is a Go duration string in a nix file ("10m").
func nixDuration(t *testing.T, path, pat string) time.Duration {
	t.Helper()
	s := nixMatch(t, path, "", pat)
	d, err := time.ParseDuration(s)
	if err != nil {
		t.Fatalf("%s: %q is not a Go duration: %v", path, s, err)
	}
	return d
}

// nixMatch is pat's one capture in path, searched after near when near is
// set. A miss fails loudly: the file changed shape and the invariant is
// unchecked again, which is the state ENG-124 was found in.
func nixMatch(t *testing.T, path, near, pat string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if near != "" {
		i := strings.Index(s, near)
		if i < 0 {
			t.Fatalf("%s no longer contains %q, so this check is blind; find where the option moved", path, near)
		}
		s = s[i:]
	}
	m := regexp.MustCompile(pat).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%s no longer matches %q, so this check is blind; find where the number moved", path, pat)
	}
	return m[1]
}
