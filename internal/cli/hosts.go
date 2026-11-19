package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Hardware of your own (DECISIONS I-1366, I-1370): `repose host
// add|ls|rm` adds a machine you own to your account, lists what it is
// doing and takes it off again. One host per account, and your projects
// run on it instead of repose's servers.

var hostNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

func checkHostName(name string) error {
	if !hostNameRe.MatchString(name) {
		return exitf(ExitUsage, "NAME must match [a-z0-9][a-z0-9-]{0,31}, got %q", name)
	}
	return nil
}

// hostStateWord is the api's host state in the words a user reads. The
// states are hosts.state in docs/interfaces/db-schema.md.
func hostStateWord(state string) string {
	switch state {
	case "ready":
		return "online"
	case "unreachable":
		return "offline"
	case "registering":
		return "waiting"
	case "draining":
		return "removing"
	case "retired", "lost":
		return "removed"
	}
	return state
}

// HostAddCmd implements `repose host add [NAME]`. The install step is
// the whole of stdout, so it pipes; what to do with it is on stderr.
func HostAddCmd(ctx context.Context, e *Env, name string) error {
	if name != "" {
		if err := checkHostName(name); err != nil {
			return err
		}
	}
	h, err := e.Client.CreateHost(ctx, CreateHostRequest{Name: name})
	if err != nil {
		return hostErr(err, name)
	}
	if e.JSON {
		return writeJSONOut(e.Out, h)
	}
	// stdout is the one thing to carry to the machine, so it pipes: the
	// install step the api composed, or, while no install line is
	// recorded for this kind of machine, the token the install takes.
	installed := h.Install != nil && h.Install.Command != ""
	switch {
	case installed:
		_, _ = fmt.Fprintln(e.Out, h.Install.Command)
	case h.Install != nil && h.Install.Argument != "":
		_, _ = fmt.Fprintln(e.Out, h.Install.Argument)
	case h.JoinToken != "":
		_, _ = fmt.Fprintln(e.Out, h.JoinToken)
	default:
		return exitf(ExitGeneric, "%s was added, but this repose api sent nothing to install it with.", h.Name)
	}
	_, _ = fmt.Fprintln(e.ErrOut, hostAddedLine(h, installed))
	return nil
}

// hostAddedLine is what `repose host add` says on stderr: where the
// line on stdout goes, how long it lasts, and, when the api had no
// install step, which page has the rest.
func hostAddedLine(h *Host, installed bool) string {
	var b strings.Builder
	if installed {
		fmt.Fprintf(&b, "%s: run this on the machine you are adding.", h.Name)
	} else {
		fmt.Fprintf(&b, "%s: this is its join token.", h.Name)
	}
	if h.JoinTokenExpiresAt != nil {
		fmt.Fprintf(&b, " It works once, before %s.", h.JoinTokenExpiresAt.Local().Format("Mon 2 Jan 15:04"))
	} else {
		b.WriteString(" It works once.")
	}
	if !installed && h.Install != nil && h.Install.URL != "" {
		fmt.Fprintf(&b, " What to do with it: %s", h.Install.URL)
	}
	return b.String()
}

// HostLsCmd implements `repose host` and `repose host ls`.
func HostLsCmd(ctx context.Context, e *Env) error {
	hosts, err := e.Client.ListHosts(ctx)
	if err != nil {
		return hostErr(err, "")
	}
	if e.JSON {
		if hosts == nil {
			hosts = []Host{}
		}
		return writeJSONOut(e.Out, hosts)
	}
	// A host the user removed is not hardware they have; the api keeps
	// the row and the listing leaves it out. `--json` is the api's
	// answer unchanged.
	hosts = slices.DeleteFunc(hosts, func(h Host) bool { return h.State == "retired" })
	if e.Quiet {
		for _, h := range hosts {
			_, _ = fmt.Fprintln(e.Out, h.Name)
		}
		return nil
	}
	if len(hosts) == 0 {
		_, _ = fmt.Fprintln(e.Out, "You have no hardware of your own.")
		return nil
	}
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].CreatedAt.Before(hosts[j].CreatedAt) })
	tw := tabwriter.NewWriter(e.Out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "HOST\tSTATE\tMEMORY\tMACHINES\tRUNNING\tLAST SEEN")
	for _, h := range hosts {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%s\n",
			h.Name, hostStateWord(h.State), orDash(hostMemory(h)), h.Projects, h.RunningProjects, orDash(hostSeen(h)))
	}
	return tw.Flush()
}

// hostMemory is the host's memory, "" before it has registered and said
// how much it has. Whole gigabytes, because a machine is sold in them
// and reports a little less: a 24 GB Mac's meminfo is about 23.7 GiB,
// and "23.7 GB" reads as the wrong machine.
func hostMemory(h Host) string {
	if h.MemBytes <= 0 {
		return ""
	}
	if h.MemBytes < 1<<30 {
		return humanBytes(h.MemBytes)
	}
	return fmt.Sprintf("%d GB", (h.MemBytes+1<<29)>>30)
}

