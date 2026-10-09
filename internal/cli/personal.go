package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The personal layer (DECISIONS I-490): machine.nix, a home-manager module
// every machine of the account gets beside its project's configuration.
// The account holds it; the laptop keeps a copy at
// ~/.config/repose/machine.nix, which `repose run` pushes when it changed
// since the last push and `repose config --global` edits. The state file
// beside it records what the laptop and the account last agreed on, so a
// copy saved on the dashboard since is never overwritten.

const machineNixFile = "machine.nix"

// personalState is ~/.config/repose/machine.nix.state: the account
// revision and the text's SHA-256 the last push or pull left both sides
// on. API is the api it applies to. MissingNoted is the revision `run`
// last said was still on the account after the laptop's file was
// deleted, so it says so once (DECISIONS I-552).
type personalState struct {
	API          string `json:"api"`
	RevisionID   string `json:"revision_id"`
	SHA256       string `json:"sha256"`
	MissingNoted string `json:"missing_noted,omitempty"`
}

func (e *Env) machineNixPath() string    { return filepath.Join(e.Dir, machineNixFile) }
func (e *Env) personalStatePath() string { return filepath.Join(e.Dir, machineNixFile+".state") }

// displayPath shows a path under the home directory with ~.
func (e *Env) displayPath(p string) string {
	if e.HomeDir != "" {
		if rel, err := filepath.Rel(e.HomeDir, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~/" + filepath.ToSlash(rel)
		}
	}
	return p
}

func textSHA(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (e *Env) loadPersonalState() (personalState, bool) {
	b, err := os.ReadFile(e.personalStatePath())
	if err != nil {
		return personalState{}, false
	}
	var st personalState
	if json.Unmarshal(b, &st) != nil || st.API != e.Cfg.APIURL {
		return personalState{}, false
	}
	return st, true
}

func (e *Env) savePersonalState(rev, text string) {
	b, _ := json.Marshal(personalState{API: e.Cfg.APIURL, RevisionID: rev, SHA256: textSHA(text)})
	if err := writeFileAtomic(e.personalStatePath(), b, 0o600); err != nil {
		e.warn("Could not record the machine.nix push (%s); the next run compares again.", oneLine(err.Error()))
	}
}

// personalView is the laptop's copy, the account's and the state, read
// once.
type personalView struct {
	Account     *PersonalConfig
	Local       string
	LocalExists bool
	State       personalState
	HaveState   bool
}

func (v *personalView) localChanged() bool {
	if !v.LocalExists {
		return false
	}
	if !v.HaveState {
		return v.Local != v.Account.Fragment
	}
	return textSHA(v.Local) != v.State.SHA256
}

func (v *personalView) accountChanged() bool {
	if !v.HaveState {
		return v.Account.Rev() != "" && v.Account.Fragment != v.Local
	}
	return v.Account.Rev() != v.State.RevisionID
}

// readPersonal reads the three. An api from before machine.nix answers
// 404, which reads as an account with none.
func (e *Env) readPersonal(ctx context.Context) (*personalView, error) {
	acct, err := e.Client.GetPersonal(ctx)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == 404 {
			acct = &PersonalConfig{}
		} else {
			return nil, err
		}
	}
	v := &personalView{Account: acct}
	if b, err := os.ReadFile(e.machineNixPath()); err == nil {
		v.Local, v.LocalExists = string(b), true
	}
	v.State, v.HaveState = e.loadPersonalState()
	return v, nil
}

// personalConflict is the refusal when both copies changed: an error,
// because it is one (the push did not happen), and its text names the two
// ways out.
type personalConflict struct {
	acct *PersonalConfig
	path string
}

func (c personalConflict) Error() string {
	where := "elsewhere"
	if c.acct != nil && c.acct.Source != nil && *c.acct.Source == "dashboard" {
		where = "on the dashboard"
	}
	when := ""
	if c.acct != nil && c.acct.CreatedAt != nil {
		when = " " + c.acct.CreatedAt.Local().Format("2006-01-02 15:04")
	}
	return fmt.Sprintf("Not pushing %s: the copy on your account was saved %s%s, after this laptop's last push. `repose config --global apply` pushes the laptop's copy over it; `repose config --global show > %s` keeps the account's.", c.path, where, when, c.path)
}

// personalConflictLine is personalConflict's text.
func (e *Env) personalConflictLine(acct *PersonalConfig) string {
	return personalConflict{acct: acct, path: e.displayPath(e.machineNixPath())}.Error()
}

