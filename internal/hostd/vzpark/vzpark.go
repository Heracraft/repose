// Package vzpark is the guest park's key and image on a Mac (design 2,
// DECISIONS I-1664, I-1662): Virtualization.framework encrypts every save
// file itself with a Secure Enclave key of the logged-in user, so the park
// holds no key of its own and its image is a plain directory beside the
// guest. Nothing here sees a secret.
package vzpark

import (
	"context"
	"errors"
	"os"

	"github.com/heracraft/repose/internal/hostd/guest"
	"github.com/heracraft/repose/internal/hostd/park"
)

// KeyID is what a park record names as its key: VZ's, which repose
// never holds.
const KeyID = "vz-secure-enclave"

// Sealer satisfies park.Sealer without a key of its own. The park makes a
// random image key and records a check of it; on a Mac that key encrypts
// nothing (the image is a plain directory and VZ seals the save file), so
// the "wrapped" key is the key itself, kept only so the record's check
// holds at the restore.
type Sealer struct{}

func (Sealer) Ready() error { return nil }

func (Sealer) Wrap(key []byte) ([]byte, string, error) {
	return append([]byte(nil), key...), KeyID, nil
}

func (Sealer) Unwrap(_ context.Context, wrapped []byte, keyID string) ([]byte, error) {
	if keyID != KeyID {
		return nil, errors.New("vzpark: the record was not written by a Mac park")
	}
	return append([]byte(nil), wrapped...), nil
}

func (Sealer) Refresh(context.Context) error { return nil }

// Image is a directory: VZ writes the save file encrypted (vz.state).
type Image struct{}

func (Image) Create(_ context.Context, _ string, _ uint64, _ []byte, dir string, uid, gid int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chown(dir, uid, gid)
}

func (Image) Open(_ context.Context, _ string, _ []byte, dir, _ string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return errors.New("vzpark: the image is not a directory")
	}
	return nil
}

// FS names the image's filesystem in the record.
func (Image) FS() string { return "apfs-dir" }

func (Image) Close(context.Context, string, string) error { return nil }

func (Image) Remove(_ context.Context, _ string, dir string) error { return os.RemoveAll(dir) }

// Deps are a Mac host's park facilities.
func Deps() guest.ParkDeps {
	return guest.ParkDeps{Sealer: Sealer{}, Image: Image{}, Fingerprint: park.Fingerprint}
}
