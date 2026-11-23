package ops_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/heracraft/repose/internal/api/apitest"
	"github.com/heracraft/repose/internal/api/ops"
	"github.com/heracraft/repose/internal/api/store"
	"github.com/heracraft/repose/internal/db"
	fakehostd "github.com/heracraft/repose/internal/fakes/hostd"
	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

// Every plan an op can be enqueued with, for both runtimes (DECISIONS
// I-1464): an erofs project never builds, and a Nix project's plans are
// what they were before erofs existed.
func TestPlanShapesByRuntime(t *testing.T) {
	gid, hid := uuid.New(), uuid.New()
	nixNew := &store.Project{GuestRuntime: "nix"}
	erofsNew := &store.Project{GuestRuntime: "erofs"}
	nixPlaced := &store.Project{GuestRuntime: "nix", GuestID: &gid, HostID: &hid, State: "stopped"}
	erofsPlaced := &store.Project{GuestRuntime: "erofs", GuestID: &gid, HostID: &hid, State: "stopped"}
	legacy := &store.Project{} // a row read before 0051 has no runtime: nix
	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"PlanCreate (nix, unchanged)", ops.PlanCreate(), []string{"build", "create_guest"}},
		{"PlanCreateFor nix", ops.PlanCreateFor("nix"), []string{"build", "create_guest"}},
		{"PlanCreateFor empty is nix", ops.PlanCreateFor(""), []string{"build", "create_guest"}},
		{"PlanCreateFor erofs", ops.PlanCreateFor("erofs"), []string{"create_guest"}},
		{"PlanRestore nix new, no closure, start", ops.PlanRestore(nixNew, false, true), []string{"build", "restore", "start_guest"}},
		{"PlanRestore nix placed, closure, no start", ops.PlanRestore(nixPlaced, true, false), []string{"destroy_guest", "restore"}},
		{"PlanRestore legacy row, no closure", ops.PlanRestore(legacy, false, false), []string{"build", "restore"}},
		{"PlanRestore erofs new, no closure, start", ops.PlanRestore(erofsNew, false, true), []string{"restore", "start_guest"}},
		{"PlanRestore erofs placed, no closure, start", ops.PlanRestore(erofsPlaced, false, true), []string{"destroy_guest", "restore", "start_guest"}},
		{"PlanBuild running (nix, unchanged)", ops.PlanBuild(true), []string{"build", "wake", "apply_config"}},
		{"PlanBuild stopped (nix, unchanged)", ops.PlanBuild(false), []string{"build"}},
		{"PlanBuildFor nix running", ops.PlanBuildFor(nixPlaced, true), []string{"build", "wake", "apply_config"}},
		{"PlanBuildFor nix stopped", ops.PlanBuildFor(nixPlaced, false), []string{"build"}},
		{"PlanBuildFor erofs running", ops.PlanBuildFor(erofsPlaced, true), []string{"wake", "apply_config"}},
		{"PlanBuildFor erofs stopped", ops.PlanBuildFor(erofsPlaced, false), []string{"wake", "apply_config"}},
		{"PlanStartFor nix pending", ops.PlanStartFor(nixPlaced, true), []string{"build", "start_guest", "apply_config"}},
		{"PlanStartFor nix none", ops.PlanStartFor(nixPlaced, false), []string{"start_guest"}},
		{"PlanStartFor erofs pending", ops.PlanStartFor(erofsPlaced, true), []string{"start_guest"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !slices.Equal(c.got, c.want) {
				t.Fatalf("got %v, want %v", c.got, c.want)
			}
			if strings.Contains(c.name, "erofs") && slices.Contains(c.got, ops.PhaseBuild) {
				t.Fatalf("an erofs plan builds: %v", c.got)
			}
		})
	}
}