// personalSync is run's half (DECISIONS I-490): push the laptop's
// machine.nix when it changed since the last push, bring an unchanged
// copy up to date with one saved on the dashboard, and refuse, with a
// line, when both changed. It never fails the run; what it says comes
// back as lines. hasPersonal reports whether the account has a
// machine.nix after it, for the tool scan's precedence.
func (e *Env) personalSync(ctx context.Context) (lines []string, hasPersonal bool) {
	v, err := e.readPersonal(ctx)
	return e.personalApply(ctx, v, err)
}

// personalApply is personalSync on a view already read.
func (e *Env) personalApply(ctx context.Context, v *personalView, err error) (lines []string, hasPersonal bool) {
	if err != nil {
		return []string{fmt.Sprintf("Could not read machine.nix from your account (%s); it was not pushed.", oneLine(err.Error()))}, false
	}
	has := strings.TrimSpace(v.Account.Fragment) != ""
	if !v.LocalExists {
		// The laptop pushed this revision and its file is gone since:
		// deleting the file removes nothing, so say once that it stays.
		if has && v.HaveState && v.State.RevisionID == v.Account.Rev() && v.State.MissingNoted != v.Account.Rev() {
			st := v.State
			st.MissingNoted = v.Account.Rev()
			if b, err := json.Marshal(st); err == nil {
				if err := writeFileAtomic(e.personalStatePath(), b, 0o600); err != nil {
					e.warn("Could not record the machine.nix notice (%s); the next run shows it again.", oneLine(err.Error()))
				}
			}
			return []string{fmt.Sprintf("%s is gone, but your account still has machine.nix and every machine gets it.", e.displayPath(e.machineNixPath()))}, has
		}
		return nil, has
	}
	local := strings.TrimSpace(v.Local) != ""
	p := e.displayPath(e.machineNixPath())
	switch lc, ac := v.localChanged(), v.accountChanged(); {
	case lc && ac:
		return []string{e.personalConflictLine(v.Account)}, has
	case lc:
		base := v.Account.Rev()
		if v.HaveState {
			base = v.State.RevisionID
		}
		res, err := e.Client.PutPersonal(ctx, v.Local, &base)
		if err != nil {
			var ae *APIError
			if errors.As(err, &ae) && ae.Code == "conflict" {
				return []string{e.personalConflictLine(v.Account)}, has
			}
			if errors.As(err, &ae) && ae.Code == "invalid" {
				return []string{fmt.Sprintf("Not pushing %s: %s", p, firstLine(ae.Message))}, has
			}
			return []string{fmt.Sprintf("Could not push %s (%s).", p, oneLine(err.Error()))}, has
		}
		e.savePersonalState(res.Rev(), v.Local)
		if res.Unchanged {
			return nil, local
		}
		if !local {
			return []string{fmt.Sprintf("Removed machine.nix from your account (%s is empty).", p)}, false
		}
		return []string{"Applying machine.nix (changed) " + personalWhere(res.Projects) + " in the background."}, true
	case ac:
		if !has {
			// Removed on the dashboard: the laptop's copy stays, and
			// stays unpushed until it changes or is applied.
			e.savePersonalState("", v.Local)
			return []string{fmt.Sprintf("machine.nix was removed from your account; %s is left as it is and is not pushed until it changes.", p)}, false
		}
		if err := writeFileAtomic(e.machineNixPath(), []byte(v.Account.Fragment), 0o644); err != nil {
			return []string{fmt.Sprintf("Could not update %s from your account (%s).", p, oneLine(err.Error()))}, has
		}
		e.savePersonalState(v.Account.Rev(), v.Account.Fragment)
		return []string{fmt.Sprintf("Updated %s from your account, where it was saved since this laptop's last push.", p)}, has
	}
	return nil, has
}

// personalWhere is "to 3 machines", "to blog", "to no machine yet".
func personalWhere(ch []PersonalChange) string {
	switch len(ch) {
	case 0:
		return "to new machines"
	case 1:
		return "to " + ch[0].Slug
	}
	return fmt.Sprintf("to %d machines", len(ch))
}

// ---- repose config --global ----

const machineNixTemplate = `# machine.nix: a home-manager module every repose machine of your account
# gets, beside each project's own configuration.
# https://repose.herakraft.co/docs/config#your-machine-nix
{ pkgs, ... }:
{
  home.packages = with pkgs; [
  ];
}
`

