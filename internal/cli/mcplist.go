package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/heracraft/repose/internal/mcpreg"
)

// mcpStatusCommand is what `repose mcp list` runs on the machine: it reads
// the agents' configs and the registry, and starts no server
// (docs/interfaces/guest-conventions.md "MCP registry").
const mcpStatusCommand = "repose-mcp status --json"

// guestHome is the home a checkout path on the machine starts with.
const guestHome = "/home/dev"

// MCPListCmd implements `repose mcp list [PROJECT]` (DECISIONS I-558).
func MCPListCmd(ctx context.Context, e *Env, projectArg string) error {
	project, target, err := connectRunning(ctx, e, projectArg)
	if err != nil {
		return err
	}
	return mcpListOn(ctx, e, target, project.Slug)
}

// mcpListOn is MCPListCmd once the machine is reachable.
func mcpListOn(ctx context.Context, e *Env, target sshTarget, slug string) error {
	out, err := runSSH(ctx, target, mcpStatusCommand, nil)
	if err != nil {
		var se *sshError
		if errors.As(err, &se) && se.ExitCode == 127 {
			return exitf(ExitGeneric, "The base of %s predates repose mcp list; it works after the machine's next update.", slug)
		}
		return stepFailed("read the MCP servers of "+slug, err, "")
	}
	var st mcpreg.Status
	if err := json.Unmarshal(out, &st); err != nil {
		return stepFailed("read the MCP servers of "+slug, fmt.Errorf("the machine's answer is not JSON"), "")
	}
	if e.JSON {
		// The machine's own document, as it sent it.
		_, err := e.Out.Write(out)
		return err
	}
	if err := writeMCPList(e.Out, st.Servers, writerIsTerminal(e.Out)); err != nil {
		return err
	}
	// A registry file the machine left out (mcpreg.Load), after the rows.
	for _, pr := range st.Problems {
		_, _ = fmt.Fprintf(e.ErrOut, "On %s: %s.\n", slug, strings.TrimSuffix(terminalText(pr), "."))
	}
	return nil
}

// writeMCPList prints one row per server: a table with a header on a
// terminal, else one tab-separated line per server, as `secrets list`.
// Every field passes terminalText: names come from any checkout's
// .mcp.json, which a cloned repository writes.
func writeMCPList(w io.Writer, servers []mcpreg.ServerStatus, tty bool) error {
	if !tty {
		for _, s := range servers {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", terminalText(s.Name), terminalText(s.From), mcpAgents(s), mcpState(s)); err != nil {
				return err
			}
		}
		return nil
	}
	if len(servers) == 0 {
		_, err := fmt.Fprintln(w, "No MCP servers.")
		return err
	}
	var b bytes.Buffer
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tFROM\tAGENTS\tSTATE")
	for _, s := range servers {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", terminalText(s.Name), terminalText(s.From), mcpAgents(s), mcpState(s))
	}
	_ = tw.Flush() // writes to memory
	// An empty STATE leaves the padding of AGENTS at the end of its line.
	var out strings.Builder
	for _, l := range strings.SplitAfter(b.String(), "\n") {
		if l == "" {
			continue
		}
		out.WriteString(strings.TrimRight(l, " \n") + "\n")
	}
	_, err := io.WriteString(w, out.String())
	return err
}

func mcpAgents(s mcpreg.ServerStatus) string {
	if len(s.Agents) == 0 {
		return "none"
	}
	return terminalText(strings.Join(s.Agents, " "))
}

// mcpState is the row's state, after the checkout a project server belongs
// to, so two checkouts' servers of one name tell apart.
func mcpState(s mcpreg.ServerStatus) string {
	state := terminalText(strings.ReplaceAll(s.State, "\t", " "))
	if s.Checkout == "" {
		return state
	}
	co := terminalText(s.Checkout)
	if rest, ok := strings.CutPrefix(co, guestHome+"/"); ok {
		co = "~/" + rest
	}
	if state == "" {
		return co
	}
	return co + "; " + state
}
