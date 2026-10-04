package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The idle warning (DECISIONS I-262). The api marks a running project
// `idle` once it has gone a day with no SSH session and no agent working;
// repose never stops it (R1-5), so the CLI says it is running with nobody
// attached and how to stop it: on every `projects` and `status`, and once
// per idle stretch on `run` and `attach` of some other project. Since
// I-289 a plan buys memory that may run at once, so there is no rate to
// print; the idle machine holds part of the plan.

// idleFor renders how long a project has been idle: hours up to two days,
// then days.
func idleFor(d time.Duration) string {
	h := int(d / time.Hour)
	if h < 48 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dd", h/24)
}

// idleLine is "running for 26h with nobody attached", or "" for a project that is not idle.
func idleLine(p *Project, now time.Time) string {
	if p.Idle == nil || p.State != "running" {
		return ""
	}
	return fmt.Sprintf("running for %s with nobody attached", idleFor(now.Sub(p.Idle.Since)))
}

// idleNotedName is the laptop file that remembers which idle stretches
// run and attach have already mentioned (docs/interfaces/cli-config.md).
const idleNotedName = "idle-noted.json"

// idleOthersNote is the one line run and attach print about other idle
// projects, naming only stretches not mentioned before, and records the
// ones it named. The file keeps only projects idle now, so a project that
// is used and goes idle again is mentioned again. A file that cannot be
// read or written costs at most a repeated mention.
func idleOthersNote(dir string, projects []Project, current string, now time.Time) string {
	path := filepath.Join(dir, idleNotedName)
	noted := map[string]time.Time{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &noted) // a damaged cache only repeats a mention
	}
	keep := map[string]time.Time{}
	var parts []string
	for i := range projects {
		p := &projects[i]
		if p.Idle == nil || p.State != "running" {
			continue
		}
		if p.ID == current {
			// Being attached is being used: it drops out of the file,
			// and its next idle stretch is mentioned.
			continue
		}
		keep[p.ID] = p.Idle.Since
		if t, ok := noted[p.ID]; ok && t.Equal(p.Idle.Since) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (idle %s)", p.Slug, idleFor(now.Sub(p.Idle.Since))))
	}
	if dir != "" && !sameNoted(noted, keep) {
		if b, err := json.Marshal(keep); err == nil {
			_ = writeFileAtomic(path, b, 0o600) // best effort, see above
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	return fmt.Sprintf("Still running with nobody on it: %s.", strings.Join(parts, ", "))
}

func sameNoted(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || !w.Equal(v) {
			return false
		}
	}
	return true
}

// idleNoteWait is how long run and attach wait, after connecting, for the
// project list the note is made from. It was asked for at the start, so
// it is almost always there already; a slow api only loses the note.
const idleNoteWait = 300 * time.Millisecond

// startIdleNote reads the project list beside the command and returns
// the function that prints the note for the project being attached.
func startIdleNote(ctx context.Context, e *Env) func(current string) {
	ch := make(chan []Project, 1)
	go func() {
		ps, err := e.Client.ListProjects(ctx)
		if err != nil {
			ps = nil
		}
		ch <- ps
	}()
	return func(current string) {
		var ps []Project
		select {
		case ps = <-ch:
		case <-time.After(idleNoteWait):
			return
		}
		if ps == nil {
			return // the list failed: say nothing, and keep what was noted
		}
		if line := idleOthersNote(e.Dir, ps, current, time.Now()); line != "" {
			_, _ = fmt.Fprintln(e.ErrOut, line)
		}
	}
}
