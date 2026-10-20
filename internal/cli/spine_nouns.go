package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/machine"
)

// The nouns of a project on a machine the user owns, besides `run` and
// `rewind` (DECISIONS I-812). The promise of the shape is leaving an
// agent and coming back to it, so `attach`, `sync`, `ps`, `ls` and
// `status` answer for a machine the user owns as they do for one repose
// runs — over that machine's own ssh, with no api and no gateway. Every
// other noun needs the api or a guest's units, and says so once rather
// than failing on a project the api never heard of
// (refuseOnOwnedMachine).

// spinePlace resolves the project a noun acts on to a machine the user
// owns: the one projectArg names, or the working directory's. ok is
// false when neither is on a machine of theirs, which is where the
// hosted path takes over; an error is the registry or the project map
// being unreadable, and nothing else.
func spinePlace(e *Env, projectArg string, deps resolveDeps) (machine.Machine, machine.Project, bool, error) {
	m, place, _, ok, err := spinePlaceOrWindow(e, projectArg, deps, false)
	return m, place, ok, err
}

// spinePlaceOrWindow is spinePlace with ps's and attach's rule for a
// single word: one that names no project of an owned machine, where the
// working directory's project is on one, is that project's window
// (orWindow, I-631). word is that window, "" when projectArg named a
// project. The rule is off for every other noun, which must not read a
// hosted project's name as this checkout's window.
func spinePlaceOrWindow(e *Env, projectArg string, deps resolveDeps, asWindow bool) (machine.Machine, machine.Project, string, bool, error) {
	none := func(err error) (machine.Machine, machine.Project, string, bool, error) {
		return machine.Machine{}, machine.Project{}, "", false, err
	}
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		return none(exitf(ExitGeneric, "%v", err))
	}
	// A laptop that has added no machine is the hosted path, and reading
	// the checkout's git remote for it would cost every noun a
	// subprocess.
	if len(projects.ByRemote) == 0 && len(projects.ByDir) == 0 {
		return none(nil)
	}
	remote, dir := checkoutKeys(e, deps)
	here, hasHere := projects.Resolve(remote, dir)
	place, word := here, ""
	switch projectArg {
	case "":
		if !hasHere {
			return none(nil)
		}
	default:
		found := false
		for _, byKey := range []map[string]machine.Project{projects.ByRemote, projects.ByDir} {
			for _, pr := range byKey {
				if pr.Slug == projectArg {
					place, found = pr, true
				}
			}
		}
		if !found {
			if !asWindow || !hasHere {
				return none(nil)
			}
			place, word = here, projectArg
		}
	}
	m, err := registryFor(e).Get(place.Machine)
	if err != nil {
		if isNoSuchMachine(err) {
			return none(exitf(ExitGuestNotRunning, "%s is on %s, which is not one of your machines any more.", place.Slug, place.Machine))
		}
		return none(err)
	}
	return m, place, word, true, nil
}

// spineFlagRefusal is the one wording for a flag that needs the api or a
// guest on a project that is on a machine the user owns (spineRunFor's,
// reused by every noun that takes the spine's path).
func spineFlagRefusal(flag string) error {
	return cobraUsageError{fmt.Errorf("%s is for a machine repose runs; this project is on a machine you own", flag)}
}

// laptopCheckoutFor is the laptop folder that is the project's checkout
// on the machine, which is what a file dropped on the attached terminal
// is placed beside (inputproxy repoDir). "" when the command was not run
// in that checkout.
func laptopCheckoutFor(e *Env, deps resolveDeps, place machine.Project) string {
	projects, err := machine.LoadProjects(e.Dir)
	if err != nil {
		return ""
	}
	remote, dir := checkoutKeys(e, deps)
	if pr, ok := projects.Resolve(remote, dir); !ok || pr != place {
		return ""
	}
	if root := gitRepoRoot(e.Cwd); root != "" {
		return root
	}
	return e.Cwd
}

// spineAttachOptions is an attach to a machine the user owns, resolved.
type spineAttachOptions struct {
	Machine machine.Machine
	Place   machine.Project
	// Window is the window to open on, "" for the session's current one.
	Window string
}

