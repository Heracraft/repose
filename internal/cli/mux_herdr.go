package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/heracraft/repose/internal/multiplexer"
)

// herdrMux is the machine's terminals in herdr (DECISIONS I-501, I-509):
// the server repose-herdr-server.service runs, driven with herdr's own
// CLI over ssh, whose answers are JSON (guest-conventions.md "herdr").
// The scripts read from those answers only ids, labels, names, agent
// kinds, statuses and focus: never a pane's cwd or title.
type herdrMux struct{}

func (herdrMux) Name() string { return multiplexer.Herdr }
func (herdrMux) Unit() string { return "tab" }

// Label is the agent itself: herdr's agent names are
// [a-z][a-z0-9_-]{0,31}, so another checkout's agents are told apart by
// their workspace, never by a "<checkout>/" prefix.
func (herdrMux) Label(extra, agent string) string { return agent }

// herdrAgent is the part of one `herdr agent list` entry the CLI reads.
type herdrAgent struct {
	Name        string `json:"name"`
	Agent       string `json:"agent"`
	Status      string `json:"agent_status"`
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	Focused     bool   `json:"focused"`
}

// herdrWorkspace is the part of one `herdr workspace list` entry the CLI
// reads.
type herdrWorkspace struct {
	ID      string `json:"workspace_id"`
	Label   string `json:"label"`
	Focused bool   `json:"focused"`
}

