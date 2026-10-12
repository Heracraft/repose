package freeze

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Record states.
const (
	// StateWriting: the image volume exists and a snapshot may be going
	// into it. Found at start, it is an image a power cut or crash broke:
	// it is discarded and never restored.
	StateWriting = "writing"
	// StateWritten: the snapshot finished and the image is on disk
	// (synced); the hypervisor may still run, paused, and the guest
	// volume's fingerprint is not taken yet. Found at start with the
	// hypervisor gone (hostd killed between the snapshot and the close),
	// the freeze is finished there and the image restored; with the
	// hypervisor still there, the guest is resumed instead.
	StateWritten = "written"
	// StateComplete: the snapshot finished, the hypervisor is gone and the
	// image is closed. Restored at the next start of hostd.
	StateComplete = "complete"
)

// Record is what hostd knows about one guest's frozen image, kept in its
// own state directory (root only), never in the guest directory the
// hypervisor can write. The wrapped key is safe on disk (I-653); nothing
// in it is tenant data.
type Record struct {
	GuestID   string    `json:"guest_id"`
	State     string    `json:"state"`
	Reason    string    `json:"reason"` // shutdown, operator, scheduled_event
	CreatedAt time.Time `json:"created_at"`
	// DoneAt is when the image was complete; the restore's age limit
	// counts from it.
	DoneAt     time.Time `json:"done_at,omitempty"`
	HostBootID string    `json:"host_boot_id"`
	// What the guest ran and on what: a restore needs all of it unchanged.
	Closure     string `json:"closure"`
	Class       string `json:"class"`
	VolumeBytes uint64 `json:"volume_bytes"`
	CHVersion   string `json:"ch_version"`
	// FS is the image volume's filesystem (I-685). Empty means ext4, what
	// a record written before I-685 has.
	FS string `json:"fs,omitempty"`
	// AuthShare: the snapshot has the login share's virtio-fs device, so
	// its virtiofsd must be up before the restore.
	AuthShare bool `json:"auth_share"`
	// Fingerprint of the guest volume when the hypervisor had exited.
	Fingerprint string `json:"fingerprint,omitempty"`
	ImageBytes  uint64 `json:"image_bytes,omitempty"`
	KeyID       string `json:"key_id"`
	WrappedKey  []byte `json:"wrapped_key"`
	KeyCheck    string `json:"key_check"`
}

// Store keeps records as <dir>/<guest_id>.json.
type Store struct{ Dir string }

func (s Store) path(id string) string { return filepath.Join(s.Dir, id+".json") }

// Put writes r atomically and durably: a temporary file, fsync, rename,
// and an fsync of the directory, so a power cut leaves the old record or
// the new one.
func (s Store) Put(r *Record) error {
	if r.GuestID == "" || strings.ContainsAny(r.GuestID, "/\x00") {
		return fmt.Errorf("freeze record: bad guest id %q", r.GuestID)
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".rec-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // gone after the rename; cleans up a failure
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close() // the write error is the one reported
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() // the sync error is the one reported
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.path(r.GuestID)); err != nil {
		return err
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }() // read-only handle for the fsync
	return d.Sync()
}

// Get reads a guest's record; (nil, nil) when there is none.
func (s Store) Get(id string) (*Record, error) {
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := &Record{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("freeze record %s: %w", id, err)
	}
	if r.GuestID != id {
		return nil, fmt.Errorf("freeze record %s names guest %s", id, r.GuestID)
	}
	return r, nil
}

// Delete removes a guest's record; absent is fine.
func (s Store) Delete(id string) error {
	err := os.Remove(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// List returns the guest ids that have a record.
func (s Store) List() ([]string, error) {
	des, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, d := range des {
		n := d.Name()
		if d.IsDir() || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(n, ".json"))
	}
	return ids, nil
}