// spineAttachFor answers the options for an attach to a machine the user
// owns, or nil when this checkout is a hosted project's. bridge is
// whether --bridge or --bridge-allow was given, which wants the guest's
// browser bridge and so cannot work here.
func spineAttachFor(e *Env, projectArg, window string, bridge bool) (*spineAttachOptions, error) {
	m, place, word, ok, err := spinePlaceOrWindow(e, projectArg, defaultResolveDeps(), true)
	if err != nil || !ok {
		return nil, err
	}
	if bridge {
		return nil, spineFlagRefusal("--bridge")
	}
	if window == "" {
		window = word
	}
	return &spineAttachOptions{Machine: m, Place: place, Window: window}, nil
}

// spineAttachCmd attaches to the project's tmux session on a machine the
// user owns: the hosted path's own attach (attachTmux), with that
// machine's ssh target. Nothing is synced and nothing is started, as on
// the hosted path; a session that is gone is `repose run`'s to start
// again.
func spineAttachCmd(ctx context.Context, e *Env, opts spineAttachOptions) error {
	m, place := opts.Machine, opts.Place
	configPath, err := machinesConfigPath()
	if err != nil {
		return exitf(ExitGeneric, "%v", err)
	}
	t := machineTarget(configPath, m)
	windows, err := spineWindowNames(ctx, t, place.Slug)
	if err != nil {
		if spineNoSession(err) {
			return exitf(ExitGuestNotRunning, "%s has no tmux session on %s. `repose run` starts one.", place.Slug, m.Name)
		}
		return stepFailed("reach "+place.Slug+" on "+m.Name, err, "")
	}
	// A window the session has not got is refused rather than attached
	// around: one word that names no project of a machine is read as a
	// window of this one (spinePlaceOrWindow), and a hosted project
	// named from this folder must not quietly open another project's
	// session.
	if opts.Window != "" && !slices.Contains(windows, opts.Window) {
		return exitf(ExitGuestNotRunning, "%s on %s has no window called %s.", place.Slug, m.Name, opts.Window)
	}
	return attachTmux(t, place.Slug, opts.Window, laptopTZ(), laptopCheckoutFor(e, defaultResolveDeps(), place), nil, nil)
}

// spineSyncFor answers the options for `repose sync` against a machine
// the user owns, or nil when this checkout is a hosted project's: the
// run's own path, stopping once the laptop's work is there and a rewind
// point is taken. --json is the one flag left out, since what it prints
// is the api's project.
func spineSyncFor(e *Env, opts RunOptions) (*SpineRunOptions, error) {
	spine, err := spineRunFor(e, opts)
	if err != nil || spine == nil {
		return nil, err
	}
	if e.JSON {
		return nil, spineFlagRefusal("--json")
	}
	// spineRunFor reads it from opts.Sync, which `repose sync` sets;
	// said here too, because a sync that started an agent would be a
	// surprise with a cost.
	spine.SyncOnly = true
	return spine, nil
}

// spineWindowNames is the names of the windows in the project's tmux
// session on the machine. The error says which kind of nothing it is: a
// session that is not there (spineNoSession) or a machine that did not
// answer.
func spineWindowNames(ctx context.Context, t sshTarget, slug string) ([]string, error) {
	out, err := runOnMachine(ctx, t, "tmux list-windows -t "+shQuote("="+slug)+" -F '#{window_name}'", nil)
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(string(out)), nil
}

// spinePsSep separates the fields of `repose ps` on a machine the user
// owns. Not a tab: tmux 3.3a, which Debian 12 ships, prints `_` for a tab
// in a format string, and the tmux on such a box is whatever its
// distribution ships rather than the one nix pins for a guest. Three
// characters so no window name, command or path collides with it, and the
// path is the last field, where a stray separator is kept whole anyway.
const spinePsSep = "|~|"

// spineNoSession is noSession's check without its message: on a machine
// the user owns it is `repose run` that starts a session, not attach.
func spineNoSession(err error) bool {
	var se *sshError
	return errors.As(err, &se) && se.ExitCode == psExitNoSession && strings.Contains(se.Stderr, "can't find session")
}

