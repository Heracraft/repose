package cli

import (
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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// What is left of design 1's appliance on a Mac (DECISIONS I-1376,
// I-1380, I-1684): a Linux VM, run by vfkit from a launchd job that ran
// `repose host run`, holding hostd and every machine. Design 2 installs
// none of it (machost.go), and there is no move across: a Mac that still
// holds an appliance is told so in one line (d1LeftLine), and `repose
// host rm --local` removes it, disk, machines and snapshots with it. What
// stays here is that removal, and the helpers the Mac's install shares
// (the downloads, the join argument, the Mac's facts, sudo).
//
// The appliance lived under ~/Library/Application Support/repose: disk.raw,
// config, vfkit.sock, bin/vfkit, the launchd agent or, with sudo, a
// LaunchDaemon in /Library/LaunchDaemons, and logs under
// ~/Library/Logs/repose.

const (
	applianceLabel = "run.repose.appliance"
	// applianceStopGrace is how long a removal gives the appliance to
	// power off before the plug is pulled: its park's 120 s deadline and
	// stop grace, inside the unit's 270 s wall, plus 60.
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

// ---- the Mac ----------------------------------------------------------

// macFacts is what the Mac says about itself.
type macFacts struct {
	OS       string // 15.8.2
	Chip     string // Apple M4
	MemBytes int64
	CPUs     int
	HV       bool
}

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

// applianceConfig is the appliance's `config` beside its disk; its
// HOST_NAME says which host the appliance was.
type applianceConfig struct {
	MemoryMiB int
	CPUs      int
	MAC       string
	HostName  string
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

// launchDaemonsDir is where the boot job's plist goes; tests move it.
var launchDaemonsDir = "/Library/LaunchDaemons"

func bootPlistPath() string { return filepath.Join(launchDaemonsDir, applianceLabel+".plist") }

// bootJobInstalled is whether the appliance starts when the Mac boots:
// its plist is in /Library/LaunchDaemons, which anyone may read.
func bootJobInstalled() bool {
	_, err := os.Stat(bootPlistPath())
	return err == nil
}

// macSudo runs `sudo` with the terminal, so it can ask for the password
// once, and with -n when there is no terminal to ask on, so it fails
// rather than hangs; tests replace it.
var macSudo = func(args ...string) error {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return exec.Command("sudo", append([]string{"-n"}, args...)...).Run()
	}
	cmd := exec.Command("sudo", args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	return cmd.Run()
}

// macUser is the Mac user's login name, whose session runs the machines.
// The darwin build has no cgo, so os/user cannot read the directory
// service.
func macUser() (string, error) {
	out, err := macExec("id", "-un")
	u := strings.TrimSpace(string(out))
	if err != nil || u == "" {
		return "", fmt.Errorf("id -un: %v", err)
	}
	return u, nil
}

// applianceFileVaultLine says the one thing a LaunchDaemon cannot do:
// with FileVault on, macOS itself waits at its unlock screen after a
// power cut, before any job of anyone's runs.
func applianceFileVaultLine(e *Env) {
	out, err := macExec("fdesetup", "status")
	if err == nil && strings.Contains(string(out), "FileVault is On") {
		_, _ = fmt.Fprintln(e.ErrOut, "FileVault is on: after a power cut or a restart this Mac waits at its unlock screen, and repose starts once someone unlocks it")
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

// applianceStop stops the VM cleanly: through launchd when a job holds
// it (the boot job needs sudo for that), else through vfkit's socket.
func applianceStop(p appliancePaths) error {
	if bootJobInstalled() {
		if _, err := macExec("launchctl", "print", "system/"+applianceLabel); err == nil {
			if err := macSudo("launchctl", "bootout", "system/"+applianceLabel); err != nil {
				return fmt.Errorf("the appliance starts at boot, and stopping it takes sudo, which refused: %v", err)
			}
			waitStopped(p)
			return nil
		}
	}
	if applianceStopAgent(p) {
		return nil
	}
	if applianceRunning(p) {
		applianceShutdown(p, applianceStopGrace)
	}
	return nil
}

// applianceStopAgent boots the login agent out, when it holds the VM,
// and waits for the VM to stop; false when no agent was loaded.
func applianceStopAgent(p appliancePaths) bool {
	dom := launchdDomain()
	if _, err := os.Stat(p.Plist); err != nil {
		return false
	}
	if _, err := macExec("launchctl", "print", dom+"/"+applianceLabel); err != nil {
		return false
	}
	_, _ = macExec("launchctl", "bootout", dom+"/"+applianceLabel)
	waitStopped(p)
	return true
}

func waitStopped(p appliancePaths) {
	deadline := time.Now().Add(applianceStopGrace + 10*time.Second)
	for applianceRunning(p) && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
}

// applianceRemove takes the appliance off this Mac: the VM stopped, the
// launchd job gone, the directory with the disk, every machine's volume
// and snapshot in it, removed. The boot job's plist is root's, so it
// goes first and through sudo; a refusal leaves everything in place
// rather than a job that restarts a VM on a disk that is gone.
func applianceRemove(e *Env) error {
	p := applianceFor(e.HomeDir)
	if err := applianceStop(p); err != nil {
		return err
	}
	if bootJobInstalled() {
		if err := macSudo("rm", "-f", bootPlistPath()); err != nil {
			return fmt.Errorf("removing %s takes sudo, which refused: %v", bootPlistPath(), err)
		}
	}
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
	if _, err := os.Stat(p.Plist); err == nil || bootJobInstalled() {
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
