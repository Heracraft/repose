// Package state is hostd's bbolt store: the guest table Hello is built from,
// the IP index allocation, the command idempotency records, and host flags.
// The guest Manager is the single writer; readers are the stream and the
// operator subcommands.
package state

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// SchemaVersion is bumped when a bucket layout changes. A downgraded hostd
// that finds a newer version refuses to start rather than misread it.
const SchemaVersion = 1

var (
	bucketMeta     = []byte("meta")
	bucketGuests   = []byte("guests")
	bucketIPs      = []byte("ips")
	bucketCommands = []byte("commands")
	keySchema      = []byte("schema_version")
	keyDraining    = []byte("draining")
)

// ErrNotFound is returned for a missing guest or command.
var ErrNotFound = errors.New("not found")

// ErrLocked is returned when another hostd holds the database.
var ErrLocked = errors.New("hostd already running")

// Guest is one row of the guest table. Secrets are never stored here.
type Guest struct {
	GuestID       string `json:"guest_id"`
	ProjectID     string `json:"project_id"`
	UserID        string `json:"user_id,omitempty"`
	ProjectSlug   string `json:"project_slug,omitempty"`
	RemoteURL     string `json:"remote_url,omitempty"`
	Class         string `json:"class"`
	VolumeBytes   uint64 `json:"volume_bytes"`
	SystemClosure string `json:"system_closure"`
	// PastClosures are the system closures this guest ran before, newest
	// first, at most MaxPastClosures: its store view holds them too, since
	// its nix database still lists their paths as valid (I-463).
	PastClosures []string `json:"past_closures,omitempty"`
	// LastGoodClosure is the newest system closure this guest's disk is
	// known to run: one that reached Ready at a boot, or that guestd
	// switched to in place. A boot of another closure that never reaches
	// Ready boots this one instead, once (DECISIONS I-590). Empty on a
	// record from before then until the guest next boots or switches, and
	// on a new or restored guest until its first boot.
	LastGoodClosure string `json:"last_good_closure,omitempty"`
	// BootFallback is the last boot that fell back to LastGoodClosure,
	// kept so a command resent with the same id answers the same way.
	BootFallback *BootFallback     `json:"boot_fallback,omitempty"`
	State        string            `json:"state"`
	Reason       string            `json:"reason,omitempty"`
	IP           string            `json:"ip"`
	MAC          string            `json:"mac"`
	Tap          string            `json:"tap"`
	CID          uint32            `json:"vsock_cid"`
	IPIndex      uint32            `json:"ip_index"`
	Env          map[string]string `json:"env,omitempty"`
	Principals   []string          `json:"principals,omitempty"`
	SSHCAPub     string            `json:"ssh_ca_pub,omitempty"`
	HooksConfig  []byte            `json:"hooks_config,omitempty"`
	ProjectJSON  []byte            `json:"project_json,omitempty"`
	Kernel       string            `json:"kernel,omitempty"`
	Initrd       string            `json:"initrd,omitempty"`
	BootID       string            `json:"boot_id,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// BootFallback records a boot of Failed that never reached Ready and was
// replaced by a boot of the guest's last good closure. Code and Message
// are hostd's fixed classification of the failure (never console text).
type BootFallback struct {
	CommandID string    `json:"command_id"`
	Failed    string    `json:"failed"`
	Code      string    `json:"code"`
	Message   string    `json:"message"`
	At        time.Time `json:"at"`
}

// MaxPastClosures bounds Guest.PastClosures.
const MaxPastClosures = 16

// Command is an idempotency record. Status is "started" until the result is
// stored, then "done".
type Command struct {
	CommandID string    `json:"command_id"`
	Kind      string    `json:"kind"`
	GuestID   string    `json:"guest_id,omitempty"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	DoneAt    time.Time `json:"done_at,omitempty"`
	Result    []byte    `json:"result,omitempty"`
}

// DB is the open store.
type DB struct {
	db   *bolt.DB
	path string
}