// spinePs is `repose ps` on a machine the user owns: the same tmux
// listing over that machine's ssh. handled is false when this checkout
// is a hosted project's. There is no guestd on such a box, so no window
// carries an agent state and the STATE column is empty.
func spinePs(ctx context.Context, e *Env, opts PsOptions) (bool, error) {
	m, place, word, ok, err := spinePlaceOrWindow(e, opts.ProjectArg, defaultResolveDeps(), true)
	if err != nil {
		return true, err
	}
	if !ok {
		return false, nil
	}
	window := opts.Window
	if window == "" {
		window = word
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return true, exitf(ExitGeneric, "%v", err)
	}
	t := machineTarget(configPath, m)
	if window != "" || opts.Lines > 0 {
		n := opts.Lines
		if n == 0 {
			n = psDefaultLines
		}
		return true, psTailTmux(ctx, e, t, place.Slug, window, n)
	}
	out, err := runOnMachine(ctx, t, psScriptSep(place.Slug, spinePsSep), nil)
	if err != nil {
		if spineNoSession(err) {
			return true, exitf(ExitGuestNotRunning, "%s has no tmux session on %s. `repose run` starts one.", place.Slug, m.Name)
		}
		return true, stepFailed("list the tmux windows on "+place.Slug, err, "")
	}
	l, err := parsePsSep(string(out), spinePsSep)
	if err != nil {
		return true, stepFailed("read the tmux windows on "+place.Slug, err, "")
	}
	return true, writePsRows(e, psRowsTmux(l, nil, time.Now()))
}

// spineStatusJSON is `repose status --json` for a project on a machine
// the user owns: what is known about it, and no field the api would have
// filled.
type spineStatusJSON struct {
	Slug    string   `json:"slug"`
	Machine string   `json:"machine"`
	Windows []string `json:"windows"`
	Points  int      `json:"points"`
	// NewestPointAt is when the point a rewind goes back to was taken;
	// absent with no points.
	NewestPointAt *time.Time `json:"newest_point_at,omitempty"`
}

// spineStatus is `repose status` on a machine the user owns: where the
// project is, what runs in its session, and how far back it can be
// rewound. handled is false when this checkout is a hosted project's.
func spineStatus(ctx context.Context, e *Env, projectArg string) (bool, error) {
	m, place, ok, err := spinePlace(e, projectArg, defaultResolveDeps())
	if err != nil {
		return true, err
	}
	if !ok {
		return false, nil
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return true, exitf(ExitGeneric, "%v", err)
	}
	t := machineTarget(configPath, m)
	var points agentPoints
	if err := agentRun(ctx, t, &points, "point", "list", "--project", place.Slug); err != nil {
		return true, stepFailed("read "+place.Slug+"'s rewind points", err, "")
	}
	// The point list above is what says the machine answered, so a
	// failure here is a session that is not running.
	windows, _ := spineWindowNames(ctx, t, place.Slug)
	newest := newestPoint(points.Points)
	if e.JSON {
		doc := spineStatusJSON{Slug: place.Slug, Machine: m.Name, Windows: windows, Points: len(points.Points)}
		if windows == nil {
			doc.Windows = []string{}
		}
		if newest != nil {
			at := newest.TakenAt
			doc.NewestPointAt = &at
		}
		return true, e.printJSON(doc)
	}
	_, _ = fmt.Fprintf(e.Out, "%s on %s\n", place.Slug, m.Name)
	statusRow(e.Out, "windows", windowsCell(windows))
	statusRow(e.Out, "undo", undoPointsCell(points.Points, newest, time.Now()))
	return true, nil
}

// windowsCell is the windows of the session, or that there is nothing in
// it: an agent left running is the thing the user came back for.
func windowsCell(windows []string) string {
	if len(windows) == 0 {
		return "none"
	}
	return strings.Join(windows, ", ")
}

// undoPointsCell is how far back the project can be rewound.
func undoPointsCell(points []agentPoint, newest *agentPoint, now time.Time) string {
	if len(points) == 0 || newest == nil {
		return "no rewind points"
	}
	return count(len(points), "rewind point") + ", newest " + pointAge(now.Sub(newest.TakenAt))
}