const (
	sumA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sumB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sumC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// publishErofs writes an erofs base row with amd64 files, as `repose-admin
// base publish --format erofs` does once the files check out.
func publishErofs(t *testing.T, h *apitest.Harness, version string) {
	t.Helper()
	u := "https://example.test/guest-" + version + "/"
	if _, err := h.Pool.Exec(h.Ctx, `insert into base_versions (version, nix_rev, format, image_amd64_url, image_amd64_sha256, kernel_amd64_url, kernel_amd64_sha256,
		initramfs_amd64_url, initramfs_amd64_sha256, kernel_version, manifest_url, released_at)
		values ($1, '', 'erofs', $2, $3, $4, $5, $6, $7, '6.8.0-49-generic', $8, now())`,
		version, u+"rootfs.erofs", sumA, u+"vmlinuz", sumB, u+"initramfs.img", sumC, u+"manifest.json"); err != nil {
		t.Fatal(err)
	}
}

// newErofsProject is NewProject for an erofs machine: no fragment, the
// first revision built from an empty menu.
func newErofsProject(t *testing.T, h *apitest.Harness, u *store.User, name string) *store.Project {
	t.Helper()
	p := h.NewProject(u, name, "large")
	if _, err := h.Pool.Exec(h.Ctx, "update projects set guest_runtime = 'erofs' where id = $1", p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Pool.Exec(h.Ctx, `update config_revisions set fragment = '', menu = '{"selection": []}' where id = $1`, *p.ConfigRevisionID); err != nil {
		t.Fatal(err)
	}
	return h.Project(p.ID)
}

func erofsHarness(t *testing.T) *apitest.Harness {
	return apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Erofs: true, Arch: "x86_64"}})
}

// An erofs project's whole life on the engine (DECISIONS I-1458, I-1464):
// a create with a BaseImage and no Build, a menu change applied in place,
// a base bump's pending base booted by the next start.
func TestErofsCreateMenuAndStart(t *testing.T) {
	h := erofsHarness(t)
	h.WaitFor("host arch from Hello", func() bool {
		hr, err := store.GetHost(h.Ctx, h.Pool, h.HostID)
		return err == nil && hr.Arch != nil && *hr.Arch == "x86_64"
	})
	publishErofs(t, h, "2026.11.22.1")
	u := h.NewUser("erin")
	p := newErofsProject(t, h, u, "ubu")
	pid := p.ID
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreateFor("erofs")}))
	if op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	var create *hostdv1.CreateGuest
	for _, c := range h.Fake.Commands() {
		if c.GetBuild() != nil {
			t.Fatal("an erofs create sent a Build")
		}
		if cg := c.GetCreateGuest(); cg != nil {
			create = cg
		}
	}
	if create == nil || create.SystemClosure != "" || create.BaseImage == nil {
		t.Fatalf("CreateGuest: %+v", create)
	}
	bi := create.BaseImage
	if bi.Version != "2026.11.22.1" || bi.Sha256 != sumA || bi.KernelSha256 != sumB || bi.InitramfsSha256 != sumC ||
		!strings.HasSuffix(bi.Url, "rootfs.erofs") || !strings.HasSuffix(bi.KernelUrl, "vmlinuz") || bi.KernelVersion != "6.8.0-49-generic" {
		t.Fatalf("BaseImage: %+v", bi)
	}
	if create.Menu == nil || create.Menu.RevisionId != p.ConfigRevisionID.String() {
		t.Fatalf("CreateGuest.menu: %+v", create.Menu)
	}
	p = h.Project(pid)
	if p.State != "running" || p.BaseVersion == nil || *p.BaseVersion != "2026.11.22.1" {
		t.Fatalf("after create: state %s base %v", p.State, p.BaseVersion)
	}

	// A menu change on the running machine: wake, then ApplyConfig with
	// the menu and no closure.
	rid := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, `insert into config_revisions (id, project_id, fragment, menu, status, personal, personal_opt_out)
		values ($1, $2, '', '{"selection": [{"id": "postgresql"}, {"package": "ffmpeg"}]}', 'built', '', true)`, rid, pid); err != nil {
		t.Fatal(err)
	}
	op = h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Phases: ops.PlanBuildFor(p, true)}))
	if op.State != "done" {
		t.Fatalf("menu apply: %+v", op.Error)
	}
	var apply *hostdv1.ApplyConfig
	for _, c := range h.Fake.Commands() {
		if a := c.GetApplyConfig(); a != nil {
			apply = a
		}
	}
	if apply == nil || apply.SystemClosure != "" || apply.Menu == nil || len(apply.Menu.Items) != 2 ||
		apply.Menu.Items[0].Id != "postgresql" || apply.Menu.Items[1].Package != "ffmpeg" {
		t.Fatalf("ApplyConfig: %+v", apply)
	}
	// guestd's lines, which hostd forwards as BuildLog under the
	// command's id, are the op's log (DECISIONS I-1536): an apply_config
	// command is bound to its op as a build's is.
	assertOpLog(t, h, op.ID, "installing postgresql", "postgresql ready", "installing ffmpeg", "ffmpeg installed")
	rev, _ := store.GetRevision(h.Ctx, h.Pool, rid)
	if rev.Status != "applied" || *h.Project(pid).ConfigRevisionID != rid {
		t.Fatalf("revision after apply: %s", rev.Status)
	}

	// A newer base, marked pending as the bump does; the next start boots
	// it and clears the mark.
	publishErofs(t, h, "2026.11.29.1")
	if _, err := h.Pool.Exec(h.Ctx, "update projects set pending_base_version = '2026.11.29.1' where id = $1", pid); err != nil {
		t.Fatal(err)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStop, ProjectID: &pid, Phases: ops.PlanStop()})); op.State != "done" {
		t.Fatalf("stop: %+v", op.Error)
	}
	// The stop's snapshot carries the base it was taken on, from the wire.
	var snapBase *string
	if err := h.Pool.QueryRow(h.Ctx, "select base_version from snapshots where project_id = $1 order by taken_at desc limit 1", pid).Scan(&snapBase); err != nil || snapBase == nil || *snapBase != "2026.11.22.1" {
		t.Fatalf("snapshot base_version %v %v", snapBase, err)
	}
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindStart, ProjectID: &pid, Phases: ops.PlanStartFor(h.Project(pid), true)})); op.State != "done" {
		t.Fatalf("start: %+v", op.Error)
	}
	var start *hostdv1.StartGuest
	for _, c := range h.Fake.Commands() {
		if s := c.GetStartGuest(); s != nil {
			start = s
		}
	}
	if start == nil || start.BaseImage == nil || start.BaseImage.Version != "2026.11.29.1" || start.Menu == nil || start.Menu.RevisionId != rid.String() {
		t.Fatalf("StartGuest: %+v", start)
	}
	p = h.Project(pid)
	if p.BaseVersion == nil || *p.BaseVersion != "2026.11.29.1" || p.PendingBaseVersion != nil {
		t.Fatalf("after start: base %v pending %v", p.BaseVersion, p.PendingBaseVersion)
	}
	for _, c := range h.Fake.Commands() {
		if c.GetBuild() != nil {
			t.Fatal("an erofs project sent a Build")
		}
	}
}

