package cli

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// LogsCmd implements `repose logs [--kind console|build|ops] [--since 1h]
// [--follow]`. follow polls every 2s (07-cli.md §5.10); poll is injected
// so tests do not sleep in a loop.
func LogsCmd(ctx context.Context, e *Env, projectArg, kind, since string, follow bool, poll func()) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	cur := sinceArg(since, time.Now())
	var last time.Time
	printed := 0
	for {
		lines, err := e.Client.ProjectLogs(ctx, project.ID, kind, cur)
		if err != nil {
			return err
		}
		printed += len(lines)
		for _, l := range lines {
			// A line at or before the cursor was printed by the previous
			// poll (the api's since is inclusive for ops).
			if !last.IsZero() && !l.TS.IsZero() && !l.TS.After(last) {
				continue
			}
			if e.JSON {
				if err := writeJSONOut(e.Out, l); err != nil {
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
// without the time rather than as 0001-01-01.
func logLineText(l LogLine, kind string) string {
	k := l.Kind
	if k == "" {
		k = kind
	}
	if l.TS.IsZero() {
		return k + " " + l.Line
	}
	return l.TS.Local().Format(time.RFC3339) + " " + k + " " + l.Line
}

// sinceArg turns --since into what the api reads: a duration such as
// "1h" or "90m" becomes the time that long before now; anything else
// (an RFC 3339 time, or empty) goes as it is. The api reads only times,
// so a duration used to be ignored.
func sinceArg(since string, now time.Time) string {
	if d, err := time.ParseDuration(since); err == nil && d > 0 {
		return now.Add(-d).UTC().Format(time.RFC3339)
	}
	return since
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

// EventsCmd implements `repose events [--since 24h] [--follow]`
// (07-cli.md's I-8 addition). Events print oldest first. The api answers
// newest first and at most 50 at a time, so a --since that reaches
// further pages back with before (I-414); --follow then asks for what
// came after the newest event printed.
func EventsCmd(ctx context.Context, e *Env, projectArg, since string, follow bool, poll func()) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	cur := sinceArg(since, time.Now())
	first := true
	for {
		events, err := e.Client.ListEvents(ctx, project.ID, cur)
		if err != nil {
			return err
		}
		if first && cur != "" {
			if events, err = eventsBackTo(ctx, e, project.ID, cur, events); err != nil {
				return err
			}
		}
		first = false
		sort.SliceStable(events, func(i, j int) bool { return events[i].TS.Before(events[j].TS) })
		for _, ev := range events {
			if e.JSON {
				if err := writeJSONOut(e.Out, ev); err != nil {
					return err
				}
				continue
			}
			_, _ = fmt.Fprintf(e.Out, "%s\t%s\t%s\t%s\n", ev.TS.Format(time.RFC3339), ev.Agent, ev.Kind, ev.Summary)
		}
		if !follow {
			return nil
		}
		if len(events) > 0 {
			cur = events[len(events)-1].TS.Format(time.RFC3339Nano)
		}
		if poll != nil {
			poll()
		} else if err := sleepOrDone(ctx, 10*time.Second); err != nil {
			return err
		}
	}
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