// hostSeen is when the host last spoke to the api, "" for one that
// never has.
func hostSeen(h Host) string {
	if h.LastHeartbeatAt == nil {
		return ""
	}
	return humanAge(*h.LastHeartbeatAt)
}

// HostRmCmd implements `repose host rm NAME`.
func HostRmCmd(ctx context.Context, e *Env, name string, yes bool, confirm func(prompt string) (bool, error)) error {
	if err := checkHostName(name); err != nil {
		return err
	}
	hosts, err := e.Client.ListHosts(ctx)
	if err != nil {
		return hostErr(err, name)
	}
	// A host already retired is not one the user has, so a second
	// `host rm` of the same name says so rather than succeeding again.
	var h *Host
	for i := range hosts {
		if hosts[i].State == "retired" {
			continue
		}
		if hosts[i].Name == name || hosts[i].ID == name {
			h = &hosts[i]
		}
	}
	if h == nil {
		return exitf(ExitGeneric, "You have no host named %s.", name)
	}
	if !yes {
		if confirm == nil {
			return exitf(ExitUsage, "Removing %s needs a confirmation; pass --yes to skip it.", h.Name)
		}
		if err := confirmOr(confirm, hostRemovePrompt(h), "Nothing removed."); err != nil {
			return err
		}
	}
	if err := e.Client.DeleteHost(ctx, h.ID); err != nil {
		return hostErr(err, name)
	}
	return nil
}

// hostRemovePrompt says what the removal costs. The api refuses while
// machines are still placed there (host_has_projects), so the question
// is only ever asked of a host with none, and what it has to say is
// where the snapshots were all along (I-1372).
func hostRemovePrompt(h *Host) string {
	return fmt.Sprintf("Remove %s? Snapshots taken on it stay on its disk, and repose has no copy. [y/N] ", h.Name)
}

// hostErr turns the api's answers on the host routes into the line the
// user reads: an api from before these routes, a name that is taken, a
// host that is not theirs, the one-host plan, and an account with no
// Own hardware plan.
func hostErr(err error, name string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	reason, _ := apiErr.Detail["reason"].(string)
	switch {
	case apiErr.Status == 404 && apiErr.Code != "not_found":
		return exitf(ExitGeneric, "This repose api does not take hardware of your own yet.")
	case apiErr.Code == "not_found" && name != "":
		return exitf(ExitGeneric, "You have no host named %s.", name)
	case reason == "name_taken" && name != "":
		return exitf(ExitGeneric, "You already have a host named %s.", name)
	case reason == "host_limit":
		return exitf(ExitGeneric, "Your plan takes %s. `repose host ls` lists what you have.", hostCount(detailInt(apiErr.Detail["limit"])))
	case reason == "own_hardware_plan_required":
		return exitf(ExitPaymentRequired, "Hardware of your own needs the Own hardware plan: https://repose.run/billing")
	case reason == "host_has_projects":
		// A machine moves host only by restore (DESIGN §3), so the api
		// refuses rather than leave them with nowhere to run. It names
		// them; this says it in the commands that clear them.
		on := detailStrings(apiErr.Detail["projects"])
		if len(on) == 0 {
			break
		}
		return exitf(ExitGeneric, "%s still has %s on it. `repose rm` or `repose snapshots restore` each of them first.", name, joinNames(on))
	}
	return err
}

// hostCount is a number of hosts in words, for a refusal that quotes
// the plan's own limit.
func hostCount(n int) string {
	if n == 1 {
		return "one host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// detailInt is a detail field that carries a number; JSON decodes it as
// a float64.
func detailInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// detailStrings is a detail field that carries a list of names.
func detailStrings(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// hostsOffline is the hosts of the account that are not answering, by
// id, read only when a project says its host is unreachable. An api
// without the route (an older one, or one this account has no hosts on)
// answers nothing, and the caller words the line without a name.
func hostsOffline(ctx context.Context, c *Client) map[string]Host {
	hosts, err := c.ListHosts(ctx)
	if err != nil {
		return nil
	}
	out := map[string]Host{}
	for _, h := range hosts {
		out[h.ID] = h
	}
	return out
}

// hostUnreachableLine is what `repose ls` says under a project whose
// host has stopped answering. A host of the user's own is named, with
// how long it has been quiet, because the user is the one who can turn
// it back on; one of repose's is not theirs to fix.
func hostUnreachableLine(p *Project, own map[string]Host, now time.Time) string {
	if !p.HostUnreachable {
		return ""
	}
	h, ok := own[p.HostID]
	if !ok {
		return "its server is not answering"
	}
	// "your host <name>" is the api's own wording for a placement it
	// refused (ops.ownHostRefusal), so the two say the same thing.
	if h.LastHeartbeatAt == nil {
		return fmt.Sprintf("your host %s has not answered", h.Name)
	}
	return fmt.Sprintf("your host %s has not answered for %s", h.Name, humanDuration(now.Sub(*h.LastHeartbeatAt)))
}
