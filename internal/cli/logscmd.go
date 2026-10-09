package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LogsCmd implements `repose logs [--kind console|build|ops] [--since T]
// [-n N] [--follow]`. follow polls every 2s (07-cli.md §5.10); poll is
// injected so tests do not sleep in a loop. tail, when above 0, keeps
// only the last tail lines of the first read, as `docker logs -n` does;
// a follow then prints every new line.
func LogsCmd(ctx context.Context, e *Env, projectArg, kind, since string, tail int, follow bool, poll func()) error {
	from, err := parseSince(since, time.Now())
	if err != nil {
		return cobraUsageError{err}
	}
	if tail < 0 {
		return cobraUsageError{fmt.Errorf("--tail takes a number of lines, 0 or more; got %d", tail)}
	}
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	cur := apiSince(from)
	var last time.Time
	printed := 0
	first := true
	for {
		lines, err := e.Client.ProjectLogs(ctx, project.ID, kind, cur)
		if err != nil {
			return err
		}
		if first && tail > 0 && len(lines) > tail {
			lines = lines[len(lines)-tail:]
		}
		first = false
		printed += len(lines)
		for _, l := range lines {
			// A line at or before the cursor was printed by the previous
			// poll (the api's since is inclusive for ops).
			if !last.IsZero() && !l.TS.IsZero() && !l.TS.After(last) {
				continue
			}
			if e.JSON {
				if err := writeLogJSON(e.Out, l); err != nil {
					return err
				}
				continue
			}
			_, _ = fmt.Fprintln(e.Out, logLineText(l, kind))
		}
		if !follow {
			if printed == 0 && !e.JSON {
				// Nothing printed and exit 0 read as a command that did
				// nothing (2026-10-07): say whose logs were empty.
				_, _ = fmt.Fprintln(e.ErrOut, emptyLogsLine(project.Slug, kind))
			}
			return nil
		}
		if n := len(lines); n > 0 && !lines[n-1].TS.IsZero() {
			last = lines[n-1].TS
			cur = last.Format(time.RFC3339Nano)
		}
		if poll != nil {
			poll()
		} else if err := sleepOrDone(ctx, 2*time.Second); err != nil {
			return err
		}
	}
}

// writeLogJSON writes the api's object for a line as it came, on one
// line (NDJSON, DECISIONS I-609).
func writeLogJSON(w io.Writer, l LogLine) error {
	if len(l.raw) == 0 {
		return writeJSONLine(w, l)
	}
	var b bytes.Buffer
	if err := json.Compact(&b, l.raw); err != nil {
		return writeJSONLine(w, l)
	}
	b.WriteByte('\n')
	_, err := w.Write(b.Bytes())
	return err
}

// emptyLogsLine says which project's logs of that kind were empty. The
// console holds only boots that failed (DECISIONS I-592).
func emptyLogsLine(slug, kind string) string {
	switch kind {
	case "build":
		return fmt.Sprintf("%s has no build log.", slug)
	case "ops":
		return fmt.Sprintf("%s has no operations.", slug)
	default:
		return fmt.Sprintf("%s has no console output: repose keeps a machine's console only from a boot that failed.", slug)
	}
}

// logLineText is one line of `repose logs`: time, kind, text. An api
// before I-322 sends build lines without a time or kind; they print
// without the time rather than as 0001-01-01. An ops line is the op's
// kind, how it ended, how long it took and its error (I-609).
func logLineText(l LogLine, kind string) string {
	k := l.Kind
	if k == "" {
		k = kind
	}
	text := l.Line
	if kind == "ops" || (text == "" && l.OpID != "" && l.State != "") {
		text = opsLineText(l)
	}
	if l.TS.IsZero() {
		return k + " " + text
	}
	return streamTime(l.TS) + " " + k + " " + text
}

// opsLineText is "failed 3.1s boot_timeout: the machine did not boot":
// the op's end state, its duration once finished, and its error.
func opsLineText(l LogLine) string {
	if l.State == "" && l.Line != "" {
		return l.Line // the fake before I-609 sent a line
	}
	parts := []string{opStateWord(l.State)}
	if l.DurationMS != nil {
		parts = append(parts, fmtElapsed(time.Duration(*l.DurationMS)*time.Millisecond))
	}
	if l.Error != nil && (l.Error.Code != "" || l.Error.Message != "") {
		switch {
		case l.Error.Code == "":
			parts = append(parts, l.Error.Message)
		case l.Error.Message == "":
			parts = append(parts, l.Error.Code)
		default:
			parts = append(parts, l.Error.Code+": "+l.Error.Message)
		}
	}
	return strings.Join(parts, " ")
}

