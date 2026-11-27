// Package machost is how repose is laid out on a Mac that runs design 2
// (DECISIONS I-1662, I-1664, I-1684): hostd as a root LaunchDaemon, each
// machine its own Virtualization.framework VM run by repose-vm, and the
// tools hostd calls (e2fsprogs, zstd) beside it, all from one signed
// bundle. `repose host add` writes this layout, `repose host rm` removes
// it, and repose-hostd-update keeps it current on launchd (I-1496's
// updater, darwin's manifest). It builds on every platform, so the
// layout, the plists and the manifest are tested where the CI runs.
package machost

import (
	"fmt"
	"html"
	"path/filepath"
	"strings"
)

// Root is where everything of repose's lives on a Mac. /Library, not the
// user's home: hostd runs as root, and a root daemon that executes or
// writes files in a directory its user owns is a way to root for
// anything that runs as that user. /Library/Application Support is
// root's and admin's to write, by Apple's convention for system-wide
// application data. The longest unix socket under it stays under
// macOS's 104-byte sun_path (TestSocketPathsFit).
const Root = "/Library/Application Support/repose"

// LogDir holds hostd's and the updater's logs, where Console.app looks.
const LogDir = "/Library/Logs/repose"

// LaunchDaemonsDir is launchd's system domain's directory.
const LaunchDaemonsDir = "/Library/LaunchDaemons"

// NewsyslogConf rotates the two logs (launchd never does).
const NewsyslogConf = "/etc/newsyslog.d/run.repose.conf"

// The launchd labels. The VMs' jobs are hostd's (run.repose.guest@<id>,
// internal/hostd/launchd).
const (
	HostdLabel  = "run.repose.hostd"
	UpdateLabel = "run.repose.hostd-update"
	GuestPrefix = "run.repose.guest@"
)

// Bundle is the bundle's file name in a hostd-<version> release.
const Bundle = "repose-host-darwin-arm64.tar.gz"

// BundleFiles are the executables a bundle carries under bin/, every one
// required: hostd and the updater (Go), the VM launcher (Swift, signed
// with the virtualization entitlement), and the tools hostd runs for
// volumes and snapshots, which macOS does not ship (e2fsprogs, zstd).
var BundleFiles = []string{"hostd", "repose-vm", "repose-hostd-update", "mke2fs", "e2fsck", "dumpe2fs", "debugfs", "zstd"}

// Paths is the layout under a root; tests move the root.
type Paths struct {
	Root string
}

// Default is the layout on a Mac.
var Default = Paths{Root: Root}

func (p Paths) j(s ...string) string { return filepath.Join(append([]string{p.Root}, s...)...) }

// Versions holds one directory per installed bundle version, current and
// the one before it.
func (p Paths) Versions() string { return p.j("versions") }

// Version is one installed bundle.
func (p Paths) Version(v string) string { return p.j("versions", v) }

// Current is the symlink to the running version; every plist names
// binaries through it, so an update is one rename.
func (p Paths) Current() string { return p.j("current") }

// Bin is a binary of the current version.
func (p Paths) Bin(name string) string { return filepath.Join(p.Current(), "bin", name) }

// State is hostd's --state: host.json, state.db, the join token before
// the registration, hub.json.
func (p Paths) State() string { return p.j("hostd") }

func (p Paths) Guests() string      { return p.j("guests") }
func (p Paths) Volumes() string     { return p.j("volumes") }
func (p Paths) Launchd() string     { return p.j("launchd") }
func (p Paths) Net() string         { return p.j("net") }
func (p Paths) Images() string      { return p.j("images") }
func (p Paths) Snapshots() string   { return p.j("snapshots") }
func (p Paths) Builds() string      { return p.j("builds") }
func (p Paths) Base() string        { return p.j("base") }
func (p Paths) ControlSock() string { return p.j("hostd.sock") }
func (p Paths) APICA() string       { return p.j("api-ca.pem") }
func (p Paths) JoinToken() string   { return filepath.Join(p.State(), "join-token") }
func (p Paths) HubJSON() string     { return filepath.Join(p.State(), "hub.json") }

// UpdateState keeps the updater's verified manifests.
func (p Paths) UpdateState() string { return p.j("update") }

// Record is install.json: which host this Mac is and who added it,
// readable by everyone (it holds no secret).
func (p Paths) Record() string { return p.j("install.json") }

// HostdPlist and UpdatePlist are the two LaunchDaemons.
func HostdPlist() string  { return filepath.Join(LaunchDaemonsDir, HostdLabel+".plist") }
func UpdatePlist() string { return filepath.Join(LaunchDaemonsDir, UpdateLabel+".plist") }

func HostdLog() string  { return filepath.Join(LogDir, "hostd.log") }
func UpdateLog() string { return filepath.Join(LogDir, "hostd-update.log") }

// API is where hostd reaches the api: the control plane's hub address
// through the edge tunnel (I-1380, I-1670), with the platform host CA.
type API struct {
	Addr, ServerName string
}

// DefaultAPI is production's, the values ops/install-host-x86.sh uses.
var DefaultAPI = API{Addr: "10.255.255.1:8443", ServerName: "api.repose.herakraft.co"}

