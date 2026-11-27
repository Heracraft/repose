package ch

import (
	"fmt"
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
	// The golden is an x86 host's; the serial console is the one thing
	// that differs on arm64 (ttyAMA0, a Mac's VZ guests).
	if c := ConsoleTTY(); c != "ttyS0" {
		want = []byte(strings.ReplaceAll(string(want), "console=ttyS0", "console="+c))
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

// The project slug names the guest on the kernel line, in ip= and in
// systemd.hostname= (I-550).
func TestCmdlineHostname(t *testing.T) {
	s := Spec{Init: "/init", IP: "10.64.0.8", Gateway: "10.64.0.1", Netmask: "255.255.252.0", Hostname: "todo-app"}
	want := "init=/init console=" + ConsoleTTY() + " ip=10.64.0.8::10.64.0.1:255.255.252.0:todo-app:eth0:off systemd.hostname=todo-app"
	if got := s.Cmdline(); got != want {
		t.Fatalf("cmdline\n got %s\nwant %s", got, want)
	}
}

// No slug, or one that is not a DNS label, leaves the name field empty and
// adds no systemd.hostname=, so the closure's repose-guest stays.
func TestCmdlineHostnameFallback(t *testing.T) {
	want := "init=/init console=" + ConsoleTTY() + " ip=10.64.0.8::10.64.0.1:255.255.252.0::eth0:off"
	for _, name := range []string{"", "-lead", "trail-", "Upper", "has space", "a.b", "a:b", strings.Repeat("a", 64)} {
		s := Spec{Init: "/init", IP: "10.64.0.8", Gateway: "10.64.0.1", Netmask: "255.255.252.0", Hostname: name}
		if got := s.Cmdline(); got != want {
			t.Fatalf("hostname %q: cmdline %s", name, got)
		}
		if Hostname(name) != "" {
			t.Fatalf("Hostname(%q) accepted", name)
		}
	}
	for _, name := range []string{"a", "x1", "my-app-2", strings.Repeat("a", 63)} {
		if Hostname(name) != name {
			t.Fatalf("Hostname(%q) refused", name)
		}
	}
}

// A closure with a store disk gets it as the first virtio-blk disk,
// read-only, the volume second (the guest's drive letters follow that
// order), and the share stays.
func TestArgsWithStoreDisk(t *testing.T) {
	s := Spec{GuestDir: "/g", Kernel: "/k", Initrd: "/i", Init: "/init", VolumeDev: "/dev/v", VCPUs: 2, MemMiB: 2048,
		Tap: "tap0", MAC: "52:54:00:00:00:01", CID: 3, StoreTag: "ro-store", StoreDisk: "/nix/store/abc-microvm-store-disk.erofs"}
	args := s.Args()
	var disks []string
	for i, a := range args {
		if a == "--disk" {
			disks = append(disks, args[i+1])
		}
	}
	if len(disks) != 2 || disks[0] != "path=/nix/store/abc-microvm-store-disk.erofs,image_type=raw,readonly=on" || !strings.HasPrefix(disks[1], "path=/dev/v,") {
		t.Fatalf("disks %q", disks)
	}
	if !strings.Contains(strings.Join(args, " "), "--fs tag=ro-store,") {
		t.Fatal("the share is gone")
	}
}

// erofsSpec is an erofs guest as create.go fills it (DECISIONS I-1480).
func erofsSpec() Spec {
	return Spec{
		GuestDir:  "/var/lib/repose/guests/0192f0a1-1111-7000-8000-000000000001",
		Kernel:    "/var/lib/repose/images/2026.11.22.1-x86_64/vmlinuz",
		Initrd:    "/var/lib/repose/images/2026.11.22.1-x86_64/initramfs.img",
		RootImage: "/var/lib/repose/images/2026.11.22.1-x86_64/rootfs.erofs",
		IP:        "10.64.4.2", Gateway: "10.64.4.1", Netmask: "255.255.252.0",
		Tap: "tap-0192f0a1", MAC: "52:54:01:92:f0:a1", CID: 1000,
		VolumeDev: "/dev/vg-guests/g-0192f0a1-1111-7000-8000-000000000001",
		VCPUs:     4, MemMiB: 8192, StoreTag: "ro-store", AuthTag: "claude-auth",
		DiskIOPS: 3000, DiskBytesPerSec: 120_000_000, Hostname: "todo-app",
	}
}

// The whole argv of an erofs guest: vda the read-only erofs, vdb the
// volume with its limits, no ro-store share, the login share kept, and
// a kernel line without init=.
func TestArgsGoldenErofs(t *testing.T) {
	if ConsoleTTY() != "ttyS0" {
		t.Skip("the golden file is the x86_64 console")
	}
	got := strings.Join(erofsSpec().Args(), "\n") + "\n"
	want, err := os.ReadFile("testdata/args-erofs.golden")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("argv differs from testdata/args-erofs.golden:\n%s", got)
	}
}

func TestCmdlineErofs(t *testing.T) {
	s := erofsSpec()
	s.Init, s.KernelParams = "/nix/store/x/init", "loglevel=4" // ignored on an erofs guest
	want := "console=" + ConsoleTTY() + " ip=10.64.4.2::10.64.4.1:255.255.252.0:todo-app:eth0:off systemd.hostname=todo-app panic=-1 reboot=t"
	if got := s.Cmdline(); got != want {
		t.Fatalf("cmdline\n got %s\nwant %s", got, want)
	}
	if strings.Contains(s.Cmdline(), "init=") {
		t.Fatal("init= on an erofs guest's kernel line")
	}
}

// vm.resize-disk targets the guest's volume and never the read-only
// root: on an erofs guest the id it is given is the one the volume's
// --disk carries, and the read-only disk carries another (I-1480).
func TestVolumeDiskIDNeverReadOnly(t *testing.T) {
	for name, s := range map[string]Spec{
		"erofs":            erofsSpec(),
		"nix":              {GuestDir: "/g", VolumeDev: "/dev/vg-guests/g-1", StoreTag: "ro-store"},
		"nix-with-a-store": {GuestDir: "/g", VolumeDev: "/dev/vg-guests/g-1", StoreTag: "ro-store", StoreDisk: "/nix/store/x/store.erofs"},
	} {
		args := s.Args()
		var disks []string
		for i, a := range args {
			if a == "--disk" {
				disks = append(disks, args[i+1])
			}
		}
		id := s.VolumeDiskID()
		// Cloud Hypervisor names a --disk without id= _disk<n> by its place.
		for n, d := range disks {
			dID := fmt.Sprintf("_disk%d", n)
			for _, opt := range strings.Split(d, ",") {
				if v, ok := strings.CutPrefix(opt, "id="); ok {
					dID = v
				}
			}
			ro := strings.Contains(d, "readonly=on")
			isVol := strings.HasPrefix(d, "path="+s.VolumeDev+",")
			if dID == id && (ro || !isVol) {
				t.Fatalf("%s: resize id %s names %s", name, id, d)
			}
			if isVol && dID != id {
				t.Fatalf("%s: the volume is %s, resize targets %s", name, dID, id)
			}
		}
	}
}

func TestCmdlineMTU(t *testing.T) {
	s := Spec{IP: "10.64.0.2", Gateway: "10.64.0.1", Netmask: "255.255.252.0", Hostname: "web"}
	if got := s.Cmdline(); strings.Contains(got, "eth0:off:") {
		t.Fatalf("no MTU: %q", got)
	}
	s.MTU = 1500
	if got := s.Cmdline(); strings.Contains(got, "eth0:off:") {
		t.Fatalf("1500 is the default and stays off the line: %q", got)
	}
	s.MTU = 65520
	if got := s.Cmdline(); !strings.Contains(got, "ip=10.64.0.2::10.64.0.1:255.255.252.0:web:eth0:off:65520 ") {
		t.Fatalf("mtu: %q", got)
	}
}
