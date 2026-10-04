// Package virtiofs runs one virtiofsd per guest as a transient unit,
// exporting that guest's store view read-only (DECISIONS I-463): an empty
// tmpfs in the unit's own mount namespace that hostd fills with the
// guest's closure (package storeview). virtiofsd has no read-only
// flag; read-only holds because the virtiofsd user cannot write under the
// export (a read-only bind mount) and the guest mounts the tag ro.
//
// The sandbox is `namespace`, not `chroot`: chroot(2) needs CAP_SYS_CHROOT,
// and virtiofsd 1.14 refuses it outright for a non-root user ("sandbox mode
// 'chroot' can only be used by root"). Namespace mode unshares a user and
// mount namespace and pivot_roots into the export, which is what an
// unprivileged process can do (DECISIONS I-48; the host enables user
// namespaces in nix/hosts/kernel.nix for exactly this).
package virtiofs

import (
	"context"
	"fmt"

	"github.com/heracraft/repose/internal/hostd/storeview"
	"github.com/heracraft/repose/internal/hostd/systemd"
)

// Config is where the export lives and who serves it.
type Config struct {
	// SharedDir is storeview.Dir for a per-guest view (I-463), or a
	// directory holding the whole store (the pre-I-463 export).
	SharedDir string
	User      string // virtiofsd
	Group     string // virtiofsd
	Binary    string // virtiofsd
	// SocketGroup is chgrp'd onto the vhost-user socket (mode 0660) so the
	// hypervisor's user, not virtiofsd's, can connect to it.
	SocketGroup string // hostd
}

// Unit is the transient unit name for a guest.
func Unit(guestID string) string { return "virtiofsd@" + guestID }

// Start launches virtiofsd for a guest; running already is not an error.
func Start(ctx context.Context, sd systemd.Systemd, cfg Config, guestID, socket string) error {
	bin := cfg.Binary
	if bin == "" {
		bin = "virtiofsd"
	}
	props := []string{"User=" + cfg.User, "Group=" + cfg.Group, "MemoryMax=1G", "Slice=guests.slice"}
	if cfg.SharedDir == storeview.Dir {
		props = append(props, storeview.UnitProps()...)
	}
	// --no-announce-submounts: the export masks .links with a tmpfs mount
	// (host-conventions.md). Announced, the guest sees it as a separate
	// virtiofs mount inside the overlay's lower layer, and overlayfs answers
	// every lookup crossing into it with EREMOTE ("Object is remote"), which
	// killed the guest's nix-daemon at its first mkdir of /nix/store/.links
	// (DECISIONS I-65). Flattened, it is an empty directory like any other.
	// A store view (I-463) is one bind mount per store path, so the same
	// flag is what keeps every path in it an ordinary directory.
	argv := []string{bin, "--socket-path", socket, "--shared-dir", cfg.SharedDir, "--sandbox", "namespace", "--cache", "auto", "--xattr", "--no-announce-submounts"}
	if cfg.SocketGroup != "" {
		argv = append(argv, "--socket-group", cfg.SocketGroup)
	}
	return sd.Run(ctx, Unit(guestID), props, argv)
}

// Stop ends a guest's virtiofsd.
func Stop(ctx context.Context, sd systemd.Systemd, guestID string) error {
	return sd.Stop(ctx, Unit(guestID))
}

// AuthConfig is the share of one user's Claude Code login into a guest
// (DECISIONS I-278): a directory holding only .credentials.json, served
// read-write by an unprivileged virtiofsd running as its own account, so
// the store's virtiofsd user can write no credential and this one can read
// no store. Guest uid and gid 1000 (dev) map to that account both ways;
// every other guest id is created as it too, since an unprivileged
// virtiofsd can create files as nobody else.
type AuthConfig struct {
	SharedDir   string // /var/lib/repose/users/<user_id>/claude-auth
	User        string // repose-auth
	Group       string // repose-auth
	UID, GID    int    // the account's numeric ids, for the translation
	Binary      string
	SocketGroup string // hostd, as for the store share
}

// AuthUnit is the transient unit serving a guest's login share.
func AuthUnit(guestID string) string { return "virtiofsd-auth@" + guestID }

// StartAuth launches the login share for a guest; running already is not an
// error. --cache never: another guest's refresh rewrites the file in place,
// and a guest must never answer a read from a page it cached before that
// (experiment B in docs/proposals/2026-09-24-claude-login-shared-folder.md
// ran this way).
func StartAuth(ctx context.Context, sd systemd.Systemd, cfg AuthConfig, guestID, socket string) error {
	bin := cfg.Binary
	if bin == "" {
		bin = "virtiofsd"
	}
	props := []string{"User=" + cfg.User, "Group=" + cfg.Group, "MemoryMax=64M", "Slice=guests.slice"}
	argv := []string{bin, "--socket-path", socket, "--shared-dir", cfg.SharedDir, "--sandbox", "namespace", "--cache", "never",
		"--translate-uid", fmt.Sprintf("map:1000:%d:1", cfg.UID), "--translate-gid", fmt.Sprintf("map:1000:%d:1", cfg.GID)}
	if cfg.SocketGroup != "" {
		argv = append(argv, "--socket-group", cfg.SocketGroup)
	}
	return sd.Run(ctx, AuthUnit(guestID), props, argv)
}

// StopAuth ends a guest's login share.
func StopAuth(ctx context.Context, sd systemd.Systemd, guestID string) error {
	return sd.Stop(ctx, AuthUnit(guestID))
}
