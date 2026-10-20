package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/heracraft/repose/internal/machine"
	"github.com/heracraft/repose/internal/menu"
)

// `repose config` on a machine the user owns (DECISIONS I-812, I-832).
// The project's environment there is a Nix profile inside its own btrfs
// subvolume, built from the `repose.nix` at the root of its checkout, so
// a rewind takes an added package back with the files
// (apps/web/src/content/docs/own-machine.md "The project's tools").
//
// What the box can do is what `repose-agent env` does: build that profile
// from the project's file (`env apply`), add nixpkgs attributes to it
// (`--add`), and list what it holds (`env show`). So on this path `config
// add` installs into the profile and `config show` prints it, while the
// file itself is the one in the user's checkout, which the sync delivers.
// The parts a host or the api answers for — a revision to go back to, the
// account's machine.nix, a package taken out of a built closure — have no
// box-side answer, and each says so in one sentence rather than half
// working.

// agentEnvApply is `repose-agent env apply`'s answer (cmd/repose-agent
// wire.go wireEnvApply): what went into the project's profile, what was
// already there, and what the file asks for that a profile cannot hold.
type agentEnvApply struct {
	Added   []string    `json:"added"`
	Present []string    `json:"present"`
	Skipped []agentSkip `json:"skipped"`
}

// agentSkip is one thing the box did not do, with the line that says why.
type agentSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// agentEnvShow is `repose-agent env show`'s answer: the profile and the
// packages in it.
type agentEnvShow struct {
	Profile  string         `json:"profile"`
	Packages []agentPackage `json:"packages"`
}

// agentPackage is one element of the profile: the name nix lists it under
// and its nixpkgs attribute path.
type agentPackage struct {
	Name string `json:"name"`
	Attr string `json:"attr"`
}

// agentToolsPlan is `repose-agent tools plan`'s answer: what the laptop's
// tools list and the project's version pins ask for that the project's
// environment has not (I-221, I-265).
type agentToolsPlan struct {
	Missing  []agentTool    `json:"missing"`
	Runtimes []agentRuntime `json:"runtimes"`
}

type agentTool struct {
	Name string `json:"name"`
}

type agentRuntime struct {
	Package string `json:"package"`
}

// agentToolsRun is `repose-agent tools run`'s answer.
type agentToolsRun struct {
	Installed []string    `json:"installed"`
	Failed    []agentSkip `json:"failed"`
}

// ownedConfigPlace is the machine a `repose config` command acts on when
// the project is on one the user owns. ok is false for a hosted project,
// and for a registry or project map that cannot be read, which the hosted
// path reports in its own words (as refuseOnOwnedMachine does).
func ownedConfigPlace(e *Env, projectArg string) (machine.Machine, machine.Project, bool) {
	m, place, ok, err := spinePlace(e, projectArg, defaultResolveDeps())
	if err != nil || !ok {
		return machine.Machine{}, machine.Project{}, false
	}
	return m, place, true
}

// spineConfigTarget is the ssh the config commands reach one machine by.
func spineConfigTarget(m machine.Machine) (sshTarget, error) {
	cfg, err := machinesConfigPath()
	if err != nil {
		return sshTarget{}, exitf(ExitGeneric, "%v", err)
	}
	return machineTarget(cfg, m), nil
}

// spineConfigJSON is `repose config show --json` for a project on a
// machine the user owns: what its environment holds, and no field the api
// would have filled.
type spineConfigJSON struct {
	Slug     string         `json:"slug"`
	Machine  string         `json:"machine"`
	Profile  string         `json:"profile"`
	Packages []agentPackage `json:"packages"`
}

