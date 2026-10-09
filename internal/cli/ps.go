package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/heracraft/repose/internal/multiplexer"
)

// `repose ps` (DECISIONS I-274, I-606): the project's tmux windows, the
// way `docker ps` lists containers. One ssh over the project's
// multiplexed connection runs `tmux list-windows`; the command column is
// tmux's pane_current_command, the process name only, never its
// arguments. STATE is the agent state guestd keeps on each agent window
// (@repose-state), TREE is where the window's pane works, as a label
// (checkout, worktree-N, ~/path). With a window, or -n, it prints the
// last lines of the window instead, the way `docker logs --tail` does.
// The output goes to the user's own terminal and nowhere else.

// PsRow is one element of `repose ps --json`, the same shape on tmux and
// herdr (I-606): a field one multiplexer cannot know is null. The keys
// each had before (tmux: index, current, activity; herdr: workspace)
// stay for one release.
type PsRow struct {
	Name string `json:"name"`
	// Agent is the agent the window runs, null for a window that runs
	// none (a shell, a dev server).
	Agent *string `json:"agent"`
	// Command is tmux's pane_current_command; null on herdr.
	Command *string `json:"command"`
	// AgentState is working, idle, needs_input or unknown, as `ls` and
	// `status` say it; null for a window that runs no agent.
	AgentState *string `json:"agent_state"`
	// State is AgentState, except on herdr, where it stays herdr's own
	// agent_status (working, blocked, idle, done) for one release, as
	// `ps --json` printed it before I-606 (I-631).
	State *string `json:"state"`
	// Tree is where the window works: checkout, worktree-N, another
	// folder as ~/path; on herdr the workspace's label.
	Tree    string `json:"tree"`
	Focused bool   `json:"focused"`
	// IdleSecs is how long since the window last printed; null on herdr.
	IdleSecs *int64 `json:"idle_seconds"`

	// Kept one release (I-606).
	Index     *int       `json:"index,omitempty"`
	Current   *bool      `json:"current,omitempty"`
	Activity  *time.Time `json:"activity,omitempty"`
	Workspace *string    `json:"workspace,omitempty"`
}

// PsAgent is one herdr agent as `herdr agent list` gives it (I-509): no
// cwd, no title.
type PsAgent struct {
	Workspace string `json:"workspace"`
	Agent     string `json:"agent"`
	Name      string `json:"name"`
	State     string `json:"state"`
	Focused   bool   `json:"focused"`
}

// herdrPsRows is herdr's agents with their workspaces' labels, in
// herdr's order.
func herdrPsRows(st herdrState) []PsAgent {
	labels := st.workspaceLabel()
	rows := []PsAgent{}
	for _, a := range st.Agents {
		rows = append(rows, PsAgent{Workspace: labels[a.WorkspaceID], Agent: a.Agent, Name: a.Name, State: a.Status, Focused: a.Focused})
	}
	return rows
}

// herdrAgentStates maps herdr's agent_status to the states `ls` and
// `status` use, the map guestd applies (sample.herdrStates).
var herdrAgentStates = map[string]string{
	"working": "working",
	"blocked": "needs_input",
	"idle":    "idle",
	"done":    "idle",
}

func herdrAgentState(status string) string {
	if s, ok := herdrAgentStates[status]; ok {
		return s
	}
	return "unknown"
}

// psRowsHerdr is herdr's agents as PsRow.
func psRowsHerdr(agents []PsAgent) []PsRow {
	rows := []PsRow{}
	for _, a := range agents {
		agent, state, raw, ws := a.Agent, herdrAgentState(a.State), a.State, a.Workspace
		rows = append(rows, PsRow{Name: a.Name, Agent: &agent, AgentState: &state, State: &raw, Tree: a.Workspace, Focused: a.Focused, Workspace: &ws})
	}
	return rows
}

