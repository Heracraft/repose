// Package appliance holds the tests of the host scripts under host/bin
// that have logic worth a test: repose-hostd-update's channel lookup
// (DECISIONS I-1496). The scripts need bash, curl, jq, openssl and
// coreutils, which every host has.
package appliance

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeHostd is a hostd that answers `hostd version` with v.
func fakeHostd(v string) []byte {
	return []byte("#!/bin/sh\necho \"hostd " + v + "\"\n")
}

type channel struct {
	t        *testing.T
	key      *ecdsa.PrivateKey
	srv      *httptest.Server
	files    map[string][]byte // path -> body
	releases []map[string]any
	status   atomic.Int32 // the releases API's answer; 0 is 200
	listed   atomic.Int32
}

func newChannel(t *testing.T) *channel {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &channel{t: t, key: key, files: map[string][]byte{}}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/Heracraft/repose/releases" {
			c.listed.Add(1)
			if s := c.status.Load(); s != 0 {
				w.WriteHeader(int(s))
				_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(c.releases)
			return
		}
		b, ok := c.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *channel) pubkeyFile() string {
	der, err := x509.MarshalPKIXPublicKey(&c.key.PublicKey)
	if err != nil {
		c.t.Fatal(err)
	}
	p := filepath.Join(c.t.TempDir(), "release.pub")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o644); err != nil {
		c.t.Fatal(err)
	}
	return p
}

// release adds hostd-<v> with both binaries and, when manifest, a signed
// manifest.json, as the CI job publishes it.
func (c *channel) release(v string, draft, manifest bool, signer *ecdsa.PrivateKey) {
	tag := "hostd-" + v
	base := c.srv.URL + "/download/" + tag + "/"
	assets := []map[string]any{}
	sums := map[string]string{}
	for _, a := range []string{"amd64", "arm64"} {
		name := "hostd-linux-" + a
		body := fakeHostd(v)
		c.files["/download/"+tag+"/"+name] = body
		s := sha256.Sum256(body)
		sums[a] = hex.EncodeToString(s[:])
		assets = append(assets, map[string]any{"name": name, "browser_download_url": base + name})
	}
	if manifest {
		payload := fmt.Sprintf("repose-hostd-manifest v1\nversion %s\namd64 %s %s\narm64 %s %s\n",
			v, sums["amd64"], base+"hostd-linux-amd64", sums["arm64"], base+"hostd-linux-arm64")
		d := sha256.Sum256([]byte(payload))
		sig, err := ecdsa.SignASN1(rand.Reader, signer, d[:])
		if err != nil {
			c.t.Fatal(err)
		}
		m, _ := json.Marshal(map[string]any{
			"version": v,
			"arches": map[string]any{
				"amd64": map[string]string{"url": base + "hostd-linux-amd64", "sha256": sums["amd64"]},
				"arm64": map[string]string{"url": base + "hostd-linux-arm64", "sha256": sums["arm64"]},
			},
			"signature": base64.StdEncoding.EncodeToString(sig),
		})
		c.files["/download/"+tag+"/manifest.json"] = m
		assets = append(assets, map[string]any{"name": "manifest.json", "browser_download_url": base + "manifest.json"})
	}
	c.releases = append(c.releases, map[string]any{"tag_name": tag, "draft": draft, "prerelease": false, "assets": assets})
}

type host struct {
	bin, state, restarted, pub string
}