// GlobalShowCmd is `repose config --global show [--revisions]`.
func GlobalShowCmd(ctx context.Context, e *Env, revisions bool) error {
	if revisions {
		revs, err := e.Client.ListPersonal(ctx)
		if err != nil {
			return err
		}
		if e.JSON {
			return writeJSONOut(e.Out, revs)
		}
		for _, r := range revs {
			_, _ = fmt.Fprintf(e.Out, "%s\t%s\t%s\t%d bytes\n", r.RevisionID, r.CreatedAt.Local().Format("2006-01-02 15:04"), r.Source, r.Bytes)
		}
		return nil
	}
	acct, err := e.Client.GetPersonal(ctx)
	if err != nil {
		return err
	}
	if e.JSON {
		return writeJSONOut(e.Out, acct)
	}
	if acct.Rev() == "" || acct.Fragment == "" {
		_, _ = fmt.Fprintf(e.ErrOut, "No machine.nix on your account yet; repose reads it from %s.\n", e.displayPath(e.machineNixPath()))
		return nil
	}
	_, _ = fmt.Fprint(e.Out, acct.Fragment)
	return nil
}

// prepareLocal brings the laptop's copy up to date before an edit: a
// missing one is written from the account (or the template), an
// unchanged one takes a copy saved on the dashboard since, and both
// changed is refused. It returns the view and the base revision a push
// of the edited copy names.
func (e *Env) prepareLocal(ctx context.Context) (*personalView, string, error) {
	v, err := e.readPersonal(ctx)
	if err != nil {
		return nil, "", err
	}
	switch lc, ac := v.localChanged(), v.accountChanged(); {
	case !v.LocalExists:
		text := v.Account.Fragment
		if strings.TrimSpace(text) == "" {
			text = machineNixTemplate
		}
		if err := writeFileAtomic(e.machineNixPath(), []byte(text), 0o644); err != nil {
			return nil, "", err
		}
		v.Local, v.LocalExists = text, true
		if v.Account.Rev() != "" {
			e.savePersonalState(v.Account.Rev(), v.Account.Fragment)
		}
	case lc && ac:
		return nil, "", exitf(ExitUsage, "%s", e.personalConflictLine(v.Account))
	case ac && strings.TrimSpace(v.Account.Fragment) != "":
		if err := writeFileAtomic(e.machineNixPath(), []byte(v.Account.Fragment), 0o644); err != nil {
			return nil, "", err
		}
		v.Local = v.Account.Fragment
		e.savePersonalState(v.Account.Rev(), v.Account.Fragment)
		_, _ = fmt.Fprintf(e.ErrOut, "Updated %s from your account first.\n", e.displayPath(e.machineNixPath()))
	}
	return v, v.Account.Rev(), nil
}

// GlobalEditCmd is `repose config --global edit`: the laptop's copy in
// $EDITOR, pushed when saved changed.
func GlobalEditCmd(ctx context.Context, e *Env, editor func(path string) error) error {
	v, base, err := e.prepareLocal(ctx)
	if err != nil {
		return err
	}
	if err := editor(e.machineNixPath()); err != nil {
		return err
	}
	b, err := os.ReadFile(e.machineNixPath())
	if err != nil {
		return err
	}
	if string(b) == v.Account.Fragment {
		_, _ = fmt.Fprintln(e.Out, "machine.nix unchanged.")
		return nil
	}
	return pushPersonal(ctx, e, string(b), &base, e.machineNixPath())
}

// GlobalApplyCmd is `repose config --global apply [PATH]`: push PATH (the
// laptop's copy by default) as the account's machine.nix, over whatever
// the account has. An empty file removes it.
func GlobalApplyCmd(ctx context.Context, e *Env, path string) error {
	if path == "" {
		path = e.machineNixPath()
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && path == e.machineNixPath() {
			acct, aerr := e.Client.GetPersonal(ctx)
			if aerr != nil {
				return exitf(ExitUsage, "No %s, and could not read machine.nix from your account (%s).", e.displayPath(path), oneLine(aerr.Error()))
			}
			if strings.TrimSpace(acct.Fragment) != "" {
				return exitf(ExitUsage, "No %s, but your account has machine.nix (revision %s) and every machine gets it. `repose config --global apply /dev/null` removes it; `repose config --global edit` brings it back to this laptop.", e.displayPath(path), shortRev(acct.Rev()))
			}
			return exitf(ExitUsage, "No %s yet. `repose config --global edit` starts one, or `repose config --global add ripgrep`.", e.displayPath(path))
		}
		return exitf(ExitUsage, "reading %s: %v", path, err)
	}
	return pushPersonal(ctx, e, string(b), nil, path)
}

