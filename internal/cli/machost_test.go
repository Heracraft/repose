package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fakeapi "github.com/heracraft/repose/internal/fakes/api"
	"github.com/heracraft/repose/internal/machost"
)

// The Mac's install of design 2 (DECISIONS I-1684), the part a Linux box
// can hold: the order of the checks, the files and their owners, the
// launchd jobs, the wait for the join, the removal, and what a Mac that
// still holds design 1's appliance reads. The fake sudo applies each step
// to a directory standing in for the Mac's /, so the reads that follow
// (the record, the jobs) see what the install wrote. The Mac itself holds
// the rest (docs/reports/, ENG-268's evidence).

const d2TestVersion = "2026.11.28.1"

type d2Mac struct {
	t       *testing.T
	root    string // the Mac's / in this test
	sudo    []string
	refuse  bool
	joinErr error
	mounts  string
	srv     *httptest.Server
	served  int
}

// mapped puts an absolute path of the Mac's under the test's root.
func (m *d2Mac) mapped(p string) string {
	for _, pre := range []string{"/Library/", "/etc/"} {
		if strings.HasPrefix(p, pre) {
			return filepath.Join(m.root, p)
		}
	}
	return p
}

func newD2Mac(t *testing.T) (*Env, *bytes.Buffer, *d2Mac) {
	t.Helper()
	m := &d2Mac{t: t, root: t.TempDir()}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "Library", "Caches"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	onAppleSilicon = func() bool { return true }
	freeGBOf = func(string) int { return 200 }
	macFS = m.mapped
	macExec = func(name string, args ...string) ([]byte, error) {
		switch {
		case name == "sw_vers":
			return []byte("15.8.3\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "machdep.cpu.brand_string":
			return []byte("Apple M1\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "hw.memsize":
			return []byte("25769803776\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "hw.ncpu":
			return []byte("10\n"), nil
		case name == "sysctl" && len(args) > 1 && args[1] == "kern.hv_support":
			return []byte("1\n"), nil
		case name == "id":
			return []byte("dev\n"), nil
		case name == "fdesetup":
			return []byte("FileVault is Off.\n"), nil
		case name == "/sbin/mount":
			return []byte(m.mounts), nil
		case strings.HasSuffix(name, "/bin/hostd") && len(args) == 1 && args[0] == "version":
			return os.ReadFile(name) // the fake hostd's body is its answer
		}
		return nil, nil
	}
	macSudo = m.doSudo
	macWaitJoined = func(context.Context, string) (string, error) {
		if m.joinErr != nil {
			return "", m.joinErr
		}
		return "h-1", nil
	}
	t.Cleanup(func() {
		onAppleSilicon = func() bool { return false }
		freeGBOf = func(string) int { return 0 }
		macFS = func(p string) string { return p }
		macExec = func(string, ...string) ([]byte, error) { return nil, nil }
		macSudo = func(...string) error { return errors.New("sudo in a test") }
	})

	// A release: the bundle and its manifest, signed by a key of the
	// test's that the lab knobs point at.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	pubPath := filepath.Join(t.TempDir(), "pub.pem")
	if err := os.WriteFile(pubPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "bin"), 0o755)
	for _, n := range machost.BundleFiles {
		body := n
		if n == "hostd" {
			body = "hostd " + d2TestVersion + "\n"
		}
		_ = os.WriteFile(filepath.Join(dir, "bin", n), []byte(body), 0o755)
	}
	var bundle bytes.Buffer
	if err := machost.WriteBundle(&bundle, dir, d2TestVersion); err != nil {
		t.Fatal(err)
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			var man machost.Manifest
			man.Version = d2TestVersion
			sum := sha256.Sum256(bundle.Bytes())
			man.Darwin.ARM64 = machost.Asset{URL: m.srv.URL + "/bundle", SHA256: hex.EncodeToString(sum[:])}
			d := sha256.Sum256([]byte(man.SignedText()))
			sig, _ := ecdsa.SignASN1(rand.Reader, key, d[:])
			man.Signature = base64.StdEncoding.EncodeToString(sig)
			_ = json.NewEncoder(w).Encode(man)
		case "/bundle":
			m.served++
			_, _ = w.Write(bundle.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	t.Setenv(envMacHostManifestURL, m.srv.URL+"/manifest")
	t.Setenv(envMacHostPubKey, pubPath)

	e := &Env{HomeDir: home}
	errOut := &bytes.Buffer{}
	e.Out, e.ErrOut = &bytes.Buffer{}, errOut
	return e, errOut, m
}

// doSudo applies the commands the install and the removal run as root.
func (m *d2Mac) doSudo(args ...string) error {
	m.sudo = append(m.sudo, strings.Join(args, " "))
	if m.refuse {
		return errors.New("exit status 1")
	}
	a := make([]string, len(args))
	for i, s := range args {
		a[i] = m.mapped(s)
	}
	switch filepath.Base(a[0]) {
	case "mkdir":
		for _, d := range a[2:] {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return err
			}
		}
	case "rm":
		for _, f := range a[2:] {
			if err := os.RemoveAll(f); err != nil {
				return err
			}
		}
	case "ditto":
		return copyMacTree(a[1], a[2])
	case "ln":
		_ = os.Remove(a[3])
		return os.Symlink(args[2], a[3])
	case "install":
		b, err := os.ReadFile(a[len(a)-2])
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if a[2] == "0600" {
			mode = 0o600
		}
		if err := os.MkdirAll(filepath.Dir(a[len(a)-1]), 0o755); err != nil {
			return err
		}
		return os.WriteFile(a[len(a)-1], b, mode)
	}
	return nil // chmod, chown, launchctl, hdiutil, true
}

func copyMacTree(src, dst string) error {
	return filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer func() { _ = in.Close() }()
		out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

func (m *d2Mac) sudoed(prefix string) bool {
	for _, s := range m.sudo {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func addOnD2Mac(t *testing.T, e *Env) (*fakeapi.Fake, error) {
	t.Helper()
	fake := fakeapi.New(fakeapi.Options{})
	t.Cleanup(fake.Close)
	e.Client = newLifecycleEnv(t, fake).Client
	return fake, HostAddCmd(context.Background(), e, "mac-mini")
}

func TestMacHostAddInstallsDesign2(t *testing.T) {
	e, errOut, m := newD2Mac(t)
	if _, err := addOnD2Mac(t, e); err != nil {
		t.Fatalf("host add: %v\n%s", err, errOut)
	}
	p := macHostPaths
	// The bundle is current, hostd and the updater are LaunchDaemons.
	if v := (machost.Paths{Root: m.mapped(p.Root)}).CurrentVersion(); v != d2TestVersion {
		t.Errorf("current version %q", v)
	}
	for _, f := range []string{machost.HostdPlist(), machost.UpdatePlist(), machost.NewsyslogConf, p.APICA(), p.Record()} {
		if _, err := os.Stat(m.mapped(f)); err != nil {
			t.Errorf("%s was not installed: %v", f, err)
		}
	}
	plist, _ := os.ReadFile(m.mapped(machost.HostdPlist()))
	if !strings.Contains(string(plist), "<string>--mac-user</string>\n    <string>dev</string>") {
		t.Errorf("hostd does not run the VMs as the Mac's user:\n%s", plist)
	}
	// The token and the key: root's alone, and gone from the user's cache.
	for _, f := range []string{p.JoinToken(), p.HubJSON()} {
		st, err := os.Stat(m.mapped(f))
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	hub, _ := os.ReadFile(m.mapped(p.HubJSON()))
	if !strings.Contains(string(hub), `"edge_endpoint"`) {
		t.Errorf("hub.json is not the bundle's wg: %s", hub)
	}
	cache, _ := os.UserCacheDir()
	for _, n := range []string{"join-token", "hub.json"} {
		if _, err := os.Stat(filepath.Join(cache, "repose", "host", n)); err == nil {
			t.Errorf("the user's copy of %s outlived the install", n)
		}
	}
	var rec machost.Record
	b, _ := os.ReadFile(m.mapped(p.Record()))
	if json.Unmarshal(b, &rec) != nil || rec.Host != "mac-mini" || rec.User != "dev" || rec.Version != d2TestVersion {
		t.Errorf("install record %s", b)
	}
	if !m.sudoed("/bin/launchctl bootstrap system " + machost.HostdPlist()) {
		t.Errorf("hostd was not bootstrapped: %v", m.sudo)
	}
	// Every root step went through sudo, and the binaries became root's.
	if !m.sudoed("/usr/sbin/chown -R root:wheel " + p.Version(d2TestVersion)) {
		t.Errorf("the binaries are not root's: %v", m.sudo)
	}
	said := errOut.String()
	for _, w := range []string{"mac-mini: installing repose on this Mac.", "fetching repose " + d2TestVersion, "waiting for mac-mini to join your account", "mac-mini joined your account in"} {
		if !strings.Contains(said, w) {
			t.Errorf("progress does not say %q:\n%s", w, said)
		}
	}
	for _, not := range []string{"appliance", "vfkit"} {
		if strings.Contains(said, not) {
			t.Errorf("design 2's install talks of %q:\n%s", not, said)
		}
	}
}

func TestMacHostAddRefusesBeforeTheMint(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(*Env, *d2Mac)
		want  string
	}{
		{"design 1's appliance is still here", func(e *Env, _ *d2Mac) {
			d := applianceFor(e.HomeDir).Dir
			_ = os.MkdirAll(d, 0o755)
			_ = os.WriteFile(filepath.Join(d, "disk.raw"), []byte("x"), 0o644)
		}, "`repose host rm --local` removes it"},
		{"repose is here already", func(_ *Env, m *d2Mac) {
			_ = os.MkdirAll(m.mapped(machost.Root), 0o755)
			_ = os.WriteFile(m.mapped(macHostPaths.Record()), []byte(`{"host":"mac-old"}`), 0o644)
		}, "This Mac is mac-old already"},
		{"macOS 13", func(*Env, *d2Mac) {
			inner := macExec
			macExec = func(n string, a ...string) ([]byte, error) {
				if n == "sw_vers" {
					return []byte("13.6\n"), nil
				}
				return inner(n, a...)
			}
		}, "repose needs macOS 14 or later"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, _, m := newD2Mac(t)
			c.setup(e, m)
			_, err := addOnD2Mac(t, e)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("host add = %v, want %q", err, c.want)
			}
			if hs, err := e.Client.ListHosts(context.Background()); err != nil || len(hs) != 0 {
				t.Errorf("a refused install minted hosts: %v %v", hs, err)
			}
			if m.served != 0 || len(m.sudo) != 0 {
				t.Errorf("a refused install fetched (%d) or ran sudo (%v)", m.served, m.sudo)
			}
		})
	}
}

func TestMacHostAddSudoRefusedSaysTheWayOn(t *testing.T) {
	e, _, m := newD2Mac(t)
	m.refuse = true
	_, err := addOnD2Mac(t, e)
	if err == nil {
		t.Fatal("an install whose sudo refused succeeded")
	}
	for _, w := range []string{"mac-mini is in your account already", "repose host add --join-file", "join-mac-mini"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("the refusal does not say %q:\n%v", w, err)
		}
	}
	if _, serr := os.Stat(m.mapped(machost.HostdPlist())); serr == nil {
		t.Error("a job was installed although sudo refused")
	}
}

func TestMacHostAddSaysWhenTheMacDoesNotJoin(t *testing.T) {
	e, _, m := newD2Mac(t)
	m.joinErr = errors.New("hostd runs and has not reached repose yet")
	_, err := addOnD2Mac(t, e)
	if err == nil || !strings.Contains(err.Error(), "has not joined your account") || !strings.Contains(err.Error(), machost.HostdLog()) {
		t.Fatalf("host add = %v", err)
	}
}

func TestMacHostAddRefusesATamperedBundle(t *testing.T) {
	e, _, m := newD2Mac(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			resp, err := http.Get(m.srv.URL + "/manifest")
			if err == nil {
				_, _ = io.Copy(w, resp.Body)
				_ = resp.Body.Close()
			}
			return
		}
		http.NotFound(w, r)
	}))
	defer other.Close()
	// A different key: the manifest's signature does not verify.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	wrong := filepath.Join(t.TempDir(), "wrong.pem")
	_ = os.WriteFile(wrong, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644)
	t.Setenv(envMacHostPubKey, wrong)
	_, err := addOnD2Mac(t, e)
	if err == nil || !strings.Contains(err.Error(), "signature does not verify") {
		t.Fatalf("host add = %v", err)
	}
	if m.served != 0 || len(m.sudo) != 0 {
		t.Errorf("an unverified manifest was acted on: served %d, sudo %v", m.served, m.sudo)
	}
}