// herdrReply is any herdr CLI answer: a result, or an error with a code.
type herdrReply struct {
	Result struct {
		Agents     []herdrAgent     `json:"agents"`
		Workspaces []herdrWorkspace `json:"workspaces"`
	} `json:"result"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// herdrState is the agents and workspaces of one listing, and the label
// of the workspace the command works in. Alias is the label the machine's
// own checkout had before I-597 (its directory name), which a machine
// whose workspace has not been renamed yet still shows.
type herdrState struct {
	Label      string
	Alias      string
	Agents     []herdrAgent
	Workspaces []herdrWorkspace
}

// herdrCheckoutLabel is the label of the workspace in the machine's own
// checkout (DECISIONS I-597). The laptop herdr's sidebar names the
// machine after the project, which is usually the checkout's directory
// name too, so a workspace labelled with that name showed the project
// twice (`recruiting` under `recruiting`). Another checkout's workspace
// keeps its name, and a machine with no checkout has `home`.
const herdrCheckoutLabel = "checkout"

// herdrMainVar is shell that sets repose_main to the machine's own
// checkout (checkoutVar's rule; $HOME when it has none), whatever folder
// the command works in.
func herdrMainVar(slug string) string {
	return checkoutVar(slug, "") + "repose_main=$repose_co\n"
}

// herdrLabelShell sets repose_l to the label of the workspace for folder
// $repose_d, after herdrMainVar: home, checkout, or the folder's name.
const herdrLabelShell = `if [ "$repose_d" = "$HOME" ]; then repose_l=home; elif [ "$repose_d" = "$repose_main" ]; then repose_l=` + herdrCheckoutLabel + `; else repose_l=${repose_d##*/}; fi
`

// herdrRenameShell, after herdrMainVar, gives the machine's own checkout
// the label I-597 names when its workspace still has the old one (the
// directory's name, from a base before I-597 or a CLI before it): the
// workspace, its tabs and agents stay as they are. A machine with no
// checkout, or one that already has the label, is left alone.
const herdrRenameShell = `repose_old=${repose_main##*/}
if [ "$repose_main" != "$HOME" ] && [ "$repose_old" != home ] && [ "$repose_old" != ` + herdrCheckoutLabel + ` ] && repose_wl=$(herdr workspace list 2>/dev/null); then
  if ! printf '%s' "$repose_wl" | jq -e '.result.workspaces | any(.label == "` + herdrCheckoutLabel + `")' >/dev/null 2>&1; then
    repose_id=$(printf '%s' "$repose_wl" | jq -r --arg l "$repose_old" 'first(.result.workspaces[] | select(.label == $l) | .workspace_id) // empty' 2>/dev/null || true)
    if [ -n "$repose_id" ]; then herdr workspace rename "$repose_id" ` + herdrCheckoutLabel + ` >/dev/null 2>&1 || true; fi
  fi
fi
`

// herdrTidyShell, after herdrMainVar, closes the workspaces that only
// repeat the checkout's once the checkout has its own (DECISIONS I-596):
// `home`, which the server made while the machine had no checkout yet
// (its first boot comes before the first sync), and one with the old
// label that a base before I-597 made again at a start. It is the herdr
// side of the tmux `shell` window's respawn: only a workspace that is one
// tab with one pane whose shell runs nothing in the foreground goes;
// anything else is left as it is. It reads the pane's process group and
// the shell's pid from herdr, nothing more.
const herdrTidyShell = `repose_old=${repose_main##*/}
if [ "$repose_main" != "$HOME" ] && repose_wl=$(herdr workspace list 2>/dev/null) && printf '%s' "$repose_wl" | jq -e '.result.workspaces | any(.label == "` + herdrCheckoutLabel + `")' >/dev/null 2>&1; then
  for repose_id in $(printf '%s' "$repose_wl" | jq -r --arg l "$repose_old" '.result.workspaces[] | select((.label == "home" or .label == $l) and .label != "` + herdrCheckoutLabel + `" and .tab_count == 1 and .pane_count == 1) | .workspace_id' 2>/dev/null || true); do
    repose_pp=$(herdr pane list 2>/dev/null | jq -r --arg w "$repose_id" 'first(.result.panes[] | select(.workspace_id == $w) | .pane_id) // empty' 2>/dev/null || true)
    [ -n "$repose_pp" ] || continue
    if herdr pane process-info --pane "$repose_pp" 2>/dev/null | jq -e '.result.process_info | .foreground_process_group_id == .shell_pid' >/dev/null 2>&1; then
      herdr workspace close "$repose_id" >/dev/null 2>&1 || true
    fi
  done
fi
`

// herdrStateScript prints the folder's workspace label (`checkout` for
// the machine's own checkout, with its old label as "#alias", `home` with
// none, another checkout's name), then herdr's agents and workspaces,
// each answer on its own line behind a marker.
func herdrStateScript(slug, extra string) string {
	return herdrMainVar(slug) + checkoutVar(slug, extra) + `repose_d=$repose_co
` + herdrLabelShell + `printf '#label %s\n' "$repose_l"
if [ "$repose_l" = ` + herdrCheckoutLabel + ` ]; then printf '#alias %s\n' "${repose_d##*/}"; fi
printf '#agents '; herdr agent list
printf '#workspaces '; herdr workspace list
`
}

// parseHerdrState reads herdrStateScript's output.
func parseHerdrState(out string) (herdrState, error) {
	var st herdrState
	seen := 0
	for _, l := range nonEmptyLines(out) {
		switch {
		case strings.HasPrefix(l, "#label "):
			st.Label = strings.TrimPrefix(l, "#label ")
		case strings.HasPrefix(l, "#alias "):
			st.Alias = strings.TrimPrefix(l, "#alias ")
		case strings.HasPrefix(l, "#agents "):
			var r herdrReply
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "#agents ")), &r); err != nil {
				return st, fmt.Errorf("herdr's agent list: %w", err)
			}
			if r.Error != nil {
				return st, fmt.Errorf("herdr agent list: %s", r.Error.Code)
			}
			st.Agents = r.Result.Agents
			seen++
		case strings.HasPrefix(l, "#workspaces "):
			var r herdrReply
			if err := json.Unmarshal([]byte(strings.TrimPrefix(l, "#workspaces ")), &r); err != nil {
				return st, fmt.Errorf("herdr's workspace list: %w", err)
			}
			if r.Error != nil {
				return st, fmt.Errorf("herdr workspace list: %s", r.Error.Code)
			}
			st.Workspaces = r.Result.Workspaces
			seen++
		}
	}
	if seen != 2 {
		return st, fmt.Errorf("herdr did not answer")
	}
	return st, nil
}

func herdrListState(ctx context.Context, t sshTarget, slug string) (herdrState, error) {
	out, err := runSSH(ctx, t, herdrStateScript(slug, t.Checkout), nil)
	if err != nil {
		return herdrState{}, err
	}
	return parseHerdrState(string(out))
}

// workspaceLabel maps workspace ids to labels.
func (st herdrState) workspaceLabel() map[string]string {
	m := map[string]string{}
	for _, w := range st.Workspaces {
		m[w.ID] = w.Label
	}
	return m
}

// pick is PickName over a listing: the lowest free "<agent>" or
// "<agent>-N" among every agent name on the server (herdr refuses a
// taken name), and whether the folder's workspace already has one of
// the same agent.
func (st herdrState) pick(agent string) (string, bool) {
	labels := st.workspaceLabel()
	taken := map[string]bool{}
	others := false
	for _, a := range st.Agents {
		if a.Name != "" {
			taken[a.Name] = true
		}
		if l := labels[a.WorkspaceID]; a.Agent == agent && (l == st.Label || (st.Alias != "" && l == st.Alias)) {
			others = true
		}
	}
	return nextWindowName(agent, func(n string) bool { return taken[n] }), others
}

func (herdrMux) PickName(ctx context.Context, t sshTarget, slug, agent string) (string, bool, error) {
	st, err := herdrListState(ctx, t, slug)
	if err != nil {
		return "", false, err
	}
	name, others := st.pick(agent)
	return name, others, nil
}

// NamesScript prints herdr's agent names; an answer that is an error
// (no .result.agents) fails jq, and set -e the probe.
func (herdrMux) NamesScript(slug string) string {
	return `repose_n=$(herdr agent list)
printf '%s\n' "$repose_n" | jq -r '.result.agents[] | .name // empty | select(. != "") | "#window " + .'
`
}

// herdrDir is a guest folder ("~/<name>", "" for the checkout) as a
// shell word, after checkoutVar.
func herdrDir(dir string) string {
	if dir == "" {
		return `"$repose_co"`
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		return homeShell(rest)
	}
	return shQuote(dir)
}

// herdrAgentStartTimeout is herdr's own cap on `agent start` (300 s,
// AgentStartParams); the CLI waits past it with `agent wait` up to
// devShellLoadTimeout (I-509).
const herdrAgentStartTimeout = 300 * time.Second

// herdrStartScript opens the agent's tab and starts the agent in it
// (features/run-and-attach.md "A prompt on herdr", steps 1, 2, 4, 5). It
// prints "#pane <id>" and "#start <status or error code>"; with
// attachOnly it types the agent's command into the tab, prints no
// "#start", and focuses the tab: `herdr pane run` gives the pane no
// agent name, so the attach's `herdr agent focus <name>` cannot find it,
// and the user must land in this tab (the Claude Code login).
func herdrStartScript(s agentStart, extra string) string {
	focus := "--no-focus"
	if s.AttachOnly {
		focus = "--focus"
	}
	var b strings.Builder
	b.WriteString(herdrMainVar(s.Slug))
	b.WriteString(checkoutVar(s.Slug, extra))
	fmt.Fprintf(&b, "repose_d=%s\n", herdrDir(s.Dir))
	b.WriteString(agentTrustScript(s.Agent, `"$repose_d"`, s.MCPApprovals))
	b.WriteString("repose_ws=\n")
	if s.Worktree {
		b.WriteString(`repose_ws=$(herdr worktree open --path "$repose_d" --no-focus 2>/dev/null | jq -r '.result.workspace.workspace_id // empty' 2>/dev/null)
repose_l=${repose_d##*/}
`)
	} else {
		b.WriteString(herdrLabelShell)
		b.WriteString(`if [ "$repose_l" = ` + herdrCheckoutLabel + ` ]; then
` + herdrRenameShell + `fi
`)
	}
	b.WriteString(`if [ -z "$repose_ws" ]; then
  repose_ws=$(herdr workspace list | jq -r --arg l "$repose_l" 'first(.result.workspaces[] | select(.label == $l) | .workspace_id) // empty')
  [ -n "$repose_ws" ] || repose_ws=$(herdr workspace create --cwd "$repose_d" --label "$repose_l" --no-focus | jq -r '.result.workspace.workspace_id // empty')
fi
[ -n "$repose_ws" ] || { echo 'herdr made no workspace' >&2; exit 1; }
if [ "$repose_l" = ` + herdrCheckoutLabel + ` ]; then
` + herdrTidyShell + `fi
`)
	fmt.Fprintf(&b, `repose_p=$(herdr tab create --workspace "$repose_ws" --cwd "$repose_d" --label %[1]s %[2]s | jq -r '.result.root_pane.pane_id // empty')
[ -n "$repose_p" ] || { echo 'herdr made no tab' >&2; exit 1; }
printf '#pane %%s\n' "$repose_p"
`, shQuote(s.Name), focus)
	if s.AttachOnly {
		fmt.Fprintf(&b, "herdr pane run \"$repose_p\" %s >/dev/null\n", shQuote(s.Agent))
		if s.Prompt != "" {
			b.WriteString(pendingPromptHerdr(s.Prompt)) // typed after the login (I-607)
		}
		return b.String()
	}
	fmt.Fprintf(&b, `repose_r=$(herdr agent start %s --kind %s --pane "$repose_p" --timeout %d 2>&1)
printf '#start %%s\n' "$(printf '%%s\n' "$repose_r" | `+herdrStatusJQ+`)"
`, shQuote(s.Name), shQuote(s.Agent), herdrAgentStartTimeout.Milliseconds())
	return b.String()
}

// herdrStatusJQ reads an agent's status, or the error's code, from a
// herdr answer, which comes on stdout or (an error) on stderr; any line
// that is not JSON is skipped.
const herdrStatusJQ = `jq -rR 'fromjson? | (.result.agent.agent_status // .error.code // empty)' 2>/dev/null | head -n 1`

// herdrWaitScript waits, for up to secs seconds, until agent name is
// idle or blocked, and prints "#start <status>", or "#start timeout".
// An agent herdr has not detected yet (the wrapper still loading the dev
// shell) answers agent_not_found, and is asked again.
func herdrWaitScript(name string, secs int) string {
	return fmt.Sprintf(`repose_end=$(( $(date +%%s) + %[2]d ))
while :; do
  repose_left=$(( repose_end - $(date +%%s) ))
  [ "$repose_left" -gt 0 ] || { echo '#start timeout'; exit 0; }
  [ "$repose_left" -le 60 ] || repose_left=60
  repose_r=$(herdr agent wait %[1]s --until idle --until blocked --timeout $(( repose_left * 1000 )) 2>&1)
  repose_s=$(printf '%%s\n' "$repose_r" | `+herdrStatusJQ+`)
  case "$repose_s" in
    idle|blocked|done) printf '#start %%s\n' "$repose_s"; exit 0 ;;
    timeout|agent_not_found|agent_not_running) sleep 1 ;;
    *) printf '#start %%s\n' "$repose_s"; exit 0 ;;
  esac
done
`, shQuote(name), secs)
}

// herdrPromptScript types the prompt with `herdr agent prompt` and
// prints its answer's status or error code (step 7).
func herdrPromptScript(name, prompt string) string {
	return fmt.Sprintf(`repose_r=$(herdr agent prompt %s %s --wait --until working --until blocked --timeout 10000 2>&1)
printf '#prompt %%s\n' "$(printf '%%s\n' "$repose_r" | `+herdrStatusJQ+`)"
`, shQuote(name), shQuote(prompt))
}

// marker is the value after "#<key> " in a script's output.
func marker(out, key string) string {
	for _, l := range nonEmptyLines(out) {
		if v, ok := strings.CutPrefix(l, "#"+key+" "); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// StartAgent is "A prompt on herdr" (features/run-and-attach.md): the
// workspace, a tab, `herdr agent start`, waiting past its 300 s cap with
// `herdr agent wait` up to devShellLoadTimeout, then `herdr agent
// prompt`. An agent that settles on blocked gets no prompt (I-486).
func (herdrMux) StartAgent(ctx context.Context, t sshTarget, s agentStart) error {
	started := time.Now()
	out, err := runSSH(ctx, t, herdrStartScript(s, t.Checkout), nil)
	if err != nil {
		return err
	}
	if s.AttachOnly {
		return nil
	}
	status := marker(string(out), "start")
	if status == "timeout" {
		if s.OnLoading != nil {
			s.OnLoading()
		}
		left := devShellLoadTimeout - time.Since(started)
		if left > 0 {
			out, err := runSSH(ctx, t, herdrWaitScript(s.Name, int(left/time.Second)), nil)
			if err != nil {
				return err
			}
			status = marker(string(out), "start")
		}
	}
	switch status {
	case "blocked":
		return &agentDialogError{agent: s.Agent}
	case "idle", "done", "working", "timeout":
		// timeout: still not ready after 30 minutes; type the prompt
		// anyway rather than hang, as the tmux path does.
	default:
		return fmt.Errorf("herdr could not start %s (%s)", s.Agent, orDash(status))
	}
	out, err = runSSH(ctx, t, herdrPromptScript(s.Name, s.Prompt), nil)
	if err != nil {
		return err
	}
	switch p := marker(string(out), "prompt"); p {
	case "working", "blocked", "idle", "done", "agent_prompt_stalled", "timeout":
		// Typed: blocked after the prompt is the agent asking about the
		// prompt itself; stalled and timeout mean herdr saw no change
		// yet, which is not a failure to type.
		return nil
	case "agent_blocked":
		return &agentDialogError{agent: s.Agent}
	default:
		return fmt.Errorf("herdr could not type the prompt (%s)", orDash(p))
	}
}

// herdrFocusScript runs before an attach: with extra (attach
// PROJECT:CHECKOUT, I-480) it focuses that checkout's workspace, creating
// it in /home/dev/<extra> when missing (a checkout the machine does not
// have exits 2); with an agent name it focuses that agent.
func herdrFocusScript(slug, extra, agentName string) string {
	var b strings.Builder
	if extra != "" {
		missing := fmt.Sprintf("%s has no checkout %s. `repose run --on %s` in its folder adds it.", slug, extra, slug)
		b.WriteString(checkoutVar(slug, extra))
		fmt.Fprintf(&b, `[ -d "$repose_co" ] || { printf '%%s\n' %[2]s >&2; exit 2; }
repose_ws=$(herdr workspace list | jq -r --arg l %[1]s 'first(.result.workspaces[] | select(.label == $l) | .workspace_id) // empty')
[ -n "$repose_ws" ] || repose_ws=$(herdr workspace create --cwd "$repose_co" --label %[1]s --no-focus | jq -r '.result.workspace.workspace_id // empty')
[ -z "$repose_ws" ] || herdr workspace focus "$repose_ws" >/dev/null 2>&1 || true
`, shQuote(extra), shQuote(missing))
	}
	if agentName != "" {
		fmt.Fprintf(&b, "herdr agent focus %s >/dev/null 2>&1 || true\n", shQuote(agentName))
	}
	return b.String()
}

// herdrFocusNamedScript is herdrFocusScript for `attach --window NAME`
// (I-606): an agent herdr does not have exits 2 with noWindow's line.
func herdrFocusNamedScript(slug, extra, agentName string) string {
	missing := fmt.Sprintf("%s has no agent %s. `repose ps %s` lists them.", slug, agentName, slug)
	return herdrFocusScript(slug, extra, "") + fmt.Sprintf("herdr agent focus %s >/dev/null 2>&1 || { printf '%%s\\n' %s >&2; exit 2; }\n", shQuote(agentName), shQuote(missing))
}

// Attach is the attach rule (features/run-and-attach.md "herdr
// projects"), first match wins: in a laptop herdr pane with the machine
// in its sidebar, the CLI stays as the session helper and opens no
// client; with herdr 0.9.0 or newer on the laptop, `herdr --remote` as
// the CLI's child; otherwise `ssh -t <slug>.repose herdr` under the
// input proxy and the reattacher.
//
// Everything before the client opens follows the command's context, so
// Ctrl-C there ends the command at once ("Interrupted.", exit 130)
// instead of being swallowed by the CLI's handler while a sidebar add or
// the focus step runs on (DECISIONS I-598).
func (herdrMux) Attach(e *Env, a attachReq) error {
	ctx := a.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	slug := a.Project.Slug
	script := herdrFocusScript(slug, a.Target.Checkout, a.Window)
	if a.Named {
		script = herdrFocusNamedScript(slug, a.Target.Checkout, a.Window)
	}
	if script != "" {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := runSSH(fctx, a.Target, script, nil)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == 2 {
			return exitf(ExitUsage, "%s", strings.TrimSpace(se.Stderr))
		}
		if err != nil {
			// The attach goes on: herdr opens on its last tab instead.
			_, _ = fmt.Fprintf(e.ErrOut, "Could not pick the tab on %s: %v\n", slug, err)
		}
	}
	lh := laptopHerdr()
	path := chooseHerdrPath(lh, a.Project, a.Target, func() bool {
		ok, _ := lh.ensureEntry(ctx, e, slug)
		return ok
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch path {
	case herdrPathSidebar:
		if a.Release != nil {
			a.Release()
		}
		_, _ = fmt.Fprintln(e.Out, sidebarLine(slug, a.Helper))
		return runHelperForeground(ctx, e, a.Helper)
	case herdrPathRemote:
		startSessionHelper(e, a.Helper)
		err := lh.remote(slug)
		if a.After != nil {
			a.After()
		}
		return err
	}
	startSessionHelper(e, a.Helper)
	return attachSSH(a.Target, slug, herdrAttachCommand, herdrAttachCommand, a.TZ, a.RepoDir, a.After, a.Renew)
}

// herdrPath is one of the attach rule's three paths.
type herdrPath int

const (
	herdrPathSSH     herdrPath = iota // ssh -t <slug>.repose herdr
	herdrPathSidebar                  // the laptop herdr pane: no client
	herdrPathRemote                   // herdr --remote as a child
)

func (p herdrPath) String() string {
	return [...]string{"ssh", "sidebar", "remote"}[p]
}

// chooseHerdrPath applies the attach rule, first match wins. inSidebar
// is asked last, since it may add the entry.
func chooseHerdrPath(lh *laptopHerdrCLI, p *Project, t sshTarget, inSidebar func() bool) herdrPath {
	if lh == nil {
		return herdrPathSSH
	}
	if inHerdrPane() && inSidebar() {
		return herdrPathSidebar
	}
	if plainAlias(t, p.Slug) {
		return herdrPathRemote
	}
	return herdrPathSSH
}

// herdrAttachCommand is the guest side of rule 3: herdr's client on the
// server the unit runs.
const herdrAttachCommand = "exec herdr"

// plainAlias says t is `<slug>.repose` alone, which a laptop herdr can
// reach through ~/.ssh/config; a target that needs -F (the Include line
// missing, tests) cannot be handed to herdr.
func plainAlias(t sshTarget, slug string) bool {
	return len(t.Args) == 1 && t.Args[0] == slug+".repose"
}

// helperStays says the session helper has something that lasts as long
// as the attach (port and MCP forwards, the browser bridge), as opposed
// to the carry alone, which ends by itself.
func helperStays(opts sessionOptions) bool {
	return opts.Forward || opts.Bridge || len(opts.MCP) > 0
}

// sidebarLine is what rule 1 prints: the machine is in the sidebar, and,
// when the command stays to keep forwards up, how it ends (I-598), so a
// command that does not return reads as what it is.
func sidebarLine(slug string, opts sessionOptions) string {
	if helperStays(opts) {
		return slug + " is in herdr's sidebar. Ctrl-C ends its forwards."
	}
	return slug + " is in herdr's sidebar."
}

// runHelperForeground is rule 1: the session helper (forwards, the
// bridge, the carry) in this process until Ctrl-C (or SIGTERM), which
// exits 0; with nothing that lasts (forwards off, no bridge) it returns
// once the carry is done. Its messages print here, in the laptop pane
// the user ran it from. A Ctrl-C that came before it started (ctx done)
// ends it at once. After the first Ctrl-C the helper's handler is gone,
// so a second one ends the process while the forwards are taken down
// (I-598).
func runHelperForeground(parent context.Context, e *Env, opts sessionOptions) error {
	if parent == nil {
		parent = context.Background()
	}
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() { <-ctx.Done(); stop() }()
	show := func(msg string) { _, _ = fmt.Fprintln(e.ErrOut, msg) }
	alive := func() bool { return ctx.Err() == nil }
	if !opts.Carry && !helperStays(opts) {
		return nil
	}
	_ = runSessionWith(ctx, opts, alive, show)
	return nil
}

// herdrPasteScript finds the pane: agent window's, or the focused pane
// of the focused workspace (any focused pane, else the first, when herdr
// marks none), sends the path with `herdr pane send-text`, no Enter, and
// prints the agent's name there, or the pane id.
func (herdrMux) PasteScript(slug, window string) string {
	find := `repose_w=$(herdr workspace list | jq -r 'first(.result.workspaces[] | select(.focused) | .workspace_id) // empty')
p=$(herdr pane list | jq -r --arg w "$repose_w" '[.result.panes[]] | (first(.[] | select(.focused and .workspace_id == $w)) // first(.[] | select(.focused)) // first(.[]) // {}) | .pane_id // empty')`
	if window != "" {
		find = fmt.Sprintf(`p=$(herdr agent list | jq -r --arg n %s 'first(.result.agents[] | select(.name == $n) | .pane_id) // empty')`, shQuote(window))
	}
	return find + fmt.Sprintf(`
[ -n "$p" ] || exit %d
herdr pane send-text "$p" "$f" >/dev/null || exit 1
n=$(herdr agent list | jq -r --arg p "$p" 'first(.result.agents[] | select(.pane_id == $p) | .name // empty) // empty')
printf '%%s\n' "${n:-$p}"
`, pasteExitNoPane)
}

// herdrAgentPane is shell that sets $p to the pane of the herdr agent
// named name, or exits typeExitNoWindow.
func herdrAgentPane(name string) string {
	return fmt.Sprintf(`p=$(herdr agent list | jq -r --arg n %s 'first(.result.agents[] | select(.name == $n) | .pane_id) // empty')
[ -n "$p" ] || exit %d
`, shQuote(name), typeExitNoWindow)
}

// TypeScript sends the text to the agent's pane, then Enter. Not
// `herdr agent prompt`: it refuses an agent that is blocked, and
// answering one that is is what `run -w` is for.
func (herdrMux) TypeScript(slug, window, text string) string {
	return herdrAgentPane(window) + fmt.Sprintf(`herdr pane send-text "$p" %s >/dev/null && herdr pane send-keys "$p" enter >/dev/null
`, shQuote(text))
}

// CloseScript closes the agent's pane.
func (herdrMux) CloseScript(slug, window string) string {
	return herdrAgentPane(window) + `herdr pane close "$p" >/dev/null
`
}

// MessageScript is a herdr notification titled repose (herdr shows it
// only when its config turns notifications on; with no client attached
// nobody sees it, as with tmux).
func (herdrMux) MessageScript(slug, text string) string {
	return fmt.Sprintf("herdr notification show repose --body %s >/dev/null 2>&1 || true", shQuote(text))
}

// SessionEnded is never true on herdr: with a client attached, herdr
// opens a fresh workspace and shell as soon as its last pane closes, so
// a session never reaches its end (I-542). A temporary herdr machine goes
// at its expiry (I-602).
func (herdrMux) SessionEnded(context.Context, sshTarget, string) (bool, error) {
	return false, nil
}

// herdrMessage is the session helper's message on herdr: one
// notification, tried again for a moment while herdr says another is
// showing or it is rate limited.
func herdrMessage(ctx context.Context, t sshTarget, msg string) {
	cmd := fmt.Sprintf("herdr notification show repose --body %s 2>/dev/null | jq -r '.result.reason // empty'", shQuote(msg))
	deadline := time.Now().Add(tmuxMessageWait)
	for {
		out, err := runSSH(ctx, t, cmd, nil)
		r := strings.TrimSpace(string(out))
		if err != nil || (r != "busy" && r != "rate_limited") || time.Now().After(deadline) {
			if r == "shown" {
				_ = sleepOrDone(ctx, 1500*time.Millisecond)
			}
			return
		}
		if sleepOrDone(ctx, 500*time.Millisecond) != nil {
			return
		}
	}
}

// laptopHerdrMin is the oldest laptop herdr the CLI drives: 0.9.0 is the
// first that accepts a remote of endpoint generation 1 (I-501).
var laptopHerdrMin = [3]int{0, 9, 0}

// laptopHerdrCLI is the laptop's herdr binary.
type laptopHerdrCLI struct {
	path string
}

// laptopHerdr is the laptop's herdr when it is on PATH and 0.9.0 or
// newer, else nil.
func laptopHerdr() *laptopHerdrCLI {
	path, err := lookHerdr("herdr")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return nil
	}
	v, ok := parseHerdrVersion(string(out))
	if !ok || versionLess(v, laptopHerdrMin) {
		return nil
	}
	return &laptopHerdrCLI{path: path}
}

// parseHerdrVersion reads "herdr 0.9.3" (or a bare "0.9.3", a "v", a
// "-preview" suffix).
func parseHerdrVersion(s string) ([3]int, bool) {
	var v [3]int
	f := strings.Fields(s)
	if len(f) == 0 {
		return v, false
	}
	w := strings.TrimPrefix(f[len(f)-1], "v")
	w, _, _ = strings.Cut(w, "-")
	w, _, _ = strings.Cut(w, "+")
	parts := strings.Split(w, ".")
	if len(parts) < 2 {
		return v, false
	}
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// remote is rule 2: `herdr --remote <slug>.repose --session default` as
// a child with the terminal, so the session helper lives as long as it
// does. herdr's exit status is the command's. herdr runs ssh on its own
// for hours (each reconnect), so it gets an environment without
// REPOSE_SSH_PREPARED: its ssh then runs `repose ssh-prepare`, which
// renews the certificate the gateway ends a connection at (I-436).
func (h *laptopHerdrCLI) remote(slug string) error {
	cmd := exec.Command(h.path, "--remote", slug+".repose", "--session", "default")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = envWithoutSSHPrepared()
	// Ctrl-C belongs to herdr's client; the CLI waits for it. A channel of
	// its own catches the signal meanwhile: signal.Ignore and Reset would
	// drop the command's own NotifyContext (cli.go) for the rest of the
	// run, After hook included, and the child would inherit SIG_IGN.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	// The terminal as herdr found it: a herdr killed by a signal (a Ctrl-C
	// that came while it connected, before its own handler; a crash)
	// leaves raw mode and its screen modes on, and the shell after the CLI
	// would read keys without echo (I-598).
	fd := int(os.Stdin.Fd())
	saved, _ := term.GetState(fd)
	timingf("herdr --remote (attach)")
	err := cmd.Run()
	if saved != nil {
		_ = term.Restore(fd, saved)
		if cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
			_, _ = os.Stdout.WriteString(terminalReset)
		}
	}
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return silent(xe.ExitCode())
	}
	if err != nil {
		return exitf(ExitGeneric, "Could not run herdr: %v.", err)
	}
	return nil
}

// terminalReset turns off what a full-screen client switches on, for
// when it died without doing so itself: the alternate screen (back to
// the normal one), a hidden cursor, mouse reporting, bracketed paste and
// the kitty keyboard protocol.
const terminalReset = "\x1b[?1049l\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[<u"
