package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// What a stop leaves on the machine (DECISIONS I-634). Before the stop,
// the status probe's git script runs on the machine; its rows add a
// clause to the stop line (`; 3 commits on main not fetched, 4 files not
// committed`) and are kept in stopLeftName, so `repose status` on the
// stopped machine still has a checkout row the next morning.

// stopLeftName is the laptop file with the git rows each project had
// when this laptop last stopped it (docs/interfaces/cli-config.md).
const stopLeftName = "stop-left.json"

// stopLeft is one project's entry.
type stopLeft struct {
	At   time.Time `json:"at"`
	Rows []gitRow  `json:"rows"`
}

// probeBeforeStop reads the machine's checkout before its stop, best
// effort: nil when it did not answer, has no checkout, or the command
// stands in for ssh (tests).
func probeBeforeStop(ctx context.Context, e *Env, p *Project) []gitRow {
	if e.TargetFor != nil || p.State != "running" {
		return nil
	}
	return guestStatusRead(ctx, e.target(p.Slug), p.Slug, laptopCommits(e, p)).git
}

// leftClause is the stop line's clause for rows: the commits the laptop
// lacks (when it could tell) and the files not committed, over every
// worktree. "" when there is nothing to say.
func leftClause(rows []gitRow) string {
	commits, files, branches := 0, 0, []string{}
	for _, r := range rows {
		if r.NotOnLaptop != nil && *r.NotOnLaptop > 0 {
			commits += *r.NotOnLaptop
			b := r.Branch
			if b == "" {
				b = r.Worktree
			}
			branches = append(branches, b)
		}
		if r.Uncommitted != nil {
			files += *r.Uncommitted
		}
	}
	var parts []string
	switch {
	case len(branches) == 1:
		parts = append(parts, fmt.Sprintf("%s on %s not fetched", count(commits, "commit"), branches[0]))
	case len(branches) > 1:
		parts = append(parts, fmt.Sprintf("%s on %d branches not fetched", count(commits, "commit"), len(branches)))
	}
	if files > 0 {
		parts = append(parts, count(files, "file")+" not committed")
	}
	return strings.Join(parts, ", ")
}

// withLeft puts the clause into a stop line that ends in a period.
func withLeft(line, clause string) string {
	if clause == "" {
		return line
	}
	return strings.TrimSuffix(line, ".") + "; " + clause + "."
}

var stopLeftMu sync.Mutex

func readStopLeft(dir string) map[string]stopLeft {
	m := map[string]stopLeft{}
	if b, err := os.ReadFile(filepath.Join(dir, stopLeftName)); err == nil {
		_ = json.Unmarshal(b, &m) // a damaged cache only loses the rows
	}
	return m
}

// saveStopLeft records rows as what project id had at its stop, and
// forgets it when rows is nil. Best effort.
func saveStopLeft(dir, id string, rows []gitRow, at time.Time) {
	if dir == "" {
		return
	}
	// Several stops in one command save at once.
	stopLeftMu.Lock()
	defer stopLeftMu.Unlock()
	m := readStopLeft(dir)
	if rows == nil {
		if _, ok := m[id]; !ok {
			return
		}
		delete(m, id)
	} else {
		m[id] = stopLeft{At: at, Rows: rows}
	}
	if b, err := json.Marshal(m); err == nil {
		_ = writeFileAtomic(filepath.Join(dir, stopLeftName), b, 0o600)
	}
}

// stoppedRows is what status shows for a stopped project: the rows of
// this laptop's last stop of it, unless the machine ran after that.
func stoppedRows(dir string, p *Project) (stopLeft, bool) {
	if p.State != "stopped" {
		return stopLeft{}, false
	}
	l, ok := readStopLeft(dir)[p.ID]
	if !ok || len(l.Rows) == 0 || (p.StartedAt != nil && p.StartedAt.After(l.At)) {
		return stopLeft{}, false
	}
	return l, true
}

// writeStoppedRows prints a stopped project's checkout rows, with when
// they were read: `checkout   main: 3 commits not on this laptop (at the
// stop, 9h ago)`.
func writeStoppedRows(w interface{ Write([]byte) (int, error) }, l stopLeft, now time.Time) {
	for i, r := range l.Rows {
		line := gitRowText(r, now)
		if i == 0 {
			statusRow(w, "checkout", line+" (at the stop, "+compactAge(now.Sub(l.At))+" ago)")
		} else {
			_, _ = fmt.Fprintln(w, statusIndent+line)
		}
	}
}
