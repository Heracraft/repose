package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSHSaying puts an ssh on PATH that prints line on stderr and exits
// 255, as ssh does when the gateway refuses with a banner.
func fakeSSHSaying(t *testing.T, line string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s\\n' " + shQuote(line) + " >&2\nexit 255\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// Review pass 2, P1-2: exec and attach to a machine that waits for its Mac
// to be unlocked spun for a minute and then said "did not answer ssh",
// while ssh had the answer in 0.23 s. The gateway's sentence is final.
func TestASavedMachineOnALockedMacAnswersAtOnce(t *testing.T) {
	fakeSSHSaying(t, "e2e-p2b is resting and wakes once you unlock e2e-d2lab; its memory is kept")
	t0 := time.Now()
	err := waitForSSH(context.Background(), sshTarget{Args: []string{"e2e-p2b.repose"}}, 20*time.Second, nil, nil)
	if took := time.Since(t0); took > 5*time.Second {
		t.Errorf("waited %s for an answer the gateway gave at once", took)
	}
	if err == nil || err.Error() != "e2e-p2b is resting and wakes once you unlock e2e-d2lab; its memory is kept." {
		t.Errorf("err = %v", err)
	}
}

// ... and exec to a machine whose Mac is offline ended with "Permission
// denied (publickey)": it says the Mac is offline and what to do.
func TestAnOfflineHostIsSaidPlainly(t *testing.T) {
	fakeSSHSaying(t, "e2e-p2a is on a host the control plane cannot reach right now; try again shortly")
	err := waitForSSH(context.Background(), sshTarget{Args: []string{"e2e-p2a.repose"}}, 20*time.Second, nil, nil)
	if err != errHostOffline {
		t.Fatalf("err = %v, want errHostOffline", err)
	}
	e := &Env{Client: newClient("http://127.0.0.1:1/v1", staticToken("tok"))}
	got := offlineError(context.Background(), e, &Project{Slug: "e2e-p2a", HostName: "e2e-d2lab"}).Error()
	if got != "Your host e2e-d2lab is offline.\nWake it or turn it on; e2e-p2a answers again once it is back." {
		t.Errorf("offline line %q", got)
	}
	if strings.Contains(got, "publickey") || strings.Contains(got, "control plane") {
		t.Errorf("internal words reached the user: %q", got)
	}
}

// The deadline's line names no internal noun.
func TestNotReadyAtTheDeadlineIsPlain(t *testing.T) {
	fakeSSHSaying(t, "environment is not accepting connections yet")
	err := waitForSSH(context.Background(), sshTarget{Args: []string{"todo-app.repose"}}, 1*time.Second, nil, nil)
	if err == nil || strings.Contains(err.Error(), "environment") || !strings.Contains(err.Error(), "did not answer in 1 s") {
		t.Errorf("err = %v", err)
	}
}
