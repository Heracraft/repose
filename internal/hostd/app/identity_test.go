package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/heracraft/repose/internal/hostd/lvm"
	"github.com/heracraft/repose/internal/hostd/register"
	"github.com/heracraft/repose/internal/hostd/shell"
	"github.com/heracraft/repose/internal/hostd/testca"
)

// waitHandler closes seen the first time the given message is logged.
type waitHandler struct {
	slog.Handler
	msg  string
	seen chan struct{}
	once sync.Once
}

func (h *waitHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.once.Do(func() { close(h.seen) })
	}
	return h.Handler.Handle(ctx, r)
}

// writeIdentity is what repose-register.service leaves behind: the
// certificate, its key and host.json, and no join token.
func writeIdentity(t *testing.T, dir string) {
	t.Helper()
	ca, err := testca.New()
	if err != nil {
		t.Fatal(err)
	}
	cert, key, err := ca.IssueClient("host-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	host, err := json.Marshal(register.HostJSON{HostID: "host-1", GuestCIDR: "10.64.4.0/22"})
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{register.CertFile: cert, register.KeyFile: key, register.HostFile: host} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// A hostd that started before the join token arrived must notice the
// identity repose-register.service wrote with it, without a restart
// (DECISIONS I-76): the unit consumes the token, so hostd's own attempts
// only ever see ErrNoToken after that.
func TestEnsureIdentityTakesTheIdentityTheUnitWrote(t *testing.T) {
	dir := t.TempDir()
	prev, prevFirst := registerRetry, registerFirstRetry
	registerRetry, registerFirstRetry = 10*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { registerRetry, registerFirstRetry = prev, prevFirst })

	h := &waitHandler{Handler: slog.NewTextHandler(io.Discard, nil), msg: "waiting for join token", seen: make(chan struct{})}
	o := Options{StateDir: dir, TokenPath: filepath.Join(dir, "join-token"), APIAddr: "127.0.0.1:1"}
	ids := make(chan *register.Identity, 1)
	errs := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	go func() {
		id, err := EnsureIdentity(ctx, o, slog.New(h), &shell.Fake{}, lvm.NewFake())
		if err != nil {
			errs <- err
			return
		}
		ids <- id
	}()

	select {
	case <-h.seen: // the loop has run at least once with neither identity nor token
	case err := <-errs:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("hostd never waited for a join token")
	}
	writeIdentity(t, dir)

	select {
	case id := <-ids:
		if id.Host.HostID != "host-1" || id.Host.GuestCIDR != "10.64.4.0/22" {
			t.Fatalf("identity %+v", id.Host)
		}
	case err := <-errs:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal("hostd did not pick up the identity repose-register wrote")
	}
}

// A host just added registers through an edge that peers its key within
// 30 s: the first tries are short and 2 s apart, doubling to the old 30 s
// wait and gRPC's 20 s connect timeout, never faster than one try every
// 2 s (d2-install's join of 43-84 s, 2026-11-27).
func TestRegisterBacksOffFromTwoSeconds(t *testing.T) {
	want := []struct{ try, wait time.Duration }{
		{3 * time.Second, 2 * time.Second},
		{6 * time.Second, 4 * time.Second},
		{12 * time.Second, 8 * time.Second},
		{20 * time.Second, 16 * time.Second},
		{20 * time.Second, 30 * time.Second},
		{20 * time.Second, 30 * time.Second},
	}
	for n, w := range want {
		if try, wait := registerBackoff(n); try != w.try || wait != w.wait {
			t.Fatalf("try %d: %s then %s, want %s then %s", n, try, wait, w.try, w.wait)
		}
	}
	if try, wait := registerBackoff(1000); try != registerMaxTry || wait != registerRetry {
		t.Fatalf("far out: %s %s", try, wait)
	}
}
