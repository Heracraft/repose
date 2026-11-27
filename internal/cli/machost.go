package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/heracraft/repose/internal/hostd/control"
	"github.com/heracraft/repose/internal/machost"
)

// `repose host add` on a Mac with Apple silicon installs design 2
// (DECISIONS I-1662, I-1664, I-1684): hostd as a root LaunchDaemon that
// starts when the Mac boots, each machine its own Virtualization.framework
// VM in a launchd job of its own, the tools hostd runs beside it, and the
// updater (I-1686), all from one bundle in the release hostd-<version>:
// manifest-darwin.json, signed with the release key, names the bundle and
// its SHA-256. Everything is root's, under /Library (internal/machost has
// the layout and why). One step takes sudo, the copy into /Library and
// the two launchd jobs; the download and every check run as the user
// first. Then hostd registers with the join bundle over the edge tunnel,
// and the command returns when it has joined the account.

// macHostVersion is the hostd-<version> release `repose host add` installs
// on a Mac. The release must be published before a CLI that pins it
// (TestReleaseAssetsAnswerAnonymously checks it answers).
const macHostVersion = "2026.11.28.1"

// The Mac's lab knobs: a manifest to install from and the key it is
// signed with, for the Mac days before a release exists. No Mac owner
// sets them (docs_test lists them as internal).
const (
	envMacHostManifestURL = "REPOSE_MAC_HOST_MANIFEST_URL"
	envMacHostPubKey      = "REPOSE_MAC_HOST_PUBKEY"
)

// macHostPaths is the layout; tests move its root.
var macHostPaths = machost.Default

// macFS maps an absolute path on the Mac to where it is; a test points
// it into a temporary directory, so the same code reads the files the
// fake sudo wrote.
var macFS = func(p string) string { return p }

// macJoinWait is how long `repose host add` waits for hostd to join.
var macJoinWait = 3 * time.Minute

// macRecord is this Mac's install record, nil when repose is not
// installed here.
func macRecord() *machost.Record {
	b, err := os.ReadFile(macFS(macHostPaths.Record()))
	if err != nil {
		return nil
	}
	var r machost.Record
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}

// macHostPresent is whether anything of design 2 is on this Mac to
// remove, finished or not: the record, the launchd job, or the root.
func macHostPresent() bool {
	if !onAppleSilicon() {
		return false
	}
	for _, p := range []string{macHostPaths.Record(), machost.HostdPlist(), macHostPaths.Root} {
		if _, err := os.Stat(macFS(p)); err == nil {
			return true
		}
	}
	return false
}

// checkMacForHost is the refusal a Mac that cannot run repose reads.
// macOS 14 is repose-vm's target and the first with Virtualization's
// save and restore; memory is macOS's 4 GiB plus one 2 GiB machine and
// room (I-1666's floors).
func checkMacForHost(f macFacts) error {
	major, _, _ := strings.Cut(f.OS, ".")
	if n, err := strconv.Atoi(major); err != nil || n < 14 {
		return exitf(ExitGeneric, "macOS %s is older than 14; repose needs macOS 14 or later.", f.OS)
	}
	if !f.HV {
		return exitf(ExitGeneric, "This Mac reports no virtualization support (kern.hv_support).")
	}
	if f.MemBytes < 7_500_000_000 {
		return exitf(ExitGeneric, "This Mac has %d GB of memory; repose needs 8 GB or more.", f.MemBytes>>30)
	}
	return nil
}

