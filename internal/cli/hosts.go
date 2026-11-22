package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"runtime"
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

// HostAddCmd implements `repose host add [NAME]`. On a Mac with Apple
// silicon the mint is followed by the install here (I-1380); elsewhere
// the install step is the whole of stdout, so it pipes, and what to do
// with it is on stderr.
func HostAddCmd(ctx context.Context, e *Env, name string, o applianceOptions) error {
	if name != "" {
		if err := checkHostName(name); err != nil {
			return err
		}
	}
	// On a Mac, every refusal that does not need a host comes first: a
	// host minted and then refused is a record the retry collides with
	// and a token nobody kept (I-1422).
	if onAppleSilicon() {
		if err := applianceCanInstall(e, o); err != nil {
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
	if onAppleSilicon() {
		argument := ""
		if h.Install != nil {
			argument = h.Install.Argument
		}
		if argument == "" {
			argument = encodeTokenBundle(h.JoinToken)
		}
		_, _ = fmt.Fprintf(e.ErrOut, "%s: installing the appliance on this Mac.\n", h.Name)
		if err := applianceInstall(ctx, e, argument, h.Name, o); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(e.ErrOut, "%s is starting and joins your account in a few minutes.\n", h.Name)
		return nil
	}
	// On a Linux x86 machine the step is the installer, which makes this
	// machine, or the one the line is carried to, a host (DECISIONS
	// I-1498); the api's line is the Mac's.
	if onLinuxX86() {
		argument := ""
		if h.Install != nil {
			argument = h.Install.Argument
		}
		if argument == "" && h.JoinToken != "" {
			argument = encodeTokenBundle(h.JoinToken)
		}
		if argument != "" {
			_, _ = fmt.Fprintf(e.Out, hostInstallX86+"\n", argument)
			_, _ = fmt.Fprintln(e.ErrOut, hostAddedLine(h, true))
			return nil
		}
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

// hostInstallX86 is the step `repose host add` prints on a Linux x86
// machine: ops/install-host-x86.sh as repose.run serves it, run as root,
// with the join argument (DECISIONS I-1498).
const hostInstallX86 = "curl -fsSL https://repose.run/install-host.sh | sudo bash -s -- --join %s"

// onLinuxX86 is where that step is the one to print; tests replace it.
var onLinuxX86 = func() bool { return runtime.GOOS == "linux" && runtime.GOARCH == "amd64" }

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
	// A host the customer removed is not hardware they have; the api
	// keeps the row for the history and every output of this command
	// leaves it out. One filter above the three, because `--json` once
	// returned the api's answer unchanged and answered four removed
	// hosts to a script while the table said the customer had none
	// (ENG-37, the customer review).
	hosts = slices.DeleteFunc(hosts, func(h Host) bool {
		return h.State == "retired" || h.State == "lost"
	})
	if e.JSON {
		if hosts == nil {
			hosts = []Host{}
		}
		// The same word the table prints, for the same reason: cli.md
		// names five states and `registering` is not one of them, so a
		// script switching on the api's own enum would switch on a word
		// no customer is ever shown.
		out := make([]Host, len(hosts))
		copy(out, hosts)
		for i := range out {
			out[i].State = hostStateWord(out[i].State)
		}
		return writeJSONOut(e.Out, out)
	}
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
	// The Mac this runs on, when it is the one that runs h: the appliance
	// goes with the host. A Mac that runs another host keeps it and says
	// which; one whose appliance was added with --join has no recorded
	// name and keeps it too, `--local` being the way to remove that one.
	switch local, installed := localAppliance(e); {
	case !installed && appliancePresent(e):
		// An install that never reached its config has no name to check
		// against h, and the only thing that makes one is `repose host
		// add` on this Mac. The host it was for is the one going, so the
		// bytes go with it rather than sitting where no command reached
		// them (ENG-118).
		if err := applianceRemove(e); err != nil {
			return exitf(ExitGeneric, "%s is off your account, but the unfinished install on this Mac could not be removed: %v", h.Name, err)
		}
		_, _ = fmt.Fprintln(e.ErrOut, "An unfinished install on this Mac went with it.")
	case !installed:
	case local == h.Name:
		if err := applianceRemove(e); err != nil {
			return exitf(ExitGeneric, "%s is off your account, but the appliance on this Mac could not be removed: %v", h.Name, err)
		}
	case local != "":
		_, _ = fmt.Fprintf(e.ErrOut, "This Mac runs %s, not %s; its appliance stays.\n", local, h.Name)
	default:
		_, _ = fmt.Fprintf(e.ErrOut, "This Mac's appliance was added with --join and stays.\n")
	}
	return nil
}

// HostRmLocalCmd implements `repose host rm --local`: the appliance on
// this Mac, and nothing on the account.
func HostRmLocalCmd(ctx context.Context, e *Env, yes bool, confirm func(prompt string) (bool, error)) error {
	name, installed := localAppliance(e)
	// Present, not installed: an install that stopped before its config.
	// It still holds the bytes, so it is still what this command is for
	// (ENG-118).
	if !installed && !appliancePresent(e) {
		return exitf(ExitGeneric, "There is no appliance on this Mac.")
	}
	h, unread := localHostOnAccount(ctx, e, name)
	if !yes {
		if confirm == nil {
			return exitf(ExitUsage, "Removing the appliance on this Mac needs a confirmation; pass --yes to skip it.")
		}
		if err := confirmOr(confirm, localRemovePrompt(name, h, unread), "Nothing removed."); err != nil {
			return err
		}
	}
	if err := applianceRemove(e); err != nil {
		return err
	}
	// Where both of 2026-11-20's wedges happened: the appliance gone, the
	// host still on the account holding a machine whose destroy needs the
	// host that just went, and the plan's slot still held so no
	// replacement could be added. I-1420 is the way out and this is the
	// only place the user finds out it exists (DECISIONS I-1440).
	if h != nil && h.Projects > 0 {
		// A host that already reports not answering can go now; one that
		// still answers cannot, because the api refuses while machines
		// are on it, and it answers until its heartbeat runs out.
		when := " once it reports not answering"
		if h.State == "unreachable" || h.State == "lost" {
			when = ""
		}
		_, _ = fmt.Fprintf(e.ErrOut, "%s is still on your account, with %s. Remove it with `repose host rm %s`%s.\n",
			h.Name, machinesWithout(h.Projects), h.Name, when)
	}
	return nil
}

// localHostOnAccount is what the account says about the host this Mac
// runs, which is where the cost of a `--local` removal actually lands:
// the machines placed here keep their rows and lose the only host that
// can finish a destroy (ENG-141, DECISIONS I-1440).
//
// The answer is advisory, and deliberately so. `--local` is the command
// that works when nothing else does: on 2026-11-20 an expired login
// refused `repose rm` and `repose host rm NAME` while `--local` ran and
// took the appliance, which is correct -- the appliance is on this Mac
// and removing it needs nobody's permission -- and is also how a user
// with no session reached the wedge. So a login that cannot answer makes
// this say less, never refuse. unread is true when the account could not
// be read at all, which is a different sentence from a host that is not
// there.
func localHostOnAccount(ctx context.Context, e *Env, name string) (h *Host, unread bool) {
	// Added with --join: this Mac has no recorded name, so there is
	// nothing to look up and nothing to apologise for.
	if name == "" || e == nil || e.Client == nil {
		return nil, false
	}
	hosts, err := e.Client.ListHosts(ctx)
	if err != nil {
		return nil, true
	}
	for i := range hosts {
		if hosts[i].State == "retired" {
			continue
		}
		if hosts[i].Name == name {
			return &hosts[i], false
		}
	}
	return nil, false
}

// localRemovePrompt is the question `host rm --local` asks. It says how
// many machines go with the appliance whenever the account can be read,
// because "every machine on its disk" was true and told a customer with
// three machines the same thing it told one with none (ENG-141).
func localRemovePrompt(name string, h *Host, unread bool) string {
	what := "the appliance on this Mac"
	if name != "" {
		what = name + "'s appliance on this Mac"
	}
	switch {
	case h != nil && h.Projects > 0:
		disks := "its disk is"
		if h.Projects > 1 {
			disks = "their disks are"
		}
		return fmt.Sprintf("Remove %s? The %s on it cannot be recovered: %s on this Mac and repose has no copy. [y/N] ",
			what, machinesOn(h.Projects), disks)
	case unread:
		return fmt.Sprintf("Remove %s, with every machine and snapshot on its disk? Your account could not be read, so this cannot say how many machines go with it. [y/N] ", what)
	default:
		return fmt.Sprintf("Remove %s, with every machine and snapshot on its disk? [y/N] ", what)
	}
}

// machinesOn and machinesWithout count machines in the two sentences
// above, so neither has to say "1 machines".
func machinesOn(n int) string {
	if n == 1 {
		return "machine"
	}
	return fmt.Sprintf("%d machines", n)
}

func machinesWithout(n int) string {
	if n == 1 {
		return "1 machine that no longer has a host"
	}
	return fmt.Sprintf("%d machines that no longer have a host", n)
}

// hostRemovePrompt says what the removal costs. The api refuses while
// machines are still placed there (host_has_projects), so the question
// is only ever asked of a host with none, and what it has to say is
// where the snapshots were all along (I-1372).
func hostRemovePrompt(h *Host) string {
	// A host that is answering cannot be removed while machines are on
	// it: the api refuses that. A host that has stopped answering can
	// be, which is the only way out of the wedge in ENG-119 -- and it is
	// the one path where removing a host loses machines, so the question
	// says how many and that their disks were on it (DECISIONS I-1420).
	if h.Projects > 0 && (h.State == "unreachable" || h.State == "lost") {
		what, disks := "the machine on it", "its disk is"
		if h.Projects > 1 {
			what, disks = fmt.Sprintf("the %d machines on it", h.Projects), "their disks are"
		}
		return fmt.Sprintf("Remove %s? It is not answering, and %s cannot be recovered: %s on that host and repose has no copy.%s [y/N] ",
			h.Name, what, disks, hostSnapshotsSentence(h))
	}
	if h.Snapshots == 0 {
		return fmt.Sprintf("Remove %s? [y/N] ", h.Name)
	}
	return fmt.Sprintf("Remove %s?%s [y/N] ", h.Name, hostSnapshotsSentence(h))
}

// hostSnapshotsSentence says what removing the host deletes besides its
// machines (DECISIONS I-1544): the snapshots kept on its disk, counted,
// of which repose has no copy. "" when it keeps none.
func hostSnapshotsSentence(h *Host) string {
	switch h.Snapshots {
	case 0:
		return ""
	case 1:
		return " This deletes the 1 snapshot kept on it; repose has no copy."
	}
	return fmt.Sprintf(" This deletes the %d snapshots kept on it; repose has no copy.", h.Snapshots)
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
		// them; this says it in the command that clears them.
		//
		// It named `repose snapshots restore` as a second way out until
		// I-1418, and that was not one: the refusal is shown for the
		// machines on the host, which may have no snapshot to restore --
		// rig1, the machine this was found on, had none and never had
		// (ENG-119). It is not needed either, because `repose rm` keeps
		// the final snapshot for 30 days, so the work a restore would
		// have saved survives the removal it asks for.
		on := detailStrings(apiErr.Detail["projects"])
		if len(on) == 0 {
			break
		}
		return exitf(ExitGeneric, "%s still has %s on it. `repose rm` each of them first.", name, joinNames(on))
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

// ownHostName is the name of the host a machine is on while that host is
// one the account owns, and "" while it is one of repose's: the
// project's `host_name` (I-1392), or, for an api that does not send the
// field, the hosts already read because one of them is quiet.
func ownHostName(p *Project, own map[string]Host) string {
	if p.HostName != "" {
		return p.HostName
	}
	if p.HostID == "" {
		return ""
	}
	return own[p.HostID].Name
}

// hostCell is `repose ls`'s HOST cell and `repose status`'s `host` row
// (I-1392, which amends I-616): the name of a host of the account's own,
// `repose` for a machine on repose's servers, which is the word
// `repose run --host repose` takes for them, and "" for one not placed
// yet, which the table prints as `-`. A host of repose's keeps I-616's
// ruling and is never named here: it is a platform internal, and its
// name and the machine's address stay under `-v`.
func hostCell(p *Project, own map[string]Host) string {
	if name := ownHostName(p, own); name != "" {
		return name
	}
	if p.HostID == "" {
		return ""
	}
	return "repose"
}
