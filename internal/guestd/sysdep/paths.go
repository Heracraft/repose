// Package sysdep holds what guestd's request handlers need from the world
// outside their own logic: where files live, how a command is run, how the
// root filesystem is frozen, and how Docker is asked whether it is alive.
// Every one of them is an interface with a fake, so the handlers are tested
// without a guest.
package sysdep

import "path/filepath"

// Paths resolves every path in docs/interfaces/guest-conventions.md under an
// optional root. Tests pass a temp directory as the root; a real guest passes
// the empty string.
type Paths struct {
	Root string
}

// join is Root/elem..., and /elem... on a real guest (empty Root): every
// path is absolute. With an empty Root, filepath.Join gave
// "run/repose/switch.log", which systemd-run refuses as StandardOutput
// ("Path ... is not absolute"), so every Switch failed (DECISIONS I-209).
func (p Paths) join(elem ...string) string {
	root := p.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(append([]string{root}, elem...)...)
}

// RunDir is /run/repose: tmpfs, created by the guest base module (02).
func (p Paths) RunDir() string { return p.join("run", "repose") }

// PathsRegistered is /run/repose/paths-registered: written after the first
// successful RegisterPaths; repose-paths.service in the guest waits for it
// before home-manager activates (DECISIONS I-67).
func (p Paths) PathsRegistered() string { return p.join("run", "repose", "paths-registered") }

// PathsLoaded is /var/lib/repose/paths-loaded, on the guest's volume:
// the sha256 of the last registration `nix-store --load-db` took. A start
// that is sent the same registration (the same closure) skips the load,
// which is on every start's path to the first login (DECISIONS I-225).
func (p Paths) PathsLoaded() string { return p.join("var", "lib", "repose", "paths-loaded") }

// NixDB is the guest's nix database, whose absence means nothing was
// ever loaded into it.
func (p Paths) NixDB() string { return p.join("nix", "var", "nix", "db", "db.sqlite") }

// SecretsDir is /run/repose/secrets: named secret values, 0400 dev.
func (p Paths) SecretsDir() string { return p.join("run", "repose", "secrets") }

// SecretsEnv is /run/repose/secrets.env, 0400 dev: the current secrets as
// export lines (DECISIONS I-475).
func (p Paths) SecretsEnv() string { return p.join("run", "repose", "secrets.env") }

// SecretsRefresh is /run/repose/secrets.refresh, 0400 dev: the guarded
// lines the BASH_ENV loader and the profile source to bring a process's
// secrets up to date without replacing a value it set itself (DECISIONS
// I-475).
func (p Paths) SecretsRefresh() string { return p.join("run", "repose", "secrets.refresh") }

// SecretsState is /run/repose/secrets.state, root 0600: the last generations
// of exported secrets, from which guestd writes secrets.refresh (I-475).
func (p Paths) SecretsState() string { return p.join("run", "repose", "secrets.state") }

// HooksSock is /run/repose/hooks.sock, the agent hook ingest.
func (p Paths) HooksSock() string { return p.join("run", "repose", "hooks.sock") }

// QuestionsDir holds the open `repose-ask` questions (DECISIONS I-244).
func (p Paths) QuestionsDir() string { return p.join("run", "repose", "questions") }

// DevSock is /run/repose/guestd.sock, the dev-mode stand-in for vsock.
func (p Paths) DevSock() string { return p.join("run", "repose", "guestd.sock") }

// Principals is /etc/ssh/principals/dev, read by sshd's
// AuthorizedPrincipalsFile.
func (p Paths) Principals() string { return p.join("etc", "ssh", "principals", "dev") }

// PrincipalsDir is the directory holding the principals files.
func (p Paths) PrincipalsDir() string { return p.join("etc", "ssh", "principals") }

// Home is /home/dev.
func (p Paths) Home() string { return p.join("home", "dev") }

// ProjectDir is /home/dev/<slug>, the project checkout.
func (p Paths) ProjectDir(slug string) string { return p.join("home", "dev", slug) }

// CheckoutFile is /home/dev/.repose/checkout: the name, under the home,
// of the checkout's directory, written by the CLI's first sync (DECISIONS
// I-368).
func (p Paths) CheckoutFile() string { return p.join("home", "dev", ".repose", "checkout") }

// ProjectJSON is /home/dev/.repose/project.json.
func (p Paths) ProjectJSON() string { return p.join("home", "dev", ".repose", "project.json") }

// EtcDir is /etc/repose.
func (p Paths) EtcDir() string { return p.join("etc", "repose") }

// EtcEnv is /etc/repose/env, sourced by /etc/profile.d/repose.sh (02).
func (p Paths) EtcEnv() string { return p.join("etc", "repose", "env") }

// BaseVersion is /etc/repose/base-version, written by the guest base (02).
func (p Paths) BaseVersion() string { return p.join("etc", "repose", "base-version") }

// CurrentSystem is /run/current-system, the running NixOS closure.
func (p Paths) CurrentSystem() string { return p.join("run", "current-system") }

// SystemProfile is the profile link a reboot lands on.
func (p Paths) SystemProfile() string {
	return p.join("nix", "var", "nix", "profiles", "system")
}

// NixStore is the read-only virtio-fs share.
func (p Paths) NixStore() string { return p.join("nix", "store") }

// Proc is /proc. Tests point it at a fixture tree.
func (p Paths) Proc() string { return p.join("proc") }

// ProcPID is /proc/<pid>.
func (p Paths) ProcPID(pid string) string { return p.join("proc", pid) }

// Mounts is /proc/mounts, read to find the root block device.
func (p Paths) Mounts() string { return p.join("proc", "mounts") }

// Uptime is /proc/uptime.
func (p Paths) Uptime() string { return p.join("proc", "uptime") }

// BootID is /proc/sys/kernel/random/boot_id.
func (p Paths) BootID() string { return p.join("proc", "sys", "kernel", "random", "boot_id") }

// InotifyMax is the directory holding max_user_instances and max_user_watches.
func (p Paths) InotifyMax(name string) string {
	return p.join("proc", "sys", "fs", "inotify", name)
}

// DockerSock is the Docker daemon's unix socket.
func (p Paths) DockerSock() string { return p.join("var", "run", "docker.sock") }

// RootMount is the filesystem FIFREEZE is applied to. Under a test root it is
// the root directory itself, which the fake freezer accepts and the real one
// never sees.
func (p Paths) RootMount() string {
	if p.Root == "" {
		return "/"
	}
	return p.Root
}

// SwitchLog is where a system activation's output lands (the transient
// unit writes it, guestd reads it back after the wait, I-148).
func (p Paths) SwitchLog() string { return p.join("run", "repose", "switch.log") }
