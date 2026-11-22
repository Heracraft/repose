package cli

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"

	"github.com/heracraft/repose/internal/parkkey"
)

// The Mac mini appliance's launcher (DECISIONS I-1376, I-1380): `repose
// host add` on a Mac puts one Linux virtual machine on it, running hostd
// and the user's machines, and `repose host run` is what launchd starts
// at every login to hold that VM. The VM is driven by vfkit (Apache-2.0,
// one binary with Apple's entitlement to use the Virtualization
// framework, checksum pinned below) rather than by this binary itself:
// the CLI's darwin build is cross-compiled without cgo, and driving the
// framework from Go needs cgo and a signed, entitled binary built on a
// macOS runner, which is a later step. Nothing here needs sudo and
// nothing of the Mac's is changed: the VM, its disk and its logs live
// under the user's own Library, and `caffeinate` holds a sleep assertion
// while the VM runs instead of a `pmset` setting.
//
// Everything lives under ~/Library/Application Support/repose:
// bin/vfkit, disk.raw (the appliance's one disk: the machines' volumes
// and snapshots are in it), share/join (the argument, until the first
// boot consumes it), efi-vars.bin, config, vfkit.sock, park-key.sock
// (where the appliance's park asks for its image keys to be wrapped and
// unwrapped, over a vsock vfkit forwards here, internal/parkkey); and
// under ~/Library/Logs/repose: launcher.log and console.log, the VM's
// console, which carries the appliance's journal (ids and states) and
// nothing of the user's. One thing lives outside: the wrapping key of
// the parked images, an item in the user's keychain (parkkeychain.go,
// DECISIONS I-1386). It is what *would* let a restart of the Mac keep
// what every machine was doing, and it does not: macOS gives the
// launcher about one second when the Mac goes down, measured, and
// writing a machine's memory takes longer (DECISIONS I-1434). The park
// works on a clean stop of the appliance and not across a restart, so
// every machine starts fresh after one.

const (
	applianceVersion  = "2026.11.22"
	applianceURL      = "https://github.com/Heracraft/repose/releases/download/appliance-" + applianceVersion + "/repose-appliance-aarch64.raw.zst"
	vfkitVersion      = "0.6.4"
	vfkitURL          = "https://github.com/crc-org/vfkit/releases/download/v" + vfkitVersion + "/vfkit"
	vfkitSHA256       = "0ed83fc8ca7aa708598835480dba1362406aa7cd1dab3b27464eb76327d9652d"
	applianceLabel    = "run.repose.appliance"
	applianceShareTag = "repose-launcher"
	applianceDiskGB   = 100
	// applianceDiskMinGB is the smallest disk the appliance is installed
	// on: the image's 2.1 GiB (its ESP and its 2 GiB root) and a pool
	// that holds a small machine's 10 GB volume beside its metadata, its
	// spare and the snapshots' headroom (DECISIONS I-1494). Below the
	// image's own size a disk file would lose the root partition's end,
	// which the 37 GiB NixOS image did at 40 GB (the lab image 6 install,
	// 2026-11-19).
	applianceDiskMinGB = 20
	// applianceDiskKeepGB stays free on the volume for the user.
	applianceDiskKeepGB = 20
	// applianceMinCPUs and applianceMinMemoryMiB are the floors the
	// sizing rules already keep (vmCPUs, vmMemoryMiB); naming them lets
	// an explicit flag be refused against the same number.
	applianceMinCPUs      = 4
	applianceMinMemoryMiB = 8192
	// applianceImageApparentBytes is the raw image's size as built: 1
	// MiB of GPT, the 64 MiB ESP, the 2 GiB root, the 1 MiB guest
	// partition the first boot grows and the backup GPT
	// (images/appliance-arm64/build.sh, DECISIONS I-1494). A disk file
	// smaller than that is a broken unpack.
	applianceImageApparentBytes = 2115 << 20
	// applianceStopGrace is how long a stop (launchd's SIGTERM at a
	// restart or a logout, `repose host rm`) gives the appliance to park
	// its machines and power off before the plug is pulled. The worst
	// case inside is a guest that will not snapshot: the park holds the
	// shutdown for its 120 s deadline plus its own 120 s stop grace
	// (nix/hosts/mac.nix, internal/hostd/guest/park.go), the unit's wall
	// cuts it at 270 (park.nix, deadline + 150), and the rest of the
	// shutdown (the guests' clean stops, the shares, LVM) takes its share
	// after that; 330 leaves it 60 s. So hostd gives up before the
	// launcher does and an image is never cut mid-write by this side,
	// while a plug at 330 catches a shutdown that is hung for another
	// reason. The agent's ExitTimeOut is this plus 30, so launchd's
	// SIGKILL comes after the plug, never before. How long macOS itself
	// waits for an agent at a restart is the part only the Mac shows.
	applianceStopGrace = 330 * time.Second
)

// appliancePaths is where the launcher keeps everything on a Mac.
type appliancePaths struct {
	Dir, Logs, Plist, Config, Disk, Share, Join, Argument, Sock, ParkKeySock, EFIVars, Vfkit, LauncherLog, ConsoleLog string
}

