package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
)

// ENG-71, found on the AWS M4 by the council: `repose host add` refused
// the Mac for lack of disk, and the same line run again two minutes
// later with nothing freed installed the appliance in under a second.
// The refusal left disk.raw behind at the image's own size, and the
// retry found it, skipped the block the sizing guard lived in, and
// installed a disk sized to the image on a volume the product had just
// said could not hold it.
//
// ENG-118 is the same leftover from the other side: an install that
// stopped before its config leaves bytes no command could clear.

// fakeMac makes this box look like a Mac the appliance could run on,
// with freeGB free on the volume, and serves a gzipped image.
func fakeMac(t *testing.T, freeGB int) (*Env, *bytes.Buffer, appliancePaths) {
	t.Helper()
	home := t.TempDir()

	onAppleSilicon = func() bool { return true }
	freeGBOf = func(string) int { return freeGB }
	macExec = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "sw_vers":
			return []byte("15.8.2\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "machdep.cpu.brand_string":
			return []byte("Apple M4\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "hw.memsize":
			return []byte("25769803776\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "hw.ncpu":
			return []byte("10\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "kern.hv_support":
			return []byte("1\n"), nil
		}
		return nil, nil // launchctl, xattr
	}
	t.Cleanup(func() {
		onAppleSilicon = func() bool { return false }
		freeGBOf = func(dir string) int { return 0 }
		macExec = func(string, ...string) ([]byte, error) { return nil, nil }
	})

	// A gzip of one sparse-ish image: unpackSparse writes it whole.
	var raw bytes.Buffer
	raw.Write(bytes.Repeat([]byte{0}, 1<<16))
	raw.WriteString("an appliance image")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write(raw.Bytes())
	_ = zw.Close()
	body := gz.Bytes()
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  image.raw.gz\n"))
			return
		}
		http.ServeContent(w, r, "image.raw.gz", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(envApplianceURL, srv.URL+"/image.raw.gz")
	t.Setenv(envVfkitURL, srv.URL+"/vfkit")
	t.Setenv(envVfkitSHA256, hex.EncodeToString(sum[:]))

	e := &Env{HomeDir: home}
	errOut := &bytes.Buffer{}
	e.Out, e.ErrOut = &bytes.Buffer{}, errOut
	return e, errOut, applianceFor(home)
}

// A refusal leaves nothing behind, and the same command run again with
// nothing freed is refused again rather than installing.
func TestApplianceRefusalIsAtomicAndRepeats(t *testing.T) {
	e, _, p := fakeMac(t, 46)
	arg := encodeTokenBundle("rjt_test")

	const want = "the appliance needs a 40 GB disk"
	err := applianceInstall(context.Background(), e, arg, "mac-mini", applianceOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("first run: %v, want a refusal naming %q", err, want)
	}
	for _, f := range []string{p.Disk, p.Config, p.Plist} {
		if _, serr := os.Stat(f); serr == nil {
			t.Errorf("the refusal left %s behind", filepath.Base(f))
		}
	}

	err = applianceInstall(context.Background(), e, arg, "mac-mini", applianceOptions{})
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("second run with nothing freed: %v, want the same refusal", err)
	}
	if _, serr := os.Stat(p.Config); serr == nil {
		t.Fatal("the retry installed the appliance on a volume the first run refused")
	}
}

// An install interrupted after the unpack leaves a disk at the image's
// own size. The next run sizes it to the plan rather than adopting it:
// a disk at image size has no room after the pool partition, which is
// what fails `repose-mac-grow` at boot (ENG-79's hypothesis).
func TestApplianceLeftoverDiskIsSizedNotAdopted(t *testing.T) {
	e, _, p := fakeMac(t, 200)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(mustCreate(t, p.Disk), 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(p.Disk, applianceImageApparentBytes); err != nil {
		t.Fatal(err)
	}
	if err := applianceInstall(context.Background(), e, encodeTokenBundle("rjt_test"), "mac-mini", applianceOptions{DiskGB: 60}); err != nil {
		t.Fatalf("install over a leftover: %v", err)
	}
	st, err := os.Stat(p.Disk)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 60<<30 {
		t.Fatalf("disk is %d bytes (%.2f GiB), want 60 GiB: the leftover was adopted at the image's size",
			st.Size(), float64(st.Size())/float64(1<<30))
	}
}

func mustCreate(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

// ENG-118: an install that stopped before its config leaves up to 2 GB
// under ~/Library/Application Support/repose, and no command cleared
// it. `rm --local` refused ("There is no appliance on this Mac") and
// `rm NAME` skipped the appliance entirely, because both asked whether
// a config could be read. Present and installed are two questions.
func TestApplianceLeftoversAreRemovable(t *testing.T) {
	e, _, p := fakeMac(t, 200)
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(p.Dir, "image.raw.gz.part")
	if err := os.WriteFile(part, []byte("two gigabytes, in spirit"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, installed := localAppliance(e); installed {
		t.Fatal("a part file with no config is not an installed appliance")
	}
	if !appliancePresent(e) {
		t.Fatal("a part file is something on this Mac to remove")
	}
	if err := HostRmLocalCmd(e, true, nil); err != nil {
		t.Fatalf("rm --local over an unfinished install: %v", err)
	}
	if _, err := os.Stat(part); err == nil {
		t.Fatal("rm --local left the part file behind")
	}

	// Nothing there at all is still a refusal, and it is the sentence
	// the customer reads.
	err := HostRmLocalCmd(e, true, nil)
	if err == nil || !strings.Contains(err.Error(), "no appliance on this Mac") {
		t.Fatalf("rm --local on a Mac with nothing: %v", err)
	}
}

// Found live on the Mac at 58 GB free: the disk-floor refusal fired
// correctly, but `repose host add` had already minted the host record,
// so the retry hit "You already have a host named mac-park2" while
// `--join` had no argument (the refused run printed none) and the
// one-host plan refused a second name. Nothing the customer could type
// recovered it. The refusal must come before the mint.
func TestApplianceRefusalMintsNoHost(t *testing.T) {
	e, _, p := fakeMac(t, 46)
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	le := newLifecycleEnv(t, fake)
	e.Client = le.Client
	e.Out, e.ErrOut = &discardWriter{}, &discardWriter{}

	before := fake.Calls("POST /v1/hosts")
	err := HostAddCmd(context.Background(), e, "mac-mini", applianceOptions{})
	if err == nil || !strings.Contains(err.Error(), "the appliance needs a 40 GB disk") {
		t.Fatalf("host add on a volume that cannot hold it: %v", err)
	}
	if n := fake.Calls("POST /v1/hosts") - before; n != 0 {
		t.Fatalf("the refused add minted %d host(s); the retry then collides on the name", n)
	}
	if _, serr := os.Stat(p.Dir); serr == nil {
		if ents, _ := os.ReadDir(p.Dir); len(ents) > 0 {
			t.Fatalf("the refused add left %d file(s) on the Mac", len(ents))
		}
	}
}