// agentBoxStatus is what `repose-agent status` prints (SPINE.md Contract
// 3). Only the projects are read here: the store's space is `repose
// machine ls`'s column and is not said twice.
type agentBoxStatus struct {
	Projects []agentBoxProject `json:"projects"`
}

type agentBoxProject struct {
	Slug   string `json:"slug"`
	Points int    `json:"points"`
}

// loggedInOnce is whether this laptop has credentials at all. `repose
// ls` tells a user whose login stopped working that the api's half of
// the list is missing, and says nothing to one who never logged in: the
// self-hosted half ships first and needs no account (I-816).
func loggedInOnce(dir string) bool {
	creds, ok, err := loadCredentials(dir)
	return err == nil && ok && (creds.RefreshToken != "" || creds.AccessToken != "")
}

// ownedRow is one project on a machine the user owns, as `repose ls`
// lists it beside the api's projects. A machine that did not answer
// still shows what the laptop recorded on it: a project nobody can see
// is a project nobody comes back to.
type ownedRow struct {
	Slug    string `json:"slug"`
	Machine string `json:"machine"`
	Points  int    `json:"points"`
	// Unreachable is a machine that did not answer; Points is then
	// unknown.
	Unreachable bool `json:"unreachable,omitempty"`
	// here marks the working directory's project, as the hosted table's
	// * does.
	here bool
}

// ownedProjects is every project on a machine the user owns, in the
// registry's order and by slug within a machine: one `repose-agent
// status` per machine, in parallel, each bounded the way `machine ls`
// bounds its reads.
func ownedProjects(ctx context.Context, e *Env) []ownedRow {
	all, err := registryFor(e).List()
	if err != nil || len(all) == 0 {
		return nil
	}
	configPath, err := machinesConfigPath()
	if err != nil {
		return nil
	}
	recorded, err := machine.LoadProjects(e.Dir)
	if err != nil {
		recorded = machine.NewProjects()
	}
	remote, dir := checkoutKeys(e, defaultResolveDeps())
	here, _ := recorded.Resolve(remote, dir)
	perMachine := make([][]ownedRow, len(all))
	var wg sync.WaitGroup
	for i, m := range all {
		wg.Add(1)
		go func(i int, m machine.Machine) {
			defer wg.Done()
			perMachine[i] = ownedRowsOf(ctx, machineTarget(configPath, m), m.Name, recorded)
		}(i, m)
	}
	wg.Wait()
	var rows []ownedRow
	for _, machineRows := range perMachine {
		for _, r := range machineRows {
			r.here = r.Machine == here.Machine && r.Slug == here.Slug
			rows = append(rows, r)
		}
	}
	return rows
}

// ownedRowsOf is one machine's projects: what the box itself holds, or,
// when it did not answer, what the laptop recorded on it.
func ownedRowsOf(ctx context.Context, t sshTarget, name string, recorded machine.Projects) []ownedRow {
	ctx, cancel := context.WithTimeout(ctx, machineSpaceWait)
	defer cancel()
	var st agentBoxStatus
	if err := agentRun(ctx, t, &st, "status"); err != nil {
		return recordedRowsOf(name, recorded)
	}
	rows := make([]ownedRow, 0, len(st.Projects))
	for _, pr := range st.Projects {
		rows = append(rows, ownedRow{Slug: pr.Slug, Machine: name, Points: pr.Points})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })
	return rows
}

// recordedRowsOf is the projects the laptop has recorded on the machine
// called name, each slug once.
func recordedRowsOf(name string, recorded machine.Projects) []ownedRow {
	var slugs []string
	for _, byKey := range []map[string]machine.Project{recorded.ByRemote, recorded.ByDir} {
		for _, pr := range byKey {
			if pr.Machine == name && !slices.Contains(slugs, pr.Slug) {
				slugs = append(slugs, pr.Slug)
			}
		}
	}
	sort.Strings(slugs)
	rows := make([]ownedRow, 0, len(slugs))
	for _, slug := range slugs {
		rows = append(rows, ownedRow{Slug: slug, Machine: name, Unreachable: true})
	}
	return rows
}