// Open opens or creates the store. A second opener gets ErrLocked within a
// second; a newer schema is refused.
func Open(path string) (*DB, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("%w: %s is locked", ErrLocked, path)
		}
		return nil, fmt.Errorf("open state %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketMeta, bucketGuests, bucketIPs, bucketCommands} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		m := tx.Bucket(bucketMeta)
		if v := m.Get(keySchema); v != nil {
			var have int
			if err := json.Unmarshal(v, &have); err != nil {
				return fmt.Errorf("schema version unreadable: %w", err)
			}
			if have > SchemaVersion {
				return fmt.Errorf("state schema %d is newer than this hostd's %d; refusing to start", have, SchemaVersion)
			}
			return nil
		}
		v, _ := json.Marshal(SchemaVersion)
		return m.Put(keySchema, v)
	})
	if err != nil {
		_ = db.Close() // returning the schema error; a close failure adds nothing
		return nil, fmt.Errorf("state %s: %w", path, err)
	}
	return &DB{db: db, path: path}, nil
}

// Path is the file the store lives in.
func (d *DB) Path() string { return d.path }

// Close closes the store.
func (d *DB) Close() error { return d.db.Close() }

// PutGuest writes a guest row.
func (d *DB) PutGuest(g *Guest) error {
	g.UpdatedAt = time.Now().UTC()
	if g.CreatedAt.IsZero() {
		g.CreatedAt = g.UpdatedAt
	}
	v, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketGuests).Put([]byte(g.GuestID), v)
	})
}

// GetGuest reads one guest row.
func (d *DB) GetGuest(id string) (*Guest, error) {
	var g *Guest
	err := d.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketGuests).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		g = &Guest{}
		return json.Unmarshal(v, g)
	})
	return g, err
}

// DeleteGuest removes a guest row.
func (d *DB) DeleteGuest(id string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketGuests).Delete([]byte(id))
	})
}

// ListGuests returns every guest row in id order.
func (d *DB) ListGuests() ([]*Guest, error) {
	var out []*Guest
	err := d.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketGuests).ForEach(func(_, v []byte) error {
			g := &Guest{}
			if err := json.Unmarshal(v, g); err != nil {
				return err
			}
			out = append(out, g)
			return nil
		})
	})
	return out, err
}

// SetGuestState updates state and reason atomically and returns the row.
func (d *DB) SetGuestState(id, st, reason string) (*Guest, error) {
	var g *Guest
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketGuests)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		g = &Guest{}
		if err := json.Unmarshal(v, g); err != nil {
			return err
		}
		g.State, g.Reason, g.UpdatedAt = st, reason, time.Now().UTC()
		nv, err := json.Marshal(g)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), nv)
	})
	return g, err
}

func idxKey(i uint32) []byte {
	var k [4]byte
	binary.BigEndian.PutUint32(k[:], i)
	return k[:]
}

// AllocIndex reserves the lowest free index in [0, max] for guestID, or
// returns the one it already holds.
func (d *DB) AllocIndex(guestID string, max uint32) (uint32, error) {
	var got uint32
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIPs)
		var found bool
		err := b.ForEach(func(k, v []byte) error {
			if string(v) == guestID {
				got, found = binary.BigEndian.Uint32(k), true
			}
			return nil
		})
		if err != nil || found {
			return err
		}
		for i := uint32(0); i <= max; i++ {
			if b.Get(idxKey(i)) == nil {
				got = i
				return b.Put(idxKey(i), []byte(guestID))
			}
		}
		return errors.New("no free guest address in the host range")
	})
	return got, err
}

// ReleaseIndex frees whatever index guestID holds.
func (d *DB) ReleaseIndex(guestID string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIPs)
		var keys [][]byte
		err := b.ForEach(func(k, v []byte) error {
			if string(v) == guestID {
				keys = append(keys, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// GetCommand reads an idempotency record.
func (d *DB) GetCommand(id string) (*Command, error) {
	var c *Command
	err := d.db.View(func(tx *bolt.Tx) error {
		v := tx.Bucket(bucketCommands).Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		c = &Command{}
		return json.Unmarshal(v, c)
	})
	return c, err
}

// StartCommand records c as started unless a record exists, in which case
// the existing record is returned and nothing is written.
func (d *DB) StartCommand(c *Command) (*Command, error) {
	var existing *Command
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCommands)
		if v := b.Get([]byte(c.CommandID)); v != nil {
			existing = &Command{}
			return json.Unmarshal(v, existing)
		}
		c.Status = "started"
		c.StartedAt = time.Now().UTC()
		v, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return b.Put([]byte(c.CommandID), v)
	})
	return existing, err
}

// RestartCommand marks a started record as started again (a replay after a
// crash), keeping its original start time.
func (d *DB) RestartCommand(id string) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCommands)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		c := &Command{}
		if err := json.Unmarshal(v, c); err != nil {
			return err
		}
		c.Status = "started"
		nv, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), nv)
	})
}

