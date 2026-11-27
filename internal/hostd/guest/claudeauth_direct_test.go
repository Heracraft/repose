package guest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a Mac the login share is a directory the launcher attaches itself
// (DECISIONS I-1666): no virtiofsd, the hypervisor's --fs as on Linux, and
// a share-dir file beside its socket path naming the user's directory. Two
// guests of one user name the same directory.
func TestDirectLoginShareNamesTheUsersDirectory(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.DirectAuthShare = true
	h.create(gid1)
	h.create(gid2)
	share := filepath.Join(h.cfg.UsersDir, "user-1", "claude-auth")
	if fi, err := os.Stat(share); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("share %v %v", fi, err)
	}
	for _, id := range []string{gid1, gid2} {
		if u := h.sd.Units["virtiofsd-auth@"+id]; u != nil {
			t.Fatalf("%s: a virtiofsd was started: %+v", id, u)
		}
		dir := filepath.Join(h.cfg.GuestsDir, id)
		argv := strings.Join(h.sd.Units["guest@"+id].Argv, " ")
		if !strings.Contains(argv, "--fs tag=claude-auth,socket="+filepath.Join(dir, "virtiofsd-auth", "virtiofsd.sock")) {
			t.Fatalf("%s: no login share on the hypervisor: %s", id, argv)
		}
		b, err := os.ReadFile(filepath.Join(dir, "virtiofsd-auth", AuthShareDirFile))
		if err != nil || strings.TrimSpace(string(b)) != share {
			t.Fatalf("%s: share-dir %q %v", id, b, err)
		}
	}
}

// With a bound (a disk image on a Mac) the share is mounted at the share's
// path with the volume's size before its root is given to the user.
func TestDirectLoginShareIsBounded(t *testing.T) {
	h := newHarness(t, nil)
	h.m.d.DirectAuthShare = true
	var got []string
	h.m.d.BoundAuthShare = func(_ context.Context, userDir, share string, bytes uint64) error {
		got = append(got, fmt.Sprintf("%s %s %d", filepath.Base(userDir), filepath.Base(share), bytes))
		return nil
	}
	h.create(gid1)
	if len(got) != 1 || got[0] != fmt.Sprintf("user-1 claude-auth %d", 16<<20) {
		t.Fatalf("bound calls %v", got)
	}
	// A bound that fails starts the guest without the share, as a failed
	// share does on Linux.
	h.m.d.BoundAuthShare = func(context.Context, string, string, uint64) error { return os.ErrPermission }
	h.create(gid2)
	if strings.Contains(strings.Join(h.sd.Units["guest@"+gid2].Argv, " "), "tag=claude-auth") {
		t.Fatal("a guest got a share whose bound failed")
	}
}