// psHerdr is `repose ps` on a machine that runs herdr: `herdr agent
// list` and `herdr workspace list` in one ssh.
func psHerdr(ctx context.Context, e *Env, target sshTarget, slug string) error {
	st, err := herdrListState(ctx, target, slug)
	if err != nil {
		return stepFailed("list the herdr agents on "+slug, err, "")
	}
	rows := herdrPsRows(st)
	switch {
	case e.JSON:
		return writeJSONOut(e.Out, psRowsHerdr(rows))
	case e.Quiet:
		for _, r := range rows {
			if r.Name != "" {
				_, _ = fmt.Fprintln(e.Out, r.Name)
			}
		}
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "WORKSPACE\tAGENT\tNAME\tSTATE")
	for _, r := range rows {
		mark := " "
		if r.Focused {
			mark = "*"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s%s\t%s\n", orDash(r.Workspace), r.Agent, orDash(r.Name), mark, stateCell(herdrAgentState(r.State)))
	}
	return tw.Flush()
}

// PsWindow is one tmux window as psScript reports it.
type PsWindow struct {
	Index    int
	Name     string
	Command  string
	Current  bool
	Activity time.Time
	IdleSecs int64
	// State is the window's @repose-state, which guestd sets on an
	// agent window when its state changes (I-606); "" on a base before
	// that, and on a window that runs no agent.
	State string
	// Path is the active pane's working directory on the machine.
	Path string
}

// psListing is psScript's answer: the windows, the home folder and the
// machine's own checkout.
type psListing struct {
	Home, Checkout string
	Windows        []PsWindow
}

// psStateOption is the tmux window option guestd keeps an agent
// window's state in (guest-conventions.md "tmux").
const psStateOption = "@repose-state"

// psScript prints the guest's clock, the home folder and the checkout,
// then one tab-separated line per window of session slug. The clock comes
// from the same machine as the activity times, so a laptop clock that is
// off does not skew "2m ago". tmux runs last: its exit is the script's.
func psScript(slug string) string {
	return "date +%s\n" + checkoutVar(slug, "") +
		"printf '#home %s\\n#co %s\\n' \"$HOME\" \"$repose_co\"\n" +
		fmt.Sprintf("tmux list-windows -t %s -F '#{window_index}\t#{window_name}\t#{pane_current_command}\t#{window_activity}\t#{window_active}\t#{%s}\t#{pane_current_path}'",
			shQuote("="+slug), psStateOption)
}

// parsePs reads psScript's output. A line of five fields is a CLI before
// I-606's script, which a test or an old fake still sends.
func parsePs(out string) (psListing, error) {
	var l psListing
	lines := nonEmptyLines(out)
	if len(lines) == 0 {
		return l, fmt.Errorf("no output")
	}
	now, err := strconv.ParseInt(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return l, fmt.Errorf("unexpected clock %q", lines[0])
	}
	for _, line := range lines[1:] {
		if v, ok := strings.CutPrefix(line, "#home "); ok {
			l.Home = v
			continue
		}
		if v, ok := strings.CutPrefix(line, "#co "); ok {
			l.Checkout = v
			continue
		}
		f := strings.SplitN(line, "\t", 7)
		if len(f) != 5 && len(f) != 7 {
			return l, fmt.Errorf("unexpected line with %d fields", len(f))
		}
		idx, _ := strconv.Atoi(f[0])
		act, _ := strconv.ParseInt(f[3], 10, 64)
		idle := now - act
		if idle < 0 {
			idle = 0
		}
		w := PsWindow{
			Index: idx, Name: f[1], Command: f[2], Current: f[4] == "1",
			Activity: time.Unix(act, 0).UTC(), IdleSecs: idle,
		}
		if len(f) == 7 {
			w.State, w.Path = f[5], f[6]
		}
		l.Windows = append(l.Windows, w)
	}
	return l, nil
}

// windowAgent is the agent a tmux window runs: by its name ("claude",
// "claude-2", "<checkout>/codex"), else by its program, the rule guestd
// applies (sample.AgentOf, AgentByCommand). Gemini's program is node, so
// it counts only by name.
func windowAgent(name, command string) string {
	base := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		base = name[i+1:]
	}
	for _, a := range agentNames {
		if isWindowOf(a, base) {
			return a
		}
	}
	for _, a := range agentNames {
		if command == a {
			return a
		}
	}
	return ""
}