// macHostCanInstall is every refusal that needs no host, run before one
// is minted (the mint is not free to undo, I-1422).
func macHostCanInstall(e *Env) error {
	if os.Geteuid() == 0 {
		return exitf(ExitUsage, "Run `repose host add` as yourself, not as root; it asks sudo for the one step that needs it.")
	}
	facts, err := readMacFacts()
	if err != nil {
		return exitf(ExitGeneric, "Could not read this Mac's facts: %v", err)
	}
	if err := checkMacForHost(facts); err != nil {
		return err
	}
	if appliancePresent(e) {
		return exitf(ExitGeneric, "%s", d1LeftLine)
	}
	if r := macRecord(); r != nil {
		return exitf(ExitGeneric, "This Mac is %s already; `repose host rm %s` takes it off first.", r.Host, r.Host)
	}
	if free := freeGBOf(macFS("/Library")); free > 0 && free < macHostMinFreeGB {
		return exitf(ExitGeneric, "This Mac has %d GB free; repose needs %d GB for itself and a machine's base image.", free, macHostMinFreeGB)
	}
	return nil
}

// macHostMinFreeGB is the bundle (about 50 MB), the guest base a first
// machine fetches (about 3 GB) and that machine's first writes.
const macHostMinFreeGB = 10

// d1LeftLine is what a Mac still holding design 1's appliance reads
// (DECISIONS I-1684): there is no move across; the appliance goes,
// machines and snapshots with it, and repose is installed again.
const d1LeftLine = "This Mac still has the appliance an earlier repose installed. `repose host rm --local` removes it, with its machines and snapshots, and then `repose host add` installs repose here."

// macJoin is the install argument split as hostd reads it: the one-shot
// token and the bundle's place on the hub (hub.json).
type macJoin struct {
	Token string
	Hub   json.RawMessage
}