// packagesRe finds the list `repose config --global add` edits.
var packagesRe = regexp.MustCompile(`(?s)(home\.packages\s*=\s*with\s+pkgs\s*;\s*\[)(.*?)(\]\s*;)`)

var nixAttrRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_+-]*(\.[A-Za-z_][A-Za-z0-9_+-]*)*$`)

// editPackages adds or removes names in the file's
// `home.packages = with pkgs; [ ... ];` list, one per line.
func editPackages(text string, names []string, add bool) (string, []string, error) {
	m := packagesRe.FindStringSubmatchIndex(text)
	if m == nil {
		return "", nil, errors.New("no `home.packages = with pkgs; [ ... ];` list")
	}
	body := text[m[4]:m[5]]
	have := map[string]bool{}
	for _, f := range strings.Fields(body) {
		have[f] = true
	}
	var changed []string
	if add {
		var b strings.Builder
		b.WriteString(strings.TrimRight(body, " \t"))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		for _, n := range names {
			if have[n] {
				continue
			}
			have[n] = true
			b.WriteString("    " + n + "\n")
			changed = append(changed, n)
		}
		b.WriteString("  ")
		body = b.String()
	} else {
		lines := strings.Split(body, "\n")
		out := lines[:0]
		drop := map[string]bool{}
		for _, n := range names {
			drop[n] = true
		}
		for _, l := range lines {
			if t := strings.TrimSpace(l); drop[t] {
				changed = append(changed, t)
				continue
			}
			out = append(out, l)
		}
		body = strings.Join(out, "\n")
	}
	return text[:m[4]] + body + text[m[5]:], changed, nil
}

// GlobalPackagesCmd is `repose config --global add|remove <package>...`.
func GlobalPackagesCmd(ctx context.Context, e *Env, args []string, add bool) error {
	verb := "add"
	if !add {
		verb = "remove"
	}
	if len(args) == 0 {
		return exitf(ExitUsage, "Name at least one package: `repose config --global %s ripgrep`.", verb)
	}
	var names []string
	for _, a := range args {
		n := menuName(a)
		if !nixAttrRe.MatchString(n) || len(n) > 200 {
			return exitf(ExitUsage, "%q is not a nixpkgs attribute name; find names at https://search.nixos.org/packages.", a)
		}
		names = append(names, n)
	}
	v, base, err := e.prepareLocal(ctx)
	if err != nil {
		return err
	}
	p := e.displayPath(e.machineNixPath())
	text, changed, err := editPackages(v.Local, names, add)
	if err != nil {
		return exitf(ExitUsage, "%s has no `home.packages = with pkgs; [ ... ];` list to %s; change it with `repose config --global edit`.", p, verb)
	}
	if len(changed) == 0 {
		if add {
			_, _ = fmt.Fprintf(e.Out, "%s already in %s.\n", joinNames(names), p)
		} else {
			_, _ = fmt.Fprintf(e.Out, "%s not in %s's package list.\n", joinNames(names), p)
		}
		return nil
	}
	if err := writeFileAtomic(e.machineNixPath(), []byte(text), 0o644); err != nil {
		return err
	}
	if add {
		_, _ = fmt.Fprintf(e.Out, "Added %s to %s.\n", joinNames(changed), p)
	} else {
		_, _ = fmt.Fprintf(e.Out, "Removed %s from %s.\n", joinNames(changed), p)
	}
	return pushPersonal(ctx, e, text, &base, e.machineNixPath())
}

// pushPersonal saves text as the account's machine.nix and follows the
// first build it started, so an error names its line in the file;
// localPath is the file errors point into.
func pushPersonal(ctx context.Context, e *Env, text string, base *string, localPath string) error {
	res, err := e.Client.PutPersonal(ctx, text, base)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Code == "conflict" {
			acct, _ := e.Client.GetPersonal(ctx)
			return exitf(ExitUsage, "%s", e.personalConflictLine(acct))
		}
		if errors.As(err, &ae) && ae.Code == "invalid" {
			RenderBuildError(e.Out, ae.Code, ae.Message, localPath, []byte(text))
			return silent(ExitBuildFailed)
		}
		return err
	}
	if localPath == e.machineNixPath() || textSHA(text) == textSHA(readOr(e.machineNixPath())) {
		e.savePersonalState(res.Rev(), text)
	}
	if e.JSON {
		return writeJSONOut(e.Out, res)
	}
	if res.Unchanged {
		_, _ = fmt.Fprintf(e.Out, "machine.nix unchanged; %s is still the account's.\n", shortRev(res.Rev()))
		return nil
	}
	if strings.TrimSpace(text) == "" {
		_, _ = fmt.Fprintf(e.Out, "Removed machine.nix from your account; %s.\n", rebuildWhat(res.Projects))
		return nil
	}
	if len(res.Projects) == 0 {
		_, _ = fmt.Fprintln(e.Out, "Saved machine.nix to your account.")
		return nil
	}
	// Follow one build: a running machine's when there is one.
	follow := -1
	for i, ch := range res.Projects {
		if ch.OpID == "" {
			continue
		}
		if follow < 0 || (ch.Running && !res.Projects[follow].Running) {
			follow = i
		}
	}
	_, _ = fmt.Fprintf(e.Out, "Saved machine.nix to your account; %s.\n", rebuildWhat(res.Projects))
	if follow < 0 {
		return nil
	}
	ch := res.Projects[follow]
	project := &Project{ID: ch.ProjectID, Slug: ch.Slug}
	_, _ = fmt.Fprintf(e.Out, "Following %s:\n", ch.Slug)
	op, pr, err := waitConfigOp(ctx, e, project, ch.OpID)
	if err != nil {
		return err
	}
	if op.State == "error" {
		RenderBuildError(e.Out, op.Error.Code, op.Error.Message, localPath, []byte(text))
		return silent(ExitBuildFailed)
	}
	if !ch.Running {
		_, _ = fmt.Fprintf(e.Out, "Built for %s, which is stopped; it switches at its next start.\n", ch.Slug)
		return nil
	}
	printApplied(e, project, op, ch.RevisionID, pr)
	return nil
}

func readOr(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// rebuildWhat says what a save rebuilds: "blog switches in place",
// "blog and api switch in place, docs at its next start".
func rebuildWhat(ch []PersonalChange) string {
	var running, stopped []string
	for _, c := range ch {
		if c.Running {
			running = append(running, c.Slug)
		} else {
			stopped = append(stopped, c.Slug)
		}
	}
	var parts []string
	if len(running) > 0 {
		verb := "switches"
		if len(running) > 1 {
			verb = "switch"
		}
		parts = append(parts, joinNames(running)+" "+verb+" in place")
	}
	if len(stopped) > 0 {
		when := "at its next start"
		if len(stopped) > 1 {
			when = "at their next start"
		}
		if len(running) > 0 {
			parts = append(parts, joinNames(stopped)+" "+when)
		} else {
			parts = append(parts, joinNames(stopped)+" switch"+map[bool]string{true: "es", false: ""}[len(stopped) == 1]+" "+when)
		}
	}
	if len(parts) == 0 {
		return "no machine to rebuild"
	}
	return strings.Join(parts, ", ")
}

// personalStep is run's machine.nix step: the read starts beside the
// resolve, the push (when one is due) happens at finish.
type personalStep struct {
	view chan personalRead
	done bool
}

type personalRead struct {
	v   *personalView
	err error
}

func (e *Env) startPersonal(ctx context.Context) *personalStep {
	s := &personalStep{view: make(chan personalRead, 1)}
	go func() {
		v, err := e.readPersonal(ctx)
		s.view <- personalRead{v, err}
	}()
	return s
}

// finish pushes, pulls or refuses (personalSync's rules) once, prints
// what it did, and sets e.personalOn for the tool scan. project is nil
// before a create.
func (s *personalStep) finish(ctx context.Context, e *Env, noPersonal bool, project *Project) {
	if s == nil || s.done {
		return
	}
	s.done = true
	r := <-s.view
	lines, has := e.personalApply(ctx, r.v, r.err)
	for _, l := range lines {
		_, _ = fmt.Fprintln(e.ErrOut, l)
	}
	e.personalOn = has && !noPersonal && (project == nil || !project.PersonalOptOut)
}

// optOutPersonal is --no-personal on an existing machine.
func (e *Env) optOutPersonal(ctx context.Context, project *Project) {
	on := true
	if _, err := e.Client.PatchProject(ctx, project.ID, PatchProjectRequest{PersonalOptOut: &on}); err != nil {
		e.warn("Could not turn machine.nix off for %s (%s).", project.Slug, oneLine(err.Error()))
		return
	}
	project.PersonalOptOut = true
	_, _ = fmt.Fprintf(e.ErrOut, "machine.nix is off for %s from now on (--no-personal); the machine switches without it in the background.\n", project.Slug)
}