// writeOwnedProjects is `repose ls`'s second table: the projects on
// machines the user owns, which the api has never heard of. after is
// whether the api's table was printed above it.
func writeOwnedProjects(w io.Writer, rows []ownedRow, after bool) {
	if len(rows) == 0 {
		return
	}
	if after {
		_, _ = fmt.Fprintln(w)
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MACHINE\tPROJECT\tPOINTS")
	for _, r := range rows {
		name := r.Slug
		if r.here {
			name += " *"
		}
		points := strconv.Itoa(r.Points)
		if r.Unreachable {
			points = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Machine, name, points)
	}
	_ = tw.Flush()
	var said []string
	for _, r := range rows {
		if r.Unreachable && !slices.Contains(said, r.Machine) {
			said = append(said, r.Machine)
			_, _ = fmt.Fprintf(w, "%s did not answer.\n", r.Machine)
		}
	}
}

// spineRefusedNouns are the commands whose work is the api's or a
// guest's: a plan's size classes, snapshots streamed to Blob, guestd's
// events and questions. A project on a machine the user owns has none of
// that, so each of these says so once (refuseOnOwnedMachine) instead of
// asking the api about a project it has never heard of. `run`, `attach`,
// `sync`, `ps`, `ls`, `status`, `config`, `rewind` and `machine` are the
// nouns that answer for such a project; the rest wait for the pass that
// gives them something true to do.
// `config` answers in spine_config.go, where the project's own
// repose.nix, built into a Nix profile on the box, is its configuration,
// and the parts a host or a server of ours would hold (a revision to go
// back to, the account's machine.nix) refuse one by one.
// `repose restore` is not among them: it takes the name of a destroyed
// project, not a project, so a refusal there would answer a name this
// path knows nothing about.
var spineRefusedNouns = []string{
	"browser", "events", "fork", "keep", "logs", "questions",
	"reply", "resize", "rm", "secrets", "snapshots", "start", "stop",
}

// spineWholeAccountFlags are the flags that take one of those nouns off
// a project and onto the account or every machine: `secrets set
// --global` the account's secrets, `stop --unused` every machine that is
// idle. A checkout on a machine the user owns must not refuse those.
var spineWholeAccountFlags = []string{"global", "unused"}

// refuseOnOwnedMachine is the one place a noun that cannot act on a
// machine the user owns says so (the audit of 2026-10-20 found eleven
// nouns failing instead on the api's project resolution). It runs from
// the root command's PersistentPreRunE, before the noun resolves
// anything, and refuses only when the project the noun would act on is
// recorded on a machine of theirs: a hosted project named here, or a
// name the project map has not got, is the api's as before.
func refuseOnOwnedMachine(env func() (*Env, error), cmd *cobra.Command, args []string, projectFlag string) error {
	if !slices.Contains(spineRefusedNouns, rootNoun(cmd)) {
		return nil
	}
	for _, name := range spineWholeAccountFlags {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			return nil
		}
	}
	e, err := env()
	if err != nil {
		// A configuration that cannot be read is the command's own env()
		// to report, in its own words.
		return nil
	}
	named := projectFlag
	if named == "" && len(args) > 0 && strings.Contains(cmd.Use, "PROJECT") {
		named = args[0]
	}
	m, place, ok, err := spinePlace(e, named, defaultResolveDeps())
	if err != nil || !ok {
		// Nothing resolved to a machine of theirs: the hosted path, and
		// its own messages, as before.
		return nil
	}
	return exitf(ExitGuestNotRunning, "`%s` is for a machine repose runs; %s is on %s, a machine you own.",
		cmd.CommandPath(), place.Slug, m.Name)
}

// rootNoun is the command's own word under `repose`: "snapshots" for
// `repose snapshots ls`, so one entry in spineRefusedNouns covers a
// group and every subcommand of it.
func rootNoun(cmd *cobra.Command) string {
	for c := cmd; c != nil && c.Parent() != nil; c = c.Parent() {
		if c.Parent().Parent() == nil {
			return c.Name()
		}
	}
	return ""
}
