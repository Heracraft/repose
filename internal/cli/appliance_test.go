package cli

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Mac appliance's launcher (DECISIONS I-1380), the parts a Linux box
// can hold: the sizing rules (the numbers the docs page quotes), the
// sparse unpack (a 37 GiB image must cost the volume its data, not its
// zeros), the join bundle, the config, the plist and vfkit's arguments.
// The Mac itself holds the rest, and the research doc §9 records it.

func TestApplianceSizing(t *testing.T) {
	for _, c := range []struct {
		total int64
		want  int
	}{
		{17179869184, 12288}, // a 16 GB Mac
		{25769803776, 18432}, // a 24 GB Mac
		{34359738368, 24576},
		{8589934592, 8192}, // under the floor
	} {
		if got := vmMemoryMiB(c.total); got != c.want {
			t.Errorf("vmMemoryMiB(%d) = %d, want %d", c.total, got, c.want)
		}
	}
	for n, want := range map[int]int{10: 8, 4: 4, 12: 10} {
		if got := vmCPUs(n); got != want {
			t.Errorf("vmCPUs(%d) = %d, want %d", n, got, want)
		}
	}
	for _, c := range []struct{ free, asked, want int }{
		{500, 0, 100}, {60, 0, 40}, {500, 200, 200}, {62, 200, 42},
	} {
		if got, err := diskGB(c.free, c.asked); got != c.want || err != nil {
			t.Errorf("diskGB(%d, %d) = %d, %v, want %d", c.free, c.asked, got, err, c.want)
		}
	}
	// Below the image's own size the disk would lose its root partition
	// (lab image 6, 2026-11-19): refuse, naming the shortfall.
	for _, c := range []struct{ free, asked int }{{52, 0}, {59, 45}, {10, 0}} {
		if gb, err := diskGB(c.free, c.asked); err == nil || !strings.Contains(err.Error(), "free up") {
			t.Errorf("diskGB(%d, %d) = %d, %v; want a refusal", c.free, c.asked, gb, err)
		}
	}
}

func TestApplianceRefusesTheWrongMac(t *testing.T) {
	ok := macFacts{OS: "15.8.2", Chip: "Apple M4", MemBytes: 25769803776, CPUs: 10, HV: true}
	if err := checkMacForAppliance(ok); err != nil {
		t.Fatalf("an M4 on 15.8.2: %v", err)
	}
	for _, c := range []struct {
		f    macFacts
		want string
	}{
		{macFacts{OS: "14.7", Chip: "Apple M4", MemBytes: ok.MemBytes, HV: true}, "older than 15"},
		{macFacts{OS: "15.8.2", Chip: "Apple M2 Pro", MemBytes: ok.MemBytes, HV: true}, "M3 chip or newer"},
		{macFacts{OS: "15.8.2", Chip: "Apple M4", MemBytes: ok.MemBytes, HV: false}, "no virtualization support"},
		{macFacts{OS: "15.8.2", Chip: "Apple M4", MemBytes: 8 << 30, HV: true}, "16 GB or more"},
	} {
		err := checkMacForAppliance(c.f)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: %v, want %q", c.f, err, c.want)
		}
	}
	if err := checkMacForAppliance(macFacts{OS: "15.8.2", Chip: "Apple M10", MemBytes: ok.MemBytes, HV: true}); err != nil {
		t.Errorf("an M10 is newer than M3: %v", err)
	}
}

func TestApplianceJoinBundle(t *testing.T) {
	arg := encodeTokenBundle("rjt_abc")
	b, err := decodeJoinBundle(arg)
	if err != nil || b.Token != "rjt_abc" || b.Version != 1 {
		t.Fatalf("round trip: %+v %v", b, err)
	}
	for _, bad := range []string{"", "not a bundle", "eyJ2IjoyLCJ0b2tlbiI6InQifQ", "eyJ2IjoxfQ"} {
		if _, err := decodeJoinBundle(bad); err == nil {
			t.Errorf("%q decoded", bad)
		}
	}
}

func TestApplianceUnpackIsSparse(t *testing.T) {
	// 3 MB of data, an 8 MiB hole, then a tail: the holes are not
	// written, the bytes come back equal.
	data := bytes.Repeat([]byte("repose"), 500000)
	data = append(data, make([]byte, 8<<20)...)
	data = append(data, []byte("tail")...)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	dest := filepath.Join(t.TempDir(), "disk.raw")
	written, err := unpackSparse(&gz, dest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("the unpacked image differs from the image")
	}
	if written >= int64(len(data)) {
		t.Fatalf("wrote %d of %d bytes; the hole was written", written, len(data))
	}
}

func TestApplianceConfigAndPlist(t *testing.T) {
	dir := t.TempDir()
	c := applianceConfig{MemoryMiB: 12288, CPUs: 8, MAC: "52:54:aa:2e:43:de", HostName: "mac-mini"}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(c.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readApplianceConfig(filepath.Join(dir, "config"))
	if err != nil || got != c {
		t.Fatalf("config round trip: %+v %v", got, err)
	}
	if _, err := readApplianceConfig(filepath.Join(dir, "none")); err == nil {
		t.Fatal("a missing config read")
	}
	for i := 0; i < 20; i++ {
		mac := randomMAC()
		if !strings.HasPrefix(mac, "52:54:aa:") || strings.Contains(mac, ":00") {
			t.Fatalf("mac %s: a zero octet, which the lease file drops", mac)
		}
	}
	p := applianceFor("/Users/dev")
	plist := launchdPlist("/usr/local/bin/repose", p.Dir, p.LauncherLog)
	for _, want := range []string{
		"<string>run.repose.appliance</string>",
		"<string>/usr/local/bin/repose</string><string>host</string><string>run</string>",
		"<key>RunAtLoad</key><true/>", "<key>KeepAlive</key><true/>",
		"<string>/Users/dev/Library/Logs/repose/launcher.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %s", want)
		}
	}
	args := strings.Join(vfkitArgs(p, c), " ")
	for _, want := range []string{
		"--cpus 8", "--memory 12288", "--nested",
		"--bootloader efi,variable-store=/Users/dev/Library/Application Support/repose/efi-vars.bin,create",
		"virtio-blk,path=/Users/dev/Library/Application Support/repose/disk.raw",
		"virtio-net,nat,mac=52:54:aa:2e:43:de", "virtio-rng",
		"virtio-fs,sharedDir=/Users/dev/Library/Application Support/repose/share,mountTag=repose-launcher",
		"virtio-serial,logFilePath=/Users/dev/Library/Logs/repose/console.log",
		"--restful-uri unix:///Users/dev/Library/Application Support/repose/vfkit.sock",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("vfkit arguments lack %s", want)
		}
	}
}
