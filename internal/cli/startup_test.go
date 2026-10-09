package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// countSSH runs fn with REPOSE_TIMING's output captured and returns how
// many ssh commands it ran.
func countSSH(t *testing.T, fn func()) int {
	t.Helper()
	var buf bytes.Buffer
	prev := timingOut
	timingOut = &buf
	defer func() { timingOut = prev }()
	fn()
	return len(regexp.MustCompile(`(?m)^repose-timing \+\d+ms ssh `).FindAllString(buf.String(), -1))
}

func laptopHomeWithGH(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	gh := filepath.Join(home, ".config", "gh", "hosts.yml")
	if err := os.MkdirAll(filepath.Dir(gh), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gh, []byte("github.com:\n    oauth_token: gho_test\n    user: dev\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// I-224: the tool logins and the carry run first in the apply's ssh, so a
// sync is two ssh commands (probe, apply), not three, and lands the same
// files and the same outcome as their own ssh did.
func TestSyncRunsTheCarryInTheApply(t *testing.T) {
	f := newSyncFixture(t)
	home := laptopHomeWithGH(t)
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var s *SyncSummary
	n := countSSH(t, func() {
		var err error
		s, err = syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
			Carry: func(markers map[string]string) (*credCarry, error) {
				return buildCredentialsAndCarry(home, f.local, credSyncOptions{RemoteURL: "gitlab.com/a/b"}, carryOptions{TZ: "Asia/Tokyo", Markers: markers})
			},
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	if n != 2 {
		t.Fatalf("ssh commands = %d, want 2 (probe, apply)", n)
	}
	if got := strings.Join(s.Copied, ","); got != "gh,git" {
		t.Fatalf("copied = %q, want gh,git", got)
	}
	if s.Carried == nil || strings.Join(s.Carried.Sent, ",") != "tz" || len(s.Carried.Failed) != 0 {
		t.Fatalf("carried = %+v", s.Carried)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); !strings.Contains(string(b), "gho_test") {
		t.Fatalf("guest hosts.yml = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "laptop edit\n" {
		t.Fatalf("guest README.md = %q", b)
	}
	// The identity was in place before the apply's git steps.
	out, err := runSSH(context.Background(), f.target, "git config --global user.email", nil)
	if err != nil || strings.TrimSpace(string(out)) != "dev@example.com" {
		t.Fatalf("guest identity = %q, %v", out, err)
	}
}

// A login the guest holds newer is kept and named, as before (the reply
// line travels inside the apply's reply).
func TestSyncCarryInTheApplyKeepsNewerGuestLogin(t *testing.T) {
	f := newSyncFixture(t)
	home := laptopHomeWithGH(t)
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(home, ".config", "gh", "hosts.yml"), old, old)
	guestGH := filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")
	_ = os.MkdirAll(filepath.Dir(guestGH), 0o700)
	if err := os.WriteFile(guestGH, []byte("guest login\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var kept []string
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
		Carry: func(markers map[string]string) (*credCarry, error) {
			return buildCredentialsAndCarry(home, f.local, credSyncOptions{Kept: func(l string) { kept = append(kept, l) }}, carryOptions{Markers: markers})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(kept, ",") != "gh" || strings.Join(s.Copied, ",") != "git" {
		t.Fatalf("kept = %v, copied = %v", kept, s.Copied)
	}
	if b, _ := os.ReadFile(guestGH); string(b) != "guest login\n" {
		t.Fatalf("guest hosts.yml overwritten: %q", b)
	}
}

// A login that cannot be written stops the run before the checkout is
// touched, with the step it used to fail at.
func TestSyncCarryInTheApplyFailureStopsBeforeTheCheckout(t *testing.T) {
	f := newSyncFixture(t)
	home := laptopHomeWithGH(t)
	// A stale ~/.gitconfig.lock in the guest: the identity's `git config
	// --global`, the carry script's last line, fails under its set -e (a
	// failure inside an `a && b` list never stopped it).
	if err := os.WriteFile(filepath.Join(f.guestHome, ".gitconfig.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
		Carry: func(markers map[string]string) (*credCarry, error) {
			return buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers})
		},
	})
	if err == nil || !strings.Contains(err.Error(), "Could not copy your tool logins to the machine") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) != "hello\n" {
		t.Fatalf("the checkout was touched: %q", b)
	}
}

// Nothing to carry and no login on the laptop: the apply has no carry
// lines and the outcome is empty, not nil.
func TestSyncCarryEmpty(t *testing.T) {
	f := newSyncFixture(t)
	f2 := t.TempDir()
	mustRun(t, f2, "git", "init", "-q")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{
		Carry: func(markers map[string]string) (*credCarry, error) {
			return buildCredentialsAndCarry(t.TempDir(), f2, credSyncOptions{}, carryOptions{Markers: markers})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Carried == nil || len(s.Copied) != 0 {
		t.Fatalf("copied = %v, carried = %+v", s.Copied, s.Carried)
	}
}

func TestHostBlockHandle(t *testing.T) {
	cfg := renderSSHConfig([]Project{{Slug: "a-b"}, {Slug: "a"}}, "user-x1", "~", true)
	for slug, want := range map[string]string{"a": "user-x1", "a-b": "user-x1", "b": ""} {
		got, ok := hostBlockHandle(cfg, slug)
		if got != want || ok != (want != "") {
			t.Errorf("%s: %q %v, want %q", slug, got, ok, want)
		}
	}
}

// The fast path's test of the files on disk: the certificate must be for
// the CLI's key, carry the project's id and have the reuse margin left;
// known_hosts and the Host block must be there.
func TestSSHFilesCover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sd, err := sshDir()
	if err != nil {
		t.Fatal(err)
	}
	p := &Project{ID: "01a0cfac-8500-75e2-b8ee-2a60cce7b7aa", Slug: "todo"}
	pub, err := ensureReposeKey()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	writeCert := func(principals []string, until time.Time) {
		t.Helper()
		line := testCertLine(t, pub, principals, until)
		if err := os.WriteFile(filepath.Join(sd, reposeKeyName+"-cert.pub"), []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCert([]string{p.ID}, now.Add(20*time.Hour))
	if _, ok := sshFilesCover(sd, p, now); ok {
		t.Fatal("covered without known_hosts or config")
	}
	_ = os.WriteFile(filepath.Join(sd, "known_hosts"), []byte("x\n"), 0o600)
	_ = os.WriteFile(filepath.Join(sd, sshHostsName), []byte(renderSSHConfig([]Project{*p}, "user-x1", "~", true)), 0o600)
	if _, ok := sshFilesCover(sd, p, now); ok {
		t.Fatal("covered without ~/.ssh/repose/config (a laptop before I-281 has only the blocks there)")
	}
	if err := writeSSHEntry(sd); err != nil {
		t.Fatal(err)
	}
	if h, ok := sshFilesCover(sd, p, now); !ok || h != "user-x1" {
		t.Fatalf("not covered: %q %v", h, ok)
	}
	writeCert([]string{"another-project"}, now.Add(20*time.Hour))
	if _, ok := sshFilesCover(sd, p, now); ok {
		t.Fatal("covered by a certificate without the project")
	}
	writeCert([]string{p.ID}, now.Add(certReuseMargin-time.Minute))
	if _, ok := sshFilesCover(sd, p, now); ok {
		t.Fatal("covered by a certificate inside the reuse margin")
	}
	writeCert([]string{p.ID}, now.Add(20*time.Hour))
	if _, ok := sshFilesCover(sd, &Project{ID: p.ID, Slug: "renamed"}, now); ok {
		t.Fatal("covered without a Host block for the slug")
	}
	// A config from a CLI before I-247 still forwards the agent: not
	// covered, so the slow path rewrites it.
	old := strings.Replace(renderSSHConfig([]Project{*p}, "user-x1", "~", true), "ForwardAgent no", "ForwardAgent yes", 1)
	_ = os.WriteFile(filepath.Join(sd, sshHostsName), []byte(old), 0o600)
	if _, ok := sshFilesCover(sd, p, now); ok {
		t.Fatal("covered by a config that still has ForwardAgent yes")
	}
}

// testCertLine signs pub as a user certificate for principals, valid
// until until, with a throwaway CA.
func testCertLine(t *testing.T, pub ssh.PublicKey, principals []string, until time.Time) string {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert := &ssh.Certificate{Key: pub, CertType: ssh.UserCert, ValidPrincipals: principals,
		ValidAfter: uint64(time.Now().Add(-time.Minute).Unix()), ValidBefore: uint64(until.Unix())}
	if err := cert.SignCert(rand.Reader, signer); err != nil {
		t.Fatal(err)
	}
	return string(ssh.MarshalAuthorizedKey(cert))
}

// I-224: a sync whose laptop side is exactly the last one's, into a
// guest that is exactly as that sync left it, applies nothing: the probe
// is the only ssh, dirty tree or clean.
func TestUnchangedSyncSkipsTheApply(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		f := newSyncFixture(t)
		ctx := context.Background()
		if dirty {
			if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(f.local, "new.txt"), []byte("new\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil || s.Unchanged {
			t.Fatalf("dirty=%v first sync: %+v %v", dirty, s, err)
		}
		var s *SyncSummary
		n := countSSH(t, func() {
			var err error
			if s, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
				t.Fatal(err)
			}
		})
		if !s.Unchanged || n != 1 || !strings.Contains(s.String(), "the machine already had them") {
			t.Fatalf("dirty=%v second sync: unchanged=%v, %d ssh, %q", dirty, s.Unchanged, n, s.String())
		}
		if dirty {
			if list := mustRun(t, f.guestRepo(), "git", "stash", "list"); list != "" {
				t.Fatalf("an unchanged sync stashed: %q", list)
			}
		}
	}
}

// Anything new on the laptop since the last sync makes the apply run: a
// laptop edit, a commit, or a guest that lost the last sync's key. The
// guest moving on by itself (its branch) with nothing new on the laptop
// is left alone (I-248, TestSyncLeavesTheGuestAloneWhenTheLaptopHasNothingNew).
func TestChangedSyncApplies(t *testing.T) {
	ctx := context.Background()
	for name, change := range map[string]func(f *syncFixture){
		"laptop edit": func(f *syncFixture) {
			_ = os.WriteFile(filepath.Join(f.local, "README.md"), []byte("edited\n"), 0o644)
		},
		"laptop commit": func(f *syncFixture) {
			_ = os.WriteFile(filepath.Join(f.local, "c.txt"), []byte("c\n"), 0o644)
			mustRun(t, f.local, "git", "add", "c.txt")
			mustRun(t, f.local, "git", "commit", "-q", "-m", "c")
		},
		"guest key gone": func(f *syncFixture) {
			_ = os.Remove(filepath.Join(f.guestRepo(), ".git", "repose-synced-key"))
		},
	} {
		f := newSyncFixture(t)
		if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
			t.Fatal(err)
		}
		change(f)
		s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if s.Unchanged {
			t.Fatalf("%s: the apply was skipped", name)
		}
	}
	// An agent's edit after the sync is not the sync's: with a laptop
	// edit to lay over it, refused, as before.
	f := newSyncFixture(t)
	_ = os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop\n"), 0o644)
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(f.guestRepo(), "README.md"), []byte("agent\n"), 0o644)
	_ = os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop, later\n"), 0o644)
	_, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{})
	wantDirtyRefusal(t, err)
}

// With the apply skipped, the carry still goes when it has something,
// alone, in the one ssh after the probe.
func TestUnchangedSyncStillCarries(t *testing.T) {
	f := newSyncFixture(t)
	home := laptopHomeWithGH(t)
	ctx := context.Background()
	carry := func(markers map[string]string) (*credCarry, error) {
		return buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers})
	}
	if _, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Carry: carry}); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml"))
	var s *SyncSummary
	n := countSSH(t, func() {
		var err error
		if s, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Carry: carry}); err != nil {
			t.Fatal(err)
		}
	})
	if !s.Unchanged || n != 2 || strings.Join(s.Copied, ",") != "gh,git" {
		t.Fatalf("unchanged=%v, %d ssh, copied %v", s.Unchanged, n, s.Copied)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); err != nil {
		t.Fatalf("login not carried: %v", err)
	}
	// Now the guest has them all, as it had them: the probe is the only
	// ssh of the run.
	n = countSSH(t, func() {
		var err error
		if s, err = syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Carry: carry}); err != nil {
			t.Fatal(err)
		}
	})
	if !s.Unchanged || n != 1 || len(s.Copied) != 0 {
		t.Fatalf("third sync: unchanged=%v, %d ssh, copied %v", s.Unchanged, n, s.Copied)
	}
	// A new login on the laptop goes at once.
	later := time.Now().Add(time.Minute)
	gh := filepath.Join(home, ".config", "gh", "hosts.yml")
	_ = os.WriteFile(gh, []byte("github.com:\n    oauth_token: gho_new\n"), 0o600)
	_ = os.Chtimes(gh, later, later)
	if s, err := syncGuest(ctx, f.target, f.local, testSlug, SyncOptions{Carry: carry}); err != nil || strings.Join(s.Copied, ",") != "gh,git" {
		t.Fatalf("new login: %+v %v", s, err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); !strings.Contains(string(b), "gho_new") {
		t.Fatalf("guest hosts.yml = %q", b)
	}
}

// I-225: a guestd_ok=false sample from before the guest's current run is
// not a dead guestd.
func TestGuestdDeadIgnoresSamplesFromBeforeTheStart(t *testing.T) {
	no := false
	start := time.Now().Add(-30 * time.Second)
	before, after := start.Add(-time.Minute), start.Add(10*time.Second)
	for _, c := range []struct {
		sampled *time.Time
		started *time.Time
		dead    bool
	}{
		{&before, &start, false}, {&after, &start, true}, {nil, &start, true}, {&before, nil, true},
	} {
		p := &Project{StartedAt: c.started, Signals: &Signals{GuestdOK: &no, SampledAt: c.sampled}}
		if got := guestdDead(p); got != c.dead {
			t.Errorf("sampled %v started %v: dead = %v, want %v", c.sampled, c.started, got, c.dead)
		}
	}
}
