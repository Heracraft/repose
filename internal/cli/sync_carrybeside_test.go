package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// I-743: on a first sync with fetches, the checkout runs beside the
// second half of the carry (Claude, MCP servers, tools), not after it.

// slowCarry is the laptop's carry with gh's login and one more part that
// waits up to 10 s for the checkout's README.md and records whether it
// saw it: run after the checkout, as before I-743, it never does.
func slowCarry(t *testing.T, f *syncFixture, home string) func(map[string]string) (*credCarry, error) {
	t.Helper()
	return func(markers map[string]string) (*credCarry, error) {
		c, err := buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers})
		if err != nil {
			return nil, err
		}
		readme := filepath.Join(f.guestRepo(), "README.md")
		err = c.p.part("slow", "i=0\nwhile [ $i -lt 100 ]; do if [ -f '"+readme+"' ]; then echo beside > ~/carry-saw; exit 0; fi; i=$((i+1)); sleep 0.1; done\necho after > ~/carry-saw\n")
		return c, err
	}
}

func TestFirstSyncChecksOutBesideTheCarry(t *testing.T) {
	f, _ := prefetchFixture(t)
	home := laptopHomeWithGH(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", Carry: slowCarry(t, f, home)})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := readFile(t, filepath.Join(f.guestHome, "carry-saw")); got != "beside\n" {
		t.Fatalf("the carry's last part ran %q the checkout, want beside it", strings.TrimSpace(got))
	}
	// The whole carry ran, and its reply still reached the summary.
	if !strings.Contains(strings.Join(s.Copied, ","), "gh") {
		t.Fatalf("copied = %v, want gh", s.Copied)
	}
	if b, err := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); err != nil || !strings.Contains(string(b), "gho_test") {
		t.Fatalf("gh's login on the guest: %q %v", b, err)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD %s, laptop %s", got, want)
	}
}

// The logins and the git config are in before the checkout: the carried
// excludes file already hides an ignored file from the apply's status.
func TestFirstSyncCarriesTheGitConfigBeforeTheCheckout(t *testing.T) {
	f, _ := prefetchFixture(t)
	home := laptopHomeWithGH(t)
	writeFile(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = Dev\n\temail = dev@example.com\n[core]\n\texcludesFile = ~/.gitignore_global\n")
	writeFile(t, filepath.Join(home, ".gitignore_global"), "*.log\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	gc, err := buildGitCarry(f.local, home)
	if err != nil {
		t.Fatal(err)
	}
	// The guest already has the file the laptop ignores and sends: with
	// the carried excludes in place before the apply's status, it is not
	// the machine's own change in the way of the sync.
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", Carry: func(markers map[string]string) (*credCarry, error) {
		if err := os.MkdirAll(f.guestRepo(), 0o755); err != nil {
			return nil, err
		}
		writeFile(t, filepath.Join(f.guestRepo(), "agent.log"), "the machine's\n")
		return buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers, Git: gc})
	}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if s.GuestKept != 0 {
		t.Fatalf("summary %+v: the apply's status ran before the carried excludes", s)
	}
}

// A login that cannot be written still stops a first sync before the
// checkout is touched, with the step it failed at.
func TestFirstSyncCarryFailureStopsBeforeTheCheckout(t *testing.T) {
	f, _ := prefetchFixture(t)
	home := laptopHomeWithGH(t)
	if err := os.WriteFile(filepath.Join(f.guestHome, ".gitconfig.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", Carry: slowCarry(t, f, home)})
	if err == nil || !strings.Contains(err.Error(), "Could not copy your tool logins to the machine") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.guestRepo(), "README.md")); !os.IsNotExist(err) {
		t.Fatalf("the checkout was touched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.guestHome, "carry-saw")); !os.IsNotExist(err) {
		t.Fatalf("the carry went on past its failed logins: %v", err)
	}
}

