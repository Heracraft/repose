package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
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
	// The guest's answer (listeners, its disk, and what runs its
	// terminals, I-509, I-567) is asked beside the api's reads.
	guest := make(chan guestStatus, 1)
	if project.State == "running" {
		go func() { guest <- guestStatusRead(ctx, e.target(project.Slug)) }()
	} else {
		guest <- guestStatus{}
	}
	route, _ := e.Client.ProjectRoute(ctx, project.ID)
	snaps, _ := e.Client.ListSnapshots(ctx, project.ID)
	events, _ := e.Client.ListEvents(ctx, project.ID, "")
	g := <-guest
	mux := g.mux
	if mux == "" {
		mux = multiplexer.Normalize(project.Multiplexer)
	}
	writeStatusLinesMux(e.Out, project, route, snaps, events, mux, g.disk)
	writeListening(e.Out, g.procs)
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
	writeStatusLinesMux(w, p, route, snaps, events, multiplexer.Normalize(p.Multiplexer), guestDisk{})
}

// writeStatusLinesMux is writeStatusLines with what the guest said: the
// multiplexer that runs now, so herdr is named after the size and its
// sessions line has no tmux clients, since herdr's arrive over SSH
// (I-509); and its root filesystem, which the disk figure is (I-567).
func writeStatusLinesMux(w io.Writer, p *Project, route *Route, snaps []Snapshot, events []Event, mux string, gd guestDisk) {
	_, _ = fmt.Fprintln(w, statusFirstLineMux(p, mux))
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
		host := route.HostName
		if host == "" {
			host = route.HostID // an api without host_name
		}
		_, _ = fmt.Fprintf(w, "  host %s   ip %s   disk %s   snapshot %s\n", orDash(host), orDash(route.GuestIP), statusDisk(p, gd), snapshotAge(snaps))
	}
	if l := diskFullLine(p, gd); l != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	if p.Signals != nil && p.State == "running" {
		docker := p.Signals.Docker
		if p.Signals.DockerContainers > docker {
			docker = p.Signals.DockerContainers
		}
		if mux == multiplexer.Herdr {
			_, _ = fmt.Fprintf(w, "  sessions %d   docker %d\n", p.Signals.SSHSessions, docker)
		} else {
			_, _ = fmt.Fprintf(w, "  sessions %d   tmux clients %d   docker %d\n", p.Signals.SSHSessions, p.Signals.TmuxClients, docker)
		}
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

// statusDisk is the disk figure: the guest's root filesystem, used over
// its size, when the guest answered; else the volume's size alone. The
// api's disk_used_bytes is not shown: it counts the volume's allocated
// blocks, which a deleted file keeps until the weekly fstrim (I-567).
func statusDisk(p *Project, gd guestDisk) string {
	if gd.Size > 0 {
		return fmt.Sprintf("%s/%s", humanBytes(gd.Used), humanBytes(gd.Size))
	}
	if p.VolumeBytes > 0 {
		return humanBytes(p.VolumeBytes)
	}
	return "-"
}

// DiskFullPercent is where status calls the disk nearly full: guestd's
// disk_high threshold (I-11), the point past which a build or an agent's
// next write can fail.
const DiskFullPercent = 90

// maxDiskGB is the largest disk a resize gives (the dashboard's largest
// size, machine.md "Memory and disk").
const maxDiskGB = 320

// diskFullLine is the line under the host line when the guest's disk is
// nearly full, with the resize that grows it: a full disk fails writes,
// which is a loss the user cannot undo (I-567, I-484).
func diskFullLine(p *Project, gd guestDisk) string {
	pct := gd.Percent()
	if gd.Size <= 0 || pct < DiskFullPercent {
		return ""
	}
	line := fmt.Sprintf("disk %d percent full", pct)
	gb := p.VolumeBytes >> 30
	if gb <= 0 || gb >= maxDiskGB {
		return line
	}
	return fmt.Sprintf("%s; `repose resize %s %dG` grows it", line, p.Slug, min(2*gb, maxDiskGB))
}

func statusFirstLine(p *Project) string {
	return statusFirstLineMux(p, multiplexer.Normalize(p.Multiplexer))
}

// statusFirstLineMux names herdr after the size; tmux is not named.
func statusFirstLineMux(p *Project, mux string) string {
	class := fmt.Sprintf("%-6s", p.Class)
	if mux == multiplexer.Herdr {
		class += " herdr "
	}
	return fmt.Sprintf("%-10s %s %-9s %-7s %-20s today %s  month %s",
		p.Slug, class, p.State, uptime(p), agentState(p), runHours(p.RunningSecondsToday), runHours(p.RunningSecondsMonth))
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

// agentState is the AGENTS column: the one agent with its state, or, for
// several, how many are in each state, needs_input first. It showed the
// first agent alone, so a machine with five read `claude: working`
// (I-567).
func agentState(p *Project) string {
	if p.Signals == nil || p.State != "running" || len(p.Signals.Agents) == 0 {
		return ""
	}
	agents := p.Signals.Agents
	if len(agents) == 1 {
		return fmt.Sprintf("%s: %s", agents[0].Agent, agents[0].State)
	}
	counts := map[string]int{}
	var order []string
	for _, st := range []string{"needs_input", "working", "idle"} {
		order = append(order, st)
		counts[st] = 0
	}
	for _, a := range agents {
		if _, ok := counts[a.State]; !ok {
			order = append(order, a.State)
		}
		counts[a.State]++
	}
	var parts []string
	for _, st := range order {
		if counts[st] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[st], st))
		}
	}
	return fmt.Sprintf("%d agents: %s", len(agents), strings.Join(parts, ", "))
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
