package admin_test

import (
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/admin"
	"github.com/heracraft/repose/internal/api/apitest"
)

// `projects show` prints the last error's text, not the pointer to it
// (production, 2026-11-22: "last error 0x394153d2160").
func TestProjectsShowPrintsLastError(t *testing.T) {
	h := apitest.New(t, apitest.Options{})
	u := h.NewUser("show")
	p := h.CreateRunning(u, "live1g")
	if _, err := h.Pool.Exec(h.Ctx, "update projects set last_error = 'host_unreachable: the host stopped answering' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	e := &admin.Env{KV: h.KV, Actor: "admin:test"}
	e.SetPool(h.Pool)
	out, err := run(t, e, "projects", "show", p.ID.String())
	if err != nil || !strings.Contains(out, "host_unreachable: the host stopped answering") || strings.Contains(out, "0x") {
		t.Fatalf("projects show: %s %v", out, err)
	}
}