// loginShells are the commands a window shows when its agent exited
// and the shell it ran in is in front again.
var loginShells = map[string]bool{"bash": true, "zsh": true, "fish": true, "sh": true, "dash": true}

// signalFresh is how old the api's sample may be for ps to take a state
// from it: hosts sample every minute.
const signalFresh = 2 * time.Minute

// windowState is a window's agent state: its @repose-state, else the
// api's sample for that window when the sample is fresh (a base before
// I-606), else unknown; "" for a window that runs no agent.
func windowState(w PsWindow, agent string, sig *Signals, now time.Time) string {
	if agent == "" {
		return ""
	}
	if loginShells[w.Command] {
		// A shell in front: the agent the window is named after exited,
		// and what guestd set for it last is stale (I-631).
		return "unknown"
	}
	if w.State != "" {
		return w.State
	}
	if sig != nil && sig.SampledAt != nil && now.Sub(*sig.SampledAt) < signalFresh {
		for _, a := range sig.Agents {
			if a.Window == w.Name && a.State != "" {
				return a.State
			}
		}
	}
	return "unknown"
}

// treeOf is the TREE column: where path is, said the way `exec
// --workdir` takes it. The machine's own checkout is "checkout", its
// worktrees (I-342) "worktree-N", anything else under the home folder
// "~/path", and anything outside it the path itself.
func treeOf(path, home, checkout string) string {
	if path == "" {
		return ""
	}
	under := func(base string) (string, bool) {
		if path == base {
			return "", true
		}
		if r, ok := strings.CutPrefix(path, base+"/"); ok {
			return r, true
		}
		return "", false
	}
	join := func(a, b string) string {
		if b == "" {
			return a
		}
		return a + "/" + b
	}
	if checkout != "" && checkout != home {
		if r, ok := under(checkout); ok {
			return join("checkout", r)
		}
		if r, ok := strings.CutPrefix(path, checkout+"-worktree-"); ok {
			n, rest, _ := strings.Cut(r, "/")
			if allDigits(n) {
				return join("worktree-"+n, rest)
			}
		}
	}
	if home != "" {
		if r, ok := under(home); ok {
			return join("~", r)
		}
	}
	return path
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// psRowsTmux is the windows as PsRow.
func psRowsTmux(l psListing, sig *Signals, now time.Time) []PsRow {
	rows := []PsRow{}
	for _, w := range l.Windows {
		w := w
		r := PsRow{Name: w.Name, Command: &w.Command, Tree: treeOf(w.Path, l.Home, l.Checkout), Focused: w.Current, IdleSecs: &w.IdleSecs,
			Index: &w.Index, Current: &w.Current, Activity: &w.Activity}
		if a := windowAgent(w.Name, w.Command); a != "" {
			st := windowState(w, a, sig, now)
			r.Agent, r.AgentState, r.State = &a, &st, &st
		}
		rows = append(rows, r)
	}
	return rows
}

// stateCell is the STATE column: "-" for no agent, and for unknown,
// which says nothing (I-567).
// It is in the words `ls` and `status` print (I-617): `needs input`,
// where --json keeps needs_input.
func stateCell(s string) string {
	if s == "unknown" {
		return "-"
	}
	if w, ok := agentStateWords[s]; ok {
		return w
	}
	return orDash(s)
}

// PsOptions are `repose ps`'s arguments.
type PsOptions struct {
	ProjectArg string
	// Window is the one window (tmux name or index, herdr agent name)
	// whose last lines to print; "" for the listing.
	Window string
	// Lines is -n: how many lines; 0 without -n.
	Lines int
}

// psDefaultLines is how many lines a window shows without -n.
const psDefaultLines = 20

// psMaxLines bounds -n: a tmux pane keeps 50000 lines of history at most
// on the machine, and more than this is a job for `repose exec`.
const psMaxLines = 10000

func newPsCmd(envJSON func(*cobra.Command) (*Env, error), env func() (*Env, error), g *globalFlags) *cobra.Command {
	var quiet bool
	var opts PsOptions
	cmd := &cobra.Command{
		Use:   "ps [PROJECT] [WINDOW]",
		Short: "List a project's agent windows, or print a window's last lines",
		Long: "List the tmux windows of PROJECT (this checkout's, by default): each window's\n" +
			"number and name, the program in its active pane (its name only), the agent's\n" +
			"state, the folder it works in (checkout, worktree-N, ~/path) and when it last\n" +
			"printed something. * marks the window repose attach opens on.\n\n" +
			"With WINDOW (or -w), print that window's last 20 lines instead, or -n N of them;\n" +
			"-n alone prints the last N lines of every window.\n\n" +
			"On a machine that runs herdr, list herdr's agents instead: the workspace, the\n" +
			"agent, its name and its state, with * on the focused one. WINDOW is an agent's\n" +
			"name.",
		Example: "  repose ps\n  repose ps todo-app claude-2\n  repose ps -w 2 -n 50",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 2 {
				return cobraUsageError{fmt.Errorf("%s takes PROJECT and WINDOW, got %s", cmd.CommandPath(), gotArgs(args))}
			}
			return nil
		},
		ValidArgsFunction: completeProject(env),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 {
				if opts.Window != "" && opts.Window != args[1] {
					return cobraUsageError{fmt.Errorf("%s and --window %s name two windows; pass one", args[1], opts.Window)}
				}
				opts.Window, args = args[1], args[:1]
			}
			if _, err := projectFrom(args, g); err != nil {
				return err
			}
			if cmd.Flags().Changed("tail") && (opts.Lines < 1 || opts.Lines > psMaxLines) {
				return cobraUsageError{fmt.Errorf("-n takes 1 to %d lines, got %d", psMaxLines, opts.Lines)}
			}
			e, err := envJSON(cmd)
			if err != nil {
				return err
			}
			if quiet && e.JSON {
				return cobraUsageError{fmt.Errorf("-q and --json are two different outputs; pass one")}
			}
			if (opts.Window != "" || opts.Lines > 0) && (quiet || e.JSON) {
				return cobraUsageError{fmt.Errorf("-q and --json go with the listing; a window's lines are text")}
			}
			e.Quiet = quiet
			return orWindow(cmd.Context(), e, args, opts.Window, g.project, func(project, window string) error {
				o := opts
				o.ProjectArg, o.Window = project, window
				return PsCmdWith(cmd.Context(), e, o)
			})
		},
	}
	cmd.Flags().Bool("json", false, "print the windows (or herdr agents) as JSON")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "print only the window (or herdr agent) names, one per line")
	cmd.Flags().StringVarP(&opts.Window, "window", "w", "", "print the last lines of window `NAME` (a name or number, or a herdr agent)")
	cmd.Flags().IntVarP(&opts.Lines, "tail", "n", 0, "print the last `N` lines: of WINDOW (default 20), or of every window")
	return cmd
}