// FinishCommand stores the result and marks the record done.
func (d *DB) FinishCommand(id string, result []byte) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCommands)
		v := b.Get([]byte(id))
		if v == nil {
			return ErrNotFound
		}
		c := &Command{}
		if err := json.Unmarshal(v, c); err != nil {
			return err
		}
		c.Status, c.DoneAt, c.Result = "done", time.Now().UTC(), result
		nv, err := json.Marshal(c)
		if err != nil {
			return err
		}
		return b.Put([]byte(id), nv)
	})
}

// ListCommands returns every record.
func (d *DB) ListCommands() ([]*Command, error) {
	var out []*Command
	err := d.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketCommands).ForEach(func(_, v []byte) error {
			c := &Command{}
			if err := json.Unmarshal(v, c); err != nil {
				return err
			}
			out = append(out, c)
			return nil
		})
	})
	return out, err
}

// PruneCommands deletes done records finished before cutoff.
func (d *DB) PruneCommands(cutoff time.Time) (int, error) {
	n := 0
	err := d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketCommands)
		var del [][]byte
		err := b.ForEach(func(k, v []byte) error {
			c := &Command{}
			if err := json.Unmarshal(v, c); err != nil {
				return err
			}
			if c.Status == "done" && c.DoneAt.Before(cutoff) {
				del = append(del, append([]byte(nil), k...))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range del {
			if err := b.Delete(k); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// Draining reports the host drain flag.
func (d *DB) Draining() (bool, error) {
	var v bool
	err := d.db.View(func(tx *bolt.Tx) error {
		v = string(tx.Bucket(bucketMeta).Get(keyDraining)) == "1"
		return nil
	})
	return v, err
}

// SetDraining sets the host drain flag.
func (d *DB) SetDraining(on bool) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		v := "0"
		if on {
			v = "1"
		}
		return tx.Bucket(bucketMeta).Put(keyDraining, []byte(v))
	})
}

// Snapshot is the JSON form used by `hostd state export` and import.
type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	Draining      bool       `json:"draining"`
	Guests        []*Guest   `json:"guests"`
	Commands      []*Command `json:"commands"`
}

// Export writes the whole store as JSON an older hostd can import.
func (d *DB) Export(w io.Writer) error {
	s := Snapshot{SchemaVersion: SchemaVersion}
	var err error
	if s.Draining, err = d.Draining(); err != nil {
		return err
	}
	if s.Guests, err = d.ListGuests(); err != nil {
		return err
	}
	if s.Commands, err = d.ListCommands(); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// Import replaces the store's contents with a Snapshot.
func (d *DB) Import(r io.Reader) error {
	var s Snapshot
	if err := json.NewDecoder(r).Decode(&s); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	return d.db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketGuests, bucketIPs, bucketCommands} {
			if err := tx.DeleteBucket(b); err != nil {
				return err
			}
			if _, err := tx.CreateBucket(b); err != nil {
				return err
			}
		}
		gb, ib, cb := tx.Bucket(bucketGuests), tx.Bucket(bucketIPs), tx.Bucket(bucketCommands)
		for _, g := range s.Guests {
			v, err := json.Marshal(g)
			if err != nil {
				return err
			}
			if err := gb.Put([]byte(g.GuestID), v); err != nil {
				return err
			}
			if err := ib.Put(idxKey(g.IPIndex), []byte(g.GuestID)); err != nil {
				return err
			}
		}
		for _, c := range s.Commands {
			v, err := json.Marshal(c)
			if err != nil {
				return err
			}
			if err := cb.Put([]byte(c.CommandID), v); err != nil {
				return err
			}
		}
		dr := "0"
		if s.Draining {
			dr = "1"
		}
		return tx.Bucket(bucketMeta).Put(keyDraining, []byte(dr))
	})
}

// ClaimIndex reserves a specific index for guestID (rebuild from disk).
func (d *DB) ClaimIndex(guestID string, idx uint32) error {
	return d.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketIPs)
		if v := b.Get(idxKey(idx)); v != nil && string(v) != guestID {
			return fmt.Errorf("index %d held by %s", idx, string(v))
		}
		return b.Put(idxKey(idx), []byte(guestID))
	})
}
