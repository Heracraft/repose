package cli

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/heracraft/repose/internal/mcpreg"
	"github.com/heracraft/repose/internal/mcpshim"
)

// MCPForwardOptions are `repose mcp forward`'s arguments and flags.
type MCPForwardOptions struct {
	Names []string
	// Inline is the command after --, for one NAME no config defines.
	Inline []string
	Remove bool
}

// mcpForwardName checks one NAME: what the machine's agents can list and
// `repose-mcp NAME` can start.
func mcpForwardName(n string) error {
	if !mcpreg.ValidName(n) || mcpreg.Reserved[n] {
		return fmt.Errorf("%q is not a server name repose can forward: %s", n, mcpNameRule)
	}
	return nil
}

// mcpNameRule is what a forwarded server's name may be.
const mcpNameRule = "letters, digits, - and _, at most 64, and not sync, run, status, hold or help"

// mcpDefs is each NAME's definition on this laptop.
func mcpDefs(home, repoDir string, opts MCPForwardOptions) (map[string]laptopMCP, error) {
	defs := map[string]laptopMCP{}
	if len(opts.Inline) > 0 {
		dir := repoDir
		if dir == "" {
			dir = home
		}
		n := opts.Names[0]
		defs[n] = laptopMCP{Name: n, Command: opts.Inline[0], Args: opts.Inline[1:], Env: map[string]string{}, Dir: dir, Source: "the command line"}
		return defs, nil
	}
	for _, n := range opts.Names {
		d, err := findLaptopMCP(home, repoDir, n)
		if err != nil {
			return nil, err
		}
		defs[n] = d
	}
	return defs, nil
}

func toolCount(n int) string {
	if n == 1 {
		return "1 tool"
	}
	return fmt.Sprintf("%d tools", n)
}

// mcpFailedLine is a server that did not start: on the laptop, with its
// last stderr line, or on the machine (machineFailure).
func mcpFailedLine(slug, name, reason, tail string) string {
	if r, ok := strings.CutPrefix(reason, machineFailure); ok {
		return fmt.Sprintf("%s: %s could not take the forward: %s.", name, slug, strings.TrimSuffix(r, "."))
	}
	l := fmt.Sprintf("%s did not start on this laptop: %s.", name, strings.TrimSuffix(reason, "."))
	if tail != "" {
		l += " It said: " + terminalText(tail)
	}
	return l
}

// endOnHangup is ctx ended also by SIGHUP (the terminal closed) and
// SIGTERM, so the laptop's servers, in process groups of their own that
// those signals never reach, are stopped with the forward.
func endOnHangup(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, syscall.SIGHUP, syscall.SIGTERM)
}

// machineFailure prefixes a failed reason that happened on the machine
// (mcpshim.Ready.Where), as the laptop end passes it to failed.
const machineFailure = "machine: "

// mcpOldBaseLine is a machine whose base has no forward.
func mcpOldBaseLine(slug string) string {
	return fmt.Sprintf("The base of %s predates the MCP forward; it works after the machine's next update.", slug)
}

// MCPForwardCmd implements `repose mcp forward NAME...`.
func MCPForwardCmd(ctx context.Context, e *Env, projectArg string, opts MCPForwardOptions) error {
	var defs map[string]laptopMCP
	if !opts.Remove {
		// The laptop's side first: a name it lacks answers at once.
		var err error
		if defs, err = mcpDefs(e.HomeDir, gitRepoRoot(e.Cwd), opts); err != nil {
			return err
		}
	}
	// A running machine only, as `browser bridge` (I-312).
	project, target, err := connectRunning(ctx, e, projectArg)
	if err != nil {
		return err
	}
	slug := project.Slug
	if opts.Remove {
		return mcpRemove(ctx, target, slug, opts.Names)
	}
	ctx, stopSignals := endOnHangup(ctx)
	defer stopSignals()
	var outMu sync.Mutex
	out := func(s string) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = fmt.Fprintln(e.Out, s)
	}
	errOut := func(s string) {
		outMu.Lock()
		defer outMu.Unlock()
		_, _ = fmt.Fprintln(e.ErrOut, s)
	}
	var mu sync.Mutex
	ready, failed := 0, 0
	toldCtrlC := false
	// settled says how to end the forward once every name has an answer,
	// whichever answer comes last; a single name says it on its own line.
	settled := func() string {
		mu.Lock()
		defer mu.Unlock()
		if toldCtrlC || ready+failed < len(defs) || ready == 0 {
			return ""
		}
		toldCtrlC = true
		if ready == 1 {
			return "Ctrl-C ends it."
		}
		return "Ctrl-C ends the forwards."
	}
	ui := mcpForwardUI{
		ready: func(r mcpshim.Ready, again bool) {
			if again {
				out(fmt.Sprintf("%s: forwarded to %s again.", r.Name, slug))
				return
			}
			l := fmt.Sprintf("%s: forwarded to %s (%s).", r.Name, slug, toolCount(r.Tools))
			switch {
			case r.New:
				l += " Agents already running list it after a restart."
			case r.Changed:
				// Codex alone ignores list_changed (critic correction 9).
				l += " Codex sessions already running see the new tools after a restart."
			}
			mu.Lock()
			ready++
			mu.Unlock()
			c := settled()
			if c != "" && len(defs) == 1 {
				l += " " + c
				c = ""
			}
			out(l)
			if c != "" {
				out(c)
			}
		},
		failed: func(name, reason, tail string) {
			mu.Lock()
			failed++
			mu.Unlock()
			errOut(mcpFailedLine(slug, name, reason, tail))
			if c := settled(); c != "" {
				out(c)
			}
		},
		gone: func(name string) {
			out(fmt.Sprintf("%s: another forward to %s took it over.", name, slug))
		},
		call: func(agent, server, tool string) { out(fmt.Sprintf("%s called %s.%s", agent, server, tool)) },
		lost: func() { errOut(fmt.Sprintf("Lost the connection to %s; reconnecting.", slug)) },
		running: func() error {
			_, err := requireRunningProject(ctx, e, slug)
			return err
		},
	}
	err = runMCPForward(ctx, target, defs, ui)
	switch {
	case ctx.Err() != nil:
		// Ctrl-C is how a forward ends: not an interruption.
		return nil
	case err == nil:
		return nil
	case errors.Is(err, errMCPOldBase):
		return exitf(ExitGeneric, "%s", mcpOldBaseLine(slug))
	case errors.Is(err, errMCPNotFrames):
		return exitf(ExitGeneric, "Could not forward to %s: %s.", slug, err)
	case errors.As(err, new(*exitError)):
		return err // the machine stopped (exit 5), from running
	case errors.Is(err, errMCPNothingLeft):
		mu.Lock()
		defer mu.Unlock()
		if failed > 0 {
			return silent(ExitGeneric) // each failure is printed
		}
		return nil // every name went to a newer forward
	}
	return stepFailed("keep the forward to "+slug+" open", err, "")
}