// orWindow runs fn on PROJECT and WINDOW for ps and attach (I-606). One
// word that names none of the account's projects, run where a project
// resolves with no --project and no -w, is a WINDOW of that project
// (I-631), as exec reads its first word: `repose ps claude-2` in the
// checkout. The project is looked up first, so the usual case costs no
// extra call.
func orWindow(ctx context.Context, e *Env, args []string, window, projectFlag string, fn func(project, window string) error) error {
	project := projectFlag
	if len(args) > 0 {
		project = args[0]
	}
	err := fn(project, window)
	if len(args) != 1 || window != "" || projectFlag != "" {
		return err
	}
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != ExitProjectNotFound || ee.msg != noSuchProjectMessage(args[0]) {
		return err
	}
	res, rerr := resolveProject(ctx, e.Client, e.Dir, e.Cwd, e.resolveArg(""), &e.Cache, defaultResolveDeps())
	if rerr != nil || res.Project == nil {
		return err
	}
	return fn("", args[0])
}

// Exit status of psScript's tmux when the session does not exist.
const psExitNoSession = 1

// PsCmd implements `repose ps [PROJECT]`, the listing.
func PsCmd(ctx context.Context, e *Env, projectArg string) error {
	return PsCmdWith(ctx, e, PsOptions{ProjectArg: projectArg})
}

