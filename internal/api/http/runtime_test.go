package httpapi_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	httpapi "github.com/heracraft/repose/internal/api/http"
	"github.com/heracraft/repose/internal/api/store"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
)

// guest_runtime at the api (DECISIONS I-1462): the Project carries it and
// pending_base_version; a create gets the setting's default and only an
// exempt account names the other; an erofs machine refuses a Nix file
// with 409 nix_config_retired and takes a menu.
func TestGuestRuntimeField(t *testing.T) {
	e := newEnv(t)
	ctx := e.h.Ctx
	tok := e.signIn(t, "sub-rt", "rt")

	// The old shape: nothing asked, nix, and the two fields present.
	r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "nixy", "class": "small"})
	if r.status != 201 || r.body["guest_runtime"] != "nix" {
		t.Fatalf("default create: %d %s", r.status, r.raw)
	}
	if v, ok := r.body["pending_base_version"]; !ok || v != nil {
		t.Fatalf("pending_base_version on a nix project: %v %v", v, ok)
	}
	e.waitOp(t, r)
	if r := e.do(t, tok, "POST", "/projects", map[string]any{"name": "bad", "class": "small", "guest_runtime": "ubuntu"}); r.status != 400 || errCode(r) != "invalid" {
		t.Fatalf("unknown runtime: %d %s", r.status, r.raw)
	}
	// An ordinary account may not pick the other runtime.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "ero", "class": "small", "guest_runtime": "erofs"})
	if d, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); r.status != 403 || d["reason"] != "guest_runtime" {
		t.Fatalf("non-exempt erofs: %d %s", r.status, r.raw)
	}
	if _, err := e.h.Pool.Exec(ctx, "update users set billing_status = 'exempt' where handle = 'rt'"); err != nil {
		t.Fatal(err)
	}
	// Exempt, but no host runs erofs yet: 503 capacity, no_runtime_host.
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "ero", "class": "small", "guest_runtime": "erofs"})
	if d, _ := r.body["error"].(map[string]any)["detail"].(map[string]any); r.status != 503 || d["reason"] != "no_runtime_host" {
		t.Fatalf("erofs with no erofs host: %d %s", r.status, r.raw)
	}
	// Each fake host numbers its guests from the same address, which two
	// real hosts never do (each has its own /22): the nix machine lets go
	// of its address so the erofs host's first guest can take it.
	if _, err := e.h.Pool.Exec(ctx, "update projects set guest_ip = null"); err != nil {
		t.Fatal(err)
	}
	erofsHost, _ := e.h.AddHost(t, "erofs-01", fakehostd.Options{Erofs: true, Arch: "x86_64"})
	e.h.WaitFor("erofs host", func() bool {
		var ok bool
		err := e.h.Pool.QueryRow(ctx, "select arch = 'x86_64' and 'erofs' = any(features) from hosts where id = $1", erofsHost).Scan(&ok)
		return err == nil && ok
	})
	s := strings.Repeat("d", 64)
	if _, err := e.h.Pool.Exec(ctx, `insert into base_versions (version, nix_rev, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256,
		initramfs_amd64_url, initramfs_amd64_sha256, kernel_version, manifest_url) values ('2026.11.22.1', '', 'erofs', 'https://x.test/r', $1, 'https://x.test/k', $1, 'https://x.test/i', $1, '6.8.0', 'https://x.test/m')`, s); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "ero", "class": "small", "guest_runtime": "erofs"})
	if r.status != 201 || r.body["guest_runtime"] != "erofs" || r.body["host_id"] != erofsHost.String() {
		t.Fatalf("exempt erofs: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("erofs create: %+v", op.Error)
	}
	pid := r.body["id"].(string)
	p := e.h.Project(uuid.MustParse(pid))
	if p.BaseVersion == nil || *p.BaseVersion != "2026.11.22.1" || p.State != "running" {
		t.Fatalf("erofs project: %+v", p)
	}
	// A Nix file is retired on it; a menu is taken.
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"fragment": "{ pkgs, ... }: { }"})
	if r.status != 409 || errCode(r) != "nix_config_retired" || r.body["error"].(map[string]any)["message"] != httpapi.MsgNixConfigRetired {
		t.Fatalf("fragment on erofs: %d %s", r.status, r.raw)
	}
	r = e.do(t, tok, "PUT", "/projects/"+pid+"/config", map[string]any{"menu": []map[string]any{{"id": "postgresql"}}})
	if r.status != 202 {
		t.Fatalf("menu on erofs: %d %s", r.status, r.raw)
	}
	if op := e.waitOp(t, r); op.State != "done" {
		t.Fatalf("menu op: %+v", op.Error)
	}
	if rev, _ := store.GetRevision(ctx, e.h.Pool, *e.h.Project(uuid.MustParse(pid)).ConfigRevisionID); rev.Status != "applied" || rev.Fragment != "" {
		t.Fatalf("menu revision: %+v", rev)
	}
	// The listing carries both fields too.
	r = e.do(t, tok, "GET", "/projects", nil)
	seen := 0
	for _, x := range r.list {
		m := x.(map[string]any)
		if _, ok := m["pending_base_version"]; ok && (m["guest_runtime"] == "nix" || m["guest_runtime"] == "erofs") {
			seen++
		}
	}
	if seen != 2 {
		t.Fatalf("list fields on %d of 2 projects: %s", seen, r.raw)
	}
	// The environment's default reaches a plain create.
	if err := store.SetSetting(ctx, e.h.Pool, store.SettingDefaultGuestRuntime, "erofs"); err != nil {
		t.Fatal(err)
	}
	r = e.do(t, tok, "POST", "/projects", map[string]any{"name": "ero2", "class": "small"})
	if r.status != 201 || r.body["guest_runtime"] != "erofs" {
		t.Fatalf("default erofs: %d %s", r.status, r.raw)
	}
}
