package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	return statusFor(ctx, e, project)
}

// statusJSON is `repose status --json`: the api's Project, unchanged,
// plus what the machine said about its checkout (I-616). Git is absent
// for a machine that is not running or did not answer.
type statusJSON struct {
	*Project
	Git []gitRow `json:"git,omitempty"`
	// GitError is why git is absent from a running machine (I-634):
	// ssh_timeout or ssh_failed. Absent when the machine answered.
	GitError string `json:"git_error,omitempty"`
	// GitAt is set on a stopped machine's rows: when this laptop read
	// them, before its stop (I-634).
	GitAt *time.Time `json:"git_at,omitempty"`
}

// statusFor prints project's status, read now.
func statusFor(ctx context.Context, e *Env, project *Project) error {
	// The machine's answer (listeners, its disk, what runs its
	// terminals and its checkout's git state, I-509, I-567, I-616) is
	// asked beside the api's reads.
	guest := make(chan guestStatus, 1)
	if project.State == "running" {
		have := laptopCommits(e, project)
		go func() { guest <- guestStatusRead(ctx, e.target(project.Slug), project.Slug, have) }()
	} else {
		guest <- guestStatus{}
	}
	left, hasLeft := stoppedRows(e.Dir, project)
	if e.JSON {
		g := <-guest
		doc := statusJSON{Project: project, Git: g.git, GitError: g.probeErr}
		if hasLeft {
			doc.Git, doc.GitAt = left.Rows, &left.At
		}
		return e.printJSON(doc)
	}
	bill := make(chan *Billing, 1)
	go func() { b, _ := e.Client.GetBilling(ctx); bill <- b }()
	var route *Route
	if e.Verbose {
		route, _ = e.Client.ProjectRoute(ctx, project.ID)
	}
	snaps, _ := e.Client.ListSnapshots(ctx, project.ID)
	events, _ := e.Client.ListEvents(ctx, project.ID, "")
	noteWaits(project, events)
	g := <-guest
	mux := g.mux
	if mux == "" {
		mux = multiplexer.Normalize(project.Multiplexer)
	}
	writeStatus(e.Out, statusView{
		p: project, route: route, snaps: snaps, events: events, mux: mux,
		disk: g.disk, procs: g.procs, git: g.git, probeErr: g.probeErr, probeDetail: g.probeDetail, bill: <-bill, now: time.Now(),
		left: left, hasLeft: hasLeft, defaultAgent: e.Cfg.DefaultAgent,
	})
	return nil
}

// ProjectsCmd implements `repose ls`: a table of every project,
// ignoring cwd, with a header row and, under a project in `error`, why
// (DECISIONS I-153). --json is the api's list, unchanged.
func ProjectsCmd(ctx context.Context, e *Env) error {
	// The plan is read beside the list, for the table only.
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
		// Whose list was empty: an empty list on the wrong account or
		// server read as a lost project (review 8.5, I-633).
		whose := ""
		if me, err := e.Client.GetMe(ctx); err == nil && me.Handle != "" {
			whose = " for " + me.Handle
		}
		if e.Cfg.APIURL != "" && e.Cfg.APIURL != defaultAPIURL {
			whose += " on " + hostOf(e.Cfg.APIURL)
		}
		// A destroyed project can still come back; "yet" said otherwise
		// (DECISIONS I-615).
		if gone, err := e.Client.ListDestroyed(ctx); err == nil && len(gone) > 0 {
			_, _ = fmt.Fprintf(e.Out, "No projects%s. %s destroyed in the last 30 days can be restored.\n", whose, countDestroyed(gone))
			return nil
		}
		_, _ = fmt.Fprintf(e.Out, "No projects yet%s.\n", whose)
		return nil
	}
	noteWaitsOf(ctx, e.Client, projects)
	writeProjectsTableHere(e.Out, projects, e.hereProjectID(projects))
	b := <-bill
	pl := planLine(b)
	if pl != "" {
		_, _ = fmt.Fprintln(e.Out, pl)
	}
	// Under the plan line, which has the figures, a warning says only
	// what happens (I-631).
	for _, l := range planWarningsAfter(b, pl != "") {
		_, _ = fmt.Fprintln(e.Out, l)
	}
	return nil
}