// PsCmdWith implements `repose ps`.
func PsCmdWith(ctx context.Context, e *Env, opts PsOptions) error {
	project, err := requireRunningProject(ctx, e, opts.ProjectArg)
	if err != nil {
		return err
	}
	target, err := connect(ctx, e, project)
	if err != nil {
		return err
	}
	mux, err := muxFor(ctx, target, project)
	if err != nil {
		return err
	}
	if opts.Window != "" || opts.Lines > 0 {
		n := opts.Lines
		if n == 0 {
			n = psDefaultLines
		}
		if mux.Name() == multiplexer.Herdr {
			return psTailHerdr(ctx, e, target, project.Slug, opts.Window, n)
		}
		return psTailTmux(ctx, e, target, project.Slug, opts.Window, n)
	}
	if mux.Name() == multiplexer.Herdr {
		return psHerdr(ctx, e, target, project.Slug)
	}
	out, err := runSSH(ctx, target, psScript(project.Slug), nil)
	if err := noSession(err, project.Slug); err != nil {
		return err
	}
	if err != nil {
		return stepFailed("list the tmux windows on "+project.Slug, err, "")
	}
	l, err := parsePs(string(out))
	if err != nil {
		return stepFailed("read the tmux windows on "+project.Slug, err, "")
	}
	rows := psRowsTmux(l, project.Signals, time.Now())
	switch {
	case e.JSON:
		return writeJSONOut(e.Out, rows)
	case e.Quiet:
		for _, r := range rows {
			_, _ = fmt.Fprintln(e.Out, r.Name)
		}
		return nil
	}
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "WINDOW\tCOMMAND\tSTATE\tTREE\tACTIVE")
	for _, r := range rows {
		mark := " "
		if r.Focused {
			mark = "*"
		}
		state := ""
		if r.AgentState != nil {
			state = *r.AgentState
		}
		_, _ = fmt.Fprintf(tw, "%d:%s%s\t%s\t%s\t%s\t%s\n", *r.Index, r.Name, mark, *r.Command, stateCell(state), orDash(r.Tree), idleAgo(*r.IdleSecs))
	}
	return tw.Flush()
}

// noSession is the error for a machine with no tmux session, or nil.
func noSession(err error, slug string) error {
	var se *sshError
	if errors.As(err, &se) && se.ExitCode == psExitNoSession && strings.Contains(se.Stderr, "can't find session") {
		return exitf(ExitGeneric, "%s has no tmux session. `repose attach %s` starts one.", slug, slug)
	}
	return nil
}

// tmuxWindowTarget is the exact tmux target of window in session slug:
// a number is the window's index, anything else its exact name (tmux
// would take a name's prefix too).
func tmuxWindowTarget(slug, window string) string {
	if allDigits(window) {
		return "=" + slug + ":" + window
	}
	return "=" + slug + ":=" + window
}

// psTailScript prints the last lines of one window's active pane: n
// lines of history and the screen, which psLastLines trims. -J joins
// lines tmux wrapped, so a line is what the program printed.
func psTailScript(slug, window string, n int) string {
	return fmt.Sprintf("tmux capture-pane -p -J -t %s -S -%d", shQuote(tmuxWindowTarget(slug, window)), n)
}

// psTailAllScript prints each window's last lines behind a line of its
// own, mark plus "<index>:<name>", which no pane's text can forge.
func psTailAllScript(slug, mark string, n int) string {
	return fmt.Sprintf(`tmux list-windows -t %[1]s -F '#{window_index}:#{window_name}' | while IFS= read -r repose_w; do
  printf '%%s %%s\n' %[2]s "$repose_w"
  tmux capture-pane -p -J -t %[1]s:"${repose_w%%%%:*}" -S -%[3]d
done`, shQuote("="+slug), shQuote(mark), n)
}

// psLastLines is the last n lines of a capture, with the blank rows
// below the last output and each line's trailing spaces gone.
func psLastLines(capture string, n int) []string {
	lines := strings.Split(capture, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func writeLines(w io.Writer, lines []string) {
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, l)
	}
}

