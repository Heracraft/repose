package park

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Tally is what this host's parks and unparks have cost, kept beside the
// park records so a count survives the restart the host goes down for
// (DECISIONS I-796). A shutdown park increments hostd's counters and
// then the host goes down, so the scrape that would have carried them
// never happens; an unpark's losses are counted before the metrics listener
// is up, which Prometheus never sees at zero either.
//
// Both halves are *running totals*, never one park's counts: hostd's
// counters are in-memory and start at zero, so a start that only added
// the last park's tally would publish "guests lost by the last park"
// and the same guest lost at every reboot would read 1, 1, 1 — a series
// `increase()` cannot see, which is the silence the alert exists to end.
// A start sets the counters to these totals and every park and unpark
// adds to both the counter and the file, so the series only ever rises.
//
// It lives beside the records as a dot file, which Store.List skips, so a
// tally is never mistaken for a guest's record.
type Tally struct {
	// Skipped is the running total, by metrics.ParkSkippedReasons, of
	// running guests this host's parks left without their memory.
	Skipped map[string]int `json:"skipped"`
	// Discarded is the running total, by metrics.UnparkDiscardedReasons, of
	// parked guests whose image an unpark could not give back.
	Discarded map[string]int `json:"discarded"`
	// LastPark is the last non-operator park, for the log line that
	// names what the restart cost. It is written by every such park,
	// including one that left nothing behind, so a successful park
	// clears what an earlier one said.
	LastPark ParkTally `json:"last_park"`
}

// ParkTally is one park's own counts, kept for the operator.
type ParkTally struct {
	// HostBootID is the boot the park ran under: a different one at the
	// next start means the host did restart and those guests started cold.
	HostBootID string    `json:"host_boot_id"`
	At         time.Time `json:"at"`
	// Reason is the park's reason (shutdown, scheduled_event).
	Reason string         `json:"reason"`
	Counts map[string]int `json:"counts"`
}

func (s Store) tallyPath() string { return filepath.Join(s.Dir, ".tally.json") }

// LoadTally reads the totals; a zero Tally when there are none yet.
func (s Store) LoadTally() (*Tally, error) {
	t := &Tally{Skipped: map[string]int{}, Discarded: map[string]int{}}
	b, err := os.ReadFile(s.tallyPath())
	if errors.Is(err, os.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, t); err != nil {
		return nil, err
	}
	if t.Skipped == nil {
		t.Skipped = map[string]int{}
	}
	if t.Discarded == nil {
		t.Discarded = map[string]int{}
	}
	return t, nil
}

// AddSkipped adds one park's counts to the skipped total and records
// that park as the last one. Called with no counts it only records the
// park, which is how a park that saved every guest clears the counts
// an earlier one left (a scheduled event that was then called off).
func (s Store) AddSkipped(last ParkTally) error {
	return s.addTally(func(t *Tally) {
		for why, n := range last.Counts {
			if n > 0 {
				t.Skipped[why] += n
			}
		}
		t.LastPark = last
	})
}

// MarkLastParkReported empties the last park's counts, keeping when
// and under which boot it ran. The line that says those guests started
// cold belongs to the start that found them, and the totals it was
// already added to are what a later start publishes.
func (s Store) MarkLastParkReported() error {
	return s.addTally(func(t *Tally) { t.LastPark.Counts = nil })
}

// AddDiscarded adds an unpark's losses to the discarded total.
func (s Store) AddDiscarded(counts map[string]int) error {
	return s.addTally(func(t *Tally) {
		for why, n := range counts {
			if n > 0 {
				t.Discarded[why] += n
			}
		}
	})
}

// addTally reads, changes and rewrites the tally. One ParkAll or UnparkAll
// runs at a time, so there is no second writer in this process.
func (s Store) addTally(f func(*Tally)) error {
	t, err := s.LoadTally()
	if err != nil {
		// A tally that cannot be parsed is started again rather than
		// kept: the alternative is a host that never counts anything.
		t = &Tally{Skipped: map[string]int{}, Discarded: map[string]int{}}
	}
	f(t)
	return s.putTally(t)
}

func (s Store) putTally(t *Tally) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".tly-*")
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
	if err := os.Rename(name, s.tallyPath()); err != nil {
		return err
	}
	d, err := os.Open(s.Dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }() // read-only handle for the fsync
	return d.Sync()
}