// opStateWord is an op's state as `repose logs --kind ops` prints it.
func opStateWord(state string) string {
	switch state {
	case "error":
		return "failed"
	case "":
		return "-"
	default:
		return state
	}
}

// streamTime is how a stream (`logs`, `events`) prints a time: local
// RFC 3339, with the offset. Tables use tableTime; --json keeps the
// api's UTC (DECISIONS I-609).
func streamTime(t time.Time) string { return t.Local().Format(time.RFC3339) }

// tableTime is how a table prints a time: local, to the minute.
func tableTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }

var sinceDays = regexp.MustCompile(`^([0-9]+)([dw])(.*)$`)

// parseSince reads --since: a duration back from now (90m, 2h30m, 2d,
// 1w, 1d12h), a date (local midnight), or an RFC 3339 time. Anything else
// is refused, where it used to reach the api unchanged and come back as
// every line (DECISIONS I-609). "" is no limit.
func parseSince(since string, now time.Time) (time.Time, error) {
	s := strings.TrimSpace(since)
	if s == "" {
		return time.Time{}, nil
	}
	bad := fmt.Errorf("--since takes a duration (90m, 2d, 1w), a date (2026-10-01) or an RFC 3339 time; got %q", since)
	if d, ok := sinceDuration(s); ok {
		if d <= 0 {
			return time.Time{}, bad
		}
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, bad
}

// sinceDuration is a Go duration with days (d) and weeks (w) allowed in
// front: "2d", "1w", "1d12h".
func sinceDuration(s string) (time.Duration, bool) {
	if d, err := time.ParseDuration(s); err == nil {
		return d, true
	}
	m := sinceDays.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n > 3650 {
		return 0, false
	}
	unit := 24 * time.Hour
	if m[2] == "w" {
		unit *= 7
	}
	d := time.Duration(n) * unit
	if m[3] != "" {
		rest, err := time.ParseDuration(m[3])
		if err != nil || rest < 0 {
			return 0, false
		}
		d += rest
	}
	return d, true
}

// apiSince is the since= the api reads: RFC 3339 in UTC, "" for none.
func apiSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func sleepOrDone(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// eventsPage is how many events each page back asks for (the api's
// most, I-414).
const eventsPage = 200

// EventsCmd implements `repose events [PROJECT] [--since 24h] [--follow]`
// (07-cli.md's I-8 addition). Events print oldest first. The api answers
// newest first and at most 50 at a time, so a --since that reaches
// further pages back with before (I-414); --follow then asks for what
// came after the newest event printed. With no PROJECT outside a
// checkout it covers every project, as `repose questions` does (I-609).
func EventsCmd(ctx context.Context, e *Env, projectArg, since string, follow bool, poll func()) error {
	from, err := parseSince(since, time.Now())
	if err != nil {
		return cobraUsageError{err}
	}
	projects, all, err := eventsProjects(ctx, e, projectArg)
	if err != nil {
		return err
	}
	w := eventsWidths{all: all}
	for _, p := range projects {
		w.project = max(w.project, len(p.Slug))
	}
	cur := map[string]string{}
	for _, p := range projects {
		cur[p.ID] = apiSince(from)
	}
	first := true
	printed := 0
	for {
		var batch []Event
		for _, p := range projects {
			events, err := e.Client.ListEvents(ctx, p.ID, cur[p.ID])
			if err != nil {
				return err
			}
			if first && cur[p.ID] != "" {
				if events, err = eventsBackTo(ctx, e, p.ID, cur[p.ID], events); err != nil {
					return err
				}
			}
			var newest time.Time
			for i := range events {
				events[i].Project = p.Slug
				if events[i].TS.After(newest) {
					newest = events[i].TS
				}
			}
			if !newest.IsZero() {
				cur[p.ID] = newest.Format(time.RFC3339Nano)
			}
			batch = append(batch, events...)
		}
		first = false
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].TS.Before(batch[j].TS) })
		printed += len(batch)
		for _, ev := range batch {
			if e.JSON {
				ev.TS = ev.TS.UTC()
				if err := writeJSONLine(e.Out, ev); err != nil {
					return err
				}
				continue
			}
			_, _ = fmt.Fprintln(e.Out, eventLine(ev, w))
		}
		if !follow {
			if printed == 0 && !e.JSON {
				_, _ = fmt.Fprintln(e.ErrOut, emptyEventsLine(projects, all, since))
			}
			return nil
		}
		if poll != nil {
			poll()
		} else if err := sleepOrDone(ctx, 10*time.Second); err != nil {
			return err
		}
	}
}