// psTailTmux prints window's last n lines, or with no window every
// window's, each behind a "==> 1:claude <==" line as tail(1) heads
// several files.
func psTailTmux(ctx context.Context, e *Env, target sshTarget, slug, window string, n int) error {
	if window != "" {
		out, err := runSSH(ctx, target, psTailScript(slug, window, n), nil)
		if err := noSession(err, slug); err != nil {
			return err
		}
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == 1 && strings.Contains(se.Stderr, "can't find window") {
			return noWindow(slug, window, "window")
		}
		if err != nil {
			return stepFailed("read the "+window+" window on "+slug, err, "")
		}
		writeLines(e.Out, psLastLines(string(out), n))
		return nil
	}
	mark := psMark()
	out, err := runSSH(ctx, target, psTailAllScript(slug, mark, n), nil)
	if err != nil {
		// list-windows fails inside the pipe; an empty answer is no session.
		return stepFailed("read the tmux windows on "+slug, err, "")
	}
	if strings.TrimSpace(string(out)) == "" {
		return exitf(ExitGeneric, "%s has no tmux session. `repose attach %s` starts one.", slug, slug)
	}
	writeSections(e.Out, string(out), mark, n)
	return nil
}

// writeSections prints psTailAllScript's answer as tail(1) prints
// several files: a blank line between sections, each headed "==> NAME <==".
func writeSections(w io.Writer, out, mark string, n int) {
	first := true
	var name string
	var body []string
	flush := func() {
		if name == "" {
			return
		}
		if !first {
			_, _ = fmt.Fprintln(w)
		}
		first = false
		_, _ = fmt.Fprintf(w, "==> %s <==\n", name)
		writeLines(w, psLastLines(strings.Join(body, "\n"), n))
	}
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(l, mark+" "); ok {
			flush()
			name, body = v, nil
			continue
		}
		body = append(body, l)
	}
	flush()
}

// psMark is a line prefix no pane prints by chance.
func psMark() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "#repose-" + hex.EncodeToString(b)
}

// noWindow is the refusal of a window the session does not have; unit
// is "window", or "agent" on herdr.
func noWindow(slug, window, unit string) error {
	return exitf(ExitUsage, "%s has no %s %s. `repose ps %s` lists them.", slug, unit, window, slug)
}

// herdrReadScript prints an agent's last n lines as text, or exits 3
// when herdr has no agent of that name.
func herdrReadScript(name string, n int) string {
	return fmt.Sprintf(`herdr agent list | jq -e --arg n %[1]s '.result.agents | any(.name == $n)' >/dev/null || exit 3
herdr agent read %[1]s --lines %[2]d --source recent-unwrapped --format text`, shQuote(name), n)
}

// psTailHerdr is psTailTmux on herdr: `herdr agent read` for the agent
// named window, or for each agent.
func psTailHerdr(ctx context.Context, e *Env, target sshTarget, slug, window string, n int) error {
	names := []string{window}
	if window == "" {
		st, err := herdrListState(ctx, target, slug)
		if err != nil {
			return stepFailed("list the herdr agents on "+slug, err, "")
		}
		names = nil
		for _, a := range st.Agents {
			if a.Name != "" {
				names = append(names, a.Name)
			}
		}
	}
	for i, name := range names {
		out, err := runSSH(ctx, target, herdrReadScript(name, n), nil)
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == 3 {
			return noWindow(slug, name, "agent")
		}
		if err != nil {
			return stepFailed("read "+name+" on "+slug, err, "")
		}
		if window == "" {
			if i > 0 {
				_, _ = fmt.Fprintln(e.Out)
			}
			_, _ = fmt.Fprintf(e.Out, "==> %s <==\n", name)
		}
		writeLines(e.Out, psLastLines(string(out), n))
	}
	return nil
}

// idleAgo is the ACTIVE column: "now" under a minute, then minutes,
// hours and days, as docker ps rounds.
func idleAgo(secs int64) string {
	switch d := time.Duration(secs) * time.Second; {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
