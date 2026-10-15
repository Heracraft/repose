package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// stopProbeTimeout bounds the stop's read of the checkout (I-740).
// status's 4 s was too short for it: a cold handshake from a laptop took
// 1.2 to 3.2 s on prod, and 4 of 13 stops lost the clause. A stop is the
// last moment the read can be made, so it waits longer than status,
// which can be run again.
const stopProbeTimeout = 15 * time.Second

// stopEarlyMaxAge is how old an early read may be when the stop uses it:
// one started before a question the user took a while to answer is read
// again, since the agent it asked about kept working.
const stopEarlyMaxAge = 10 * time.Second

// stopProbe is a read of a machine's checkout started before the api
// answered (startStopProbes). have is the laptop's commits it sent.
type stopProbe struct {
	have    []string
	started time.Time
	done    chan struct{}
	g       guestStatus
}

// startStopProbes starts the read of each machine names gives, beside
// the api's read of the projects (I-740): the ssh handshake was most of
// a stop's time, and it needs only the slug. Each read uses the cache's
// guess for the project's id and remote, for the laptop's commits; the
// stop checks the read against the project the api returns
// (probeBeforeStop). A name the api resolves to a machine that is not
// running wastes one refused connection.
func startStopProbes(ctx context.Context, e *Env, names []string) map[string]*stopProbe {
	if e.TargetFor != nil && e.readGuest == nil {
		return nil
	}
	m := map[string]*stopProbe{}
	for _, name := range names {
		name = e.resolveArg(name)
		if name == "" || strings.Contains(name, ":") {
			continue
		}
		guess := cachedGuess(e, name, defaultResolveDeps())
		if guess == nil {
			guess = &Project{Slug: name}
		}
		if m[guess.Slug] != nil {
			continue
		}
		sp := &stopProbe{have: laptopCommits(e, guess), started: time.Now(), done: make(chan struct{})}
		m[guess.Slug] = sp
		go func() {
			defer close(sp.done)
			sp.g = e.readGuestFor(ctx, guess.Slug, sp.have, stopProbeTimeout)
		}()
	}
	return m
}

// readGuestFor reads the guest's status within budget. A master whose
// connection died (the laptop slept) would hold the read for the whole
// budget, so masterAlive stops it first; a live one carries the read.
// Tests that stand in for ssh with TargetFor read nothing.
func (e *Env) readGuestFor(ctx context.Context, slug string, have []string, budget time.Duration) guestStatus {
	if e.readGuest != nil {
		return e.readGuest(ctx, slug, have, budget)
	}
	if e.TargetFor != nil {
		return guestStatus{}
	}
	t := e.target(slug)
	masterAlive(ctx, t)
	return guestStatusReadIn(ctx, t, slug, have, budget)
}

// probeBeforeStop reads the machine's checkout before its stop: the
// early read when it answers the same question (the same laptop commits,
// not older than stopEarlyMaxAge), else a new one. An empty status for a
// machine that is not running; probeErr says when it did not answer.
func probeBeforeStop(ctx context.Context, e *Env, p *Project) guestStatus {
	if p.State != "running" {
		return guestStatus{}
	}
	have := laptopCommits(e, p)
	if sp := e.stopEarly[p.Slug]; sp != nil && slices.Equal(sp.have, have) && time.Since(sp.started) <= stopEarlyMaxAge {
		select {
		case <-sp.done:
			timingf("stop: %s read beside the api", p.Slug)
			return sp.g
		case <-ctx.Done():
			return guestStatus{probeErr: "ssh_timeout"}
		}
	}
	return e.readGuestFor(ctx, p.Slug, have, stopProbeTimeout)
}

// stopClause is the stop line's clause for what the read found: what
// the machine keeps (leftClause), or that the checkout is unknown and
// why, in status's words.
func stopClause(g guestStatus) string {
	if g.probeErr != "" {
		return "checkout " + checkoutUnknown(g.probeErr, g.probeDetail, stopProbeTimeout)
	}
	return leftClause(g.git)
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
