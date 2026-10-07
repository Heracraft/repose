package gateway

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// dialLikeOpenSSH offers the certificate, then the plain key, on one
// connection, as `ssh kanali.repose` does, and returns the banners.
func (h *harness) dialLikeOpenSSH(t *testing.T, login string, cert, key ssh.Signer) string {
	t.Helper()
	var banners strings.Builder
	cfg := &ssh.ClientConfig{
		User:            login,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(cert, key)},
		HostKeyCallback: h.hostKeyCallback(),
		BannerCallback: func(m string) error {
			banners.WriteString(m)
			return nil
		},
		Timeout: 5 * time.Second,
	}
	c, err := ssh.Dial("tcp", h.addr, cfg)
	if err == nil {
		_ = c.Close()
		t.Fatal("connected")
	}
	return strings.TrimSpace(banners.String())
}

// I-599, the dev box of 2026-10-07 20:12Z..20:36Z: workers ran `ssh
// kanali.repose` against their own stopped project, every attempt counted
// as a failed handshake, and after 20 the gateway banned the address for
// 10 minutes ("too many authentication attempts from your address"),
// locking it out of every project, with nothing logged. A user's own
// current certificate refused for its project (stopped, a principal from
// before an rm and restore, a project that is gone) never counts towards
// the ban, and each refusal is logged with its real reason.
func TestOwnCertificateRefusalsNeverBanTheSource(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	cert := h.userCert(t, key, []string{h.project.ID}, time.Hour)
	stale := h.userCert(t, key, []string{"01a0ef90-0000-7000-8000-000000000000"}, time.Hour)
	h.api.SetState(h.project.ID, "stopped")
	stopped := fmt.Sprintf(MsgStoppedFmt, h.project.Slug, h.project.Slug)
	for i := 0; i < banFailures+5; i++ {
		if b := h.dialLikeOpenSSH(t, h.login, cert, key); b != stopped {
			t.Fatalf("attempt %d against the stopped project: banner %q, want %q", i+1, b, stopped)
		}
	}
	for i := 0; i < banFailures+5; i++ {
		if b := h.dialLikeOpenSSH(t, h.login, stale, key); b != MsgWrongPrincipal {
			t.Fatalf("attempt %d with the certificate from before the restore: banner %q", i+1, b)
		}
	}
	missing := "gone." + strings.SplitN(h.login, ".", 2)[1]
	for i := 0; i < banFailures+5; i++ {
		if b := h.dialLikeOpenSSH(t, missing, cert, key); b != fmt.Sprintf(MsgNotFoundFmt, missing) {
			t.Fatalf("attempt %d at a project that is gone: banner %q", i+1, b)
		}
	}
	if b := h.dialLikeOpenSSH(t, h.login, cert, key); b != stopped {
		t.Fatalf("after %d refusals the source is refused: %q", 3*(banFailures+5), b)
	}
	logs := h.logs.String()
	if strings.Contains(logs, `"auth_ban"`) {
		t.Fatal("a ban for the user's own refusals")
	}
	if !strings.Contains(logs, `"reason":"stopped"`) || !strings.Contains(logs, `"counted":false`) {
		t.Fatalf("the refusal is not logged with its reason:\n%s", logs)
	}
}

// The scanner protection stays: 20 connections with no certificate of the
// user ban the source, the ban is logged with the source's prefix alone,
// and the next connection gets the one-line refusal.
func TestFailuresStillBanAndTheBanIsLogged(t *testing.T) {
	h := newHarness(t, harnessOpts{})
	_, key := genKey(t)
	for i := 0; i < banFailures; i++ {
		if _, _, err := h.dial(h.login, key); err == nil {
			t.Fatal("a plain key connected")
		}
	}
	if line := firstLine(t, "", h.addr); line != "repose gateway: "+MsgRateLimited {
		t.Fatalf("after %d failures: first line %q", banFailures, line)
	}
	var ban, counted string
	for _, l := range strings.Split(h.logs.String(), "\n") {
		if strings.Contains(l, `"auth_ban"`) {
			ban = l
		}
		if strings.Contains(l, `"auth_fail"`) && strings.Contains(l, `"counted":true`) {
			counted = l
		}
	}
	if ban == "" || !strings.Contains(ban, `"source_prefix":"127.0.0.0/24"`) || !strings.Contains(ban, `"failures":20`) {
		t.Fatalf("no auth_ban line with the prefix: %q", ban)
	}
	if counted == "" || !strings.Contains(counted, `"reason":"no_cert"`) {
		t.Fatalf("counted failures not logged: %q", counted)
	}
	for _, l := range []string{ban, counted} {
		if strings.Contains(l, "127.0.0.1") {
			t.Fatalf("a source address in the log: %s", l)
		}
	}
}