func applianceFor(home string) appliancePaths {
	dir := filepath.Join(home, "Library", "Application Support", "repose")
	logs := filepath.Join(home, "Library", "Logs", "repose")
	return appliancePaths{
		Dir:    dir,
		Logs:   logs,
		Plist:  filepath.Join(home, "Library", "LaunchAgents", applianceLabel+".plist"),
		Config: filepath.Join(dir, "config"),
		Disk:   filepath.Join(dir, "disk.raw"),
		Share:  filepath.Join(dir, "share"),
		Join:   filepath.Join(dir, "share", "join"),
		// The install argument, kept 0600 beside the install so a retry
		// after a failed download does not need it on the command line
		// (ENG-42). It goes with the directory at `repose host rm`.
		Argument:    filepath.Join(dir, "join-argument"),
		Sock:        filepath.Join(dir, "vfkit.sock"),
		ParkKeySock: filepath.Join(dir, "park-key.sock"),
		EFIVars:     filepath.Join(dir, "efi-vars.bin"),
		Vfkit:       filepath.Join(dir, "bin", "vfkit"),
		LauncherLog: filepath.Join(logs, "launcher.log"),
		ConsoleLog:  filepath.Join(logs, "console.log"),
	}
}

// ---- the sizing rules -------------------------------------------------
// Three quarters of the Mac's memory for the VM, in whole MiB, never
// under 8 GiB: 12,288 on a 16 GB Mac and 18,432 on 24 (I-1376); macOS
// keeps the quarter, and the host reserve inside the VM is the fleet's
// eighth (DESIGN §4). All but two cores, never under four. A disk of 100
// GB, bounded by what the volume has free less 20, never under 20.

func vmMemoryMiB(totalBytes int64) int {
	mib := int(totalBytes / 1048576 * 3 / 4)
	if mib < 8192 {
		mib = 8192
	}
	return mib
}

func vmCPUs(n int) int {
	if n-2 < 4 {
		return 4
	}
	return n - 2
}

func diskGB(freeGB, asked int) (int, error) {
	gb := asked
	if gb <= 0 {
		gb = applianceDiskGB
	}
	if cap := freeGB - applianceDiskKeepGB; gb > cap {
		gb = cap
	}
	if gb < applianceDiskMinGB {
		return 0, fmt.Errorf("this volume has %d GB free; the appliance needs a %d GB disk and leaves %d GB free, so free up %d GB first",
			freeGB, applianceDiskMinGB, applianceDiskKeepGB, applianceDiskMinGB+applianceDiskKeepGB-freeGB)
	}
	return gb, nil
}

// ---- the Mac ----------------------------------------------------------

// macFacts is what the Mac says about itself.
type macFacts struct {
	OS       string // 15.8.2
	Chip     string // Apple M4
	MemBytes int64
	CPUs     int
	HV       bool
}

var chipRe = regexp.MustCompile(`Apple M([3-9]|[1-9][0-9])`)

// macExec runs a command on the Mac and returns its output; tests
// replace it.
var macExec = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

func readMacFacts() (macFacts, error) {
	var f macFacts
	get := func(name string, args ...string) (string, error) {
		out, err := macExec(name, args...)
		if err != nil {
			return "", fmt.Errorf("%s: %w", name, err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	var err error
	if f.OS, err = get("sw_vers", "-productVersion"); err != nil {
		return f, err
	}
	if f.Chip, err = get("sysctl", "-n", "machdep.cpu.brand_string"); err != nil {
		return f, err
	}
	mem, err := get("sysctl", "-n", "hw.memsize")
	if err != nil {
		return f, err
	}
	f.MemBytes, _ = strconv.ParseInt(mem, 10, 64)
	cpus, err := get("sysctl", "-n", "hw.ncpu")
	if err != nil {
		return f, err
	}
	f.CPUs, _ = strconv.Atoi(cpus)
	hv, err := get("sysctl", "-n", "kern.hv_support")
	if err != nil {
		return f, err
	}
	f.HV = hv == "1"
	return f, nil
}

// checkMacForAppliance is the one refusal a Mac that cannot run the
// appliance reads; nil means it can (docs page "What a Mac mini needs").
func checkMacForAppliance(f macFacts) error {
	major, _, _ := strings.Cut(f.OS, ".")
	if n, err := strconv.Atoi(major); err != nil || n < 15 {
		return exitf(ExitGeneric, "macOS %s is older than 15; the appliance needs macOS 15 or later.", f.OS)
	}
	if !chipRe.MatchString(f.Chip) {
		return exitf(ExitGeneric, "%s cannot run the appliance; it needs an M3 chip or newer (nested virtualization).", f.Chip)
	}
	if !f.HV {
		return exitf(ExitGeneric, "This Mac reports no virtualization support (kern.hv_support).")
	}
	if f.MemBytes < 15_000_000_000 {
		return exitf(ExitGeneric, "This Mac has %d GB of memory; the appliance needs 16 GB or more.", f.MemBytes>>30)
	}
	return nil
}

// onAppleSilicon is whether this binary runs where the appliance can.
// A var, as macExec is, so a test on another box can drive the install.
var onAppleSilicon = func() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// ---- the join bundle --------------------------------------------------

// joinBundle is the install step's argument as the api mints it
// (docs/interfaces/api.md `install.argument`): the token and the
// machine's place on the hub. The launcher validates it and hands it to
// the appliance whole; the appliance splits it.
type joinBundle struct {
	Version int    `json:"v"`
	Token   string `json:"token"`
}

func decodeJoinBundle(s string) (joinBundle, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return joinBundle{}, errors.New("the argument is not what `repose host add` printed")
	}
	var b joinBundle
	if err := json.Unmarshal(raw, &b); err != nil || b.Version != 1 || b.Token == "" {
		return joinBundle{}, errors.New("the argument is not what `repose host add` printed")
	}
	return b, nil
}

// encodeTokenBundle wraps a bare token the way the api wraps its
// argument, for an api that answered a token and no argument.
func encodeTokenBundle(token string) string {
	raw, _ := json.Marshal(joinBundle{Version: 1, Token: token})
	return base64.RawURLEncoding.EncodeToString(raw)
}

// ---- the files --------------------------------------------------------

// applianceConfig is `config` beside the disk: the knobs `host run`
// reads, one per line, so a user can edit one and start again.
type applianceConfig struct {
	MemoryMiB int
	CPUs      int
	MAC       string
	HostName  string
}

func (c applianceConfig) String() string {
	return fmt.Sprintf("VM_MEMORY_MIB=%d\nVM_CPUS=%d\nVM_MAC=%s\nHOST_NAME=%s\n", c.MemoryMiB, c.CPUs, c.MAC, c.HostName)
}

func readApplianceConfig(path string) (applianceConfig, error) {
	var c applianceConfig
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "VM_MEMORY_MIB":
			c.MemoryMiB, _ = strconv.Atoi(v)
		case "VM_CPUS":
			c.CPUs, _ = strconv.Atoi(v)
		case "VM_MAC":
			c.MAC = v
		case "HOST_NAME":
			c.HostName = v
		}
	}
	if c.MemoryMiB == 0 || c.CPUs == 0 || c.MAC == "" {
		return c, fmt.Errorf("%s: no VM_MEMORY_MIB, VM_CPUS and VM_MAC", path)
	}
	return c, nil
}

