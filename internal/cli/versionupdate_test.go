package cli

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
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease is a release server with one or more tags, signed with its
// own key, as install.sh's own tests use REPOSE_INSTALL_BASE_URL and
// REPOSE_INSTALL_PUBKEY.
type fakeRelease struct {
	t       *testing.T
	latest  string
	assets  map[string][]byte // "<tag>/<name>" → bytes
	key     *ecdsa.PrivateKey
	pubPEM  string
	srv     *httptest.Server
	hitPath []string
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeRelease{
		t:      t,
		assets: map[string][]byte{},
		key:    key,
		pubPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hitPath = append(f.hitPath, r.URL.Path)
		if r.URL.Path == "/releases/latest" {
			if f.latest == "" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Location", f.srv.URL+"/releases/tag/"+f.latest)
			w.WriteHeader(http.StatusFound)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/releases/download/")
		b, ok := f.assets[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// archiveName is the archive this laptop would download for tag.
func archiveName(tag string) string {
	return fmt.Sprintf("repose_%s_%s_%s.tar.gz", tag, runtime.GOOS, runtime.GOARCH)
}

// publish makes tag's archive, checksums.txt and checksums.txt.sig. The
// binary inside prints `repose <says> (herakraft)`, so a release whose
// binary is the wrong one can be told from one that will not start.
func (f *fakeRelease) publish(tag, says string) {
	f.t.Helper()
	script := "#!/bin/sh\necho \"repose " + says + " (herakraft)\"\n"
	f.assets[tag+"/"+archiveName(tag)] = tarGz(f.t, "repose", []byte(script))
	f.sign(tag)
}

// sign rewrites tag's checksums.txt from whatever assets it has, and
// signs it.
func (f *fakeRelease) sign(tag string) {
	f.t.Helper()
	var lines []string
	for key, b := range f.assets {
		name, ok := strings.CutPrefix(key, tag+"/")
		if !ok || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}
		sum := sha256.Sum256(b)
		lines = append(lines, hex.EncodeToString(sum[:])+"  "+name)
	}
	checksums := []byte(strings.Join(lines, "\n") + "\n")
	f.assets[tag+"/checksums.txt"] = checksums
	digest := sha256.Sum256(checksums)
	sig, err := ecdsa.SignASN1(rand.Reader, f.key, digest[:])
	if err != nil {
		f.t.Fatal(err)
	}
	f.assets[tag+"/checksums.txt.sig"] = sig
}

func tarGz(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// install puts a binary at <dir>/repose and points the command at it.
func installFakeCLI(t *testing.T, f *fakeRelease, version string) (dir, exe string) {
	t.Helper()
	dir = t.TempDir()
	exe = filepath.Join(dir, "repose")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho \"repose "+version+" (herakraft)\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envInGuest, "")
	t.Setenv("PATH", dir) // nothing else named repose comes first
	t.Setenv("REPOSE_TEST_GOOS", runtime.GOOS)
	prevVersion, prevExe := cliVersion, executablePath
	prevLatest, prevDownload, prevKey := releaseLatestURL, releaseDownloadURL, releasePubKey
	t.Cleanup(func() {
		cliVersion, executablePath = prevVersion, prevExe
		releaseLatestURL, releaseDownloadURL, releasePubKey = prevLatest, prevDownload, prevKey
	})
	cliVersion = version
	executablePath = func() string { return exe }
	releaseLatestURL = f.srv.URL + "/releases/latest"
	releaseDownloadURL = f.srv.URL + "/releases/download"
	releasePubKey = f.pubPEM
	return dir, exe
}

func sha256File(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func runUpdate(t *testing.T, target string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errw bytes.Buffer
	err := VersionUpdateCmd(context.Background(), &out, &errw, target)
	code = ExitOK
	if err != nil {
		var ex *exitError
		if !errors.As(err, &ex) {
			t.Fatalf("not an exitError: %v", err)
		}
		code = ex.code
		if ex.msg != "" {
			errw.WriteString(ex.msg + "\n")
		}
	}
	return out.String(), errw.String(), code
}

// `repose version update` replaces the running binary when the signature
// on checksums.txt and the archive's own checksum both pass, and only
// then (DECISIONS I-1086).
func TestVersionUpdateReplacesTheBinary(t *testing.T) {
	f := newFakeRelease(t)
	f.publish("v0.1.44", "0.1.44")
	f.latest = "v0.1.44"
	_, exe := installFakeCLI(t, f, "0.1.43")
	before := sha256File(t, exe)

	out, _, code := runUpdate(t, "")
	if code != ExitOK {
		t.Fatalf("exit %d, %q", code, out)
	}
	if want := "Updated " + exe + " from 0.1.43 to 0.1.44.\n"; out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if sha256File(t, exe) == before {
		t.Fatal("the binary was not replaced")
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "0.1.44") {
		t.Fatalf("the new binary is %q", b)
	}
	fi, err := os.Stat(exe)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v (%v)", fi.Mode().Perm(), err)
	}
	// No leftovers beside it.
	ents, _ := os.ReadDir(filepath.Dir(exe))
	if len(ents) != 1 {
		t.Fatalf("%d files beside the binary", len(ents))
	}
}

func TestVersionUpdateAlreadyTheLatest(t *testing.T) {
	f := newFakeRelease(t)
	f.publish("v0.1.44", "0.1.44")
	f.latest = "v0.1.44"
	_, exe := installFakeCLI(t, f, "0.1.44")
	before := sha256File(t, exe)
	out, _, code := runUpdate(t, "")
	if code != ExitOK || out != "repose 0.1.44 is the latest release.\n" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if sha256File(t, exe) != before {
		t.Fatal("the binary changed")
	}
}

func TestVersionUpdateRollsBack(t *testing.T) {
	f := newFakeRelease(t)
	f.publish("v0.1.40", "0.1.40")
	f.publish("v0.1.44", "0.1.44")
	f.latest = "v0.1.44"
	_, exe := installFakeCLI(t, f, "0.1.44")
	out, _, code := runUpdate(t, "v0.1.40")
	if code != ExitOK {
		t.Fatalf("exit %d, %q", code, out)
	}
	if want := "Updated " + exe + " from 0.1.44 to 0.1.40.\n"; out != want {
		t.Fatalf("stdout %q, want %q", out, want)
	}
	if b, _ := os.ReadFile(exe); !strings.Contains(string(b), "0.1.40") {
		t.Fatalf("the new binary is %q", b)
	}
	// The latest redirect is never read when a version is named.
	for _, p := range f.hitPath {
		if p == "/releases/latest" {
			t.Error("a named version read the latest redirect")
		}
	}
}

// Each refusal leaves the old binary byte for byte as it was.
func TestVersionUpdateRefusesAndKeepsTheBinary(t *testing.T) {
	for _, c := range []struct {
		name   string
		break_ func(f *fakeRelease)
		target string
		code   int
		want   string
	}{
		{
			name:   "no signature",
			break_: func(f *fakeRelease) { delete(f.assets, "v0.1.44/checksums.txt.sig") },
			code:   ExitGeneric,
			want:   "Not updated: release v0.1.44 has no signature (checksums.txt.sig).",
		},
		{
			name: "bad signature",
			break_: func(f *fakeRelease) {
				other := newFakeRelease(f.t)
				sum := sha256.Sum256(f.assets["v0.1.44/checksums.txt"])
				sig, err := ecdsa.SignASN1(rand.Reader, other.key, sum[:])
				if err != nil {
					f.t.Fatal(err)
				}
				f.assets["v0.1.44/checksums.txt.sig"] = sig
			},
			code: ExitGeneric,
			want: "Not updated: the signature on checksums.txt of v0.1.44 does not verify.",
		},
		{
			name: "archive does not match checksums.txt",
			break_: func(f *fakeRelease) {
				// The archive is changed after checksums.txt was signed, so
				// the signature still passes and only the hash catches it.
				f.assets["v0.1.44/"+archiveName("v0.1.44")] = tarGz(f.t, "repose", []byte("#!/bin/sh\necho \"repose 0.1.44 (herakraft)\"\n# tampered\n"))
			},
			code: ExitGeneric,
			want: "Not updated: " + archiveName("v0.1.44") + " does not match checksums.txt.",
		},
		{
			name: "the new binary does not start",
			break_: func(f *fakeRelease) {
				f.assets["v0.1.44/"+archiveName("v0.1.44")] = tarGz(f.t, "repose", []byte("\x7fELF not really\n"))
				f.sign("v0.1.44")
			},
			code: ExitGeneric,
			want: "Not updated: the downloaded repose did not start",
		},
		{
			name: "the new binary is another version",
			break_: func(f *fakeRelease) {
				f.assets["v0.1.44/"+archiveName("v0.1.44")] = tarGz(f.t, "repose", []byte("#!/bin/sh\necho \"repose 0.1.41 (herakraft)\"\n"))
				f.sign("v0.1.44")
			},
			code: ExitGeneric,
			want: `Not updated: the downloaded repose says "repose 0.1.41 (herakraft)", not 0.1.44.`,
		},
		{
			name:   "no such release",
			target: "v0.1.99",
			code:   ExitGeneric,
			want:   "There is no release v0.1.99.",
		},
		{
			name:   "before releases were signed",
			target: "v0.1.20",
			code:   ExitUsage,
			want:   "v0.1.20 came out before releases were signed. Install it with install.sh --version v0.1.20.",
		},
		{
			name:   "not a release tag",
			target: "latest",
			code:   ExitUsage,
			want:   "latest is not a release tag, which looks like v0.1.40.",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeRelease(t)
			f.publish("v0.1.44", "0.1.44")
			f.latest = "v0.1.44"
			if c.break_ != nil {
				c.break_(f)
			}
			_, exe := installFakeCLI(t, f, "0.1.43")
			before := sha256File(t, exe)
			out, errw, code := runUpdate(t, c.target)
			if code != c.code {
				t.Errorf("exit %d, want %d (%q %q)", code, c.code, out, errw)
			}
			if !strings.Contains(errw, c.want) {
				t.Errorf("stderr %q, want %q", errw, c.want)
			}
			if out != "" {
				t.Errorf("stdout %q on a refusal", out)
			}
			if got := sha256File(t, exe); got != before {
				t.Errorf("the binary changed: %s → %s", before, got)
			}
			if ents, _ := os.ReadDir(filepath.Dir(exe)); len(ents) != 1 {
				t.Errorf("%d files beside the binary", len(ents))
			}
		})
	}
}

// The refusals that touch no network: a machine, a development build, a
// binary a package manager owns.
func TestVersionUpdateRefusesBeforeTheNetwork(t *testing.T) {
	f := newFakeRelease(t)
	f.publish("v0.1.44", "0.1.44")
	f.latest = "v0.1.44"

	t.Run("on a machine", func(t *testing.T) {
		installFakeCLI(t, f, "0.1.43")
		t.Setenv(envInGuest, "1")
		_, errw, code := runUpdate(t, "")
		if code != ExitUsage || !strings.Contains(errw, "The repose on a machine comes with its base and updates with it.") {
			t.Fatalf("exit %d, %q", code, errw)
		}
	})
	t.Run("development build", func(t *testing.T) {
		installFakeCLI(t, f, "dev")
		_, errw, code := runUpdate(t, "")
		if code != ExitUsage || !strings.Contains(errw, "This repose is a development build (dev), so there is no release to update it to.") {
			t.Fatalf("exit %d, %q", code, errw)
		}
	})
	t.Run("nix store", func(t *testing.T) {
		installFakeCLI(t, f, "0.1.43")
		executablePath = func() string { return "/nix/store/abc-repose-0.1.43/bin/repose" }
		_, errw, code := runUpdate(t, "")
		if code != ExitGeneric || !strings.Contains(errw, "/nix/store/abc-repose-0.1.43/bin/repose belongs to Nix. Update it with Nix.") {
			t.Fatalf("exit %d, %q", code, errw)
		}
	})
	t.Run("windows", func(t *testing.T) {
		installFakeCLI(t, f, "0.1.43")
		t.Setenv("REPOSE_TEST_GOOS", "windows")
		_, errw, code := runUpdate(t, "")
		if code != ExitGeneric || !strings.Contains(errw, "repose has no Windows release. Install it inside WSL: https://repose.herakraft.co/docs/install") {
			t.Fatalf("exit %d, %q", code, errw)
		}
	})
	if len(f.hitPath) != 0 {
		t.Errorf("a refusal that needs no network read %v", f.hitPath)
	}
}

func TestVersionUpdateNamesAnotherReposeFirstOnPATH(t *testing.T) {
	f := newFakeRelease(t)
	f.publish("v0.1.44", "0.1.44")
	f.latest = "v0.1.44"
	_, exe := installFakeCLI(t, f, "0.1.43")
	other := t.TempDir()
	if err := os.WriteFile(filepath.Join(other, "repose"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", other+string(os.PathListSeparator)+filepath.Dir(exe))
	out, _, code := runUpdate(t, "")
	want := "Another repose is first on your PATH, at " + filepath.Join(other, "repose") + "; this one is at " + exe + ".\n"
	if code != ExitOK || !strings.HasSuffix(out, want) {
		t.Fatalf("exit %d, stdout %q, want it to end with %q", code, out, want)
	}
}

// A path a package manager owns is never written, so the CLI names the
// owner instead (DECISIONS I-1086).
func TestBinaryOwner(t *testing.T) {
	for _, c := range []struct{ path, owner string }{
		{"/nix/store/4k2-repose-0.1.43/bin/repose", "Nix"},
		{"/opt/homebrew/bin/repose", "Homebrew"},
		{"/opt/homebrew/Cellar/repose/0.1.43/bin/repose", "Homebrew"},
		{"/usr/local/Cellar/repose/0.1.43/bin/repose", "Homebrew"},
		{"/home/linuxbrew/.linuxbrew/bin/repose", "Homebrew"},
		{"/snap/repose/12/bin/repose", "snap"},
		{"/usr/bin/repose", "your system's package manager"},
		{"/bin/repose", "your system's package manager"},
		{"/home/a/.local/bin/repose", ""},
		{"/usr/local/bin/repose", ""},
		{"/Users/a/.local/bin/repose", ""},
		{"", ""},
	} {
		if got := binaryOwner(c.path); got != c.owner {
			t.Errorf("binaryOwner(%q) = %q, want %q", c.path, got, c.owner)
		}
	}
}

// The key compiled into the CLI is the one install.sh carries; a
// rotation that changes one and not the other fails here and in
// test/supplychain.
func TestReleaseKeyParses(t *testing.T) {
	block, _ := pem.Decode([]byte(releasePubKeyPEM))
	if block == nil {
		t.Fatal("the release key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := key.(*ecdsa.PublicKey); !ok {
		t.Fatalf("the release key is %T", key)
	}
}
