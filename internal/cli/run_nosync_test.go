package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// `run --no-sync` leaves the checkout alone but still copies the laptop's
// tool logins, with or without the attach (DECISIONS I-366). Before, the
// logins rode only the sync's apply, so --no-sync sent none.
func TestRunWithoutSyncStillCopiesToolLogins(t *testing.T) {
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	f := newRunFixture(t, fake)
	f.env.HomeDir = laptopHomeWithGH(t)
	out := &discardWriter{}
	f.env.Out = out
	if err := os.WriteFile(filepath.Join(f.local, "README.md"), []byte("laptop edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runRun(context.Background(), f.env, RunOptions{Name: testSlug, NoSync: true, NoAttach: true}, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestHome, ".config", "gh", "hosts.yml")); !strings.Contains(string(b), "gho_test") {
		t.Fatalf("guest hosts.yml = %q, want the laptop's login", b)
	}
	if b, _ := os.ReadFile(filepath.Join(f.guestRepo(), "README.md")); string(b) == "laptop edit\n" {
		t.Fatal("--no-sync synced the checkout")
	}
	if !strings.Contains(out.buf.String(), "Logins copied: gh") {
		t.Fatalf("output = %q, want the Credentials line", out.buf.String())
	}
}