// A menu that fails on the running machine fails the op with hostd's
// code and leaves the revision failed; the machine keeps running.
func TestErofsMenuFailure(t *testing.T) {
	h := erofsHarness(t)
	h.WaitFor("host arch", func() bool {
		hr, err := store.GetHost(h.Ctx, h.Pool, h.HostID)
		return err == nil && hr.Arch != nil
	})
	publishErofs(t, h, "2026.11.22.1")
	u := h.NewUser("fay")
	p := newErofsProject(t, h, u, "ubu")
	pid := p.ID
	if op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindCreate, ProjectID: &pid, Phases: ops.PlanCreateFor("erofs")})); op.State != "done" {
		t.Fatalf("create: %+v", op.Error)
	}
	h.Fake.SetMenuFail("menu_failed", "postgresql: apt-get install failed")
	rid := store.NewID()
	if _, err := h.Pool.Exec(h.Ctx, `insert into config_revisions (id, project_id, fragment, menu, status, personal, personal_opt_out)
		values ($1, $2, '', '{"selection": [{"id": "postgresql"}]}', 'built', '', true)`, rid, pid); err != nil {
		t.Fatal(err)
	}
	op := h.WaitOp(h.Enqueue(ops.NewOp{Kind: ops.KindBuild, ProjectID: &pid, RevisionID: &rid, Phases: ops.PlanBuildFor(h.Project(pid), true)}))
	if op.State != "error" {
		t.Fatalf("a failed menu finished %s", op.State)
	}
	assertOpLog(t, h, op.ID, "installing postgresql", "postgresql: apt-get install failed")
	if rev, _ := store.GetRevision(h.Ctx, h.Pool, rid); rev.Status != "failed" {
		t.Fatalf("revision %s after a failed menu", rev.Status)
	}
	if p := h.Project(pid); p.State != "running" {
		t.Fatalf("machine %s after a failed menu", p.State)
	}
}