// hereProjectID is the project a command run here with no PROJECT acts
// on, from what `repose ls` already has (no api call): REPOSE_PROJECT,
// then this folder's link, then its git remote, by resolveProject's
// order (I-616). "" for none, and in the home folder (I-601).
func (e *Env) hereProjectID(projects []Project) string {
	byID := map[string]*Project{}
	for i := range projects {
		byID[projects[i].ID] = &projects[i]
	}
	if x := e.resolveArg(""); x != "" {
		x, _, _ = strings.Cut(x, ":")
		for i := range projects {
			if projects[i].ID == x || projects[i].Slug == x || projects[i].Name == x {
				return projects[i].ID
			}
		}
		return ""
	}
	if e.Cwd == "" {
		return ""
	}
	deps := defaultResolveDeps()
	if inHomeFolder(e.Cwd, deps) {
		return ""
	}
	key := dirKey(e.Cwd, deps)
	for _, k := range uniqueStrings(key, e.Cwd) {
		if co, ok := e.Cache.Checkouts[k]; ok && byID[co.ProjectID] != nil {
			return co.ProjectID
		}
	}
	remote := deps.RemoteFor(e.Cwd)
	for _, k := range uniqueStrings(key, e.Cwd) {
		if id, ok := e.Cache.ByDir[k]; ok && byID[id] != nil && byID[id].RemoteURL == remote {
			return id
		}
	}
	if remote == "" {
		return ""
	}
	if c, ok := e.Cache.ByRemote[remote]; ok && byID[c.ProjectID] != nil && byID[c.ProjectID].RemoteURL == remote {
		return c.ProjectID
	}
	for i := range projects {
		if projects[i].RemoteURL == remote {
			return projects[i].ID
		}
	}
	return ""
}

// planLine is the line under `repose ls` with what the plan buys and
// how much of it is in use (I-616): the memory that refuses a start,
// the disk that refuses a create, the egress that adds a charge. Empty
// with no plan, or from an api that does not say.
func planLine(b *Billing) string {
	if b == nil || b.Subscription == nil || b.Usage.MemoryGB <= 0 {
		return ""
	}
	line := fmt.Sprintf("%s: %d of %d GB running", billingPlanName(b), b.Usage.RunningGB, b.Usage.MemoryGB)
	if b.Usage.DiskHeldGB != nil && b.Usage.DiskGB > 0 {
		line += fmt.Sprintf(", %s of %d GB disk", gbFigure(*b.Usage.DiskHeldGB), b.Usage.DiskGB)
	}
	if b.Usage.EgressIncludedGB > 0 {
		line += fmt.Sprintf(", %s of %d GB egress this month", gbFigure(b.Usage.EgressGB), b.Usage.EgressIncludedGB)
	}
	return line
}

// billingPlanName is the plan's name as the api lists it ("Solo"), else its id
// with a capital.
func billingPlanName(b *Billing) string {
	id := b.Subscription.Plan
	for _, p := range b.Plans {
		if p.ID == id && p.Name != "" {
			return p.Name
		}
	}
	if id == "" {
		return "Plan"
	}
	return strings.ToUpper(id[:1]) + id[1:]
}

// gbFigure is a GB figure with at most one decimal, none when whole.
func gbFigure(f float64) string {
	return strconv.FormatFloat(math.Round(f*10)/10, 'f', -1, 64)
}

// planWarnings are the lines `repose ls` and `repose status` print near
// a limit that refuses or stops something or adds a charge (I-585,
// I-616): the disk past the plan, egress past the allowance, a failed
// payment. Each names what happens.
func planWarnings(b *Billing) []string { return planWarningsAfter(b, false) }