// mcpRemove is --remove: forward/NAME.json goes, and the agents drop NAME
// at their next start.
func mcpRemove(ctx context.Context, t sshTarget, slug string, names []string) error {
	out, err := runSSH(ctx, t, mcpHoldCommand(names, "--remove"), nil)
	if err != nil {
		var se *sshError
		if errors.As(err, &se) && (se.ExitCode == 127 || strings.Contains(se.Stderr, mcpOldHoldText)) {
			return exitf(ExitGeneric, "%s", mcpOldBaseLine(slug))
		}
		return stepFailed("remove the forward from "+slug, err, "")
	}
	var missing []string
	for _, l := range strings.Fields(string(out)) {
		if mcpreg.ValidName(l) {
			missing = append(missing, l)
		}
	}
	if len(missing) > 0 {
		return exitf(ExitGeneric, "%s has no forwarded MCP server named %s.", slug, strings.Join(missing, ", "))
	}
	return nil
}

// runSessionMCP is [mcp] forward while attached: the same forward, beside
// the attach, for as long as it lasts. Success says nothing; a server that
// did not start, or a name this laptop lacks, says so through tmux.
func runSessionMCP(ctx context.Context, t sshTarget, names []string, home, repoDir string, say func(string), alive func() bool) {
	ctx, stopSignals := endOnHangup(ctx)
	defer stopSignals()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for alive() && ctx.Err() == nil {
			if sleepOrDone(ctx, time.Second) != nil {
				return
			}
		}
		cancel()
	}()
	defs := map[string]laptopMCP{}
	for _, n := range names {
		if err := mcpForwardName(n); err != nil {
			say(fmt.Sprintf("repose could not forward %q from [mcp] forward in config.toml: a name is %s.", n, mcpNameRule))
			continue
		}
		d, err := findLaptopMCP(home, repoDir, n)
		if err != nil {
			say("repose could not forward MCP server " + n + ": " + oneLine(err.Error()))
			continue
		}
		defs[n] = d
	}
	if len(defs) == 0 {
		return
	}
	err := runMCPForward(ctx, t, defs, mcpForwardUI{
		ready: func(mcpshim.Ready, bool) {},
		failed: func(name, reason, _ string) {
			if r, ok := strings.CutPrefix(reason, machineFailure); ok {
				say(fmt.Sprintf("The machine could not take the forward of MCP server %s: %s.", name, strings.TrimSuffix(r, ".")))
				return
			}
			say(fmt.Sprintf("MCP server %s did not start on your laptop: %s.", name, strings.TrimSuffix(reason, ".")))
		},
		gone:   func(string) {},
		call:   func(string, string, string) {},
		lost:   func() {},
		attach: true,
	})
	switch {
	case ctx.Err() != nil, err == nil, errors.Is(err, errMCPNothingLeft):
	case errors.Is(err, errMCPOldBase):
		say("This machine's base predates the MCP forward; [mcp] forward works after its next update.")
	default:
		say("repose could not forward your MCP servers: " + oneLine(err.Error()))
	}
}
