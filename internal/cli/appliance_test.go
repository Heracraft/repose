package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
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

// ENG-117, measured on a real M4 on 2026-11-20: the first `repose host
// add` failed at 31 s with "the image: context deadline exceeded
// (Client.Timeout or context cancellation while reading body)", and the
// second succeeded in 37 s. The Env's client carries a 30 s Timeout for
// the api's calls, http.Client.Timeout covers reading the body, and the
// downloads took that client whole, so a 2 GB image ran under a
// 30-second whole-request deadline. These four hold the download's own
// client, what ends a dead connection instead, what a retry continues
// from, and what the customer reads.

// slowBody serves b one byte at a time, each after a pause: the shape of
// a long download against a short whole-request deadline.
func slowBody(b []byte, each time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.WriteHeader(http.StatusOK)
		for i := range b {
			time.Sleep(each)
			_, _ = w.Write(b[i : i+1])
			w.(http.Flusher).Flush()
		}
	}
}

func TestApplianceDownloadOutlivesTheApiTimeout(t *testing.T) {
	body := []byte("an appliance image, byte by byte")
	srv := httptest.NewServer(slowBody(body, 10*time.Millisecond))
	defer srv.Close()
	// The production Env: the api's 30 s client, through both wrappers,
	// which return the client they were given (timing.go, verbose.go).
	e := &Env{httpClient: withVerbose(withTiming(&http.Client{Timeout: 320 * time.Millisecond}), srv.URL)}
	c := e.applianceHTTP()
	if c.Transport == nil {
		t.Error("the download lost the Env's transport, so -v and --timing log nothing")
	}
	sum := sha256.Sum256(body)
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	if _, err := fetchTo(context.Background(), c, nil, srv.URL, dest, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("a download longer than the api's own timeout failed: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the file on disk is not the download: %q %v", got, err)
	}
}

func TestApplianceDownloadGivesUpWhenItStalls(t *testing.T) {
	// No whole-request deadline means a connection that dies mid-body
	// would otherwise be held until TCP gives up, a quarter of an hour.
	hold := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "64")
		_, _ = w.Write([]byte("four"))
		w.(http.Flusher).Flush()
		<-hold
	}))
	defer func() { close(hold); srv.Close() }()
	defer func(d time.Duration) { applianceStallWindow = d }(applianceStallWindow)
	applianceStallWindow = 150 * time.Millisecond

	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	done := make(chan error, 1)
	go func() {
		_, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, "")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled download reported success")
		}
		if !strings.Contains(err.Error(), "no data") {
			t.Errorf("a stalled download does not say it stalled: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("a stalled download was never given up; nothing ends a dead connection")
	}
}

func TestApplianceDownloadRetryContinuesFromDisk(t *testing.T) {
	body := []byte(strings.Repeat("repose appliance ", 64))
	const cut = 200
	var ranges []string
	first := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ranges = append(ranges, r.Header.Get("Range"))
		if first {
			// The connection dies part way, as a home connection does.
			first = false
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body[:cut])
			w.(http.Flusher).Flush()
			return
		}
		http.ServeContent(w, r, "image.raw.gz", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()

	sum := sha256.Sum256(body)
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	got, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("a download that died part way was not retried: %v", err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("digest %s, want %s", got, hex.EncodeToString(sum[:]))
	}
	onDisk, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(onDisk, body) {
		t.Fatalf("the file on disk is not the download (%d of %d bytes): %v", len(onDisk), len(body), err)
	}
	if len(ranges) < 2 || ranges[1] != fmt.Sprintf("bytes=%d-", cut) {
		t.Errorf("the retry asked for %v; it should continue from byte %d, not fetch 2 GB again", ranges, cut)
	}
}

func TestApplianceFetchFailureTellsTheCustomerWhatToDo(t *testing.T) {
	arg := encodeTokenBundle("rjt_abc")
	msg := applianceFetchFailed("The appliance image", errors.New("no data for 1m0s after 1.4 GB"), arg, "mac-mini")
	for _, want := range []string{
		"The appliance image did not download",
		"no data for 1m0s after 1.4 GB",
		"mac-mini is in your account",
		"repose host add --join " + arg,
		"continues from what is already here",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the failure a customer reads does not say %q:\n%s", want, msg)
		}
	}
	// `repose host add` minted the host before the install, so plain
	// `repose host add` again is refused by the one-host limit.
	if strings.Contains(msg, "repose host add\n") || strings.Contains(msg, "repose host add.") {
		t.Errorf("the failure sends the customer back to a command the host limit refuses:\n%s", msg)
	}
	// On the --join path there is no host row of ours to name.
	if m := applianceFetchFailed("vfkit", errors.New("broken pipe"), arg, ""); strings.Contains(m, "in your account") {
		t.Errorf("the --join path names a host the api did not mint:\n%s", m)
	}
}

func TestApplianceStalePartIsNotADeadEnd(t *testing.T) {
	// An install the customer gave up on leaves a part file behind. If
	// the CLI is upgraded meanwhile, the image behind the URL is a
	// different one and continuing from that part cannot match the
	// checksum: the attempt after it starts from nothing rather than
	// refusing for good.
	body := []byte(strings.Repeat("the image this CLI wants ", 32))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "image.raw.gz", time.Time{}, bytes.NewReader(body))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "image.raw.gz")
	if err := os.WriteFile(dest+".part", []byte("an older image's first bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := hex.EncodeToString(func() []byte { s := sha256.Sum256(body); return s[:] }())
	got, err := fetchTo(context.Background(), &http.Client{}, nil, srv.URL, dest, sum)
	if err != nil {
		t.Fatalf("a part file from an older image is a dead end: %v", err)
	}
	if got != sum {
		t.Errorf("digest %s, want %s", got, sum)
	}
	onDisk, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(onDisk, body) {
		t.Fatalf("the file on disk is not the download: %v", err)
	}
}