// spineConfigShow is `repose config show [PROJECT]` on a machine the user
// owns: the packages the project's profile holds, which is what the box
// can answer. The file they were built from is the repose.nix in the
// user's own checkout. handled is false when the project is a hosted
// machine's.
func spineConfigShow(ctx context.Context, e *Env, projectArg string, revisions bool) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	if revisions {
		return true, spineNoRevisions(place, m)
	}
	t, err := spineConfigTarget(m)
	if err != nil {
		return true, err
	}
	show, err := spineEnvShow(ctx, t, place, m)
	if err != nil {
		return true, err
	}
	if e.JSON {
		doc := spineConfigJSON{Slug: place.Slug, Machine: m.Name, Profile: show.Profile, Packages: show.Packages}
		if doc.Packages == nil {
			doc.Packages = []agentPackage{}
		}
		return true, e.printJSON(doc)
	}
	if len(show.Packages) == 0 {
		_, _ = fmt.Fprintf(e.Out, "%s on %s has no packages of its own.\n", place.Slug, m.Name)
		return true, nil
	}
	for _, p := range show.Packages {
		_, _ = fmt.Fprintln(e.Out, p.Name)
	}
	return true, nil
}

// spineConfigAdd is `repose config add PACKAGE...` on a machine the user
// owns: the names go into the project's profile, beside what its
// repose.nix asks for. A menu entry is the packages it installs; its
// service, if it has one, is the box's own to run (own-machine.md "The
// project's tools"). With no names it is the menu's own listing, which
// needs no project and is not this path's (ConfigMenuListCmd).
func spineConfigAdd(ctx context.Context, e *Env, projectArg string, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	t, err := spineConfigTarget(m)
	if err != nil {
		return true, err
	}
	cat, err := menu.Load()
	if err != nil {
		return true, exitf(ExitGeneric, "%v", err)
	}
	var attrs []string
	for _, arg := range args {
		name := menuName(arg)
		entry, inCatalog := cat.Entry(name)
		switch {
		case inCatalog:
			got, err := catalogAttrs(cat, entry)
			if err != nil {
				return true, err
			}
			if len(got) == 0 {
				return true, exitf(ExitUsage, "%s is a menu entry a machine repose runs builds, and it installs no package; on %s there is nothing to add for it.", name, m.Name)
			}
			if entry.Kind == "service" {
				_, _ = fmt.Fprintf(e.Out, "%s's service is %s's own to run; its package is what goes in.\n", name, m.Name)
			}
			attrs = append(attrs, got...)
		case menu.ValidPackage(name):
			attrs = append(attrs, name)
		default:
			return true, exitf(ExitUsage, "%q is not a nixpkgs attribute name (letters, digits, _ - + and dots, like gcc or python312Packages.black).", arg)
		}
	}
	res, err := spineEnvApply(ctx, t, place, m, attrs)
	if err != nil {
		return true, err
	}
	return true, e.spineReportAdd(place, m, attrs, res)
}

// spineReportAdd says what became of the names the user asked for, and
// what the same apply did for the project's own file beside them.
func (e *Env) spineReportAdd(place machine.Project, m machine.Machine, asked []string, res agentEnvApply) error {
	var added, present, rest []string
	for _, attr := range res.Added {
		if slices.Contains(asked, attr) {
			added = append(added, attr)
			continue
		}
		rest = append(rest, attr)
	}
	for _, attr := range res.Present {
		if slices.Contains(asked, attr) {
			present = append(present, attr)
		}
	}
	if len(present) > 0 {
		_, _ = fmt.Fprintf(e.Out, "%s %s already in %s.\n", joinNames(present), isAre(len(present)), place.Slug)
	}
	if len(added) > 0 {
		_, _ = fmt.Fprintf(e.Out, "Added %s to %s on %s.\n", joinNames(added), place.Slug, m.Name)
	}
	if len(rest) > 0 {
		_, _ = fmt.Fprintf(e.Out, "Installed %s from %s too.\n", joinNames(rest), repoConfigName)
	}
	for _, s := range res.Skipped {
		_, _ = fmt.Fprintln(e.Out, spineSkipLine(s))
	}
	// None of what was asked for is in the environment, and the box said
	// why: those names were the whole of the command, so it failed
	// (ExitBuildFailed, as a hosted build that failed exits).
	if len(added) == 0 && len(present) == 0 {
		return silent(ExitBuildFailed)
	}
	return nil
}

