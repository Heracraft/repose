package storeview

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// Bind makes each store path appear at /<name> in the mount namespace of
// pid, whose root is the view. It runs on a thread of its own that joins
// that namespace and is never handed back: the goroutine exits locked, so
// the runtime ends the thread with it. Errors name a path by its index,
// never by name, since closure paths carry what a tenant installed.
func Bind(pid int, paths []string) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		// No UnlockOSThread: after setns this thread is in another mount
		// namespace and must not run any other goroutine.
		errc <- bind(pid, paths)
	}()
	return <-errc
}

type entry struct {
	name string
	link string // a store path that is a symlink is recreated, not bound
	fd   int    // a detached clone of the path, for move_mount
	dir  bool
}

func bind(pid int, paths []string) error {
	// setns(CLONE_NEWNS) refuses a thread that shares its filesystem
	// attributes with others, which every Go thread does.
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return fmt.Errorf("unshare: %w", err)
	}
	entries := make([]entry, 0, len(paths))
	defer func() {
		for _, e := range entries {
			if e.fd > 0 {
				_ = unix.Close(e.fd)
			}
		}
	}()
	// Everything is opened in hostd's namespace first; after setns the
	// host store is out of sight.
	for i, p := range paths {
		name, ok := storeName(p)
		if !ok {
			return fmt.Errorf("store path %d of %d is not a top-level store path", i+1, len(paths))
		}
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return fmt.Errorf("store path %d of %d: %w", i+1, len(paths), err)
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFLNK:
			buf := make([]byte, unix.PathMax)
			n, err := unix.Readlink(p, buf)
			if err != nil {
				return fmt.Errorf("store path %d of %d: %w", i+1, len(paths), err)
			}
			entries = append(entries, entry{name: name, link: string(buf[:n])})
		case unix.S_IFDIR, unix.S_IFREG:
			fd, err := unix.OpenTree(unix.AT_FDCWD, p, unix.OPEN_TREE_CLONE|unix.OPEN_TREE_CLOEXEC|unix.AT_SYMLINK_NOFOLLOW)
			if err != nil {
				return fmt.Errorf("store path %d of %d: open_tree: %w", i+1, len(paths), err)
			}
			entries = append(entries, entry{name: name, fd: fd, dir: st.Mode&unix.S_IFMT == unix.S_IFDIR})
			ro := &unix.MountAttr{Attr_set: unix.MOUNT_ATTR_RDONLY | unix.MOUNT_ATTR_NOSUID | unix.MOUNT_ATTR_NODEV}
			if err := unix.MountSetattr(fd, "", unix.AT_EMPTY_PATH, ro); err != nil {
				return fmt.Errorf("store path %d of %d: mount_setattr: %w", i+1, len(paths), err)
			}
		default:
			return fmt.Errorf("store path %d of %d is not a file, directory or link", i+1, len(paths))
		}
	}
	ns, err := unix.Open(fmt.Sprintf("/proc/%d/ns/mnt", pid), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open mount namespace: %w", err)
	}
	err = unix.Setns(ns, unix.CLONE_NEWNS)
	_ = unix.Close(ns)
	if err != nil {
		return fmt.Errorf("setns: %w", err)
	}
	// setns made the namespace's root (the view) this thread's / and cwd.
	var root unix.Stat_t
	if err := unix.Stat("/", &root); err != nil {
		return err
	}
	for i, e := range entries {
		target := "/" + e.name
		var st unix.Stat_t
		switch err := unix.Lstat(target, &st); {
		case err == nil && (e.link != "" || st.Dev != root.Dev):
			continue // recreated or bound already
		case err == nil:
			// A mount point left by an earlier attempt; bind onto it.
		case err == unix.ENOENT:
			if err := mkpoint(target, e); err != nil {
				return fmt.Errorf("store path %d of %d: %w", i+1, len(paths), err)
			}
			if e.link != "" {
				continue
			}
		default:
			return fmt.Errorf("store path %d of %d: %w", i+1, len(paths), err)
		}
		if err := unix.MoveMount(e.fd, "", unix.AT_FDCWD, target, unix.MOVE_MOUNT_F_EMPTY_PATH); err != nil {
			return fmt.Errorf("store path %d of %d: move_mount: %w", i+1, len(paths), err)
		}
		// A clone keeps the source's peer group: shared with the host's
		// /nix/store. Private, so nothing mounted on either side reaches
		// the other.
		if err := unix.MountSetattr(unix.AT_FDCWD, target, 0, &unix.MountAttr{Propagation: unix.MS_PRIVATE}); err != nil {
			return fmt.Errorf("store path %d of %d: private: %w", i+1, len(paths), err)
		}
	}
	return nil
}

func mkpoint(target string, e entry) error {
	switch {
	case e.link != "":
		return unix.Symlink(e.link, target)
	case e.dir:
		return unix.Mkdir(target, 0o555)
	}
	fd, err := unix.Open(target, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_CLOEXEC, 0o444)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}