// eventsProjects is the projects `repose events` reads: the one named or
// this checkout's, or, with neither (outside a checkout, or in the home
// folder), every project. A checkout with a remote and no project is
// still an error, as for every command.
func eventsProjects(ctx context.Context, e *Env, projectArg string) ([]Project, bool, error) {
	arg := e.resolveArg(projectArg)
	res, err := resolveProject(ctx, e.Client, e.Dir, e.Cwd, arg, &e.Cache, defaultResolveDeps())
	if err != nil {
		return nil, false, err
	}
	if res.Project != nil {
		return []Project{*res.Project}, false, nil
	}
	if arg != "" || res.Remote != "" {
		return nil, false, errNoProject(res, e.Command)
	}
	ps, err := e.Client.ListProjects(ctx)
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Slug < ps[j].Slug })
	return ps, true, nil
}

// emptyEventsLine says which projects had no events, and since when, so
// an empty answer does not read as a command that did nothing.
func emptyEventsLine(projects []Project, all bool, since string) string {
	window := "since " + strings.TrimSpace(since)
	if _, ok := sinceDuration(strings.TrimSpace(since)); ok {
		window = "in the last " + strings.TrimSpace(since)
	}
	if strings.TrimSpace(since) == "" {
		window = "kept"
	}
	switch {
	case all && len(projects) == 0:
		return "You have no projects."
	case all:
		return fmt.Sprintf("No events on any of your projects %s.", window)
	default:
		return fmt.Sprintf("No events on %s %s.", projects[0].Slug, window)
	}
}

// eventsWidths are the padded columns of `repose events`: the project
// column appears only when it covers every project.
type eventsWidths struct {
	all     bool
	project int
}

// eventVerbs are what an event's kind prints as, the words notify.Title
// uses for the same kinds; --json keeps the kind. A kind not here prints
// with its separators as spaces.
var eventVerbs = map[string]string{
	"completed":            "finished",
	"needs_input":          "needs input",
	"error":                "hit an error",
	"agent_message":        "says",
	"agent_question":       "asks",
	"idle_running":         "idle",
	"temp_expiring":        "expiring",
	"temp_destroyed":       "destroyed",
	"personal_failed":      "machine.nix failed",
	"boot_failed":          "boot failed",
	"guest_state_changed":  "machine",
	"notifications_paused": "notifications paused",
}

func eventVerb(kind string) string {
	if v, ok := eventVerbs[kind]; ok {
		return v
	}
	return strings.NewReplacer("_", " ", ".", " ").Replace(kind)
}

// eventLine is one row: local time, the project (when the list covers
// every project), the agent and its window, what happened, the summary.
func eventLine(ev Event, w eventsWidths) string {
	agent := ev.Agent
	if agent == "" {
		agent = "-"
	}
	if ev.Window != "" {
		agent += " (" + ev.Window + ")"
	}
	var b strings.Builder
	b.WriteString(streamTime(ev.TS))
	if w.all {
		fmt.Fprintf(&b, "  %-*s", w.project, ev.Project)
	}
	fmt.Fprintf(&b, "  %-10s  %-12s  %s", agent, eventVerb(ev.Kind), flatSummary(ev.Summary))
	return strings.TrimRight(b.String(), " ")
}

// flatSummary keeps a summary on its row: an agent's message can hold
// newlines.
func flatSummary(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// eventsBackTo pages back from the oldest of got until the events reach
// since, and returns them all. A page with nothing new ends it, which is
// what an api older than I-414 answers.
func eventsBackTo(ctx context.Context, e *Env, projectID, since string, got []Event) ([]Event, error) {
	limit, err := time.Parse(time.RFC3339Nano, since)
	if err != nil {
		return got, nil
	}
	seen := map[string]bool{}
	for _, ev := range got {
		seen[ev.ID] = true
	}
	for len(got) > 0 {
		oldest := got[0]
		for _, ev := range got {
			if ev.TS.Before(oldest.TS) {
				oldest = ev
			}
		}
		if !oldest.TS.After(limit) {
			break
		}
		page, err := e.Client.ListEventsBefore(ctx, projectID, oldest.ID, eventsPage)
		if err != nil {
			return nil, err
		}
		added := false
		for _, ev := range page {
			if seen[ev.ID] || !ev.TS.After(limit) {
				continue
			}
			seen[ev.ID], added = true, true
			got = append(got, ev)
		}
		if !added {
			break
		}
	}
	return got, nil
}