// randomMAC is a locally administered address with no zero octet: macOS's
// DHCP lease file drops leading zeros, which cost the lab a run.
func randomMAC() string {
	var b [3]byte
	if _, err := io.ReadFull(randReader, b[:]); err != nil {
		b = [3]byte{0x2e, 0x43, 0xde}
	}
	for i := range b {
		b[i] = b[i]%239 + 16
	}
	return fmt.Sprintf("52:54:aa:%02x:%02x:%02x", b[0], b[1], b[2])
}

var randReader io.Reader = randomSource{}

type randomSource struct{}

func (randomSource) Read(p []byte) (int, error) {
	f, err := os.Open("/dev/urandom")
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadFull(f, p)
}

// launchdPlist is the agent that runs `repose host run` at every login
// and brings it back if it exits. ExitTimeOut is how long launchd waits
// after its SIGTERM before it kills the launcher, and with it the VM:
// the default is 20 s, which no park fits in.
func launchdPlist(self, dir, launcherLog string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + applianceLabel + `</string>
  <key>ProgramArguments</key>
  <array><string>` + self + `</string><string>host</string><string>run</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ExitTimeOut</key><integer>` + strconv.Itoa(int((applianceStopGrace+30*time.Second)/time.Second)) + `</integer>
  <key>ProcessType</key><string>Interactive</string>
  <key>WorkingDirectory</key><string>` + dir + `</string>
  <key>StandardOutPath</key><string>` + launcherLog + `</string>
  <key>StandardErrorPath</key><string>` + launcherLog + `</string>
</dict>
</plist>
`
}

// vfkitArgs is the VM: nested virtualization, EFI with its variable
// store beside the image, the one disk, NAT, entropy, the launcher's
// share, the park-key vsock (the guest dials the port, vfkit connects
// to the unix socket `host run` listens on), and the console to a file.
func vfkitArgs(p appliancePaths, c applianceConfig) []string {
	return []string{
		"--cpus", strconv.Itoa(c.CPUs), "--memory", strconv.Itoa(c.MemoryMiB), "--nested",
		"--bootloader", "efi,variable-store=" + p.EFIVars + ",create",
		"--device", "virtio-blk,path=" + p.Disk,
		"--device", "virtio-net,nat,mac=" + c.MAC,
		"--device", "virtio-rng",
		"--device", "virtio-fs,sharedDir=" + p.Share + ",mountTag=" + applianceShareTag,
		"--device", "virtio-vsock,port=" + strconv.Itoa(parkkey.Port) + ",socketURL=" + p.ParkKeySock,
		"--device", "virtio-serial,logFilePath=" + p.ConsoleLog,
		"--restful-uri", "unix://" + p.Sock,
	}
}

// unpackSparse writes the compressed image to dest as a sparse file:
// whole zero blocks of 1 MiB become holes, so the volume pays for the
// image's data and not its zeros (the 37 GiB NixOS image cost the whole
// 37 GiB unpacked with gunzip on the lab Mac). The image is zstd since
// the Debian appliance (DECISIONS I-1494); a gzip one, which every image
// before it was, is read too, told apart by its first bytes.
func unpackSparse(r io.Reader, dest string) (written int64, err error) {
	zr, closeZ, err := decompressor(r)
	if err != nil {
		return 0, err
	}
	defer closeZ()
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	const block = 1 << 20
	buf := make([]byte, block)
	zero := make([]byte, block)
	var pos int64
	for {
		n, rerr := io.ReadFull(zr, buf)
		if n > 0 {
			if n == block && bytes.Equal(buf, zero) {
				pos += int64(n)
			} else {
				if _, err := f.WriteAt(buf[:n], pos); err != nil {
					return pos, err
				}
				pos += int64(n)
				written += int64(n)
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return pos, rerr
		}
	}
	if err := f.Truncate(pos); err != nil {
		return pos, err
	}
	return written, nil
}

// decompressor reads r as zstd or gzip, whichever its magic says.
func decompressor(r io.Reader) (io.Reader, func(), error) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(4)
	if err != nil {
		return nil, nil, fmt.Errorf("reading the image: %w", err)
	}
	switch {
	case bytes.Equal(magic, []byte{0x28, 0xb5, 0x2f, 0xfd}):
		zr, err := zstd.NewReader(br)
		if err != nil {
			return nil, nil, err
		}
		return zr, zr.Close, nil
	case magic[0] == 0x1f && magic[1] == 0x8b:
		zr, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, err
		}
		return zr, func() { _ = zr.Close() }, nil
	}
	return nil, nil, errors.New("the image is neither zstd nor gzip")
}