// HostdArgs is hostd's command line on a Mac: every path under the
// layout (the Linux defaults under /var/lib and /run do not exist
// there), the VMs run as user, the owned-host rules, the park and the
// rest (VZ's save file, I-1666), and no Nix.
func HostdArgs(p Paths, user string, api API) []string {
	return []string{
		p.Bin("hostd"),
		"--state", p.State(),
		"--guests-dir", p.Guests(),
		"--builds-dir", p.Builds(),
		"--base-dir", p.Base(),
		"--images-dir", p.Images(),
		"--snapshot-dir", p.Snapshots(),
		"--control-sock", p.ControlSock(),
		"--join-token", p.JoinToken(),
		"--nix-mode", "off",
		"--build-user", "",
		"--api-addr", api.Addr,
		"--api-server-name", api.ServerName,
		"--api-ca", p.APICA(),
		"--owned-host",
		"--park", "--park-deadline", "120s",
		"--pause-idle",
		"--cpu-reset", "off",
		"--ready-timeout", "5m", "--paths-timeout", "10m", "--setup-timeout", "10m",
		"--mac-user", user,
		"--mac-launcher", p.Bin("repose-vm"),
		"--mac-volumes-dir", p.Volumes(),
		"--mac-launchd-dir", p.Launchd(),
		"--mac-net-dir", p.Net(),
		"--mac-mke2fs", p.Bin("mke2fs"),
		"--mac-e2fsck", p.Bin("e2fsck"),
	}
}

// binPath is PATH for both daemons: the bundle's tools first (hostd runs
// zstd, dumpe2fs and debugfs by name), then macOS's own.
func binPath(p Paths) string {
	return filepath.Join(p.Current(), "bin") + ":/usr/bin:/bin:/usr/sbin:/sbin"
}

// HostdPlistText is hostd's LaunchDaemon: root, at boot with nobody
// logged in (proposal 2.9), kept alive. A hostd stop or restart touches
// no machine: each VM is its own launchd job (I-1664). Interactive,
// because every frame a machine sends crosses hostd's switch (I-1668).
func HostdPlistText(p Paths, user string, api API) string {
	return plist(HostdLabel, HostdArgs(p, user, api), binPath(p), HostdLog(), `  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ExitTimeOut</key><integer>30</integer>
  <key>ProcessType</key><string>Interactive</string>
`)
}

// UpdatePlistText is the updater, every 30 minutes as on Linux (I-1496).
// args are its flags beyond the defaults (a lab's manifest and key).
func UpdatePlistText(p Paths, args []string) string {
	return plist(UpdateLabel, append([]string{p.Bin("repose-hostd-update")}, args...), binPath(p), UpdateLog(), `  <key>StartInterval</key><integer>1800</integer>
  <key>ProcessType</key><string>Background</string>
`)
}

func plist(label string, argv []string, path, log, extra string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>
`)
	for _, a := range argv {
		fmt.Fprintf(&b, "    <string>%s</string>\n", html.EscapeString(a))
	}
	b.WriteString(`  </array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>` + html.EscapeString(path) + `</string></dict>
` + extra + `  <key>StandardOutPath</key><string>` + log + `</string>
  <key>StandardErrorPath</key><string>` + log + `</string>
</dict>
</plist>
`)
	return b.String()
}

// NewsyslogText rotates each log at 10 MB, five kept, compressed.
func NewsyslogText() string {
	return "# repose's logs on this Mac (repose host add; DECISIONS I-1684)\n" +
		"# logfilename\t[owner:group]\tmode\tcount\tsize(KB)\twhen\tflags\n" +
		HostdLog() + "\troot:wheel\t644\t5\t10240\t*\tJ\n" +
		UpdateLog() + "\troot:wheel\t644\t5\t10240\t*\tJ\n"
}

// Record is install.json: what `repose host add` installed on this Mac,
// so `repose host rm` knows which host it is and the updater knows whose
// session restores the machines.
type Record struct {
	Host    string `json:"host"`
	User    string `json:"user"`
	UID     int    `json:"uid"`
	Version string `json:"version"`
}

// HostCAPEM is the platform host CA hostd checks the api's certificate
// against over the hub (nix/hosts/platform.nix; public material). The
// same text as ops/install-host-x86.sh's API_CA (TestHostCAMatchesTheX86Installer).
const HostCAPEM = `-----BEGIN CERTIFICATE-----
MIIBYzCCAQmgAwIBAgIBATAKBggqhkjOPQQDAjAZMRcwFQYDVQQDEw5yZXBvc2Ug
aG9zdCBjYTAeFw0yNjA5MjAxNDM1NDRaFw0zNjA5MTcxNTM1NDRaMBkxFzAVBgNV
BAMTDnJlcG9zZSBob3N0IGNhMFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEEf0Z
a+ShTl8C17TyIihGfxGA8/+LPSztZO7U+1jkulN/0PyP+u0mFtoz1No7eNIsLQdI
4XqvAoE1SQ5NJMb9+6NCMEAwDgYDVR0PAQH/BAQDAgKEMA8GA1UdEwEB/wQFMAMB
Af8wHQYDVR0OBBYEFJ3mh/uToG0kXZreInW+yRCV7Vv0MAoGCCqGSM49BAMCA0gA
MEUCIQD4CrivKkoYjKlkHwnK7Ors2lohLv4cFQ+yUU5IhKmIUgIgRiWHt/xsWPzy
pgFYxi/RgOzA9GSronavaaOscYi+Vng=
-----END CERTIFICATE-----`