// planWarningsAfter is planWarnings, without the figures the plan line
// above already printed when figures is true.
func planWarningsAfter(b *Billing, figures bool) []string {
	var out []string
	if l := diskOverPlanLine(b); l != "" {
		if figures {
			l = "disk past the plan: creating, restoring, forking and growing a disk are refused until your projects hold less"
		}
		out = append(out, l)
	}
	if b == nil || b.Subscription == nil {
		return out
	}
	if inc := b.Usage.EgressIncludedGB; inc > 0 && b.Usage.EgressGB > float64(inc) {
		if figures {
			out = append(out, fmt.Sprintf("egress past the plan: each GB past %d GB adds $0.05, and your machines stop at %d GB", inc, 4*inc))
		} else {
			out = append(out, fmt.Sprintf("egress: %s GB this month, past the plan's %d GB: each GB past it adds $0.05, and your machines stop at %d GB",
				gbFigure(b.Usage.EgressGB), inc, 4*inc))
		}
	}
	if b.Subscription.Status == "past_due" {
		out = append(out, "payment failed: starting a machine is refused, and running machines stop on the third day; update your card at "+billingURL)
	}
	return out
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
	writeProjectsTableHere(w, projects, "")
}

// writeProjectsTableHere is the table with the row of here, the project
// a command run in this folder acts on, marked `*` after its name, as
// `docker context ls` marks the current context (I-616).
func writeProjectsTableHere(w io.Writer, projects []Project, here string) {
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
	head := "PROJECT\tSIZE\tSTATE\tUP\tAGENTS"
	if left {
		head += "\tLEFT"
	}
	if disk {
		head += "\tDISK"
	}
	_, _ = fmt.Fprintln(tw, head)
	for i := range projects {
		p := &projects[i]
		name := p.Slug
		if here != "" && p.ID == here {
			name += " *"
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s",
			name, p.Class, p.State, orDash(uptime(p)), orDash(agentState(p)))
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
		if r := bootFallbackReason(p); r != "" {
			_, _ = fmt.Fprintf(w, "%s: %s\n", p.Slug, withNext(r, ""))
		}
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// statusView is what `repose status` prints, read.
type statusView struct {
	p      *Project
	route  *Route // only under -v: the host and the machine's address
	snaps  []Snapshot
	events []Event
	mux    string
	disk   guestDisk
	procs  []listeningProc
	git    []gitRow
	// probeErr and probeDetail say why git is empty on a running
	// machine (I-634).
	probeErr, probeDetail string
	// left is a stopped machine's rows from this laptop's stop of it.
	left    stopLeft
	hasLeft bool
	// defaultAgent is config.toml's default_agent, which the setup line
	// compares the project's agent with.
	defaultAgent string
	bill         *Billing
	now          time.Time
}

func writeStatusLines(w io.Writer, p *Project, route *Route, snaps []Snapshot, events []Event) {
	writeStatusLinesMux(w, p, route, snaps, events, multiplexer.Normalize(p.Multiplexer), guestDisk{})
}

// writeStatusLinesMux is writeStatusLines with what the guest said: the
// multiplexer that runs now, so herdr is named after the size and the
// attached row has no tmux clients, since herdr's arrive over SSH
// (I-509); and its root filesystem, which the disk figure is (I-567).
func writeStatusLinesMux(w io.Writer, p *Project, route *Route, snaps []Snapshot, events []Event, mux string, gd guestDisk) {
	writeStatus(w, statusView{p: p, route: route, snaps: snaps, events: events, mux: mux, disk: gd, now: time.Now()})
}

// statusRow prints one labelled row of `repose status`, in the
// `fly status` style (I-616): the label in a column, then the value.
func statusRow(w io.Writer, label, value string) {
	_, _ = fmt.Fprintf(w, "  %-10s %s\n", label, value)
}

// statusIndent lines a continuation up under a row's value.
const statusIndent = "             "

// writeStatus prints `repose status`: a header with the name, state,
// size and a multiplexer other than tmux; one line for each thing that
// needs the reader (a stop by the platform, an unused machine, a
// temporary one's end, an error, a disk near full, a plan limit); then
// labelled rows. The host and the machine's address are platform
// internals nobody acts on, so they print only under -v (I-616, which
// amends I-192); --json has them as host_id and guest_ip.
func writeStatus(w io.Writer, v statusView) {
	p := v.p
	gd := v.disk
	if gd.Size <= 0 {
		gd = apiDisk(p)
	}
	_, _ = fmt.Fprintln(w, statusFirstLineMux(p, v.mux))
	if r := abuseStopReason(p); r != "" {
		// DECISIONS I-239: the platform stopped it, and says why.
		_, _ = fmt.Fprintf(w, "  %s\n", r)
	}
	if l := idleLine(p, v.now); l != "" {
		// DECISIONS I-262, I-617: nobody on it for a day.
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	if t := tempWhen(p, v.now); t != "" {
		// DECISIONS I-347: a temporary machine, and how long it has.
		_, _ = fmt.Fprintf(w, "  temporary: %s\n", t)
	}
	if l := setupLine(p, v.defaultAgent); l != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	if p.State == "error" {
		reason := projectReason(p)
		if reason == "" {
			reason = "its last operation failed"
		}
		_, _ = fmt.Fprintf(w, "  error: %s\n", withNext(reason, fmt.Sprintf("`repose start %s` restarts it.", p.Slug)))
	}
	if r := bootFallbackReason(p); r != "" {
		// DECISIONS I-590: running, on its previous system.
		_, _ = fmt.Fprintf(w, "  %s\n", withNext(r, ""))
	}
	running := p.State == "running"
	if running && guestdDead(p) {
		_, _ = fmt.Fprintf(w, "  repose's service on the machine is not answering, so the agents and sessions below are old; `repose start %s` restarts it\n", p.Slug)
	}
	if l := diskFullLine(p, gd); l != "" {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	for _, l := range planWarnings(v.bill) {
		_, _ = fmt.Fprintf(w, "  %s\n", l)
	}
	if a := agentList(p); a != "" {
		statusRow(w, "agents", a)
	}
	if v.hasLeft {
		writeStoppedRows(w, v.left, v.now)
	} else {
		writeGitRowsOr(w, v.git, v.now, v.probeErr, v.probeDetail)
	}
	if running && p.Signals != nil {
		statusRow(w, "attached", attachedCell(p.Signals, v.mux))
		docker := p.Signals.Docker
		if p.Signals.DockerContainers > docker {
			docker = p.Signals.DockerContainers
		}
		if docker > 0 {
			statusRow(w, "docker", count(docker, "container"))
		}
	}
	writeListening(w, v.procs)
	statusRow(w, "disk", statusDisk(p, gd)+", "+snapshotCell(p, v.snaps))
	if last := newestEvent(v.events); last != nil {
		agent := last.Agent
		if agent != "" {
			agent += " "
		}
		verb, summary := eventCells(*last)
		statusRow(w, "last event", fmt.Sprintf("%s, %s%s %q", humanAge(last.TS), agent, verb, summary))
	}
	if v.route != nil {
		host := v.route.HostName
		if host == "" {
			host = v.route.HostID // an api without host_name
		}
		statusRow(w, "host", fmt.Sprintf("%s, ip %s", orDash(host), orDash(v.route.GuestIP)))
	}
}

// attachedCell is who is on the machine: its SSH sessions (an attach, an
// editor, a `repose exec`) and, on tmux, the clients attached to the
// session; "nobody" when there are none.
func attachedCell(s *Signals, mux string) string {
	tmux := 0
	if mux != multiplexer.Herdr {
		tmux = s.TmuxClients
	}
	if s.SSHSessions == 0 && tmux == 0 {
		return "nobody"
	}
	cell := count(s.SSHSessions, "SSH session")
	if tmux > 0 {
		cell += ", " + count(tmux, "tmux client")
	}
	return cell
}

// count is "1 commit", "3 commits".
func count(n int, noun string) string {
	return fmt.Sprintf("%d %s", n, plural(n, noun, noun+"s"))
}

// snapshotCell is the newest snapshot's age, from the list or else the
// project's last_snapshot_at. A fork has none of its own until its first
// stop or night: "no snapshot yet" says that, where "none" read as if
// the fork had come from nowhere.
func snapshotCell(p *Project, snaps []Snapshot) string {
	if s := newestSnapshot(snaps); s != nil {
		return "snapshot " + humanAge(s.CreatedAt)
	}
	if p.LastSnapshotAt != nil {
		return "snapshot " + humanAge(*p.LastSnapshotAt)
	}
	return "no snapshot yet"
}

// statusDisk is the disk figure: the guest's root filesystem, used over
// its size, from the guest's own answer or else the api's newest sample;
// with neither, the volume's size alone. The api's disk_used_bytes is not
// shown: it counts the volume's allocated blocks, which a deleted file
// keeps until the guest's daily fstrim (I-567, I-585).
//
// With the volume's size known it follows in parentheses, as `resize`
// prints it: "1.0 GB of 78.0 GB (80G disk)". The filesystem keeps part
// of the volume for itself, so `resize 80G` then a status of 78.0 GB
// read as two answers (review 5.7, I-633).
func statusDisk(p *Project, gd guestDisk) string {
	if gd.Size > 0 {
		s := fmt.Sprintf("%s of %s", humanBytes(gd.Used), humanBytes(gd.Size))
		if p.VolumeBytes > 0 {
			s += fmt.Sprintf(" (%s disk)", diskSize(p.VolumeBytes))
		}
		return s
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

// statusFirstLineMux is the header: the name, the state with its uptime,
// the size, and herdr when it runs the terminals (tmux is not named). The
// running hours it ended with since I-289 cost nothing on a plan, so they
// are gone (I-616); the agents have their own row.
func statusFirstLineMux(p *Project, mux string) string {
	state := p.State
	if up := uptime(p); up != "" {
		state += " " + up
	}
	line := fmt.Sprintf("%s  %s  %s", p.Slug, state, p.Class)
	if mux == multiplexer.Herdr {
		line += "  herdr"
	}
	return line
}

// uptime is only meaningful while running: v0.1.4 printed "47h30m" for a
// project that had been in `error` for two days.
func uptime(p *Project) string {
	if p.State != "running" || p.StartedAt == nil {
		return ""
	}
	return humanDuration(time.Since(*p.StartedAt))
}

// agentStateWords are guestd's agent states as text prints them
// (I-617); --json keeps the api's snake_case. `unknown` (an agent quiet
// for less than the idle time, or a window that just closed) tells the
// reader nothing, so it is not named.
var agentStateWords = map[string]string{"needs_input": "needs input", "working": "working", "idle": "idle"}

// agentStateOrder puts the agent that needs you first.
var agentStateOrder = []string{"needs_input", "working", "idle"}

func agentWord(state string) string {
	if w, ok := agentStateWords[state]; ok {
		return w
	}
	if state == "unknown" {
		return ""
	}
	return strings.ReplaceAll(state, "_", " ")
}

// agentState is the AGENTS column: the one agent with its state, or, for
// several, how many are in each state, needs input first. It showed the
// first agent alone, so a machine with five read `claude: working`
// (I-567).
func agentState(p *Project) string {
	if p.Signals == nil || p.State != "running" || len(p.Signals.Agents) == 0 {
		return ""
	}
	now := time.Now()
	agents := p.Signals.Agents
	if len(agents) == 1 {
		if w := agentWord(agents[0].State); w != "" {
			return fmt.Sprintf("%s: %s%s%s", agents[0].Agent, w, staleMark(p, now), waitAge(p, agents[0], now))
		}
		return agents[0].Agent
	}
	// The longest wait is the one to act on (I-634).
	var longest string
	var longestD time.Duration
	for _, a := range agents {
		if a.State != "needs_input" {
			continue
		}
		if t, ok := p.waitedSince[agentKey(a)]; ok && now.Sub(t) > longestD {
			longestD, longest = now.Sub(t), waitAge(p, a, now)
		}
	}
	counts := map[string]int{}
	order := append([]string{}, agentStateOrder...)
	for _, a := range agents {
		if agentWord(a.State) == "" {
			continue
		}
		if _, ok := counts[a.State]; !ok && !slices.Contains(order, a.State) {
			order = append(order, a.State)
		}
		counts[a.State]++
	}
	var parts []string
	for _, st := range order {
		if counts[st] > 0 {
			part := fmt.Sprintf("%d %s", counts[st], agentWord(st))
			if st == "needs_input" && longest != "" {
				part += " (" + strings.TrimSpace(longest) + ")"
			}
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d agents", len(agents))
	}
	return fmt.Sprintf("%d agents: %s%s", len(agents), strings.Join(parts, ", "), staleMark(p, now))
}

// agentKey is how an agent's window is matched to its events: the
// window's name, else the agent's.
func agentKey(a AgentSignal) string {
	if a.Window != "" {
		return a.Window
	}
	return a.Agent
}

// waitAge is " 4h" after an agent that needs input, for how long it has
// waited: since the event that started its wait (I-634). "" when no
// event says.
func waitAge(p *Project, a AgentSignal, now time.Time) string {
	if a.State != "needs_input" {
		return ""
	}
	t, ok := p.waitedSince[agentKey(a)]
	if !ok {
		return ""
	}
	if d := now.Sub(t); d >= time.Minute {
		return " " + compactAge(d)
	}
	return " now"
}

// staleMark is "?" after agent states read from a sample older than
// signalFresh, as ps marks them (I-634): the agent may have moved on.
func staleMark(p *Project, now time.Time) string {
	if p.Signals != nil && p.Signals.SampledAt != nil && now.Sub(*p.Signals.SampledAt) >= signalFresh {
		return "?"
	}
	return ""
}

// noteWaits reads, from a project's events, when each agent that needs
// input began to wait: the newest event of its window, when that event
// is the question (I-634). Anything after it (done, an error) means the
// event that started this wait is not in the list.
func noteWaits(p *Project, events []Event) {
	if p.Signals == nil {
		return
	}
	newest := map[string]Event{}
	for _, ev := range events {
		k := ev.Window
		if k == "" {
			k = ev.Agent
		}
		if k == "" {
			continue
		}
		if cur, ok := newest[k]; !ok || ev.TS.After(cur.TS) {
			newest[k] = ev
		}
	}
	for _, a := range p.Signals.Agents {
		if a.State != "needs_input" {
			continue
		}
		ev, ok := newest[agentKey(a)]
		if !ok || (ev.Kind != "needs_input" && ev.Kind != "agent_question") {
			continue
		}
		if p.waitedSince == nil {
			p.waitedSince = map[string]time.Time{}
		}
		p.waitedSince[agentKey(a)] = ev.TS
	}
}

// needsAnswer reports whether an agent on p waits for an answer.
func needsAnswer(p *Project) bool {
	if p.Signals == nil || p.State != "running" {
		return false
	}
	for _, a := range p.Signals.Agents {
		if a.State == "needs_input" {
			return true
		}
	}
	return false
}

// noteWaitsOf reads the events of each project with an agent that needs
// input, at once, for ls (I-634). A failed read leaves that project's
// ages out.
func noteWaitsOf(ctx context.Context, c *Client, projects []Project) {
	var wg sync.WaitGroup
	for i := range projects {
		p := &projects[i]
		if !needsAnswer(p) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if evs, err := c.ListEvents(ctx, p.ID, ""); err == nil {
				noteWaits(p, evs)
			}
		}()
	}
	wg.Wait()
}

// agentList is status's agents row: each agent by its window's name
// (claude-2 when two run), with its state, the ones that need you first
// (I-616): `claude-2 needs input, claude working, codex idle`.
func agentList(p *Project) string {
	if p.Signals == nil || p.State != "running" || len(p.Signals.Agents) == 0 {
		return ""
	}
	rank := func(st string) int {
		if i := slices.Index(agentStateOrder, st); i >= 0 {
			return i
		}
		return len(agentStateOrder)
	}
	agents := slices.Clone(p.Signals.Agents)
	sort.SliceStable(agents, func(i, j int) bool { return rank(agents[i].State) < rank(agents[j].State) })
	now := time.Now()
	parts := make([]string, 0, len(agents))
	for _, a := range agents {
		name := a.Window
		if name == "" {
			name = a.Agent
		}
		if w := agentWord(a.State); w != "" {
			name += " " + w + staleMark(p, now) + waitAge(p, a, now)
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, ", ")
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

// newestEvent and snapshotCell pick by time, not position: the api lists
// newest first and the fake oldest first, and status took the last
// element, which showed a running project's first event
// (guest_state_changed "creating") as its last (I-192).
func newestEvent(events []Event) *Event {
	var best *Event
	for i := range events {
		if best == nil || events[i].TS.After(best.TS) {
			best = &events[i]
		}
	}
	return best
}

// statusWatchEvery is how often `repose status --watch` reads again.
var statusWatchEvery = 5 * time.Second

// StatusWatch is `repose status --watch` (I-616): on a terminal it
// redraws the screen in place; elsewhere it prints the status again only
// when it changed. An api that cannot be reached, or answers 5xx or 429,
// does not end the watch: the last status stays, with a line saying the
// read failed. Any other error (no such project, logged out) ends it.
func StatusWatch(ctx context.Context, e *Env, projectArg string, tty bool) error {
	out := e.Out
	var last, lastErr string
	for {
		var b strings.Builder
		e2 := *e
		e2.Out = &b
		err := StatusCmd(ctx, &e2, projectArg)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil && !transientErr(err):
			return err
		case err != nil:
			msg := fmt.Sprintf("Could not read the status at %s: %s", time.Now().Format("15:04:05"), briefErr(err))
			if tty {
				_, _ = fmt.Fprint(out, "\x1b[H\x1b[2J"+last+msg+"\n")
			} else if msg != lastErr {
				_, _ = fmt.Fprintln(e.ErrOut, msg)
			}
			lastErr = msg
		case tty:
			last = b.String()
			_, _ = fmt.Fprint(out, "\x1b[H\x1b[2J"+last)
		case b.String() != last:
			if last != "" && !e.JSON {
				_, _ = fmt.Fprintln(out)
			}
			last = b.String()
			_, _ = fmt.Fprint(out, last)
		}
		if err == nil {
			lastErr = ""
		}
		if sleepOrDone(ctx, statusWatchEvery) != nil {
			return nil
		}
	}
}

// transientErr is an error a later read can clear: the api not reached,
// a 5xx, a 429.
func transientErr(err error) bool {
	var u *unreachableError
	if errors.As(err, &u) {
		return true
	}
	var a *APIError
	return errors.As(err, &a) && (a.Status >= 500 || a.Status == 429)
}

// projectStates are the states `--wait` takes: the api's state enum
// (docs/interfaces/README.md).
var projectStates = []string{"creating", "building", "starting", "running", "stopping", "stopped", "restoring", "destroying", "destroyed", "error"}

// statusWaitEvery is how often `repose status --wait` reads the state.
var statusWaitEvery = 2 * time.Second

// StatusWait is `repose status --wait STATE` (I-616): it reads the
// project until it is in STATE and then prints its status (exit 0), for
// a script that started something elsewhere (the dashboard, a fork) and
// needs it there. A project that lands in error, or is destroyed, while
// another state is awaited exits 1 at once, as does the timeout.
func StatusWait(ctx context.Context, e *Env, projectArg, want string, timeout time.Duration) error {
	if !slices.Contains(projectStates, want) {
		return exitf(ExitUsage, "--wait takes a state: %s.", strings.Join(projectStates, ", "))
	}
	p, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		if p.State == want {
			return statusFor(ctx, e, p)
		}
		if p.State == "error" || p.State == "destroyed" {
			msg := fmt.Sprintf("%s is %s, not %s", p.Slug, p.State, want)
			if r := projectReason(p); r != "" && p.State == "error" {
				msg += ": " + r
			}
			return exitf(ExitGeneric, "%s.", strings.TrimSuffix(msg, "."))
		}
		if !time.Now().Before(deadline) {
			return exitf(ExitGeneric, "%s is still %s after %s, not %s.", p.Slug, p.State, shortDuration(timeout), want)
		}
		if sleepOrDone(ctx, statusWaitEvery) != nil {
			return exitf(ExitInterrupted, "Interrupted.")
		}
		next, err := e.Client.GetProject(ctx, p.ID)
		switch {
		case isNotFound(err) && want != "destroyed":
			return exitf(ExitGeneric, "%s is destroyed, not %s.", p.Slug, want)
		case isNotFound(err):
			_, _ = fmt.Fprintf(e.Out, "%s is destroyed.\n", p.Slug)
			return nil
		case err != nil && !transientErr(err):
			return err
		case err == nil:
			p = next
		}
	}
}

// shortDuration is d without its zero parts: 10m, 1h, 1h30m, 45s.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
