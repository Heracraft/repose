package admin_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/store"
)

func sum(b string) string {
	s := sha256.Sum256([]byte(b))
	return hex.EncodeToString(s[:])
}

// `base publish --format erofs` fetches every file before it writes the
// row (DECISIONS I-1464): a 404 is refused, a digest that SHA256SUMS or
// the manifest contradicts is refused, and a release that checks out is
// written with its format, which `base list` then shows.
func TestBasePublishErofsChecksFiles(t *testing.T) {
	files := map[string]string{"rootfs.erofs": "rootfs bytes", "vmlinuz": "kernel bytes", "initramfs.img": "initramfs bytes"}
	var sums strings.Builder
	for n, b := range files {
		fmt.Fprintf(&sums, "%s  %s\n", sum(b), n)
	}
	manifest := fmt.Sprintf(`{"version": "2026.11.22.1", "arch": "amd64", "kernel_version": "6.8.0-49-generic",
		"files": [{"name": "rootfs.erofs", "sha256": %q}, {"name": "vmlinuz", "sha256": %q}, {"name": "initramfs.img", "sha256": %q}]}`,
		sum(files["rootfs.erofs"]), sum(files["vmlinuz"]), sum(files["initramfs.img"]))
	headRefused := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Errorf("publish sent credentials to %s", r.URL)
		}
		name := strings.TrimPrefix(r.URL.Path, "/rel/")
		if r.URL.Path == "/moved/rootfs.erofs" {
			http.Redirect(w, r, "/rel/rootfs.erofs", http.StatusFound)
			return
		}
		if r.Method == http.MethodHead && headRefused {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body string
		switch {
		case name == "SHA256SUMS":
			body = sums.String()
		case name == "manifest.json":
			body = manifest
		case files[name] != "":
			body = files[name]
		default:
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") == "bytes=0-0" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte(body[:1]))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	h := apitest.New(t, apitest.Options{NoHost: true})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	rel := srv.URL + "/rel/"
	args := func(version string, over map[string]string) []string {
		a := map[string]string{
			"--amd64-url": rel + "rootfs.erofs", "--amd64-sha256": sum(files["rootfs.erofs"]),
			"--amd64-kernel-url": rel + "vmlinuz", "--amd64-kernel-sha256": sum(files["vmlinuz"]),
			"--amd64-initramfs-url": rel + "initramfs.img", "--amd64-initramfs-sha256": sum(files["initramfs.img"]),
			"--kernel-version": "6.8.0-49-generic", "--manifest-url": rel + "manifest.json",
		}
		for k, v := range over {
			a[k] = v
		}
		out := []string{"base", "publish", "--format", "erofs", "--version", version, "--changelog", "ubuntu base"}
		for k, v := range a {
			out = append(out, k, v)
		}
		return out
	}
	rows := func() int {
		var n int
		_ = h.Pool.QueryRow(h.Ctx, "select count(*) from base_versions where format = 'erofs'").Scan(&n)
		return n
	}
	// A 404 is refused before the insert.
	out, err := run(t, e, args("2026.11.22.1", map[string]string{"--amd64-kernel-url": rel + "missing"})...)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || rows() != 0 {
		t.Fatalf("404: %s %v (rows %d)", out, err, rows())
	}
	// A digest SHA256SUMS contradicts is refused.
	wrong := sum("something else")
	out, err = run(t, e, args("2026.11.22.1", map[string]string{"--amd64-sha256": wrong})...)
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS says") || rows() != 0 {
		t.Fatalf("wrong sha256: %s %v", out, err)
	}
	// A manifest for another version is refused.
	out, err = run(t, e, args("2026.11.23.1", nil)...)
	if err == nil || !strings.Contains(err.Error(), "for version 2026.11.22.1") || rows() != 0 {
		t.Fatalf("manifest version: %s %v", out, err)
	}
	// Missing flags are usage errors.
	if _, err := run(t, e, args("2026.11.22.1", map[string]string{"--manifest-url": ""})...); err == nil || !strings.Contains(err.Error(), "--manifest-url") {
		t.Fatalf("no manifest: %v", err)
	}
	// A good release, through a redirect and with HEAD refused (the ranged
	// GET answers 206), is written.
	headRefused = true
	out, err = run(t, e, args("2026.11.22.1", map[string]string{"--amd64-url": srv.URL + "/moved/rootfs.erofs"})...)
	if err != nil || !strings.Contains(out, "base 2026.11.22.1 (erofs, amd64, kernel 6.8.0-49-generic) published") {
		t.Fatalf("good release: %s %v", out, err)
	}
	b, err := store.GetBase(h.Ctx, h.Pool, "2026.11.22.1")
	if err != nil || b.Format != "erofs" || b.KernelAmd64SHA256 == nil || *b.KernelAmd64SHA256 != sum(files["vmlinuz"]) || b.ImageArm64URL != nil {
		t.Fatalf("row: %+v %v", b, err)
	}
	// A Nix project's newest base is still the Nix one: none here.
	if _, err := store.LatestBase(h.Ctx, h.Pool); err == nil {
		t.Fatal("LatestBase returned an erofs row")
	}
	out, err = run(t, e, "base", "list")
	if err != nil || !strings.Contains(out, "FORMAT") || !strings.Contains(out, "erofs") {
		t.Fatalf("base list: %s %v", out, err)
	}
	// The runtime setting.
	if _, err := run(t, e, "settings", "set", "default_guest_runtime", "ubuntu"); err == nil {
		t.Fatal("settings set took a value outside nix|erofs")
	}
	if out, err := run(t, e, "settings", "set", "default_guest_runtime", "erofs"); err != nil || !strings.Contains(out, "default_guest_runtime = erofs") {
		t.Fatalf("settings set: %s %v", out, err)
	}
	if r, err := store.DefaultGuestRuntime(h.Ctx, h.Pool); err != nil || r != "erofs" {
		t.Fatalf("default runtime %q %v", r, err)
	}
	if _, err := run(t, e, "settings", "set", "edge_wg_pubkey", "x"); err == nil {
		t.Fatal("settings set wrote a key another command owns")
	}
}
