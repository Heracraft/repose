package cli

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

// StatusCmd implements `repose status [PROJECT] [--json]` (07-cli.md §5.7).
func StatusCmd(ctx context.Context, e *Env, projectArg string) error {
	project, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, project)
	}
	route, _ := e.Client.ProjectRoute(ctx, project.ID)
	snaps, _ := e.Client.ListSnapshots(ctx, project.ID)
	events, _ := e.Client.ListEvents(ctx, project.ID, "")
	writeStatusLines(e.Out, project, route, snaps, events)
	if project.State == "running" {
		writeListening(e.Out, guestListening(ctx, e.target(project.Slug)))
	}
	return nil
}

// ProjectsCmd implements `repose ls`: a table of every project,
// ignoring cwd, with a header row and, under a project in `error`, why
// (DECISIONS I-153). --json is the api's list, unchanged.
func ProjectsCmd(ctx context.Context, e *Env) error {
	projects, err := e.Client.ListProjects(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		if projects == nil {
			projects = []Project{}
		}
		return writeJSONOut(e.Out, projects)
	}
	if e.Quiet {
		for _, p := range projects {
			_, _ = fmt.Fprintln(e.Out, p.Slug)
		}
		return nil
	}
	if len(projects) == 0 {
		_, _ = fmt.Fprintln(e.Out, "No projects yet.")
		return nil
	}
	writeProjectsTable(e.Out, projects)
	return nil
}

func writeProjectsTable(w io.Writer, projects []Project) {
	now := time.Now()
	// LEFT, a temporary machine's time left (I-347), is a column only
	// while one of them is listed (I-484).
	left := false
	for i := range projects {
		left = left || projects[i].ExpiresAt != nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "PROJECT\tCLASS\tSTATE\tUP\tAGENTS\tTODAY\tMONTH"
	if left {
		head += "\tLEFT"
	}
	_, _ = fmt.Fprintln(tw, head)
	for i := range projects {
		p := &projects[i]
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s\t%s\t%s",
			p.Slug, p.Class, p.State, orDash(uptime(p)), orDash(agentState(p)),
			runHours(p.RunningSecondsToday), runHours(p.RunningSecondsMonth))
		if left {
			row += "\t" + orDash(tempLeft(p, now))
		}
		_, _ = fmt.Fprintln(tw, row)
	}
	_ = tw.Flush()
	for i := range projects {
		p := &projects[i]
		if r := abuseStopReason(p); r != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", p.Slug, r)
		}
		if l := idleLine(p, now); l != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", p.Slug, l)
		}
		if p.State == "error" {
			reason := projectReason(p)
			if reason == "" {
				reason = "its last operation failed"
			}
			_, _ = fmt.Fprintf(w, "%s: %s\n", p.Slug, withNext(reason, fmt.Sprintf("`repose start %s` restarts it.", p.Slug)))
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func writeStatusLines(w io.Writer, p *Project, route *Route, snaps []Snapshot, events []Event) {
	_, _ = fmt.Fprintln(w, statusFirstLine(p))
	if r := abuseStopReason(p); r != "" {
		// DECISIONS I-239: the platform stopped it, and says why.
		_, _ = fmt.Fprintf(w, "  %s\n", r)
	}
	if l := idleLine(p, time.Now()); l != "" {
		// DECISIONS I-262: nobody on it for a day, and still billing.
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	if t := tempWhen(p, time.Now()); t != "" {
		// DECISIONS I-347: a temporary machine, and how long it has.
		_, _ = fmt.Fprintf(w, "  temporary: %s\n", t)
	}
	if p.State == "error" {
		reason := projectReason(p)
		if reason == "" {
			reason = "its last operation failed"
		}
		_, _ = fmt.Fprintf(w, "  error: %s\n", withNext(reason, fmt.Sprintf("`repose start %s` restarts it.", p.Slug)))
	}
	if route != nil {
		disk := "-"
		if p.VolumeBytes > 0 {
			disk = fmt.Sprintf("%s/%s", humanBytes(p.DiskUsedBytes), humanBytes(p.VolumeBytes))
		}
		host := route.HostName
		if host == "" {
			host = route.HostID // an api without host_name
		}
		_, _ = fmt.Fprintf(w, "  host %s   ip %s   disk %s   snapshot %s\n", orDash(host), orDash(route.GuestIP), disk, snapshotAge(snaps))
	}
	if p.Signals != nil && p.State == "running" {
		docker := p.Signals.Docker
		if p.Signals.DockerContainers > docker {
			docker = p.Signals.DockerContainers
		}
		_, _ = fmt.Fprintf(w, "  sessions %d   tmux clients %d   docker %d\n", p.Signals.SSHSessions, p.Signals.TmuxClients, docker)
		if guestdDead(p) {
			_, _ = fmt.Fprintf(w, "  the environment's agent (guestd) is not answering; `repose start %s` restarts it\n", p.Slug)
		}
	}
	if last := newestEvent(events); last != nil {
		agent := last.Agent
		if agent != "" {
			agent += " "
		}
		_, _ = fmt.Fprintf(w, "  last event %s: %s%s %q\n", humanAge(last.TS), agent, last.Kind, last.Summary)
	}
}

func statusFirstLine(p *Project) string {
	return fmt.Sprintf("%-10s %-6s %-9s %-7s %-20s today %s  month %s",
		p.Slug, p.Class, p.State, uptime(p), agentState(p), runHours(p.RunningSecondsToday), runHours(p.RunningSecondsMonth))
}

// runHours renders running seconds as `2h14m` under a day and whole hours
// from there (`41h`): what `repose status` and `repose ls` show since
// I-289, in place of a price.
func runHours(secs int64) string {
	if secs <= 0 {
		return "0h"
	}
	h := secs / 3600
	m := (secs % 3600) / 60
	if h >= 24 || m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}

// uptime is only meaningful while running: v0.1.4 printed "47h30m" for a
// project that had been in `error` for two days.
func uptime(p *Project) string {
	if p.State != "running" || p.StartedAt == nil {
		return ""
	}
	return humanDuration(time.Since(*p.StartedAt))
}

func agentState(p *Project) string {
	if p.Signals == nil || p.State != "running" {
		return ""
	}
	for _, a := range p.Signals.Agents {
		return fmt.Sprintf("%s: %s", a.Agent, a.State)
	}
	return ""
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func humanAge(t time.Time) string {
	d := time.Since(t).Round(time.Minute)
	if d < time.Minute {
		return "just now"
	}
	return humanDuration(d) + " ago"
}

// snapshotAge and newestEvent pick by time, not position: the api lists
// newest first and the fake oldest first, and status took the last
// element, which showed a running project's first event
// (guest_state_changed "creating") as its last (I-192).
func snapshotAge(snaps []Snapshot) string {
	s := newestSnapshot(snaps)
	if s == nil {
		return "none"
	}
	return humanAge(s.CreatedAt)
}

func newestEvent(events []Event) *Event {
	var best *Event
	for i := range events {
		if best == nil || events[i].TS.After(best.TS) {
			best = &events[i]
		}
	}
	return best
}