func decodeMacJoin(s string) (macJoin, error) {
	if _, err := decodeJoinBundle(s); err != nil {
		return macJoin{}, err
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimSpace(s))
	var b struct {
		Token string          `json:"token"`
		WG    json.RawMessage `json:"wg"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return macJoin{}, errors.New("the argument is not what `repose host add` printed")
	}
	var wg map[string]any
	if json.Unmarshal(b.WG, &wg) != nil || wg["private_key"] == nil || wg["address"] == nil || wg["edge_pubkey"] == nil || wg["edge_endpoint"] == nil {
		// Without its place on the hub a Mac cannot reach the api: it has
		// no WireGuard but the one hostd brings up from this (I-1670).
		return macJoin{}, errors.New("the argument carries no place on repose's network; it is from an older repose. Run `repose host rm` and `repose host add` again")
	}
	return macJoin{Token: b.Token, Hub: b.WG}, nil
}

// macHostInstall installs repose on this Mac with the argument and waits
// for hostd to join. hostName is the host the api just made, or "" on the
// --join path; argumentPath is where the argument is kept for a retry.
func macHostInstall(ctx context.Context, e *Env, argument, hostName, argumentPath string) error {
	if !onAppleSilicon() {
		return exitf(ExitGeneric, "repose installs on a Mac with Apple silicon; `repose host add` elsewhere prints the step for the machine being added.")
	}
	join, err := decodeMacJoin(argument)
	if err != nil {
		return exitf(ExitUsage, "%s", err.Error())
	}
	if hostName == "" {
		// The --join path has had no checks yet.
		if err := macHostCanInstall(e); err != nil {
			return err
		}
	}
	user, err := macUser()
	if err != nil {
		return err
	}
	uid := os.Getuid()
	t0 := time.Now()

	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	stage := filepath.Join(cache, "repose", "host")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(stage, 0o700)
	failed := func(what string, err error) error {
		return exitf(ExitGeneric, "%s", macInstallFailed(what, err, argumentPath, hostName))
	}

	m, err := macHostManifest(ctx, e)
	if err != nil {
		return failed("Repose for this Mac", err)
	}
	_, _ = fmt.Fprintf(e.ErrOut, "fetching repose %s for this Mac\n", m.Version)
	bundle := filepath.Join(stage, machost.Bundle)
	if _, err := fetchTo(ctx, e.applianceHTTP(), e.ErrOut, m.Darwin.ARM64.URL, bundle, m.Darwin.ARM64.SHA256); err != nil {
		return failed("Repose for this Mac", err)
	}
	unpacked := filepath.Join(stage, m.Version)
	_ = os.RemoveAll(unpacked)
	f, err := os.Open(bundle)
	if err != nil {
		return err
	}
	v, err := machost.ExtractBundle(f, unpacked)
	_ = f.Close()
	if err != nil || v != m.Version {
		return exitf(ExitGeneric, "The bundle for this Mac is not the one its manifest names (%v); nothing was installed.", err)
	}
	if out, err := macExec(filepath.Join(unpacked, "bin", "hostd"), "version"); err != nil || strings.TrimSpace(string(out)) != "hostd "+m.Version {
		return exitf(ExitGeneric, "hostd %s does not run on this Mac (%v); nothing was installed.", m.Version, err)
	}

	p := macHostPaths
	rec, _ := json.Marshal(machost.Record{Host: hostName, User: user, UID: uid, Version: m.Version})
	var updArgs []string
	if u := os.Getenv(envMacHostManifestURL); u != "" {
		updArgs = append(updArgs, "--manifest-url", u)
	}
	if k := os.Getenv(envMacHostPubKey); k != "" {
		updArgs = append(updArgs, "--pubkey", filepath.Join(p.Root, "update.pub"))
	}
	files := map[string]struct {
		body []byte
		mode string
		dest string
	}{
		"hostd.plist":    {[]byte(machost.HostdPlistText(p, user, machost.DefaultAPI)), "0644", machost.HostdPlist()},
		"update.plist":   {[]byte(machost.UpdatePlistText(p, updArgs)), "0644", machost.UpdatePlist()},
		"newsyslog.conf": {[]byte(machost.NewsyslogText()), "0644", machost.NewsyslogConf},
		"api-ca.pem":     {[]byte(machost.HostCAPEM + "\n"), "0644", p.APICA()},
		"install.json":   {append(rec, '\n'), "0644", p.Record()},
		"join-token":     {[]byte(join.Token + "\n"), "0600", p.JoinToken()},
		"hub.json":       {append([]byte(join.Hub), '\n'), "0600", p.HubJSON()},
	}
	if k := os.Getenv(envMacHostPubKey); k != "" {
		b, err := os.ReadFile(k)
		if err != nil {
			return err
		}
		files["update.pub"] = struct {
			body []byte
			mode string
			dest string
		}{b, "0644", filepath.Join(p.Root, "update.pub")}
	}
	order := []string{"api-ca.pem", "install.json", "join-token", "hub.json", "newsyslog.conf", "update.pub", "hostd.plist", "update.plist"}
	for _, name := range order {
		fl, ok := files[name]
		if !ok {
			continue
		}
		if err := os.WriteFile(filepath.Join(stage, name), fl.body, 0o600); err != nil {
			return err
		}
	}
	// The token and the key are the user's for no longer than the copy.
	defer func() {
		for _, n := range []string{"join-token", "hub.json"} {
			_ = os.Remove(filepath.Join(stage, n))
		}
	}()

	if term.IsTerminal(int(os.Stdin.Fd())) {
		_, _ = fmt.Fprintln(e.ErrOut, "sudo asks for your password to install repose's service, which starts when this Mac boots")
	}
	ver := p.Version(m.Version)
	steps := [][]string{
		{"/bin/mkdir", "-p", p.Versions(), p.State(), machost.LogDir, filepath.Dir(machost.NewsyslogConf)},
		{"/bin/chmod", "0755", p.Root},
		{"/bin/chmod", "0700", p.State()},
		{"/bin/rm", "-rf", ver},
		{"/usr/bin/ditto", unpacked, ver},
		{"/usr/sbin/chown", "-R", "root:wheel", ver},
		{"/bin/chmod", "-R", "go-w", ver},
		{"/bin/ln", "-sfhn", filepath.Join("versions", m.Version), p.Current()},
	}
	for _, name := range order {
		fl, ok := files[name]
		if !ok {
			continue
		}
		steps = append(steps, []string{"/usr/bin/install", "-m", fl.mode, "-o", "root", "-g", "wheel", filepath.Join(stage, name), fl.dest})
	}
	for _, s := range steps {
		if err := macSudo(s...); err != nil {
			return exitf(ExitGeneric, "%s", macInstallFailed("sudo", fmt.Errorf("%s: %v", filepath.Base(s[0]), err), argumentPath, hostName))
		}
	}
	for _, job := range []struct{ label, plist string }{{machost.HostdLabel, machost.HostdPlist()}, {machost.UpdateLabel, machost.UpdatePlist()}} {
		if err := macSudo("/bin/launchctl", "bootstrap", "system", job.plist); err != nil {
			// Loaded already: a retry. kickstart -k runs the new files.
			if err := macSudo("/bin/launchctl", "kickstart", "-k", "system/"+job.label); err != nil && job.label == machost.HostdLabel {
				return exitf(ExitGeneric, "launchd took no job for hostd: %v. `repose host rm --local` removes what was installed.", err)
			}
		}
	}
	applianceFileVaultLine(e)

	who := hostName
	if who == "" {
		who = "This Mac"
	}
	_, _ = fmt.Fprintf(e.ErrOut, "waiting for %s to join your account\n", who)
	id, err := macWaitJoined(ctx, p.ControlSock())
	if err != nil {
		return exitf(ExitGeneric, "%s has not joined your account after %s: %v. hostd keeps trying; its log is %s.", who, macJoinWait, err, machost.HostdLog())
	}
	_ = id
	_ = os.RemoveAll(unpacked)
	_ = os.Remove(bundle)
	if argumentPath != "" && hostName != "" {
		// The argument is spent: the registration used its token.
		_ = os.Remove(argumentPath)
	}
	_, _ = fmt.Fprintf(e.ErrOut, "%s joined your account in %ds.\n", who, int(time.Since(t0).Seconds()+0.5))
	return nil
}

// macHostManifest is the pinned release's manifest, checked against the
// release key, or the lab's from envMacHostManifestURL with its key.
func macHostManifest(ctx context.Context, e *Env) (machost.Manifest, error) {
	url, pub, pinned := machost.ManifestURL(macHostVersion), machost.ReleasePubKeyPEM, true
	if u := os.Getenv(envMacHostManifestURL); u != "" {
		url, pinned = u, false
	}
	if k := os.Getenv(envMacHostPubKey); k != "" {
		b, err := os.ReadFile(k)
		if err != nil {
			return machost.Manifest{}, err
		}
		pub = string(b)
	}
	body, err := fetchText(ctx, e.applianceHTTP(), url)
	if err != nil {
		return machost.Manifest{}, err
	}
	m, err := machost.ParseManifest([]byte(body), pub)
	if err != nil {
		return machost.Manifest{}, err
	}
	if pinned && m.Version != macHostVersion {
		return machost.Manifest{}, fmt.Errorf("the release names %s, not %s", m.Version, macHostVersion)
	}
	return m, nil
}

// macWaitJoined waits for hostd's control socket to say it is registered
// and connected to the api, and returns the host's id.
var macWaitJoined = func(ctx context.Context, sock string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, macJoinWait)
	defer cancel()
	c := control.NewClient(macFS(sock))
	last := errors.New("hostd has not started")
	for {
		s, err := c.Status(ctx)
		switch {
		case err == nil && s.HostID != "" && s.Connected:
			return s.HostID, nil
		case err == nil:
			last = errors.New("hostd runs and has not reached repose yet")
		case ctx.Err() == nil:
			last = errors.New("hostd does not answer yet")
		}
		select {
		case <-ctx.Done():
			return "", last
		case <-time.After(time.Second):
		}
	}
}

// macInstallFailed is what a customer reads when the install stopped
// before hostd ran. The host is on the account already, so a plain
// second `repose host add` is refused by the one-host limit; the install
// alone, with the same argument, is the way on (ENG-117's rule).
func macInstallFailed(what string, err error, argumentPath, hostName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s did not install: %v.\n", what, err)
	if hostName != "" {
		fmt.Fprintf(&b, "%s is in your account already. ", hostName)
	}
	if argumentPath == "" {
		b.WriteString("Run the same `repose host add --join-file` again when the cause is fixed.")
		return b.String()
	}
	fmt.Fprintf(&b, "Run this on the Mac when the cause is fixed; it continues from what is already here:\n"+
		"  repose host add --join-file %s\n"+
		"That file holds the argument. It carries a key, so it is not passed on the command line.", argumentPath)
	return b.String()
}

// macHostRemove takes repose off this Mac: the two daemons, every
// machine's VM (killed: its disk goes with it), the login shares'
// images, and everything under the root and the logs, so the memory and
// the disk are the Mac's again. A sudo refusal leaves it as it was and
// says so.
func macHostRemove(e *Env) error {
	p := macHostPaths
	if term.IsTerminal(int(os.Stdin.Fd())) {
		_, _ = fmt.Fprintln(e.ErrOut, "sudo asks for your password to remove repose's service")
	}
	if err := macSudo("/usr/bin/true"); err != nil {
		return fmt.Errorf("removing repose takes sudo, which refused: %v", err)
	}
	// Not loaded is done already; every step below is best effort but the
	// last, which says whether the files went.
	_ = macSudo("/bin/launchctl", "bootout", "system/"+machost.UpdateLabel)
	_ = macSudo("/bin/launchctl", "bootout", "system/"+machost.HostdLabel)
	uid := os.Getuid()
	if r := macRecord(); r != nil && r.UID > 0 {
		uid = r.UID
	}
	// Each machine's job, in whichever domain it runs (an agent in the
	// user's session, or a system job started with nobody logged in).
	ents, _ := os.ReadDir(macFS(p.Launchd()))
	for _, ent := range ents {
		label, ok := strings.CutSuffix(ent.Name(), ".plist")
		if !ok || !strings.HasPrefix(label, machost.GuestPrefix) {
			continue
		}
		for _, dom := range []string{"gui/" + strconv.Itoa(uid), "system"} {
			_ = macSudo("/bin/launchctl", "kill", "SIGKILL", dom+"/"+label)
			_ = macSudo("/bin/launchctl", "bootout", dom+"/"+label)
		}
	}
	// The login shares are disk images attached under the root.
	for _, mp := range macMountsUnder(p.Root) {
		_ = macSudo("/usr/bin/hdiutil", "detach", "-force", mp)
	}
	for _, f := range []string{machost.HostdPlist(), machost.UpdatePlist(), machost.NewsyslogConf} {
		_ = macSudo("/bin/rm", "-f", f)
	}
	if err := macSudo("/bin/rm", "-rf", p.Root, machost.LogDir); err != nil {
		return fmt.Errorf("%s was not removed: %v", p.Root, err)
	}
	return nil
}

// macMountsUnder lists mount points under dir, deepest first.
func macMountsUnder(dir string) []string {
	out, err := macExec("/sbin/mount")
	if err != nil {
		return nil
	}
	var mps []string
	for _, line := range strings.Split(string(out), "\n") {
		_, rest, ok := strings.Cut(line, " on ")
		if !ok {
			continue
		}
		i := strings.LastIndex(rest, " (")
		if i < 0 {
			continue
		}
		if mp := rest[:i]; strings.HasPrefix(mp, dir+"/") {
			mps = append(mps, mp)
		}
	}
	// Deepest first, so a mount inside another is detached before it.
	for i := 0; i < len(mps); i++ {
		for j := i + 1; j < len(mps); j++ {
			if len(mps[j]) > len(mps[i]) {
				mps[i], mps[j] = mps[j], mps[i]
			}
		}
	}
	return mps
}