// Placement (DECISIONS I-1464): an erofs project is refused on a host
// that does not name erofs, as no_runtime_host, and placed on one that
// does. A Nix project still places on a host that names neither.
func TestErofsPlacement(t *testing.T) {
	h := apitest.New(t, apitest.Options{FakeOpts: fakehostd.Options{Arch: "x86_64"}})
	h.WaitFor("host arch", func() bool {
		hr, err := store.GetHost(h.Ctx, h.Pool, h.HostID)
		return err == nil && hr.Arch != nil
	})
	publishErofs(t, h, "2026.11.22.1")
	u := h.NewUser("gus")
	place := func(runtime string) (*uuid.UUID, error) {
		var got *uuid.UUID
		err := db.InTx(h.Ctx, h.Pool, func(tx db.Tx) error {
			id, err := h.Engine.PlaceNew(h.Ctx, tx, &store.Project{ID: store.NewID(), UserID: u.ID, Class: "large", VolumeBytes: 40 << 30, GuestRuntime: runtime})
			got = id
			return err
		})
		return got, err
	}
	_, err := place("erofs")
	var refused *ops.PlacementRefusal
	if !errors.As(err, &refused) || refused.Detail["reason"] != "no_runtime_host" {
		t.Fatalf("erofs on a host without the feature: %v", err)
	}
	if id, err := place("nix"); err != nil || id == nil || *id != h.HostID {
		t.Fatalf("nix placement: %v %v", id, err)
	}
	erofsHost, _ := h.AddHost(t, "erofs-01", fakehostd.Options{Erofs: true, Arch: "x86_64"})
	h.WaitFor("erofs host arch", func() bool {
		var ok bool
		err := h.Pool.QueryRow(h.Ctx, "select arch is not null and 'erofs' = any(features) from hosts where id = $1", erofsHost).Scan(&ok)
		return err == nil && ok
	})
	id, err := place("erofs")
	if err != nil || id == nil || *id != erofsHost {
		t.Fatalf("erofs placement: %v %v, want %s", id, err, erofsHost)
	}
	// An aarch64 host with the feature but no arm64 image is refused too.
	if _, err := h.Pool.Exec(h.Ctx, "update hosts set arch = 'aarch64' where id = $1", erofsHost); err != nil {
		t.Fatal(err)
	}
	if _, err := place("erofs"); !errors.As(err, &refused) || refused.Detail["reason"] != "no_runtime_host" {
		t.Fatalf("erofs on an arch with no image: %v", err)
	}
}

// assertOpLog fails unless the op's stored log is want, line for line:
// guestd's lines, and no api line whose seq could take one of theirs.
func assertOpLog(t *testing.T, h *apitest.Harness, opID uuid.UUID, want ...string) {
	t.Helper()
	var got []string
	for i := 0; i < 50; i++ {
		lines, err := h.Logs.Read(h.Ctx, opID, 0, 1000)
		if err != nil {
			t.Fatal(err)
		}
		got = got[:0]
		for _, l := range lines {
			got = append(got, l.Line)
		}
		if slices.Equal(got, want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("op log %q want %q", got, want)
}

// ENG-184 (DECISIONS I-1558): an erofs machine's apply_config waits past
// hostd's own bound on an ApplyMenu, so the api never gives up on a menu
// that hostd and guestd are still applying. It used the default 10
// minutes: on the Mac `config add postgresql` failed `timeout` at 10 min
// while hostd still waited (its 20 min) and apt was still installing.
// A Nix machine's apply_config keeps its 10 minutes.
func TestErofsApplyConfigOutlastsHostdsMenuBound(t *testing.T) {
	h := erofsHarness(t)
	u := h.NewUser("ines")
	ubu := newErofsProject(t, h, u, "ubu")
	nix := h.NewProject(u, "nixy", "small")
	op := func(p uuid.UUID) *store.Op {
		return &store.Op{ProjectID: &p, Kind: ops.KindBuild, Step: 1, Params: map[string]any{"phases": []any{ops.PhaseWake, ops.PhaseApplyConfig}}}
	}
	got := h.Engine.TimeoutForTest(h.Ctx, op(ubu.ID))
	// hostd's vsockclient.MenuTimeout, restated: the api does not import
	// hostd's packages.
	const hostdMenuTimeout = 65 * time.Minute
	if got <= hostdMenuTimeout {
		t.Fatalf("erofs apply_config times out at %s, not past hostd's %s ApplyMenu bound", got, hostdMenuTimeout)
	}
	if n := h.Engine.TimeoutForTest(h.Ctx, op(nix.ID)); n != 10*time.Minute {
		t.Fatalf("Nix apply_config times out at %s, want the 10 min it had", n)
	}
	t.Logf("apply_config: erofs %s, Nix %s", got, h.Engine.TimeoutForTest(h.Ctx, op(nix.ID)))
}
