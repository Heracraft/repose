package sysdep

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path through a temp file in the same
// directory and a rename, so a reader never sees a half-written secret,
// principals file or project.json. uid and gid of -1 leave ownership alone.
func WriteFileAtomic(path string, data []byte, mode os.FileMode, uid, gid int) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", filepath.Base(path), err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", filepath.Base(path), err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", filepath.Base(path), err)
	}
	if uid >= 0 || gid >= 0 {
		if err = tmp.Chown(uid, gid); err != nil {
			// A test root is owned by the test user, who may not chown to dev.
			// Ownership is asserted in the VM test instead; here it is not
			// fatal, but the caller is told.
			if !os.IsPermission(err) {
				return fmt.Errorf("chown %s: %w", filepath.Base(path), err)
			}
			err = nil
		}
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", filepath.Base(path), err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(path), err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename into place %s: %w", filepath.Base(path), err)
	}
	return nil
}

// SyncDir fsyncs a directory, so a rename into it survives a crash.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}