// applianceStallWindow is how long a download may make no progress
// before it is given up. There is no whole-request deadline on a 2 GB
// fetch (ENG-117, I-1414), so this is what ends a connection that died:
// TCP alone can hold a dead one open for a quarter of an hour. It covers
// the wait for the response headers too, because the watchdog starts
// before the request and counts from zero. A var so a test can shorten
// it; detection takes between one and two windows, since the watchdog
// ticks at the window.
var applianceStallWindow = 60 * time.Second

// applianceFetchTries is how many times a download is attempted. A 2 GB
// fetch over a home connection fails for real, and every attempt after
// the first continues from the bytes already on disk, so a retry is not
// 2 GB again.
const applianceFetchTries = 3

// applianceFetchFailed is what a customer reads when a download did not
// finish. `repose host add` minted the host before the install, so the
// account has it already and a plain second `repose host add` is refused
// by the one-host limit: the install alone, with the same argument, is
// the way out, and it continues from the part file (ENG-117). hostName
// is empty on the `--join` path, where the api minted nothing here.
func applianceFetchFailed(what string, err error, argumentPath, hostName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s did not download: %v.\n", what, err)
	if hostName != "" {
		fmt.Fprintf(&b, "%s is in your account already. ", hostName)
	}
	// The argument is a live key, so the line the customer is most
	// likely to copy keeps it off argv (ENG-42): it goes through a file
	// they already have, or a pipe.
	fmt.Fprintf(&b, "Run this on the Mac when the connection is back; it continues from what is already here:\n"+
		"  repose host add --join-file %s\n"+
		"That file holds the argument. It carries a key, so it is not passed on the command line.", argumentPath)
	return b.String()
}

// stallError is a download that stopped making progress, told apart from
// the context cancellation that the watchdog actually raises so the
// retry and the sentence can both name it.
type stallError struct {
	after  int64
	window time.Duration
}

func (e *stallError) Error() string {
	return fmt.Sprintf("no data for %s after %s", e.window, humanBytes(e.after))
}

// stallReader counts the bytes read through it, for the watchdog.
type stallReader struct {
	r io.Reader
	n atomic.Int64
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.n.Add(int64(n))
	return n, err
}

// watchProgress cancels the request when a whole window passes with no
// bytes read, and reports through fired so the error says so rather than
// blaming the context. It returns when done is closed.
func watchProgress(s *stallReader, window time.Duration, cancel context.CancelFunc, fired *atomic.Bool, done <-chan struct{}) {
	t := time.NewTicker(window)
	defer t.Stop()
	last := s.n.Load()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			now := s.n.Load()
			if now == last {
				fired.Store(true)
				cancel()
				return
			}
			last = now
		}
	}
}

// retryableFetch is whether another attempt is worth making: a network
// or read failure, a stall, or a server that is busy. A refusal, a
// missing asset or a checksum mismatch is not retried, so a wrong
// published checksum does not cost three downloads.
func retryableFetch(err error) bool {
	var st *fetchStatusError
	if errors.As(err, &st) {
		return st.status == http.StatusTooManyRequests || st.status >= 500
	}
	var stall *stallError
	if errors.As(err, &stall) {
		return true
	}
	var sum *checksumError
	if errors.As(err, &sum) {
		return sum.resumed
	}
	return true
}

// fetchStatusError is a response that was not 200 or 206.
type fetchStatusError struct {
	url    string
	status int
}

func (e *fetchStatusError) Error() string {
	return fmt.Sprintf("%s: the server answered %d", e.url, e.status)
}

// checksumError is a complete download that is not the file we wanted.
// resumed says the attempt continued from a part file, which is the one
// way this is worth retrying: the part may be a prefix of an older
// image, left by an install the customer gave up on before the CLI was
// upgraded. The part file is gone by then, so the retry starts fresh.
type checksumError struct {
	name, got, want string
	resumed         bool
}

func (e *checksumError) Error() string {
	return fmt.Sprintf("%s: checksum %s, not %s; nothing was installed", e.name, e.got, e.want)
}

// openRange is the body at url from byte off, and the offset the body
// actually starts at: off when the server honoured the range, 0 when it
// sent the whole file instead.
func openRange(ctx context.Context, c *http.Client, url string, off int64) (io.ReadCloser, int64, error) {
	if path, ok := strings.CutPrefix(url, "file://"); ok {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, err
		}
		if off > 0 {
			if _, err := f.Seek(off, io.SeekStart); err != nil {
				_ = f.Close()
				return nil, 0, err
			}
		}
		return f, off, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if off > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, 0, nil
	case http.StatusPartialContent:
		return resp.Body, off, nil
	}
	_ = resp.Body.Close()
	return nil, 0, &fetchStatusError{url: url, status: resp.StatusCode}
}

// hashFile is the SHA-256 of what is in the part file and how many bytes
// that was, so a continued download still reports the digest of the
// whole file.
func hashFile(h hash.Hash, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	return io.Copy(h, f)
}

// fetchTo downloads url to dest, verifying its SHA-256 when want is set,
// and reports the hex digest. A failed attempt leaves what it got in
// dest.part and the next one continues from there; out, when it is not
// nil, gets a line per retry, because a 2 GB fetch that starts again in
// silence reads as a hang.
func fetchTo(ctx context.Context, c *http.Client, out io.Writer, url, dest, want string) (string, error) {
	var got string
	var err error
	for try := 1; ; try++ {
		got, err = fetchOnce(ctx, c, url, dest, want)
		if err == nil || try == applianceFetchTries || ctx.Err() != nil || !retryableFetch(err) {
			return got, err
		}
		if out != nil {
			from := ""
			if st, serr := os.Stat(dest + ".part"); serr == nil && st.Size() > 0 {
				from = fmt.Sprintf(", continuing from %s", humanBytes(st.Size()))
			}
			_, _ = fmt.Fprintf(out, "%v; trying again (%d of %d)%s\n", err, try+1, applianceFetchTries, from)
		}
		select {
		case <-ctx.Done():
			return got, err
		case <-time.After(2 * time.Second):
		}
	}
}

