package app

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/shell"
)

// How hostd restarts a Mac (I-1690): the Apple menu's restart in the
// user's session while someone is logged in (through launchctl asuser
// from a root hostd), shutdown when that cannot be asked or nobody is
// logged in, and an error for a user hostd with nobody logged in.
func TestRestartArgv(t *testing.T) {
	tries, err := restartArgv(true, true, 501, "dev")
	if err != nil || len(tries) != 2 || !slices.Equal(tries[0][:6], []string{"/bin/launchctl", "asuser", "501", "/usr/bin/sudo", "-u", "dev"}) ||
		tries[0][6] != "/usr/bin/osascript" || tries[1][0] != "/sbin/shutdown" {
		t.Fatalf("root, logged in: %v %v", tries, err)
	}
	if tries, _ := restartArgv(false, true, 501, "dev"); len(tries) != 1 || tries[0][0] != "/usr/bin/osascript" {
		t.Fatalf("user, logged in: %v", tries)
	}
	if tries, _ := restartArgv(true, false, 501, "dev"); len(tries) != 1 || tries[0][0] != "/sbin/shutdown" {
		t.Fatalf("root, nobody: %v", tries)
	}
	if _, err := restartArgv(false, false, 501, "dev"); err == nil {
		t.Fatal("a user hostd with nobody logged in cannot restart")
	}
}

type restartRunner struct {
	shell.Runner
	fail map[string]bool
	ran  []string
}

func (r *restartRunner) Run(_ context.Context, argv ...string) (shell.Result, error) {
	r.ran = append(r.ran, strings.Join(argv, " "))
	for k := range r.fail {
		if slices.Contains(argv, k) {
			return shell.Result{}, errors.New("refused")
		}
	}
	return shell.Result{}, nil
}

func TestRestartMacFallsBackToShutdown(t *testing.T) {
	r := &restartRunner{fail: map[string]bool{"/usr/bin/osascript": true}}
	if err := restartMac(context.Background(), r, true, true, 501, "dev"); err != nil {
		t.Fatal(err)
	}
	if len(r.ran) != 2 || !strings.HasPrefix(r.ran[1], "/sbin/shutdown") {
		t.Fatalf("ran %v", r.ran)
	}
	r = &restartRunner{fail: map[string]bool{"/usr/bin/osascript": true}}
	if err := restartMac(context.Background(), r, false, true, 501, "dev"); err == nil {
		t.Fatal("a user hostd has no shutdown to fall back on")
	}
}

type hangingRunner struct{ ran []string }

func (h *hangingRunner) Start(context.Context, io.Reader, ...string) (shell.Proc, error) {
	return nil, errors.New("not used")
}

func (h *hangingRunner) Run(ctx context.Context, argv ...string) (shell.Result, error) {
	h.ran = append(h.ran, argv[0])
	if slices.Contains(argv, "/usr/bin/osascript") {
		<-ctx.Done() // an Automation consent prompt nobody answers
		return shell.Result{}, ctx.Err()
	}
	return shell.Result{}, nil
}

// A System Events restart that never answers (the lab Mac, from root) is
// given up after politeTimeout and the Mac restarts with shutdown.
func TestRestartMacDoesNotWaitOnAConsentPrompt(t *testing.T) {
	old := politeTimeout
	politeTimeout = 50 * time.Millisecond
	t.Cleanup(func() { politeTimeout = old })
	h := &hangingRunner{}
	if err := restartMac(context.Background(), h, true, true, 501, "dev"); err != nil {
		t.Fatal(err)
	}
	if len(h.ran) != 2 || h.ran[1] != "/sbin/shutdown" {
		t.Fatalf("ran %v", h.ran)
	}
}
