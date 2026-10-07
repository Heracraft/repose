package system

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/heracraft/repose/internal/guestd/sysdep"
)

// The guest's /nix/store is an overlay. Its lower layer is the host's view
// of this guest's closures over virtio-fs (/nix/.ro-store, DECISIONS I-463),
// its upper layer the guest's own disk (/nix/.rw-store). A nix command in
// the guest that deletes a path the lower layer serves cannot delete the
// host's copy; overlayfs writes a whiteout (a 0:0 character device) into the
// upper dir instead, and the whiteout hides the host's copy from then on,
// whatever the guest's nix database says (kanali, 2026-10-06: 3,621 of them,
// one over the system the guest booted next). So guestd roots every path
// the lower layer serves (I-588), a start removes whiteouts before stage 2
// (I-587, nix/guest/base/boot.nix), and a switch to a closure a whiteout
// hides says so (I-589).

// storeView is where the overlay's layers are, under Paths.Root.
type storeView struct {
	upper  string
	lowers []string
}

// storeDir is the store's logical directory: what a GC root names.
const storeDir = "/nix/store"

// validityChunk bounds the paths one `nix-store --check-validity` gets,
// which keeps its argv far below ARG_MAX (about 80 bytes each).
const validityChunk = 2000

// readView finds the /nix/store overlay in /proc/mounts. ok is false when
// /nix/store is not an overlay (nothing to protect or repair). Mounted in
// stage 1, its options name the initrd's view of the real root
// (/mnt-root with the scripted initrd, /sysroot with systemd's), which is
// removed, as repose-pin-profile does.
func (h *Handler) readView() (storeView, bool) {
	b, err := os.ReadFile(h.paths.Mounts())
	if err != nil {
		return storeView{}, false
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || unescapeMount(f[1]) != storeDir || f[2] != "overlay" {
			continue
		}
		var v storeView
		for _, opt := range strings.Split(f[3], ",") {
			k, val, _ := strings.Cut(opt, "=")
			switch k {
			case "upperdir":
				v.upper = h.under(unescapeMount(val))
			case "lowerdir":
				for _, l := range strings.Split(val, ":") {
					if l != "" {
						v.lowers = append(v.lowers, h.under(unescapeMount(l)))
					}
				}
			}
		}
		if v.upper == "" || len(v.lowers) == 0 {
			return storeView{}, false
		}
		return v, true
	}
	return storeView{}, false
}

// under maps an overlay directory as /proc/mounts gives it to a path under
// Paths.Root, dropping the initrd's prefix.
func (h *Handler) under(dir string) string {
	for _, prefix := range []string{"/mnt-root/", "/sysroot/"} {
		if strings.HasPrefix(dir, prefix) {
			dir = "/" + strings.TrimPrefix(dir, prefix)
			break
		}
	}
	if h.paths.Root == "" {
		return dir
	}
	return filepath.Join(h.paths.Root, dir)
}

// unescapeMount undoes /proc/mounts' octal escapes (\040 for a space).
func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				out.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		out.WriteByte(s[i])
	}
	return out.String()
}

// isStoreName reports whether name looks like a store path's basename: a
// 32-character hash, a dash and a name.
func isStoreName(name string) bool {
	return len(name) > 33 && name[32] == '-' && !strings.ContainsAny(name, "/\x00")
}

// lowerNames lists the store paths the lower layers serve.
func (v storeView) lowerNames() (map[string]bool, error) {
	names := map[string]bool{}
	for _, l := range v.lowers {
		es, err := os.ReadDir(l)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", filepath.Base(l), err)
		}
		for _, e := range es {
			if isStoreName(e.Name()) {
				names[e.Name()] = true
			}
		}
	}
	return names, nil
}

// whiteout reports whether the upper dir holds an overlayfs whiteout for
// name: a character device with device number 0:0.
func (v storeView) whiteout(name string) bool {
	fi, err := os.Lstat(filepath.Join(v.upper, name))
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Rdev == 0
}

// registeredPaths lists the store paths a `nix-store --dump-db` listing
// registers: every store path line but the derivers. The listing is closed
// under references, so the reference lines name nothing new.
func registeredPaths(reg []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(string(reg), "\n") {
		name, ok := strings.CutPrefix(line, storeDir+"/")
		if !ok || !isStoreName(name) || strings.HasSuffix(name, ".drv") || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, storeDir+"/"+name)
	}
	return out
}

// hidden returns the paths among these that a whiteout in the upper dir
// hides, sorted.
func (v storeView) hidden(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		if v.whiteout(filepath.Base(p)) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// invalidPaths asks the guest's nix which of these paths its database does
// not list as valid.
func (h *Handler) invalidPaths(ctx context.Context, paths []string) (map[string]bool, error) {
	invalid := map[string]bool{}
	for start := 0; start < len(paths); start += validityChunk {
		chunk := paths[start:min(start+validityChunk, len(paths))]
		res, err := h.run.Run(ctx, sysdep.RunSpec{
			Argv:      append([]string{"nix-store", "--check-validity", "--print-invalid"}, chunk...),
			MaxOutput: len(chunk)*128 + 4096,
			Env:       sysdep.DevEnv(h.paths, "root"),
		})
		if err != nil {
			return nil, fmt.Errorf("nix-store --check-validity: %w", err)
		}
		if res.ExitCode != 0 {
			return nil, fmt.Errorf("nix-store --check-validity exited %d", res.ExitCode)
		}
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				invalid[line] = true
			}
		}
	}
	return invalid, nil
}

