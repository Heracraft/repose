package cli

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	"syscall"
	"time"

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
// DECISIONS I-1386), which is what lets a restart of the Mac keep what
// every machine was doing.

const (
	applianceVersion  = "2026.11.19"
	applianceURL      = "https://github.com/Heracraft/repose/releases/download/appliance-" + applianceVersion + "/repose-appliance-aarch64.raw.gz"
	vfkitVersion      = "0.6.4"
	vfkitURL          = "https://github.com/crc-org/vfkit/releases/download/v" + vfkitVersion + "/vfkit"
	vfkitSHA256       = "0ed83fc8ca7aa708598835480dba1362406aa7cd1dab3b27464eb76327d9652d"
	applianceLabel    = "run.repose.appliance"
	applianceShareTag = "repose-launcher"
	applianceDiskGB   = 100
	// applianceDiskMinGB is the smallest disk the image fits: the raw
	// image is 37 GiB as built (its root partition ends there), so a disk
	// file truncated below it loses the root partition's end and the
	// pool's partition, and the appliance never finds its root (the lab
	// image 6 install, 2026-11-19, on a Mac with 52 GB free).
	applianceDiskMinGB = 40
	// applianceDiskKeepGB stays free on the volume for the user.
	applianceDiskKeepGB = 20
	// applianceImageApparentBytes is the raw image's size as built (37
	// GiB): a disk file smaller than that is a broken unpack.
	applianceImageApparentBytes = 37 << 30
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
	Dir, Logs, Plist, Config, Disk, Share, Join, Sock, ParkKeySock, EFIVars, Vfkit, LauncherLog, ConsoleLog string
}

func applianceFor(home string) appliancePaths {
	dir := filepath.Join(home, "Library", "Application Support", "repose")
	logs := filepath.Join(home, "Library", "Logs", "repose")
	return appliancePaths{
		Dir:         dir,
		Logs:        logs,
		Plist:       filepath.Join(home, "Library", "LaunchAgents", applianceLabel+".plist"),
		Config:      filepath.Join(dir, "config"),
		Disk:        filepath.Join(dir, "disk.raw"),
		Share:       filepath.Join(dir, "share"),
		Join:        filepath.Join(dir, "share", "join"),
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
func onAppleSilicon() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

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

// unpackSparse writes the gzipped image to dest as a sparse file: the
// image is 37 GiB of which a few are data, and writing every zero would
// cost the volume the whole 37 GiB (measured on the lab Mac). Whole
// zero blocks of 1 MiB become holes.
func unpackSparse(r io.Reader, dest string) (written int64, err error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return 0, err
	}
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

// openURL is the body at url: over HTTP, or a local file for a `file://`
// URL, which the lab uses for an image copied beside the Mac
// (ops/dev/macmini appliance-install).
func openURL(ctx context.Context, c *http.Client, url string) (io.ReadCloser, error) {
	if path, ok := strings.CutPrefix(url, "file://"); ok {
		return os.Open(path)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("%s: the server answered %d", url, resp.StatusCode)
	}
	return resp.Body, nil
}

// fetchTo downloads url to dest, verifying its SHA-256 when want is set,
// and reports the hex digest.
func fetchTo(ctx context.Context, c *http.Client, url, dest, want string) (string, error) {
	body, err := openURL(ctx, c, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	part := dest + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(part)
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if want != "" && got != want {
		_ = os.Remove(part)
		return got, fmt.Errorf("%s: checksum %s, not %s; nothing was installed", filepath.Base(dest), got, want)
	}
	return got, os.Rename(part, dest)
}

// fetchText is a small file (a checksum) as a string.
func fetchText(ctx context.Context, c *http.Client, url string) (string, error) {
	body, err := openURL(ctx, c, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	b, err := io.ReadAll(io.LimitReader(body, 4096))
	return strings.TrimSpace(string(b)), err
}

func freeGBOf(dir string) int {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0
	}
	return int(uint64(st.Bavail) * uint64(st.Bsize) >> 30)
}

// ---- install ----------------------------------------------------------

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
	client := e.applianceHTTP()

	if _, err := os.Stat(p.Vfkit); err != nil {
		url, want := envOr(envVfkitURL, vfkitURL), envOr(envVfkitSHA256, vfkitSHA256)
		_, _ = fmt.Fprintf(e.ErrOut, "fetching vfkit %s\n", vfkitVersion)
		if _, err := fetchTo(ctx, client, url, p.Vfkit, want); err != nil {
			return exitf(ExitGeneric, "vfkit: %v", err)
		}
		if err := os.Chmod(p.Vfkit, 0o755); err != nil {
			return err
		}
		_, _ = macExec("xattr", "-d", "com.apple.quarantine", p.Vfkit)
	}

	if st, err := os.Stat(p.Disk); err != nil || st.Size() < applianceImageApparentBytes {
		url := envOr(envApplianceURL, applianceURL)
		_, _ = fmt.Fprintf(e.ErrOut, "fetching the appliance image %s (about 2 GB)\n", applianceVersion)
		sum, err := fetchText(ctx, client, url+".sha256")
		if err != nil {
			return exitf(ExitGeneric, "the image's checksum: %v", err)
		}
		if f := strings.Fields(sum); len(f) > 0 {
			sum = f[0]
		}
		gz := filepath.Join(p.Dir, "image.raw.gz")
		if _, err := fetchTo(ctx, client, url, gz, sum); err != nil {
			return exitf(ExitGeneric, "the image: %v", err)
		}
		_, _ = fmt.Fprintln(e.ErrOut, "unpacking the image")
		in, err := os.Open(gz)
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
		_ = os.Remove(gz)
		// The disk the user chose, as a sparse file APFS fills as the
		// machines write; the appliance grows its pool into it at boot.
		gb, err := diskGB(freeGBOf(p.Dir), o.DiskGB)
		if err != nil {
			return exitf(ExitUsage, "%v", err)
		}
		if err := os.Truncate(p.Disk, int64(gb)<<30); err != nil {
			return err
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

// applianceHTTP is the client the downloads use: the Env's when a test
// set one, else a plain client with no timeout, since an image is 2 GB.
func (e *Env) applianceHTTP() *http.Client {
	if e.httpClient != nil {
		return e.httpClient
	}
	return &http.Client{}
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