// spineSkipLine is one thing the box did not do: a package that would not
// build, or something the file declares that a profile cannot hold.
func spineSkipLine(s agentSkip) string {
	return fmt.Sprintf("%s was not applied: %s.", s.Name, strings.TrimSuffix(s.Reason, "."))
}

// spineConfigRemove is `repose config remove PACKAGE...` on a machine the
// user owns. A profile there only grows: the carried tools and the
// project's own packages share it and nothing rebuilds it from scratch,
// so the way back to an environment without a package is the rewind point
// it is inside (I-832).
func spineConfigRemove(e *Env, projectArg string) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	return true, exitf(ExitGuestNotRunning,
		"A package is not taken out again on a machine you own; `repose rewind` takes %s on %s back to a point before it went in.",
		place.Slug, m.Name)
}

// spineConfigEdit is `repose config edit [PROJECT]` on a machine the user
// owns: the project's repose.nix is a file in the checkout, so that file
// is what $EDITOR opens. It reaches the box with the next run or sync,
// which is the one thing a user would otherwise assume happened here.
func spineConfigEdit(e *Env, projectArg string, editor func(path string) error) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	root := laptopCheckoutFor(e, defaultResolveDeps(), place)
	if root == "" {
		return true, exitf(ExitUsage, "%s's %s is a file in its checkout; run this in that folder.", place.Slug, repoConfigName)
	}
	path := filepath.Join(root, repoConfigName)
	before, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, fileReadError(path, err)
	}
	if err := editor(path); err != nil {
		return true, err
	}
	after, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return true, fileReadError(path, err)
	}
	if string(after) == string(before) {
		return true, nil
	}
	_, _ = fmt.Fprintf(e.Out, "%s is edited here; it reaches %s with the next run.\n", repoConfigName, m.Name)
	return true, nil
}

// spineConfigApply is `repose config apply [PATH]` on a machine the user
// owns: with no path, the project's own repose.nix on the box, applied
// again. A file named here is not sent: what the box builds from is the
// checkout's own file, and the sync is what delivers it.
func spineConfigApply(ctx context.Context, e *Env, projectArg, path string) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	if path != "" {
		return true, exitf(ExitUsage, "A file is not sent to a machine you own; %s builds %s from the %s in its checkout, which `repose run` sends.", m.Name, place.Slug, repoConfigName)
	}
	t, err := spineConfigTarget(m)
	if err != nil {
		return true, err
	}
	res, err := spineEnvApply(ctx, t, place, m, nil)
	if err != nil {
		return true, err
	}
	if len(res.Added) > 0 {
		_, _ = fmt.Fprintf(e.Out, "Installed %s for %s on %s.\n", joinNames(res.Added), place.Slug, m.Name)
	}
	for _, s := range res.Skipped {
		_, _ = fmt.Fprintln(e.Out, spineSkipLine(s))
	}
	if len(res.Added) == 0 && len(res.Skipped) == 0 {
		_, _ = fmt.Fprintf(e.Out, "Nothing to install; %s on %s has what it asks for.\n", place.Slug, m.Name)
	}
	return true, nil
}

// spineConfigRevisions answers the three commands that want a revision on
// a machine the user owns: `config revisions`, `config show --revisions`
// and `config apply --revision`.
func spineConfigRevisions(e *Env, projectArg string) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	return true, spineNoRevisions(place, m)
}

// spineNoRevisions is the one sentence for a revision on a machine the
// user owns: a revision is a build the api kept, and this path has no
// api. The profile is inside the project's rewind points, so the way back
// to an earlier environment is a rewind (I-832).
func spineNoRevisions(place machine.Project, m machine.Machine) error {
	return exitf(ExitGuestNotRunning, "Revisions are a machine repose runs; %s is on %s, where `repose rewind` takes its packages back with its files.", place.Slug, m.Name)
}