// fetchOnce is one attempt at url into dest.part, continuing from what
// an earlier attempt left there, and renames it onto dest when it has
// the whole file.
func fetchOnce(ctx context.Context, c *http.Client, url, dest, want string) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	part := dest + ".part"
	h := sha256.New()
	var off int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		if n, err := hashFile(h, part); err == nil {
			off = n
		} else {
			h.Reset()
		}
	}

	sr := &stallReader{}
	var fired atomic.Bool
	done := make(chan struct{})
	go watchProgress(sr, applianceStallWindow, cancel, &fired, done)
	defer close(done)

	body, start, err := openRange(ctx, c, url, off)
	if err != nil {
		return "", fetchErr(err, &fired, sr, 0)
	}
	defer func() { _ = body.Close() }()
	if start != off {
		// The server ignored the range and sent the whole file: start
		// over rather than append to what is already there.
		h.Reset()
		off = 0
	}
	flags := os.O_CREATE | os.O_WRONLY
	if off == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	sr.r = body
	_, err = io.Copy(io.MultiWriter(f, h), sr)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// What arrived stays in the part file: the next attempt, in this
		// process or the next run, continues from it.
		return "", fetchErr(err, &fired, sr, off)
	}
	gotSum := hex.EncodeToString(h.Sum(nil))
	if want != "" && gotSum != want {
		_ = os.Remove(part)
		return gotSum, &checksumError{name: filepath.Base(dest), got: gotSum, want: want, resumed: off > 0}
	}
	return gotSum, os.Rename(part, dest)
}

// fetchErr names a stall as a stall: the watchdog cancels the request, so
// what the copy returns is a context cancellation that says nothing
// about what happened.
func fetchErr(err error, fired *atomic.Bool, sr *stallReader, had int64) error {
	if fired.Load() {
		return &stallError{after: had + sr.n.Load(), window: applianceStallWindow}
	}
	return err
}

// fetchText is a small file (a checksum) as a string. The download
// client has no whole-request deadline, because an image is 2 GB
// (ENG-117); 64 bytes get one of their own, so a server that accepts the
// connection and says nothing does not hold the install.
func fetchText(ctx context.Context, c *http.Client, url string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	body, _, err := openRange(ctx, c, url, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	b, err := io.ReadAll(io.LimitReader(body, 4096))
	return strings.TrimSpace(string(b)), err
}

var freeGBOf = func(dir string) int {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int(uint64(st.Bavail) * uint64(st.Bsize) >> 30)
}

// ---- install ----------------------------------------------------------

// applianceOptionsFrom reads the three sizing flags as the customer
// typed them. They are strings rather than ints for two reasons
// (ENG-38). An int flag's zero value is also "not given", so `--cpus 0`
// was indistinguishable from the flag left out and silently took the
// default, where the same CLI refuses `--expires 0`. And pflag wraps an
// int parse failure as `invalid argument "banana" for "--disk" flag:
// strconv.ParseInt: ...`, which is Go's sentence and not this CLI's.
// An empty string is the flag left out, and 0 in the result still means
// "take the default", which is what the sizing rules below read.
func applianceOptionsFrom(disk, memory, cpus string) (applianceOptions, error) {
	var o applianceOptions
	var err error
	if o.DiskGB, err = wholeAtLeast(disk, "gigabytes", applianceDiskMinGB); err != nil {
		return o, err
	}
	if o.MemoryMiB, err = wholeAtLeast(memory, "MiB of memory", applianceMinMemoryMiB); err != nil {
		return o, err
	}
	if o.CPUs, err = wholeAtLeast(cpus, "cores", applianceMinCPUs); err != nil {
		return o, err
	}
	return o, nil
}

// wholeAtLeast reads one of them: "" is the flag left out, anything else
// is a whole number at or above floor, refused in prose that quotes the
// input as typed.
func wholeAtLeast(in, what string, floor int) (int, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(in)
	if err != nil {
		return 0, cobraUsageError{fmt.Errorf("%q is not a whole number of %s", in, what)}
	}
	if n < floor {
		return 0, cobraUsageError{fmt.Errorf("the appliance needs at least %d %s, not %d", floor, what, n)}
	}
	return n, nil
}

// readJoinArgument is the install argument, from wherever the customer
// put it: `--join -` reads standard input, `--join-file PATH` reads a
// file, and `--join VALUE` still works for the lab. The argument is
// base64url JSON carrying a live WireGuard private key (I-1380), so as
// a flag value it sits in argv for the whole install, about 97 seconds
// on the AWS Mac, where any process on that Mac can read it from `ps`
// (ENG-42). The key dies at Register, so the window is the install, and
// the window is the whole of it. `repose secrets set` already answers
// this for a secret's value and this follows it rather than inventing a
// second shape.
func readJoinArgument(join, file string, stdin io.Reader) (string, error) {
	switch {
	case join != "" && file != "":
		return "", cobraUsageError{errors.New("--join and --join-file are two ways to give one argument; pass one")}
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return "", exitf(ExitUsage, "Could not read the install argument from %s: %v", file, err)
		}
		return trimArgument(string(b), file)
	case join == "-":
		if stdin == nil {
			stdin = os.Stdin
		}
		b, err := io.ReadAll(io.LimitReader(stdin, maxJoinBytes+1))
		if err != nil {
			return "", exitf(ExitUsage, "Could not read the install argument from standard input: %v", err)
		}
		return trimArgument(string(b), "standard input")
	}
	return join, nil
}

