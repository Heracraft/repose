package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
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
	bill := make(chan *Billing, 1)
	go func() { b, _ := e.Client.GetBilling(ctx); bill <- b }()
	route, _ := e.Client.ProjectRoute(ctx, project.ID)
	snaps, _ := e.Client.ListSnapshots(ctx, project.ID)
	events, _ := e.Client.ListEvents(ctx, project.ID, "")
	g := <-guest
	mux := g.mux
	if mux == "" {
		mux = multiplexer.Normalize(project.Multiplexer)
	}
	writeStatusLinesMux(e.Out, project, route, snaps, events, mux, g.disk)
	if l := diskOverPlanLine(<-bill); l != "" {
		_, _ = fmt.Fprintf(e.Out, "  %s\n", l)
	}
	writeListening(e.Out, g.procs)
	return nil
}

// ProjectsCmd implements `repose ls`: a table of every project,
// ignoring cwd, with a header row and, under a project in `error`, why
// (DECISIONS I-153). --json is the api's list, unchanged.
func ProjectsCmd(ctx context.Context, e *Env) error {
	// The plan's disk is read beside the list, for the table only.
	bill := make(chan *Billing, 1)
	if !e.JSON && !e.Quiet {
		go func() { b, _ := e.Client.GetBilling(ctx); bill <- b }()
	}
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
	if l := diskOverPlanLine(<-bill); l != "" {
		_, _ = fmt.Fprintln(e.Out, l)
	}
	return nil
}

// diskOverPlanLine is the line `repose ls` and `repose status` print
// while the projects hold more than the plan's disk (DECISIONS I-585):
// creating, restoring, forking and growing a disk are refused until they
// hold less, which the user would otherwise learn only from a refusal.
// Empty within the plan, with no plan, or from an api that does not say.
func diskOverPlanLine(b *Billing) string {
	if b == nil || b.Subscription == nil || b.Usage.DiskHeldGB == nil || b.Usage.DiskGB <= 0 {
		return ""
	}
	held := *b.Usage.DiskHeldGB
	if held <= float64(b.Usage.DiskGB) {
		return ""
	}
	return fmt.Sprintf("disk: your projects hold %s GB of the plan's %d GB; creating, restoring, forking and growing a disk are refused until they hold less",
		strconv.FormatFloat(held, 'f', -1, 64), b.Usage.DiskGB)
}

func writeProjectsTable(w io.Writer, projects []Project) {
	now := time.Now()
	// LEFT, a temporary machine's time left (I-347), is a column only
	// while one of them is listed (I-484).
	// DISK, a nearly full disk (I-567), likewise.
	left, disk := false, false
	for i := range projects {
		left = left || projects[i].ExpiresAt != nil
		disk = disk || diskFullCell(&projects[i]) != ""
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	head := "PROJECT\tCLASS\tSTATE\tUP\tAGENTS\tTODAY\tMONTH"
	if left {
		head += "\tLEFT"
	}
	if disk {
		head += "\tDISK"
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
		if disk {
			row += "\t" + orDash(diskFullCell(p))
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
	if gd.Size <= 0 {
		gd = apiDisk(p)
	}
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
// its size, from the guest's own answer or else the api's newest sample;
// with neither, the volume's size alone. The api's disk_used_bytes is not
// shown: it counts the volume's allocated blocks, which a deleted file
// keeps until the guest's daily fstrim (I-567, I-585).
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

// apiDisk is the guest's root filesystem as the api's newest sample has
// it (I-567): what status shows when the guest did not answer over SSH,
// and what `repose ls` reads. Zero Size when the api did not say.
func apiDisk(p *Project) guestDisk {
	if p.RootSizeBytes <= 0 || p.RootUsedBytes < 0 || p.RootUsedBytes > p.RootSizeBytes {
		return guestDisk{}
	}
	return guestDisk{Used: p.RootUsedBytes, Size: p.RootSizeBytes}
}

// diskFullCell is `repose ls`'s DISK column: "93% full" at DiskFullPercent
// or more, else empty. The column is there only while a listed project
// has one, as LEFT is (I-567, I-484).
func diskFullCell(p *Project) string {
	if d := apiDisk(p); d.Size > 0 && d.Percent() >= DiskFullPercent {
		return fmt.Sprintf("%d%% full", d.Percent())
	}
	return ""
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
// (I-567). guestd's `unknown` (an agent quiet for less than the idle
// time, or a window that just closed) tells the reader nothing, so it is
// not named: one such agent reads `claude`, and several are counted
// without it.
func agentState(p *Project) string {
	if p.Signals == nil || p.State != "running" || len(p.Signals.Agents) == 0 {
		return ""
	}
	agents := p.Signals.Agents
	if len(agents) == 1 {
		if agents[0].State == "unknown" || agents[0].State == "" {
			return agents[0].Agent
		}
		return fmt.Sprintf("%s: %s", agents[0].Agent, agents[0].State)
	}
	counts := map[string]int{}
	var order []string
	for _, st := range []string{"needs_input", "working", "idle"} {
		order = append(order, st)
		counts[st] = 0
	}
	for _, a := range agents {
		if a.State == "unknown" || a.State == "" {
			continue
		}
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
	if len(parts) == 0 {
		return fmt.Sprintf("%d agents", len(agents))
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