// spinePersonalSwitch is `repose config --global on|off PROJECT` on a
// machine the user owns: machine.nix is the account's file the api
// pushes, and a project on such a machine has no account behind it
// (I-816).
func spinePersonalSwitch(e *Env, projectArg string) (bool, error) {
	m, place, ok := ownedConfigPlace(e, projectArg)
	if !ok {
		return false, nil
	}
	return true, exitf(ExitGuestNotRunning, "machine.nix is for a machine repose runs; %s is on %s, where the project's %s is the whole of its configuration.", place.Slug, m.Name, repoConfigName)
}

// catalogAttrs is the nixpkgs attributes a menu entry installs: the
// `pkgs.<attr>` names in the `home.packages` lists of its own rendering,
// with its options at their defaults. The box takes attributes (`env
// apply --add`) and has no home-manager to evaluate a module against, so
// everything else an entry declares — a service, a `programs.*` option, a
// user unit — installs nothing there, and an entry that declares only
// those comes back empty.
func catalogAttrs(cat *menu.Catalog, entry *menu.Entry) ([]string, error) {
	fragment, err := cat.Render(menu.Selection{{ID: entry.ID}})
	if err != nil {
		var me *menu.Error
		if errors.As(err, &me) {
			return nil, exitf(ExitUsage, "%s", me.Message)
		}
		return nil, exitf(ExitGeneric, "%v", err)
	}
	var attrs []string
	for _, list := range homePackageLists(fragment) {
		for _, word := range strings.Fields(list) {
			attr, ok := strings.CutPrefix(word, "pkgs.")
			if !ok || !menu.ValidPackage(attr) || slices.Contains(attrs, attr) {
				continue
			}
			attrs = append(attrs, attr)
		}
	}
	return attrs, nil
}

// homePackageLists is the body of each `home.packages = [ ... ];` of a
// fragment. The catalog's own snippets are one such line each, so this
// reads the list and nothing around it.
func homePackageLists(fragment string) []string {
	const key = "home.packages"
	var out []string
	for rest := fragment; ; {
		i := strings.Index(rest, key)
		if i < 0 {
			return out
		}
		rest = rest[i+len(key):]
		open := strings.IndexByte(rest, '[')
		shut := strings.IndexByte(rest, ']')
		if open < 0 || shut < open {
			return out
		}
		out = append(out, rest[open+1:shut])
		rest = rest[shut+1:]
	}
}

// spineEnvShow reads what the project's profile on the box holds.
func spineEnvShow(ctx context.Context, t sshTarget, place machine.Project, m machine.Machine) (agentEnvShow, error) {
	var show agentEnvShow
	if err := agentRun(ctx, t, &show, "env", "show", "--project", place.Slug); err != nil {
		return show, spineEnvFailed("read "+place.Slug+"'s packages", place, m, err)
	}
	return show, nil
}

// spineEnvApply builds the project's environment on the box: the packages
// its repose.nix asks for, and the attributes add names beside them.
func spineEnvApply(ctx context.Context, t sshTarget, place machine.Project, m machine.Machine, add []string) (agentEnvApply, error) {
	var res agentEnvApply
	args := []string{"env", "apply", "--project", place.Slug}
	if len(add) > 0 {
		args = append(args, "--add", strings.Join(add, ","))
	}
	if err := agentRun(ctx, t, &res, args...); err != nil {
		return res, spineEnvFailed("apply "+place.Slug+"'s packages", place, m, err)
	}
	return res, nil
}

// spineEnvFailed is a config command's failure. The box with no Nix and
// the project the box has not got each get their own sentence; anything
// else is the agent's own (spineRunFailed).
func spineEnvFailed(step string, place machine.Project, m machine.Machine, err error) error {
	if agentNoNix(err) {
		return spineNoNix(m)
	}
	var ae *agentError
	if errors.As(err, &ae) && ae.Code == agentCodeNotFound {
		return exitf(ExitGuestNotRunning, "%s is not on %s yet; `repose run` prepares it.", place.Slug, m.Name)
	}
	return spineRunFailed(step, err)
}

// agentNoNix is the agent's refusal for a box where nix is not installed
// (cmd/repose-agent/nix.go nixReady). Its code is not_found, which is
// also a project the store has not got, so the message is what separates
// the two.
func agentNoNix(err error) bool {
	var ae *agentError
	return errors.As(err, &ae) && ae.Code == agentCodeNotFound && strings.HasPrefix(ae.Message, "nix is not installed")
}