func newHost(t *testing.T, running, pub string) host {
	t.Helper()
	dir := t.TempDir()
	h := host{bin: filepath.Join(dir, "hostd"), state: filepath.Join(dir, "state"), restarted: filepath.Join(dir, "restarted"), pub: pub}
	if err := os.WriteFile(h.bin, fakeHostd(running), 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h host) run(t *testing.T, c *channel, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"host/bin/repose-hostd-update"}, args...)...)
	cmd.Env = append(os.Environ(),
		"REPOSE_HOSTD_RELEASES_URL="+c.srv.URL+"/repos/Heracraft/repose/releases?per_page=30",
		"REPOSE_HOSTD_BIN="+h.bin,
		"REPOSE_HOSTD_STATE_DIR="+h.state,
		"REPOSE_RELEASE_PUBKEY_FILE="+h.pub,
		"REPOSE_HOSTD_RESTART=touch "+h.restarted,
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

func (h host) version(t *testing.T) string {
	t.Helper()
	out, err := exec.Command(h.bin, "version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (h host) wasRestarted() bool {
	_, err := os.Stat(h.restarted)
	return err == nil
}

// The channel is the newest non-draft hostd-<version> release that has a
// manifest.json, in version order (2026.11.22.10 after .9); a release
// without a manifest (hostd-2026.11.22.1, published before I-1496's
// amendment), a draft, the dead hostd-stable and every other release are
// passed over.
func TestHostdUpdateFindsTheNewestVersionedRelease(t *testing.T) {
	c := newChannel(t)
	c.release("2026.11.22.1", false, false, c.key)
	c.release("2026.11.22.9", false, true, c.key)
	c.release("2026.11.22.10", false, true, c.key)
	c.release("2026.11.22.11", true, true, c.key)
	c.releases = append(c.releases,
		map[string]any{"tag_name": "hostd-stable", "draft": false, "assets": []map[string]any{}},
		map[string]any{"tag_name": "appliance-2026.11.22.1", "draft": false, "assets": []map[string]any{}})
	h := newHost(t, "2026.11.22.9", c.pubkeyFile())

	out, code := h.run(t, c)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := h.version(t); got != "hostd 2026.11.22.10" {
		t.Fatalf("hostd is %q after the update, want 2026.11.22.10:\n%s", got, out)
	}
	if !h.wasRestarted() {
		t.Fatalf("hostd was not restarted:\n%s", out)
	}
	for _, want := range []string{"event=release_found tag=hostd-2026.11.22.10", "event=manifest_verified", "event=sha256_verified", "event=updated from=2026.11.22.9 to=2026.11.22.10"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(h.state, "manifest.json")); err != nil {
		t.Errorf("the verified manifest is not cached: %v", err)
	}
}

// A rate-limited lookup (403, or 429) keeps the last good manifest: a
// host already on it does nothing and hostd is not restarted, and the
// unit does not fail for it. With nothing cached it says so and exits 0.
func TestHostdUpdateRateLimitKeepsTheCachedManifest(t *testing.T) {
	c := newChannel(t)
	c.release("2026.11.22.10", false, true, c.key)
	h := newHost(t, "2026.11.22.9", c.pubkeyFile())
	if out, code := h.run(t, c); code != 0 {
		t.Fatalf("first run: exit %d:\n%s", code, out)
	}
	_ = os.Remove(h.restarted)

	c.status.Store(http.StatusForbidden)
	out, code := h.run(t, c)
	if code != 0 {
		t.Fatalf("a 403 lookup exited %d:\n%s", code, out)
	}
	if !strings.Contains(out, "event=lookup_failed status=403") || !strings.Contains(out, "event=up_to_date version=2026.11.22.10") {
		t.Fatalf("a 403 did not fall back to the cached manifest:\n%s", out)
	}
	if h.wasRestarted() {
		t.Fatal("a 403 restarted hostd")
	}

	fresh := newHost(t, "2026.11.22.9", c.pubkeyFile())
	c.status.Store(http.StatusTooManyRequests)
	out, code = fresh.run(t, c)
	if code != 0 || !strings.Contains(out, "event=lookup_failed status=429") || !strings.Contains(out, "reason=no_cached_manifest") {
		t.Fatalf("a 429 with nothing cached: exit %d:\n%s", code, out)
	}
	if fresh.wasRestarted() || fresh.version(t) != "hostd 2026.11.22.9" {
		t.Fatal("a 429 with nothing cached changed hostd")
	}
}

// The lookup is not the trust: a manifest signed by another key is
// refused, nothing is swapped, nothing is cached.
func TestHostdUpdateRefusesAForeignSignature(t *testing.T) {
	c := newChannel(t)
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c.release("2026.11.22.10", false, true, other)
	h := newHost(t, "2026.11.22.9", c.pubkeyFile())
	out, code := h.run(t, c)
	if code == 0 || !strings.Contains(out, "event=update_refused reason=signature") {
		t.Fatalf("a foreign signature: exit %d:\n%s", code, out)
	}
	if h.wasRestarted() || h.version(t) != "hostd 2026.11.22.9" {
		t.Fatal("a foreign signature changed hostd")
	}
	if _, err := os.Stat(filepath.Join(h.state, "manifest.json")); err == nil {
		t.Fatal("an unverified manifest was cached")
	}
}

// Version order, not string order: 2026.11.23 is newer than
// 2026.11.22.10, which is newer than 2026.11.22.9.
func TestHostdUpdateOrdersVersionsNotStrings(t *testing.T) {
	c := newChannel(t)
	c.release("2026.11.22.10", false, true, c.key)
	c.release("2026.11.23", false, true, c.key)
	c.release("2026.11.22.9", false, true, c.key)
	h := newHost(t, "2026.11.22.1", c.pubkeyFile())
	out, code := h.run(t, c)
	if code != 0 || h.version(t) != "hostd 2026.11.23" {
		t.Fatalf("picked %q, want 2026.11.23 (exit %d):\n%s", h.version(t), code, out)
	}
}
