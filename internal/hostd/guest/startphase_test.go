package guest

import (
	"os"
	"path/filepath"
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