func TestMacHostRmTakesItAllOff(t *testing.T) {
	e, errOut, m := newD2Mac(t)
	if _, err := addOnD2Mac(t, e); err != nil {
		t.Fatal(err)
	}
	p := macHostPaths
	// A machine's job, its volume, and a login share attached under the root.
	_ = os.MkdirAll(m.mapped(p.Launchd()), 0o755)
	_ = os.WriteFile(m.mapped(filepath.Join(p.Launchd(), machost.GuestPrefix+"g1.plist")), []byte("x"), 0o644)
	_ = os.MkdirAll(m.mapped(p.Volumes()), 0o755)
	_ = os.WriteFile(m.mapped(filepath.Join(p.Volumes(), "g1.raw")), []byte("a disk"), 0o644)
	share := filepath.Join(p.Root, "users", "dev", "claude-auth")
	m.mounts = "/dev/disk3s1s1 on / (apfs, sealed)\n/dev/disk5s1 on " + share + " (apfs, local, nodev, nosuid, journaled)\n"
	m.sudo = nil
	if err := HostRmCmd(context.Background(), e, "mac-mini", true, nil); err != nil {
		t.Fatalf("host rm: %v\n%s", err, errOut)
	}
	for _, w := range []string{
		"/bin/launchctl bootout system/" + machost.HostdLabel,
		"/bin/launchctl bootout system/" + machost.UpdateLabel,
		"/bin/launchctl kill SIGKILL gui/" + fmt.Sprint(os.Getuid()) + "/" + machost.GuestPrefix + "g1",
		"/usr/bin/hdiutil detach -force " + share,
	} {
		if !m.sudoed(w) {
			t.Errorf("removal did not run %q: %v", w, m.sudo)
		}
	}
	for _, f := range []string{p.Root, machost.HostdPlist(), machost.UpdatePlist(), machost.NewsyslogConf, machost.LogDir} {
		if _, err := os.Stat(m.mapped(f)); err == nil {
			t.Errorf("%s is still on the Mac", f)
		}
	}
	// Reinstall works after.
	m.sudo = nil
	e2 := *e
	fake := fakeapi.New(fakeapi.Options{})
	defer fake.Close()
	e2.Client = newLifecycleEnv(t, fake).Client
	if err := HostAddCmd(context.Background(), &e2, "mac-mini2"); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if r := macRecord(); r == nil || r.Host != "mac-mini2" {
		t.Errorf("record after the reinstall: %+v", r)
	}
}

func TestMacHostRmOfAnotherHostKeepsThisMac(t *testing.T) {
	e, errOut, m := newD2Mac(t)
	if _, err := addOnD2Mac(t, e); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(m.mapped(macHostPaths.Record()), []byte(`{"host":"mac-other"}`), 0o644)
	m.sudo = nil
	if err := HostRmCmd(context.Background(), e, "mac-mini", true, nil); err != nil {
		t.Fatal(err)
	}
	if len(m.sudo) != 0 {
		t.Errorf("removing another host touched this Mac: %v", m.sudo)
	}
	if !strings.Contains(errOut.String(), "This Mac runs mac-other, not mac-mini; repose stays on it.") {
		t.Errorf("said: %s", errOut)
	}
}

func TestHostRunOnAD1MacSaysWhatToDo(t *testing.T) {
	var out bytes.Buffer
	cmd := newHostCmd(func() (*Env, error) { return &Env{}, nil }, &globalFlags{})
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"run"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // launchd's SIGTERM, already sent
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "`repose host rm --local` removes it") {
		t.Errorf("host run said %q", out.String())
	}
}
