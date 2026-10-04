package ch

import (
	"os"
	"strings"
	"testing"
)

func TestArgsGolden(t *testing.T) {
	s := Spec{
		GuestDir: "/var/lib/repose/guests/0192f0a1-1111-7000-8000-000000000001",
		Kernel:   "/nix/store/abc-nixos-system/kernel", Initrd: "/nix/store/abc-nixos-system/initrd",
		Init: "/nix/store/abc-nixos-system/init", KernelParams: "loglevel=4 reboot=t panic=-1\n",
		IP: "10.64.4.2", Gateway: "10.64.4.1", Netmask: "255.255.252.0",
		Tap: "tap-0192f0a1", MAC: "52:54:01:92:f0:a1", CID: 1000,
		VolumeDev: "/dev/vg-guests/g-0192f0a1-1111-7000-8000-000000000001",
		VCPUs:     4, MemMiB: 8192, StoreTag: "ro-store", AuthTag: "claude-auth",
		DiskIOPS: 3000, DiskBytesPerSec: 120_000_000,
	}
	got := strings.Join(s.Args(), "\n") + "\n"
	want, err := os.ReadFile("testdata/args.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("argv differs from testdata/args.golden:\n%s", got)
	}
}

// A guest with no user id has no login share (I-278): no second --fs.
func TestArgsWithoutAuthTag(t *testing.T) {
	s := Spec{GuestDir: "/g", StoreTag: "ro-store"}
	n := 0
	for _, a := range s.Args() {
		if a == "--fs" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d --fs arguments without an auth tag", n)
	}
}

// Without limits the disk is rendered as before I-450.
func TestArgsWithoutDiskLimits(t *testing.T) {
	s := Spec{GuestDir: "/g", VolumeDev: "/dev/vg-guests/g-1"}
	for i, a := range s.Args() {
		if a == "--disk" {
			if got := s.Args()[i+1]; got != "path=/dev/vg-guests/g-1,image_type=raw,direct=on" {
				t.Fatalf("--disk %s", got)
			}
			return
		}
	}
	t.Fatal("no --disk")
}
