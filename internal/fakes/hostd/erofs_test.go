package hostd

import (
	"slices"
	"strings"
	"testing"

	hostdv1 "github.com/heracraft/repose/internal/gen/hostd/v1"
)

func goodBase(v string) *hostdv1.BaseImage {
	s := strings.Repeat("a", 64)
	return &hostdv1.BaseImage{Version: v, Url: "https://x.test/rootfs.erofs", Sha256: s, KernelUrl: "https://x.test/vmlinuz", KernelSha256: s,
		InitramfsUrl: "https://x.test/initramfs.img", InitramfsSha256: s, KernelVersion: "6.8.0"}
}

func create(id, guest string, closure string, bi *hostdv1.BaseImage, menu *hostdv1.MenuSpec) *hostdv1.Command {
	return &hostdv1.Command{CommandId: id, Cmd: &hostdv1.Command_CreateGuest{CreateGuest: &hostdv1.CreateGuest{
		ProjectId: "p-" + guest, GuestId: guest, Class: "large", SystemClosure: closure, BaseImage: bi, Menu: menu}}}
}

// The old shape still works and the new one is refused where hostd
// refuses it (DECISIONS I-1458): a CreateGuest with system_closure only
// boots a Nix guest exactly as before; base_image beside it is
// invalid_argument; a base_image on a host without erofs, or a malformed
// one, is invalid_argument; an erofs guest records its base and menu.
func TestErofsShapes(t *testing.T) {
	nixOnly := New(Options{HostID: "h1"})
	if r := nixOnly.Execute(create("c1", "g1", "/nix/store/abc-system", nil, nil)); !r.Ok {
		t.Fatalf("system_closure only: %+v", r.Error)
	}
	if g := nixOnly.Guests()[0]; g.Closure != "/nix/store/abc-system" || g.BaseVersion != "" || g.State != "running" {
		t.Fatalf("nix guest: %+v", g)
	}
	feats := nixOnly.Hello().Features
	if !slices.Contains(feats, "nix") || slices.Contains(feats, "erofs") {
		t.Fatalf("nix host features %v", feats)
	}
	for name, cmd := range map[string]*hostdv1.Command{
		"both set":            create("c2", "g2", "/nix/store/abc-system", goodBase("v1"), nil),
		"erofs on a nix host": create("c3", "g3", "", goodBase("v1"), nil),
	} {
		r := nixOnly.Execute(cmd)
		if r.Ok || r.Error.Code != "invalid_argument" {
			t.Fatalf("%s: %+v", name, r)
		}
	}

	both := New(Options{HostID: "h2", Erofs: true})
	if r := both.Execute(create("c4", "g4", "/nix/store/abc-system", goodBase("v1"), nil)); r.Ok || r.Error.Code != "invalid_argument" || r.Error.Message != MsgBothSet {
		t.Fatalf("both set on an erofs host: %+v", r)
	}
	bad := goodBase("v1")
	bad.KernelSha256 = "nothex"
	if r := both.Execute(create("c5", "g5", "", bad, nil)); r.Ok || r.Error.Code != "invalid_argument" {
		t.Fatalf("bad kernel sha256: %+v", r)
	}
	menu := &hostdv1.MenuSpec{RevisionId: "r1", Items: []*hostdv1.MenuItem{{Id: "postgresql"}, {Package: "ffmpeg"}}}
	r := both.Execute(create("c6", "g6", "", goodBase("v1"), menu))
	if !r.Ok {
		t.Fatalf("erofs create: %+v", r.Error)
	}
	var g *Guest
	for _, x := range both.Guests() {
		if x.GuestID == "g6" {
			g = x
		}
	}
	if g == nil || g.BaseVersion != "v1" || g.Menu == nil || len(g.Menu.Items) != 2 || g.Closure != "" {
		t.Fatalf("erofs guest: %+v", g)
	}
	for _, gs := range both.Hello().Guests {
		if gs.GuestId == "g6" && gs.BaseVersion != "v1" {
			t.Fatalf("Hello base_version %q", gs.BaseVersion)
		}
	}
	// ApplyConfig takes the field the guest's runtime takes, never both.
	apply := func(id, guest, closure string, m *hostdv1.MenuSpec) *hostdv1.Result {
		return both.Execute(&hostdv1.Command{CommandId: id, Cmd: &hostdv1.Command_ApplyConfig{ApplyConfig: &hostdv1.ApplyConfig{GuestId: guest, SystemClosure: closure, Menu: m}}})
	}
	if r := apply("a1", "g6", "/nix/store/x", menu); r.Ok || r.Error.Code != "invalid_argument" {
		t.Fatalf("apply with both: %+v", r)
	}
	if r := apply("a2", "g6", "/nix/store/x", nil); r.Ok || r.Error.Code != "invalid_argument" {
		t.Fatalf("closure to an erofs guest: %+v", r)
	}
	if r := apply("a3", "g6", "", &hostdv1.MenuSpec{RevisionId: "r2"}); !r.Ok {
		t.Fatalf("menu to an erofs guest: %+v", r.Error)
	}
	both.SetMenuFail("menu_failed", "postgresql: did not start")
	if r := apply("a4", "g6", "", menu); r.Ok || r.Error.Code != "menu_failed" {
		t.Fatalf("failing menu: %+v", r)
	}
	// A start with a new base boots it and reports it; the menu's failure
	// rides as menu_error with the guest running.
	if r := both.Execute(&hostdv1.Command{CommandId: "s0", Cmd: &hostdv1.Command_StopGuest{StopGuest: &hostdv1.StopGuest{GuestId: "g6"}}}); !r.Ok {
		t.Fatalf("stop: %+v", r.Error)
	}
	r = both.Execute(&hostdv1.Command{CommandId: "s1", Cmd: &hostdv1.Command_StartGuest{StartGuest: &hostdv1.StartGuest{GuestId: "g6", BaseImage: goodBase("v2"), Menu: menu}}})
	if !r.Ok || r.GetStart().GetBootedBaseVersion() != "v2" || r.GetStart().GetMenuError().GetCode() != "menu_failed" {
		t.Fatalf("start onto v2: %+v", r)
	}
	if r := apply("a5", "g1", "", menu); !strings.Contains(r.GetError().GetMessage(), "not on this host") {
		t.Fatalf("unknown guest: %+v", r)
	}

	erofsOnly := New(Options{HostID: "h3", Erofs: true, NoNix: true})
	if r := erofsOnly.Execute(create("c7", "g7", "/nix/store/abc-system", nil, nil)); r.Ok || r.Error.Code != "invalid_argument" {
		t.Fatalf("closure on an erofs-only host: %+v", r)
	}
	if r := erofsOnly.Execute(&hostdv1.Command{CommandId: "b1", Cmd: &hostdv1.Command_Build{Build: &hostdv1.Build{ProjectId: "p"}}}); r.Ok || r.Error.Code != "invalid_argument" {
		t.Fatalf("build on an erofs-only host: %+v", r)
	}
	if f := erofsOnly.Hello().Features; slices.Contains(f, "nix") || !slices.Contains(f, "erofs") {
		t.Fatalf("erofs-only features %v", f)
	}
}