// spineNoNix is the one refusal a box without Nix gets, which the run
// prints as its warning too (spineApplyEnv). The install is what puts Nix
// there (own-machine.md "What the install puts on the box"), and `machine
// add` with the machine's own name runs it again.
func spineNoNix(m machine.Machine) error {
	return exitf(ExitGuestNotRunning, "%s has no Nix, so a project's packages cannot be installed there; `repose machine add %s --ssh %s` installs it.",
		m.Name, m.Name, m.Where())
}

// spineApplyEnv applies the project's environment before the run takes
// its rewind point, so the point holds the environment the agent will
// work in (I-832). The checkout's repose.nix, which the sync has just
// delivered, is what the box builds from; the hash the laptop last
// applied is remembered per machine and project in the cache the hosted
// run uses (repoconfig.go), so an unchanged file costs the box nothing
// and a project with no such file costs it nothing at all.
//
// Every failure here is a warning, not the end of the run: a box whose
// environment did not apply still runs an agent, and a run that refused
// to start one would leave the user with neither.
func (e *Env) spineApplyEnv(ctx context.Context, t sshTarget, m machine.Machine, place machine.Project, root string, tick func(string)) {
	if root == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(root, repoConfigName))
	if errors.Is(err, os.ErrNotExist) {
		// A project that declares no environment asks for no work and no
		// line, as it does on a hosted machine (applyRepoConfig).
		return
	}
	if err != nil {
		e.warn("Could not read %s (%s); %s on %s keeps the packages it has.", repoConfigName, oneLine(err.Error()), place.Slug, m.Name)
		return
	}
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	sent := loadRepoConfigSent(e.Dir)
	key := place.Machine + "/" + place.Slug
	if last, ok := sent[key]; ok && last.Hash == hash {
		return
	}
	res, err := spineEnvApply(ctx, t, place, m, nil)
	if err != nil {
		e.warn("%s", err.Error())
		return
	}
	sent[key] = repoConfigRecord{Hash: hash}
	saveRepoConfigSent(e.Dir, sent)
	if len(res.Added) > 0 {
		tick("Applied " + repoConfigName)
	}
	for _, s := range res.Skipped {
		e.warn("%s", spineSkipLine(s))
	}
}

// spineToolNames is what a plan asks for, as the user's own words for it:
// each tool's name, and the package a version pin needs.
func spineToolNames(plan agentToolsPlan) []string {
	var names []string
	for _, tool := range plan.Missing {
		names = append(names, tool.Name)
	}
	for _, rt := range plan.Runtimes {
		names = append(names, rt.Package)
	}
	return names
}

// spineInstallTools installs what the project needs that the box has not
// got: the laptop's tools list and the version pins the project carries
// (I-221, I-265), which `tools plan` answers for in milliseconds. A box
// that cannot answer still runs an agent, so a failure is one line and
// the run goes on.
func (e *Env) spineInstallTools(ctx context.Context, t sshTarget, m machine.Machine, place machine.Project, tick func(string)) {
	var plan agentToolsPlan
	if err := agentRun(ctx, t, &plan, "tools", "plan", "--project", place.Slug); err != nil {
		e.warn("%s", spineRunFailed("read what "+place.Slug+" needs on "+m.Name, err).Error())
		return
	}
	if len(plan.Missing) == 0 && len(plan.Runtimes) == 0 {
		return
	}
	var res agentToolsRun
	if err := agentRun(ctx, t, &res, "tools", "run", "--project", place.Slug); err != nil {
		e.warn("%s", spineRunFailed("install "+joinNames(spineToolNames(plan))+" on "+m.Name, err).Error())
		return
	}
	if len(res.Installed) > 0 {
		tick("Installed " + joinNames(res.Installed))
	}
	for _, s := range res.Failed {
		e.warn("%s was not installed: %s.", s.Name, strings.TrimSuffix(s.Reason, "."))
	}
}