// maxJoinBytes bounds what is read; the bundle is a few hundred bytes
// and a pipe that never ends must not fill memory.
const maxJoinBytes = 64 << 10

func trimArgument(s, from string) (string, error) {
	s = strings.TrimRight(s, "\r\n")
	s = strings.TrimSpace(s)
	if s == "" {
		return "", exitf(ExitUsage, "No install argument on %s.", from)
	}
	return s, nil
}

// applianceCanInstall is every refusal that does not need a host: this
// Mac's own facts and the volume's free space. `repose host add` runs
// it before it mints anything, because the mint is not free to undo. A
// refusal after the mint left the host record behind, so the retry read
// "You already have a host named mac-park2", `--join` had no argument
// (the refused run printed none) and the one-host plan refused a second
// name: nothing the customer could type recovered it (ENG-71, found on
// the Mac at 58 GB free; DECISIONS I-1422).
//
// It is the CLI's order, not the api's: the api is asked once, when
// there is something to ask it for.
func applianceCanInstall(e *Env, o applianceOptions) error {
	facts, err := readMacFacts()
	if err != nil {
		return exitf(ExitGeneric, "Could not read this Mac's facts: %v", err)
	}
	if err := checkMacForAppliance(facts); err != nil {
		return err
	}
	p := applianceFor(e.HomeDir)
	if _, err := os.Stat(p.Config); err == nil {
		// Installed: nothing here will be sized, so nothing here refuses.
		return nil
	}
	// The directory is made by the install, so before one has run the
	// volume to measure is the home it sits in.
	dir := p.Dir
	if _, err := os.Stat(dir); err != nil {
		dir = e.HomeDir
	}
	if _, err := diskGB(freeGBOf(dir), o.DiskGB); err != nil {
		return exitf(ExitUsage, "%v", err)
	}
	return nil
}

// applianceOptions is what `repose host add` takes on a Mac.
type applianceOptions struct {
	DiskGB    int
	MemoryMiB int
	CPUs      int
}