// When the fetches fall short, the first apply's carry still ends whole
// and reports, and the second payload carries only the bundles.
func TestFirstSyncResendAfterACarryBeside(t *testing.T) {
	f, _ := prefetchFixture(t)
	home := laptopHomeWithGH(t)
	writeFile(t, filepath.Join(f.local, "README.md"), "pushed, then force-pushed away\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "gone from GitHub")
	mustRun(t, f.local, "git", "update-ref", "refs/remotes/origin/main", mustRun(t, f.local, "git", "rev-parse", "HEAD"))
	writeFile(t, filepath.Join(f.local, "README.md"), "unpushed\n")
	mustRun(t, f.local, "git", "commit", "-q", "-am", "unpushed")
	var sent [][]string
	observeSyncPayload = func(names []string) { sent = append(sent, names) }
	t.Cleanup(func() { observeSyncPayload = nil })
	s, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{RemoteURL: "github.com/a/b", Carry: slowCarry(t, f, home)})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(sent) != 2 || strings.Join(sent[1], " ") != "bundle" {
		t.Fatalf("payloads %v, want the second to be the bundle alone", sent)
	}
	// The first apply stopped before any checkout, so the slow part gave
	// up waiting for one: it ran to its end, not cut off.
	if got := readFile(t, filepath.Join(f.guestHome, "carry-saw")); got != "after\n" {
		t.Fatalf("carry-saw = %q", got)
	}
	if !strings.Contains(strings.Join(s.Copied, ","), "gh") {
		t.Fatalf("copied = %v, want gh", s.Copied)
	}
	if got, want := mustRun(t, f.guestRepo(), "git", "rev-parse", "HEAD"), mustRun(t, f.local, "git", "rev-parse", "HEAD"); got != want {
		t.Fatalf("guest HEAD %s, laptop %s", got, want)
	}
}

// The carry's tar travels compressed, in the apply's payload as in its
// own ssh: a Claude carry is mostly text.
func TestCarryTravelsCompressed(t *testing.T) {
	f := newSyncFixture(t)
	home := laptopHomeWithGH(t)
	big := strings.Repeat("<xsd:element name=\"x\" type=\"y\"/>\n", 1<<15) // 1.1 MB of schema-like text
	var sent [][]string
	observeSyncPayload = func(names []string) { sent = append(sent, names) }
	t.Cleanup(func() { observeSyncPayload = nil })
	var plain int
	_, err := syncGuest(context.Background(), f.target, f.local, testSlug, SyncOptions{Carry: func(markers map[string]string) (*credCarry, error) {
		c, err := buildCredentialsAndCarry(home, f.local, credSyncOptions{}, carryOptions{Markers: markers})
		if err != nil {
			return nil, err
		}
		if err := c.p.file("big.xsd", []byte(big)); err != nil {
			return nil, err
		}
		if err := c.p.part("big", "cp \"$1/big.xsd\" ~/big.xsd\n"); err != nil {
			return nil, err
		}
		plain = len(big)
		return c, nil
	}})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(sent) != 1 || !strings.Contains(" "+strings.Join(sent[0], " ")+" ", " carry.tar.gz ") || strings.Contains(" "+strings.Join(sent[0], " ")+" ", " carry.tar ") {
		t.Fatalf("payload %v, want carry.tar.gz and no carry.tar", sent)
	}
	if got := readFile(t, filepath.Join(f.guestHome, "big.xsd")); len(got) != plain {
		t.Fatalf("big.xsd on the guest is %d bytes, want %d", len(got), plain)
	}
	p := newGuestPayload()
	if err := p.file("big.xsd", []byte(big)); err != nil {
		t.Fatal(err)
	}
	_ = p.tw.Close()
	z, err := p.gz()
	if err != nil {
		t.Fatal(err)
	}
	if len(z)*10 > plain {
		t.Fatalf("compressed %d bytes to %d", plain, len(z))
	}
}
