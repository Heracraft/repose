package sysdep

import (
	"strings"
	"testing"
)

func TestDetectRuntime(t *testing.T) {
	cases := []struct {
		name      string
		mountinfo string
		want      Runtime
	}{
		{"erofs guest", FixtureMountinfoErofs, RuntimeErofs},
		{"nix guest", FixtureMountinfoNix, RuntimeNix},
		// Half the shape is not the shape: an erofs without the ext4, or
		// the ext4 somewhere else, stays a Nix guest.
		{"ro erofs only", strings.ReplaceAll(FixtureMountinfoErofs, " /run/repose/rw ", " /mnt/rw "), RuntimeNix},
		{"rw not ext4", strings.ReplaceAll(FixtureMountinfoErofs, "- ext4 /dev/vdb rw,discard\n29", "- xfs /dev/vdb rw,discard\n29"), RuntimeNix},
		{"ro not erofs", strings.ReplaceAll(FixtureMountinfoErofs, "- erofs /dev/vda", "- squashfs /dev/vda"), RuntimeNix},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Paths{Root: t.TempDir()}
			if err := WriteMountinfo(p, c.mountinfo); err != nil {
				t.Fatal(err)
			}
			if got := DetectRuntime(p); got != c.want {
				t.Fatalf("DetectRuntime = %s, want %s", got, c.want)
			}
		})
	}
}

func TestDetectRuntimeWithoutMountinfoIsNix(t *testing.T) {
	if got := DetectRuntime(Paths{Root: t.TempDir()}); got != RuntimeNix {
		t.Fatalf("DetectRuntime = %s, want nix", got)
	}
}

func TestReadMountinfoUnescapesAndTakesTheTopMount(t *testing.T) {
	p := Paths{Root: t.TempDir()}
	content := FixtureMountinfoErofs +
		`40 24 0:30 / /run/repose/rw rw - tmpfs tmpfs rw` + "\n" +
		`41 26 0:31 / /home/dev/my\040dir rw - tmpfs tmpfs rw` + "\n"
	if err := WriteMountinfo(p, content); err != nil {
		t.Fatal(err)
	}
	ms, err := ReadMountinfo(p.Mountinfo())
	if err != nil {
		t.Fatal(err)
	}
	m, ok := FindMount(ms, ErofsRWMount)
	if !ok || m.FSType != "tmpfs" {
		t.Fatalf("top mount at %s = %+v, want the later tmpfs", ErofsRWMount, m)
	}
	if _, ok := FindMount(ms, "/home/dev/my dir"); !ok {
		t.Fatal(`the \040 escape was not undone`)
	}
}

func TestErofsPathsTargetTheVolume(t *testing.T) {
	nix := Paths{}
	erofs := Paths{Erofs: true}
	if nix.RootMount() != "/" {
		t.Fatalf("nix RootMount = %s", nix.RootMount())
	}
	if erofs.RootMount() != "/run/repose/rw" {
		t.Fatalf("erofs RootMount = %s", erofs.RootMount())
	}
	if !strings.Contains(strings.Join(DevEnv(erofs, "root"), " "), "PATH="+ErofsPATH) {
		t.Fatal("erofs DevEnv does not carry the Ubuntu PATH")
	}
	if !strings.Contains(strings.Join(DevEnv(nix, "root"), " "), "PATH="+GuestPATH) {
		t.Fatal("nix DevEnv PATH changed")
	}
}