// applianceInstall puts the appliance on this Mac with the argument and
// starts it (I-1380). hostName is what the api called the host, or ""
// when the argument was minted elsewhere. Progress goes to stderr; what
// to read next does not.
func applianceInstall(ctx context.Context, e *Env, argument, hostName string, o applianceOptions) error {
	if !onAppleSilicon() {
		return exitf(ExitGeneric, "The appliance runs on a Mac with Apple silicon; this is %s/%s.", runtime.GOOS, runtime.GOARCH)
	}
	if _, err := decodeJoinBundle(argument); err != nil {
		return exitf(ExitUsage, "%s", err.Error())
	}
	facts, err := readMacFacts()
	if err != nil {
		return exitf(ExitGeneric, "Could not read this Mac's facts: %v", err)
	}
	if err := checkMacForAppliance(facts); err != nil {
		return err
	}
	p := applianceFor(e.HomeDir)
	for _, d := range []string{filepath.Dir(p.Vfkit), p.Share, p.Logs, filepath.Dir(p.Plist)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	for _, d := range []string{p.Dir, p.Share} {
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	// The argument, kept where a retry can read it without putting a
	// live key in argv (ENG-42). 0600 in a 0700 directory, and it goes
	// with the directory at `repose host rm`.
	if err := os.WriteFile(p.Argument, []byte(argument+"\n"), 0o600); err != nil {
		return err
	}
	client := e.applianceHTTP()

	// What `installed` means, decided once and in one place: a config on
	// disk. Everything before it -- a part file, an unpacked disk, a
	// vfkit binary -- is an install that did not finish, which this run
	// adopts and which `repose host rm` clears (ENG-71, ENG-118,
	// DECISIONS I-1422).
	_, cfgErr := os.Stat(p.Config)
	installed := cfgErr == nil

	// The sizing guard, before anything is fetched and on every run that
	// is not already installed. It used to run after the unpack, inside
	// the block a second run skips: so a refusal left disk.raw behind at
	// the image's own size, and the same command run again adopted it,
	// installing a disk sized to the image on a volume the product had
	// just said could not hold it (ENG-71, on the AWS M4 at 46 GB free).
	// Up here it costs no download, leaves nothing behind, and answers
	// the retry with the same sentence.
	gb := 0
	if !installed {
		var err error
		if gb, err = diskGB(freeGBOf(p.Dir), o.DiskGB); err != nil {
			return exitf(ExitUsage, "%v", err)
		}
	}

	if _, err := os.Stat(p.Vfkit); err != nil {
		url, want := envOr(envVfkitURL, vfkitURL), envOr(envVfkitSHA256, vfkitSHA256)
		_, _ = fmt.Fprintf(e.ErrOut, "fetching vfkit %s\n", vfkitVersion)
		if _, err := fetchTo(ctx, client, e.ErrOut, url, p.Vfkit, want); err != nil {
			return exitf(ExitGeneric, "%s", applianceFetchFailed("vfkit", err, p.Argument, hostName))
		}
		if err := os.Chmod(p.Vfkit, 0o755); err != nil {
			return err
		}
		_, _ = macExec("xattr", "-d", "com.apple.quarantine", p.Vfkit)
	}

	if st, err := os.Stat(p.Disk); err != nil || st.Size() < applianceImageApparentBytes {
		url := envOr(envApplianceURL, applianceURL)
		_, _ = fmt.Fprintf(e.ErrOut, "fetching the appliance image %s (about 200 MB)\n", applianceVersion)
		sum, err := fetchText(ctx, client, url+".sha256")
		if err != nil {
			return exitf(ExitGeneric, "%s", applianceFetchFailed("The appliance image's checksum", err, p.Argument, hostName))
		}
		if f := strings.Fields(sum); len(f) > 0 {
			sum = f[0]
		}
		img := filepath.Join(p.Dir, "image.raw.zst")
		if _, err := fetchTo(ctx, client, e.ErrOut, url, img, sum); err != nil {
			return exitf(ExitGeneric, "%s", applianceFetchFailed("The appliance image", err, p.Argument, hostName))
		}
		_, _ = fmt.Fprintln(e.ErrOut, "unpacking the image")
		in, err := os.Open(img)
		if err != nil {
			return err
		}
		_, err = unpackSparse(in, p.Disk+".part")
		_ = in.Close()
		if err != nil {
			return exitf(ExitGeneric, "unpacking the image: %v", err)
		}
		if err := os.Rename(p.Disk+".part", p.Disk); err != nil {
			return err
		}
		_ = os.Remove(img)
	}

	// The disk the user chose, as a sparse file APFS fills as the
	// machines write; the appliance grows its pool into it at boot. Every
	// install that is not already live sizes it, including one that found
	// a disk a previous attempt left: at the image's own size there is
	// nothing after the pool partition for the boot-time grow to claim,
	// and `repose-mac-grow` fails while the host still registers and
	// reads online (ENG-79). A live appliance is never resized here:
	// its disk has machines on it.
	if !installed {
		// Grow only, never shrink. Every real plan is a grow (the floor
		// is 20 GB and the image is 2.1 GiB), so this costs nothing and it
		// closes the one window where a resize could destroy a live
		// disk: two `host add` on one Mac at once, where the second
		// still reads `installed` false while the first has written its
		// config and started vfkit.
		if st, err := os.Stat(p.Disk); err == nil && st.Size() < int64(gb)<<30 {
			if err := os.Truncate(p.Disk, int64(gb)<<30); err != nil {
				return err
			}
		}
	}

	if _, err := os.Stat(p.Config); err != nil {
		c := applianceConfig{MemoryMiB: o.MemoryMiB, CPUs: o.CPUs, MAC: randomMAC(), HostName: hostName}
		if c.MemoryMiB == 0 {
			c.MemoryMiB = vmMemoryMiB(facts.MemBytes)
		}
		if c.CPUs == 0 {
			c.CPUs = vmCPUs(facts.CPUs)
		}
		if err := os.WriteFile(p.Config, []byte(c.String()), 0o644); err != nil {
			return err
		}
	} else if hostName != "" {
		c, err := readApplianceConfig(p.Config)
		if err == nil && c.HostName != hostName {
			c.HostName = hostName
			_ = os.WriteFile(p.Config, []byte(c.String()), 0o644)
		}
	}

	// The argument, for the first boot alone: the appliance deletes it
	// from the share once read.
	if err := os.WriteFile(p.Join+".tmp", []byte(argument+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(p.Join+".tmp", p.Join); err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.WriteFile(p.Plist, []byte(launchdPlist(self, p.Dir, p.LauncherLog)), 0o644); err != nil {
		return err
	}
	return applianceStart(e, p, self)
}

// applianceHTTP is the client the downloads use: the Env's transport, so
// -v and --timing still log, on a client with no whole-request deadline,
// since an image is 2 GB. The Env's own client carries a 30 s Timeout
// for the api's calls and http.Client.Timeout covers reading the body,
// so taking that client whole capped the image download at 30 seconds
// and failed the first `repose host add` about half the time (ENG-117,
// I-1414). What ends a dead connection instead is the stall window in
// fetchOnce. A test's client is still the one used; only its deadline
// goes.
func (e *Env) applianceHTTP() *http.Client {
	if e.httpClient == nil {
		return &http.Client{}
	}
	c := *e.httpClient
	c.Timeout = 0
	return &c
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// launchdDomain is the user's GUI session when there is one, else the
// per-user domain (a Mac reached over ssh alone).
func launchdDomain() string {
	uid := strconv.Itoa(os.Getuid())
	if _, err := macExec("launchctl", "print", "gui/"+uid); err == nil {
		return "gui/" + uid
	}
	return "user/" + uid
}

// applianceStart hands the agent to launchd, and when launchd takes no
// agent (a session-less domain, "Bad request"), starts `host run`
// detached from a subshell with every descriptor off the terminal,
// because `nohup` under a tty does not detach on macOS and the VM died
// with the lab's ssh session. The agent takes over at the next login.
func applianceStart(e *Env, p appliancePaths, self string) error {
	dom := launchdDomain()
	if _, err := macExec("launchctl", "bootstrap", dom, p.Plist); err == nil {
		return nil
	}
	if _, err := macExec("launchctl", "kickstart", "-k", dom+"/"+applianceLabel); err == nil {
		return nil
	}
	if applianceRunning(p) {
		return nil
	}
	_, _ = fmt.Fprintln(e.ErrOut, "launchd took no agent for this session; the appliance starts detached and the agent takes over at the next login")
	log, err := os.OpenFile(p.LauncherLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.Command(self, "host", "run")
	cmd.Dir = p.Dir
	cmd.Stdin = nil
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ---- run ----------------------------------------------------------------

// applianceRun holds the VM in the foreground (what launchd calls): vfkit
// under caffeinate, the park-key service beside it for as long as the VM
// runs, and a clean shutdown of the appliance when launchd stops it
// (SIGTERM), which the appliance takes as the power button, parks every
// machine (nix/hosts/park.nix, I-1386) and powers off.
func applianceRun(ctx context.Context, e *Env) error {
	if !onAppleSilicon() {
		return exitf(ExitGeneric, "The appliance runs on a Mac with Apple silicon; this is %s/%s.", runtime.GOOS, runtime.GOARCH)
	}
	p := applianceFor(e.HomeDir)
	c, err := readApplianceConfig(p.Config)
	if err != nil {
		return exitf(ExitGeneric, "No appliance on this Mac (%v); `repose host add` puts one here.", err)
	}
	if st, err := os.Stat(p.Disk); err != nil || st.Size() == 0 {
		return exitf(ExitGeneric, "No appliance image at %s; run `repose host add` again.", p.Disk)
	}
	_ = os.Remove(p.Sock)
	logf := func(format string, args ...any) {
		_, _ = fmt.Fprintf(e.Out, time.Now().UTC().Format(time.RFC3339)+" "+format+"\n", args...)
	}
	logf("starting: cpus=%d memory_mib=%d", c.CPUs, c.MemoryMiB)
	// The park-key service outlives the SIGTERM below on purpose: the
	// park that a stop triggers wraps its keys through it.
	_ = os.Remove(p.ParkKeySock)
	ln, err := net.Listen("unix", p.ParkKeySock)
	if err != nil {
		return exitf(ExitGeneric, "park-key socket: %v", err)
	}
	_ = os.Chmod(p.ParkKeySock, 0o600)
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go parkkey.Serve(serveCtx, ln, &parkKeychainKeeper{log: logf}, logf)
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	args := append([]string{"-s", "-i", p.Vfkit}, vfkitArgs(p, c)...)
	cmd := exec.Command("caffeinate", args...)
	cmd.Stdout, cmd.Stderr = e.Out, e.ErrOut
	if err := cmd.Start(); err != nil {
		return exitf(ExitGeneric, "vfkit: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		logf("stopped")
		if err != nil {
			return exitf(ExitGeneric, "vfkit: %v", err)
		}
		return nil
	case <-ctx.Done():
		logf("stopping: the appliance parks its machines and powers off")
		applianceShutdown(p, applianceStopGrace)
		<-done
		logf("stopped")
		return nil
	}
}

// vfkitClient speaks to vfkit's REST socket.
func vfkitClient(sock string) *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
}

// vmState is vfkit's state, "" when it does not answer.
func vmState(p appliancePaths) string {
	resp, err := vfkitClient(p.Sock).Get("http://vfkit/vm/state")
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var v struct {
		State string `json:"state"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&v)
	return v.State
}

func applianceRunning(p appliancePaths) bool { return vmState(p) != "" }

func vmRequest(p appliancePaths, state string) {
	body := strings.NewReader(`{"state":"` + state + `"}`)
	resp, err := vfkitClient(p.Sock).Post("http://vfkit/vm/state", "application/json", body)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// applianceShutdown presses the power button and gives the appliance
// `grace` to power off, then pulls the plug.
func applianceShutdown(p appliancePaths, grace time.Duration) {
	vmRequest(p, "Stop")
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		s := vmState(p)
		if s == "" || s == "VirtualMachineStateStopped" {
			return
		}
		time.Sleep(time.Second)
	}
	vmRequest(p, "HardStop")
}

// ---- stop and remove --------------------------------------------------

// applianceStop stops the VM cleanly: through launchd when the agent
// holds it, else through vfkit's socket.
func applianceStop(p appliancePaths) {
	dom := launchdDomain()
	if _, err := os.Stat(p.Plist); err == nil {
		if _, err := macExec("launchctl", "print", dom+"/"+applianceLabel); err == nil {
			_, _ = macExec("launchctl", "bootout", dom+"/"+applianceLabel)
			deadline := time.Now().Add(applianceStopGrace + 10*time.Second)
			for applianceRunning(p) && time.Now().Before(deadline) {
				time.Sleep(time.Second)
			}
			return
		}
	}
	if applianceRunning(p) {
		applianceShutdown(p, applianceStopGrace)
	}
}

// applianceRemove takes the appliance off this Mac: the VM stopped, the
// agent gone, the directory with the disk, every machine's volume and
// snapshot in it, removed.
func applianceRemove(e *Env) error {
	p := applianceFor(e.HomeDir)
	applianceStop(p)
	_, _ = macExec("launchctl", "bootout", launchdDomain()+"/"+applianceLabel)
	_ = os.Remove(p.Plist)
	return os.RemoveAll(p.Dir)
}

// appliancePresent is whether there is anything of an appliance on this
// Mac to remove, finished or not: a part file from a download that
// stopped, a disk from an install that did not reach its config, a
// vfkit binary, or the launchd agent. localAppliance below answers a
// different question, whether one is installed and live, and the two
// were the same question until an unfinished install left bytes that
// `rm --local` refused to clear and `rm NAME` walked past (ENG-118,
// DECISIONS I-1422).
func appliancePresent(e *Env) bool {
	if !onAppleSilicon() {
		return false
	}
	p := applianceFor(e.HomeDir)
	if _, err := os.Stat(p.Plist); err == nil {
		return true
	}
	ents, err := os.ReadDir(p.Dir)
	return err == nil && len(ents) > 0
}

// localAppliance is the appliance installed on this Mac, if any: its
// recorded host name ("" for one added with --join) and whether it is
// there at all.
func localAppliance(e *Env) (name string, installed bool) {
	if !onAppleSilicon() {
		return "", false
	}
	p := applianceFor(e.HomeDir)
	c, err := readApplianceConfig(p.Config)
	if err != nil {
		return "", false
	}
	return c.HostName, true
}
