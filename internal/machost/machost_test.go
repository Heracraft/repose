package machost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// testKey is a throwaway release key and its public PEM.
func testKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signed(t *testing.T, k *ecdsa.PrivateKey, version, url, sum string) []byte {
	t.Helper()
	var m Manifest
	m.Version = version
	m.Darwin.ARM64 = Asset{URL: url, SHA256: sum}
	d := sha256.Sum256([]byte(m.SignedText()))
	sig, err := ecdsa.SignASN1(rand.Reader, k, d[:])
	if err != nil {
		t.Fatal(err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(sig)
	b, _ := json.Marshal(m)
	return b
}

func TestManifestVerifies(t *testing.T) {
	k, pub := testKey(t)
	sum := strings.Repeat("a", 64)
	b := signed(t, k, "2026.11.28.1", "https://example.test/b.tar.gz", sum)
	m, err := ParseManifest(b, pub)
	if err != nil || m.Version != "2026.11.28.1" || m.Darwin.ARM64.SHA256 != sum {
		t.Fatalf("ParseManifest = %+v, %v", m, err)
	}
	// Any field the Mac acts on, changed after signing, is refused.
	for _, tamper := range []func(*Manifest){
		func(m *Manifest) { m.Version = "2026.11.28.2" },
		func(m *Manifest) { m.Darwin.ARM64.URL = "https://evil.test/b.tar.gz" },
		func(m *Manifest) { m.Darwin.ARM64.SHA256 = strings.Repeat("b", 64) },
	} {
		var mm Manifest
		_ = json.Unmarshal(b, &mm)
		tamper(&mm)
		bb, _ := json.Marshal(mm)
		if _, err := ParseManifest(bb, pub); err == nil {
			t.Errorf("a tampered manifest verified: %+v", mm)
		}
	}
	// Another key's signature is refused.
	_, other := testKey(t)
	if _, err := ParseManifest(b, other); err == nil {
		t.Error("a manifest verified against another key")
	}
	// The release key parses.
	if _, err := parsePub(ReleasePubKeyPEM); err != nil {
		t.Fatal(err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2026.11.23", "2026.11.22.10", 1},
		{"2026.11.22.10", "2026.11.22.9", 1},
		{"2026.11.22", "2026.11.22.0", 0},
		{"2026.11.22.1", "2026.11.22.1", 0},
		{"2026.1.1", "2026.11.1", -1},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNewestManifestURL(t *testing.T) {
	rel := `[
	 {"tag_name":"v0.1.70","draft":false,"assets":[{"name":"checksums.txt","browser_download_url":"u0"}]},
	 {"tag_name":"hostd-2026.11.22.10","draft":false,"assets":[{"name":"manifest.json","browser_download_url":"lin"},{"name":"manifest-darwin.json","browser_download_url":"u10"}]},
	 {"tag_name":"hostd-2026.11.23","draft":false,"assets":[{"name":"manifest-darwin.json","browser_download_url":"u23"}]},
	 {"tag_name":"hostd-2026.11.24","draft":true,"assets":[{"name":"manifest-darwin.json","browser_download_url":"draft"}]},
	 {"tag_name":"hostd-2026.11.25","draft":false,"assets":[{"name":"manifest.json","browser_download_url":"linux-only"}]},
	 {"tag_name":"hostd-stable","draft":false,"assets":[{"name":"manifest-darwin.json","browser_download_url":"dead"}]}
	]`
	got, err := NewestManifestURL([]byte(rel))
	if err != nil || got != "u23" {
		t.Fatalf("NewestManifestURL = %q, %v; want u23", got, err)
	}
	if _, err := NewestManifestURL([]byte(`[{"tag_name":"hostd-1","assets":[{"name":"manifest.json"}]}]`)); err == nil {
		t.Error("a list with no darwin manifest gave a URL")
	}
}

// fakeBundleDir makes DIR/bin/<every BundleFiles entry>.
func fakeBundleDir(t *testing.T, version, launcher string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range BundleFiles {
		body := "#!/bin/sh\necho " + n + "\n"
		switch n {
		case "hostd":
			body = "hostd " + version
		case "repose-vm":
			body = launcher
		}
		if err := os.WriteFile(filepath.Join(dir, "bin", n), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func packed(t *testing.T, dir, version string) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := WriteBundle(&b, dir, version); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestBundleIsReproducibleAndRoundTrips(t *testing.T) {
	d1 := fakeBundleDir(t, "2026.11.28.1", "vm1")
	d2 := fakeBundleDir(t, "2026.11.28.1", "vm1")
	// Different modes and times on disk make the same bytes.
	_ = os.Chmod(filepath.Join(d2, "bin", "zstd"), 0o555)
	a, b := packed(t, d1, "2026.11.28.1"), packed(t, d2, "2026.11.28.1")
	if !bytes.Equal(a, b) {
		t.Fatal("the same files packed in two directories gave different bytes")
	}
	dest := filepath.Join(t.TempDir(), "x")
	v, err := ExtractBundle(bytes.NewReader(a), dest)
	if err != nil || v != "2026.11.28.1" {
		t.Fatalf("ExtractBundle = %q, %v", v, err)
	}
	for _, n := range BundleFiles {
		st, err := os.Stat(filepath.Join(dest, "bin", n))
		if err != nil || st.Mode().Perm() != 0o755 {
			t.Errorf("bin/%s: %v %v", n, st, err)
		}
	}
}

func evilBundle(t *testing.T, add func(*tar.Writer)) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	add(tw)
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

func TestExtractBundleRefuses(t *testing.T) {
	file := func(tw *tar.Writer, name, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	all := func(tw *tar.Writer) {
		for _, n := range BundleFiles {
			file(tw, "bin/"+n, "x")
		}
		file(tw, "VERSION", "2026.11.28.1\n")
	}
	cases := map[string]func(*tar.Writer){
		"a path out of dest": func(tw *tar.Writer) { all(tw); file(tw, "../../etc/x", "x") },
		"an absolute path":   func(tw *tar.Writer) { all(tw); file(tw, "/etc/sudoers.d/x", "x") },
		"a symlink": func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "bin/hostd", Linkname: "/bin/sh", Typeflag: tar.TypeSymlink})
		},
		"an unknown binary": func(tw *tar.Writer) { all(tw); file(tw, "bin/sudo", "x") },
		"a missing tool": func(tw *tar.Writer) {
			for _, n := range BundleFiles[1:] {
				file(tw, "bin/"+n, "x")
			}
			file(tw, "VERSION", "1\n")
		},
		"no VERSION": func(tw *tar.Writer) {
			for _, n := range BundleFiles {
				file(tw, "bin/"+n, "x")
			}
		},
		"a bad VERSION": func(tw *tar.Writer) {
			for _, n := range BundleFiles {
				file(tw, "bin/"+n, "x")
			}
			file(tw, "VERSION", "../x\n")
		},
	}
	for name, add := range cases {
		dest := filepath.Join(t.TempDir(), "x")
		if _, err := ExtractBundle(bytes.NewReader(evilBundle(t, add)), dest); err == nil {
			t.Errorf("%s: extracted", name)
		}
		if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: dest left behind", name)
		}
	}
}

// macOS's sun_path is 104 bytes. The longest socket under the layout is a
// guest's hybrid-vsock listener for a guest-initiated connection,
// <guests>/<36-char id>/vsock.sock_<port>.
func TestSocketPathsFit(t *testing.T) {
	id := strings.Repeat("0", 36)
	for _, p := range []string{
		filepath.Join(Default.Guests(), id, "vsock.sock_4294967295"),
		filepath.Join(Default.Guests(), id, "console.sock"),
		filepath.Join(Default.Net(), "tap-"+strings.Repeat("0", 11)+".sock"),
		Default.ControlSock(),
	} {
		if len(p) >= 104 {
			t.Errorf("%s is %d bytes, over macOS's sun_path", p, len(p))
		}
	}
}

func TestPlistsAreWellFormed(t *testing.T) {
	for name, text := range map[string]string{
		"hostd":  HostdPlistText(Default, "a&b", DefaultAPI),
		"update": UpdatePlistText(Default, []string{"--manifest-url", "https://x.test/m?a=1&b=2"}),
	} {
		d := xml.NewDecoder(strings.NewReader(text))
		d.Strict = true
		for {
			_, err := d.Token()
			if err != nil {
				if err.Error() != "EOF" {
					t.Errorf("%s plist: %v", name, err)
				}
				break
			}
		}
	}
	h := HostdPlistText(Default, "ada", DefaultAPI)
	for _, want := range []string{
		"<string>" + Default.Bin("hostd") + "</string>",
		"<string>--mac-user</string>\n    <string>ada</string>",
		"<string>--owned-host</string>",
		"<string>--nix-mode</string>\n    <string>off</string>",
		"<key>KeepAlive</key><true/>",
		"<string>" + HostdLog() + "</string>",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("hostd plist lacks %q", want)
		}
	}
}

// fakeMac is an install root with a current version and an updater
// against a test release server.
type fakeMac struct {
	t       *testing.T
	p       Paths
	srv     *httptest.Server
	key     *ecdsa.PrivateKey
	pub     string
	bundles map[string][]byte
	man     []byte
	status  atomic.Int32 // the releases list's status, 0 is 200
	kicks   int
	healthy func() error
	cycled  int
}

func newFakeMac(t *testing.T, current string) *fakeMac {
	f := &fakeMac{t: t, p: Paths{Root: t.TempDir()}, bundles: map[string][]byte{}}
	f.key, f.pub = testKey(t)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			if s := f.status.Load(); s != 0 {
				w.WriteHeader(int(s))
				return
			}
			_, _ = fmt.Fprintf(w, `[{"tag_name":"hostd-2026.12.1","assets":[{"name":"manifest-darwin.json","browser_download_url":"%s/manifest"}]}]`, f.srv.URL)
		case r.URL.Path == "/manifest":
			_, _ = w.Write(f.man)
		case strings.HasPrefix(r.URL.Path, "/b/"):
			_, _ = w.Write(f.bundles[strings.TrimPrefix(r.URL.Path, "/b/")])
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	if current != "" {
		f.install(current, "vm-"+current)
	}
	return f
}

// install puts version in place as current, as `repose host add` does.
func (f *fakeMac) install(version, launcher string) {
	b := packed(f.t, fakeBundleDir(f.t, version, launcher), version)
	if err := os.MkdirAll(f.p.Versions(), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if _, err := ExtractBundle(bytes.NewReader(b), f.p.Version(version)); err != nil {
		f.t.Fatal(err)
	}
	if err := SwapCurrent(f.p, version); err != nil {
		f.t.Fatal(err)
	}
}

// publish makes version the newest release.
func (f *fakeMac) publish(version, launcher string) {
	b := packed(f.t, fakeBundleDir(f.t, version, launcher), version)
	f.bundles[version] = b
	sum := sha256.Sum256(b)
	f.man = signed(f.t, f.key, version, f.srv.URL+"/b/"+version, fmt.Sprintf("%x", sum))
}

func (f *fakeMac) updater() *Updater {
	return &Updater{
		Paths: f.p, ReleasesURL: f.srv.URL + "/releases", PubKeyPEM: f.pub, HTTP: f.srv.Client(),
		Exec: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if strings.HasSuffix(name, "/hostd") && len(args) == 1 && args[0] == "version" {
				return os.ReadFile(name) // the fake hostd's body is its answer
			}
			if name == "/bin/launchctl" {
				f.kicks++
				return nil, nil
			}
			return nil, fmt.Errorf("unexpected %s %v", name, args)
		},
		Healthy: func(context.Context) error {
			if f.healthy != nil {
				return f.healthy()
			}
			return nil
		},
		CycleLaunchers: func(context.Context) (bool, string, error) { f.cycled++; return true, "", nil },
		HealthWait:     1,
	}
}

func TestUpdateSwapsAndKeepsThePrevious(t *testing.T) {
	f := newFakeMac(t, "2026.11.27.1")
	f.install("2026.11.26.1", "vm-old") // an older one, pruned after
	_ = SwapCurrent(f.p, "2026.11.27.1")
	f.publish("2026.11.28.1", "vm-2026.11.27.1") // same launcher
	if err := f.updater().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if v := f.p.CurrentVersion(); v != "2026.11.28.1" {
		t.Fatalf("current is %s", v)
	}
	if f.kicks != 1 {
		t.Errorf("hostd restarted %d times, want 1", f.kicks)
	}
	if f.cycled != 0 {
		t.Error("an unchanged launcher cycled the machines")
	}
	ents, _ := os.ReadDir(f.p.Versions())
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "2026.11.27.1 2026.11.28.1" {
		t.Errorf("versions kept: %v, want the previous and the current", names)
	}
	// A second pass finds nothing newer and restarts nothing.
	if err := f.updater().Run(context.Background()); err != nil || f.kicks != 1 {
		t.Errorf("second pass: %v, kicks %d", err, f.kicks)
	}
}

func TestUpdateCyclesAChangedLauncher(t *testing.T) {
	f := newFakeMac(t, "2026.11.27.1")
	f.publish("2026.11.28.1", "vm-new")
	if err := f.updater().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.cycled != 1 {
		t.Errorf("a changed launcher cycled %d times, want 1", f.cycled)
	}
}

func TestUpdateRollsBackAHostdThatDoesNotAnswer(t *testing.T) {
	f := newFakeMac(t, "2026.11.27.1")
	f.publish("2026.11.28.1", "vm-2026.11.27.1")
	f.healthy = func() error {
		if f.p.CurrentVersion() == "2026.11.28.1" {
			return errors.New("no answer")
		}
		return nil
	}
	if err := f.updater().Run(context.Background()); !errors.Is(err, ErrRolledBack) {
		t.Fatalf("Run = %v, want ErrRolledBack", err)
	}
	if v := f.p.CurrentVersion(); v != "2026.11.27.1" {
		t.Errorf("current after a rollback is %s", v)
	}
	if f.kicks != 2 {
		t.Errorf("hostd restarted %d times, want 2 (new, then old)", f.kicks)
	}
}

func TestUpdateRefuses(t *testing.T) {
	t.Run("a bad checksum", func(t *testing.T) {
		f := newFakeMac(t, "2026.11.27.1")
		f.publish("2026.11.28.1", "x")
		f.bundles["2026.11.28.1"] = append(f.bundles["2026.11.28.1"], 0)
		if err := f.updater().Run(context.Background()); err == nil || f.p.CurrentVersion() != "2026.11.27.1" || f.kicks != 0 {
			t.Errorf("Run = %v, current %s, kicks %d", err, f.p.CurrentVersion(), f.kicks)
		}
	})
	t.Run("an older manifest", func(t *testing.T) {
		f := newFakeMac(t, "2026.11.27.1")
		f.publish("2026.11.26.1", "x")
		if err := f.updater().Run(context.Background()); err != nil || f.p.CurrentVersion() != "2026.11.27.1" || f.kicks != 0 {
			t.Errorf("Run = %v, current %s, kicks %d", err, f.p.CurrentVersion(), f.kicks)
		}
	})
	t.Run("a hostd that names another version", func(t *testing.T) {
		f := newFakeMac(t, "2026.11.27.1")
		dir := fakeBundleDir(t, "2026.11.27.9", "x") // hostd says .9
		b := packed(t, dir, "2026.11.28.1")
		f.bundles["v"] = b
		sum := sha256.Sum256(b)
		f.man = signed(t, f.key, "2026.11.28.1", f.srv.URL+"/b/v", fmt.Sprintf("%x", sum))
		if err := f.updater().Run(context.Background()); err == nil || f.p.CurrentVersion() != "2026.11.27.1" {
			t.Errorf("Run = %v, current %s", err, f.p.CurrentVersion())
		}
	})
}

func TestUpdateUsesTheKeptManifestWhenGitHubRefuses(t *testing.T) {
	f := newFakeMac(t, "2026.11.27.1")
	f.publish("2026.11.28.1", "vm-2026.11.27.1")
	if err := f.updater().Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.status.Store(http.StatusForbidden)
	if err := f.updater().Run(context.Background()); err != nil || f.kicks != 1 {
		t.Errorf("a refused lookup with a kept manifest: %v, kicks %d", err, f.kicks)
	}
	g := newFakeMac(t, "2026.11.27.1")
	g.status.Store(http.StatusTooManyRequests)
	if err := g.updater().Run(context.Background()); err == nil {
		t.Error("a refused lookup with no kept manifest succeeded")
	}
}

func TestHostCAMatchesTheX86Installer(t *testing.T) {
	b, err := os.ReadFile("../../ops/install-host-x86.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "API_CA='")
	j := strings.Index(s[i:], "-----END CERTIFICATE-----")
	if i < 0 || j < 0 {
		t.Fatal("no API_CA in ops/install-host-x86.sh")
	}
	if got := s[i+len("API_CA='") : i+j+len("-----END CERTIFICATE-----")]; got != HostCAPEM {
		t.Errorf("ops/install-host-x86.sh's API_CA and machost.HostCAPEM differ")
	}
	if API := DefaultAPI; !strings.Contains(s, "API_ADDR="+API.Addr) || !strings.Contains(s, "API_SERVER_NAME="+API.ServerName) {
		t.Errorf("ops/install-host-x86.sh's api address or name differs from DefaultAPI %+v", API)
	}
}