// syncRoots makes ViewRoots hold one root per name, each a symlink to
// /nix/store/<name>, and nothing else. New roots go in before stale ones
// go out, and each is renamed into place, so a garbage collection running
// meanwhile never finds a served path unrooted that was rooted before.
func (h *Handler) syncRoots(want map[string]bool) (added, removed int, err error) {
	dir := h.paths.ViewRoots()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, 0, err
	}
	es, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	have := map[string]bool{}
	var stale []string
	for _, e := range es {
		name := e.Name()
		if want[name] {
			if t, err := os.Readlink(filepath.Join(dir, name)); err == nil && t == storeDir+"/"+name {
				have[name] = true
				continue
			}
			continue // replaced below
		}
		stale = append(stale, name)
	}
	names := make([]string, 0, len(want))
	for name := range want {
		if !have[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		tmp := filepath.Join(dir, ".tmp-"+name)
		_ = os.Remove(tmp)
		if err := os.Symlink(storeDir+"/"+name, tmp); err != nil {
			return added, removed, err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			_ = os.Remove(tmp)
			return added, removed, err
		}
		added++
	}
	for _, name := range stale {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			return added, removed, err
		}
		removed++
	}
	return added, removed, nil
}

// SyncViewRoots roots every path the lower layer serves that the guest's
// database lists as valid, and drops the roots of paths it no longer
// serves (I-588). guestd runs it at start and RegisterPaths after every
// registration, so the roots follow the view across boots, in-place
// switches and a guestd upgrade. A failure is logged and changes nothing
// else: the start's whiteout repair (I-587) still undoes what a garbage
// collection did without them.
func (h *Handler) SyncViewRoots(ctx context.Context) {
	h.viewMu.Lock()
	defer h.viewMu.Unlock()
	v, ok := h.readView()
	if !ok {
		return
	}
	h.syncView(ctx, v, nil, nil)
}

// syncView is SyncViewRoots with the view already read. invalid, when not
// nil, is what the caller learnt of these paths' validity; known lists the
// paths it covers.
func (h *Handler) syncView(ctx context.Context, v storeView, invalid map[string]bool, known map[string]bool) {
	names, err := v.lowerNames()
	if err != nil {
		h.log.Warn("store view roots not updated: the shared store could not be listed",
			"event", "view_roots", "result", "error")
		return
	}
	var ask []string
	for name := range names {
		if !known[storeDir+"/"+name] {
			ask = append(ask, storeDir+"/"+name)
		}
	}
	sort.Strings(ask)
	more, err := h.invalidPaths(ctx, ask)
	if err != nil {
		// Unknown validity: root them all. nix skips a root to a path its
		// database does not list, so this protects at least as much.
		h.log.Warn("store path validity unknown; rooting every shared path",
			"event", "view_roots", "result", "validity_unknown")
		more = map[string]bool{}
	}
	want := map[string]bool{}
	notValid := 0
	for name := range names {
		p := storeDir + "/" + name
		if invalid[p] || more[p] {
			notValid++
			continue
		}
		want[name] = true
	}
	added, removed, err := h.syncRoots(want)
	if err != nil {
		h.log.Warn("store view roots not updated", "event", "view_roots", "result", "error",
			"added", added, "removed", removed)
		return
	}
	h.log.Info("store view rooted", "event", "view_roots", "result", "ok",
		"rooted", len(want), "added", added, "removed", removed, "not_valid", notValid)
}

// checkHidden refuses a switch to a closure that a whiteout in this
// guest's upper dir hides, wholly or in part (I-589). Activating it would
// fail at its first missing file, or boot into "stage 2 init script not
// found" after a reboot. The whiteouts can only be removed while the
// overlay is not mounted, which a start does (I-587).
func (h *Handler) checkHidden(closure string, registration []byte) error {
	v, ok := h.readView()
	if !ok {
		return nil
	}
	paths := append([]string{closure}, registeredPaths(registration)...)
	hidden := v.hidden(paths)
	if len(hidden) == 0 {
		return nil
	}
	h.log.Warn("switch refused: store paths hidden by whiteouts", "event", "switch",
		"result", "store_paths_hidden", "hidden", len(hidden))
	return sysdep.NotFound("switch: %d of the store paths %s needs are hidden in this machine's store, %s first: "+
		"a nix garbage collection inside the machine deleted them. Restart the machine; a start repairs its store",
		len(hidden), closure, hidden[0])
}
