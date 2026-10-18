package freeze

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Unfrozen is what one freeze left behind: how many running guests it did
// not freeze, by reason (DECISIONS I-796). A shutdown freeze increments
// hostd's counter and then the host goes down, so the scrape that would
// have carried it never happens; the tally is written here instead and
// added to the counter at the next start, where Prometheus sees it.
//
// It lives beside the records as a dot file, which Store.List skips, so a
// tally is never mistaken for a guest's record.
type Unfrozen struct {
	// HostBootID is the boot the freeze ran under, for the operator.
	HostBootID string    `json:"host_boot_id"`
	At         time.Time `json:"at"`
	// Reason is the freeze's reason (shutdown, scheduled_event).
	Reason string `json:"reason"`
	// Counts is metrics.FreezeUnfrozenReasons to the number of guests.
	Counts map[string]int `json:"counts"`
}

func (s Store) unfrozenPath() string { return filepath.Join(s.Dir, ".unfrozen.json") }

// PutUnfrozen writes the tally, replacing any older one: what the next
// start reports is the last freeze before it.
func (s Store) PutUnfrozen(u *Unfrozen) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".unf-*")
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
	if err := os.Rename(name, s.unfrozenPath()); err != nil {
		return err
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }() // read-only handle for the fsync
	return d.Sync()
}

// TakeUnfrozen reads the tally and removes it, so it is reported once;
// (nil, nil) when there is none.
func (s Store) TakeUnfrozen() (*Unfrozen, error) {
	b, err := os.ReadFile(s.unfrozenPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Removed whatever the contents are: a tally that cannot be parsed
	// would otherwise be read at every start forever.
	_ = os.Remove(s.unfrozenPath())
	u := &Unfrozen{}
	if err := json.Unmarshal(b, u); err != nil {
		return nil, err
	}
	return u, nil
}
