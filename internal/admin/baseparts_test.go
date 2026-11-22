package admin_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/store"
)

// A rootfs.erofs published as parts (DECISIONS I-1504): the whole-file
// URL answers 404, <url>.part0 answers 200 and SHA256SUMS lists the parts.
// The publish accepts it and keeps the whole-file URL and digest; a
// 404 with no part0, or a part0 that SHA256SUMS does not list, is still
// refused.
func TestBasePublishErofsAcceptsParts(t *testing.T) {
	whole := strings.Repeat("r", 3000)
	part0, part1 := whole[:2000], whole[2000:]
	kernel, initrd := "kernel bytes", "initramfs bytes"
	listParts := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/rel/")
		var body string
		switch name {
		case "rootfs.erofs.part0":
			body = part0
		case "rootfs.erofs.part1":
			body = part1
		case "vmlinuz":
			body = kernel
		case "initramfs.img":
			body = initrd
		case "SHA256SUMS":
			var b strings.Builder
			if listParts {
				fmt.Fprintf(&b, "%s  rootfs.erofs.part0\n%s  rootfs.erofs.part1\n", sum(part0), sum(part1))
			}
			fmt.Fprintf(&b, "%s  vmlinuz\n%s  initramfs.img\n", sum(kernel), sum(initrd))
			body = b.String()
		case "amd64-manifest.json":
			body = fmt.Sprintf(`{"version": "2026.11.22.8", "files": [{"name": "rootfs.erofs", "sha256": %q, "parts": [{"name": "rootfs.erofs.part0", "sha256": %q}, {"name": "rootfs.erofs.part1", "sha256": %q}]}, {"name": "vmlinuz", "sha256": %q}, {"name": "initramfs.img", "sha256": %q}]}`,
				sum(whole), sum(part0), sum(part1), sum(kernel), sum(initrd))
		default:
			http.NotFound(w, r) // the whole rootfs.erofs, among others
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	h := apitest.New(t, apitest.Options{NoHost: true})
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	rel := srv.URL + "/rel/"
	args := func(rootfs string) []string {
		return []string{"base", "publish", "--format", "erofs", "--version", "2026.11.22.8", "--changelog", "parts",
			"--amd64-url", rootfs, "--amd64-sha256", sum(whole),
			"--amd64-kernel-url", rel + "vmlinuz", "--amd64-kernel-sha256", sum(kernel),
			"--amd64-initramfs-url", rel + "initramfs.img", "--amd64-initramfs-sha256", sum(initrd),
			"--kernel-version", "6.8.0-49-generic", "--manifest-url", rel + "amd64-manifest.json"}
	}
	// A file with neither a whole nor a part0 is still a 404.
	if out, err := run(t, e, args(rel+"missing.erofs")...); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("no whole, no parts: %s %v", out, err)
	}
	// part0 served but not in SHA256SUMS: refused.
	listParts = false
	if out, err := run(t, e, args(rel+"rootfs.erofs")...); err == nil || !strings.Contains(err.Error(), "part0") {
		t.Fatalf("unlisted parts: %s %v", out, err)
	}
	listParts = true
	out, err := run(t, e, args(rel+"rootfs.erofs")...)
	if err != nil || !strings.Contains(out, "base 2026.11.22.8 (erofs") {
		t.Fatalf("parts: %s %v", out, err)
	}
	b, err := store.GetBase(h.Ctx, h.Pool, "2026.11.22.8")
	if err != nil || b.ImageAmd64URL == nil || *b.ImageAmd64URL != rel+"rootfs.erofs" || *b.ImageAmd64SHA256 != sum(whole) {
		t.Fatalf("row keeps the whole-file URL and digest: %+v %v", b, err)
	}
}
