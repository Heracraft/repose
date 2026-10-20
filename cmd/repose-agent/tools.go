package main

// `tools plan` and `tools run`: the box's half of the tools carry
// (DECISIONS I-221, I-222). The laptop writes
// `~/.repose/tools-wanted.json` (docs/interfaces/guest-conventions.md
// "Tools carry") on every run, and on a machine the user owns nothing
// read it: the list was sent again every run and the user was told
// nothing. A guest does this work in nix/guest/base/tools-carry.sh under
// a user unit; here `plan` reports what the project's environment lacks
// and `run` installs it.
//
// Both install into the project's own profile, not into a user profile,
// so a rewind takes the tools back with the files, and both keep their
// records inside the live subvolume for the same reason: the marker a
// rewound project no longer has is a list the laptop sends again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// toolItem and toolsWanted are the file's shape, which is the contract in
// guest-conventions.md "Tools carry" and internal/cli/carry_tools.go.
type toolItem struct {
	Name    string   `json:"name"`
	Bins    []string `json:"bins"`
	Manager string   `json:"manager,omitempty"`
	Pkg     string   `json:"pkg,omitempty"`
	Version string   `json:"version,omitempty"`
}

type toolsWanted struct {
	Hash  string     `json:"hash"`
	Items []toolItem `json:"items"`
	Node  string     `json:"node,omitempty"`
	Ruby  string     `json:"ruby,omitempty"`
	Java  string     `json:"java,omitempty"`
}

// The shapes a name, a command and a version may have. The laptop bounds
// them already (internal/cli/carry_tools.go); the agent bounds them again
// because it builds shell words and an installable out of them, and an
// entry outside them is left where it is rather than run.
var (
	toolNameRe = regexp.MustCompile(`^[A-Za-z0-9@][A-Za-z0-9@/._+-]{0,199}$`)
	toolBinRe  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]{0,99}$`)
	toolVerRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,99}$`)
	pinRe      = regexp.MustCompile(`^[0-9][0-9.]*$`)
)

// managers are the laptop package managers an item may carry; anything
// else is nixpkgs or nothing.
var managers = []string{"npm", "pnpm", "bun", "go", "cargo", "uv", "pipx"}

// installTimeout is the seconds one install gets, as the guest gives it.
const installTimeout = "1200"

// The runtimes a project pins (DECISIONS I-265): the key in
// tools-wanted.json is also the command.
var runtimePins = []string{"node", "ruby", "java"}

func cmdTools(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "no tools command given")
	}
	switch args[0] {
	case "plan":
		return cmdToolsPlan(ctx, env, args[1:])
	case "run":
		return cmdToolsRun(ctx, env, args[1:])
	}
	_, _ = fmt.Fprint(env.Stderr, usage)
	return errf(codeInvalidArgument, "unknown tools command %q", args[0])
}

// cmdToolsPlan reports what the carry's list asks for that the project's
// environment has not. It installs nothing and runs nothing but
// `command -v` and a `--version`, so it costs milliseconds and the CLI
// can run it in the probe's ssh.
func cmdToolsPlan(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "tools plan")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	live, err := requireProject(env, *project)
	if err != nil {
		return err
	}
	out := wireToolsPlan{Missing: []wireTool{}, Runtimes: []wireRuntime{}, Failed: []wireSkip{}}
	wanted, err := readWanted(env.Home)
	if err != nil {
		return err
	}
	// No list, or a list this project has already had a pass over: there
	// is nothing to say, which is what makes a second run quiet.
	if wanted == nil {
		return emit(env.Stdout, out)
	}
	out.Hash = wanted.Hash
	profile := profileDir(env.Store, *project)
	failed := readFailed(live)
	// A list this project has had a pass over says nothing about what is
	// missing -- that is what makes a second run quiet -- but it must
	// still report what the pass could not install. A tool that failed is
	// a tool the box has not got, and a marker is not a reason to stop
	// saying so.
	if markerHas(live, wanted.Hash) {
		for _, item := range wanted.Items {
			if reason, ok := failed[itemKey(item)]; ok {
				out.Failed = append(out.Failed, wireSkip{Name: item.Name, Reason: reason})
			}
		}
		return emit(env.Stdout, out)
	}
	for _, item := range wanted.Items {
		if !itemOK(item) || env.anyBin(ctx, profile, item.Bins) {
			continue
		}
		// An item that failed before is reported with the line that said
		// why, and not asked for again until the laptop's entry for it
		// changes (the guest keeps the same record).
		if reason, ok := failed[itemKey(item)]; ok {
			out.Failed = append(out.Failed, wireSkip{Name: item.Name, Reason: reason})
			continue
		}
		out.Missing = append(out.Missing, wireTool{Name: item.Name, Bins: item.Bins})
	}
	for _, lang := range runtimePins {
		want := wanted.pin(lang)
		if want == "" {
			continue
		}
		have := env.runtimeVersion(ctx, profile, lang)
		if have == want {
			continue
		}
		out.Runtimes = append(out.Runtimes, wireRuntime{
			Runtime: lang, Want: want, Have: have, Package: runtimeAttr(lang, want),
		})
	}
	return emit(env.Stdout, out)
}

// cmdToolsRun installs what plan named, into the project's profile, and
// writes the marker the carry reads, so the laptop stops sending an
// unchanged list.
func cmdToolsRun(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "tools run")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	live, err := requireProject(env, *project)
	if err != nil {
		return err
	}
	start := time.Now()
	nix := env.nixThere(ctx)
	out := wireToolsRun{Installed: []string{}, Failed: []wireSkip{}}
	wanted, err := readWanted(env.Home)
	if err != nil {
		return err
	}
	if wanted == nil {
		out.MS = time.Since(start).Milliseconds()
		return emit(env.Stdout, out)
	}
	out.Hash = wanted.Hash
	if markerHas(live, wanted.Hash) {
		out.MS = time.Since(start).Milliseconds()
		return emit(env.Stdout, out)
	}
	profile := profileDir(env.Store, *project)
	if err := os.MkdirAll(filepath.Join(live, ".repose", "tools"), 0o755); err != nil {
		return errf(codeInternal, "make the project's tools directory: %v", err)
	}
	verb := ""
	if nix {
		verb = env.profileVerb(ctx)
	}

	// The runtimes first, as the guest's pass does: a project pinned to
	// node 20 gets that node before the tools that run under it.
	for _, lang := range runtimePins {
		want := wanted.pin(lang)
		if want == "" {
			continue
		}
		attr, reason, done := env.runtimePass(ctx, live, profile, verb, nix, lang, want)
		switch {
		case reason != "":
			out.Failed = append(out.Failed, wireSkip{Name: runtimeAttr(lang, want), Reason: reason})
		case done:
			out.Installed = append(out.Installed, attr)
		}
	}
	failed := readFailed(live)
	for _, item := range wanted.Items {
		if !itemOK(item) || env.anyBin(ctx, profile, item.Bins) {
			continue
		}
		key := itemKey(item)
		// An item that failed before is not tried again until the
		// laptop's entry for it changes, and the answer still says it is
		// not there and why.
		if reason, ok := failed[key]; ok {
			out.Failed = append(out.Failed, wireSkip{Name: item.Name, Reason: reason})
			continue
		}
		_, _ = fmt.Fprintf(env.Stderr, "Installing %s\n", item.Name)
		reason, ok := env.installItem(ctx, profile, verb, nix, item)
		if !ok {
			appendFailed(live, key, reason)
			out.Failed = append(out.Failed, wireSkip{Name: item.Name, Reason: reason})
			continue
		}
		out.Installed = append(out.Installed, item.Name)
	}

	// The marker, so the carry stops sending this list. A list that
	// changed under the pass gets no marker and so another pass, which is
	// how the guest's loop ends too.
	if again, err := readWanted(env.Home); err == nil && again != nil && again.Hash == wanted.Hash {
		writeMarker(live, wanted.Hash)
	}
	out.MS = time.Since(start).Milliseconds()
	return emit(env.Stdout, out)
}

// installItem installs one tool: the nixpkgs package that has
// bin/<command> first, into the project's profile, else the laptop's
// manager, into a directory on the login PATH. It answers the one line
// that says why not.
func (e *Env) installItem(ctx context.Context, profile, verb string, nix bool, item toolItem) (string, bool) {
	primary := item.Bins[0]
	reason := ""
	if nix {
		if attr := e.resolveAttr(ctx, primary); attr != "" {
			why, ok := e.profileAdd(ctx, profile, verb, attr)
			switch {
			case ok && e.anyBin(ctx, profile, item.Bins):
				return "", true
			case ok:
				reason = "nixpkgs#" + attr + " is in the profile, but none of its commands is on PATH"
			default:
				reason = why
			}
		} else {
			reason = "no nixpkgs package has bin/" + primary
		}
	} else {
		reason = "nix is not installed on this box"
	}
	script := managerScript(item)
	if script == "" {
		return reason, false
	}
	res, err := e.login(ctx, profile, script)
	if err != nil {
		return failReason(res, err), false
	}
	if !e.anyBin(ctx, profile, item.Bins) {
		return "installed with " + item.Manager + ", but none of its commands is on PATH", false
	}
	return "", true
}

// managerScript is the laptop manager's install, into a directory the
// login PATH has ("" for an item with no manager, which is nixpkgs or
// nothing). These installs land outside the project's profile, so a
// rewind does not take them back; nothing else on a box the user owns
// can place them.
func managerScript(item toolItem) string {
	if !slices.Contains(managers, item.Manager) || !toolNameRe.MatchString(item.Pkg) {
		return ""
	}
	version := item.Version
	if version != "" && !toolVerRe.MatchString(version) {
		return ""
	}
	at := version
	if at == "" {
		at = "latest"
	}
	timeout := "timeout " + installTimeout + " "
	switch item.Manager {
	case "npm", "pnpm", "bun":
		return timeout + `npm install -g --no-fund --no-audit --prefix "$HOME/.local" ` + shellQuote(item.Pkg+"@"+at)
	case "go":
		return `mkdir -p "$HOME/.local/bin" && ` + timeout +
			`env GOBIN="$HOME/.local/bin" go install ` + shellQuote(item.Pkg+"@"+at)
	case "cargo":
		script := timeout + `cargo install --locked --root "$HOME/.local" ` + shellQuote(item.Pkg)
		if version != "" {
			script += " --version " + shellQuote(version)
		}
		return script
	case "uv", "pipx":
		spec := item.Pkg
		if version != "" {
			spec += "==" + version
		}
		return timeout + "uv tool install " + shellQuote(spec)
	}
	return ""
}

// runtimePass makes the pinned version of one runtime the one the
// project's environment provides (DECISIONS I-265). The attribute it
// added is recorded in the live subvolume and removed when the project
// asks for another version. It answers the attribute it installed,
// the reason it could not, and whether anything changed.
func (e *Env) runtimePass(ctx context.Context, live, profile, verb string, nix bool, lang, want string) (string, string, bool) {
	if have := e.runtimeVersion(ctx, profile, lang); have == want {
		return "", "", false
	}
	attr := runtimeAttr(lang, want)
	if !nix {
		return "", "nix is not installed on this box", false
	}
	record := filepath.Join(live, ".repose", "tools", lang)
	if prev, err := os.ReadFile(record); err == nil {
		if old := strings.TrimSpace(string(prev)); old != "" && old != attr {
			e.profileRemove(ctx, profile, old)
		}
	}
	_, _ = fmt.Fprintf(e.Stderr, "Installing %s\n", attr)
	reason, ok := e.profileAdd(ctx, profile, verb, attr)
	if ok && e.runtimeVersion(ctx, profile, lang) == want {
		if err := os.WriteFile(record, []byte(attr+"\n"), 0o644); err != nil {
			return "", oneLine(err.Error()), false
		}
		return attr, "", true
	}
	// What it added is not the runtime the project asked for, so it does
	// not stay in the profile pretending to be.
	e.profileRemove(ctx, profile, attr)
	_ = os.Remove(record)
	if reason == "" {
		reason = "the project's " + lang + " is still " + displayVersion(e.runtimeVersion(ctx, profile, lang))
	}
	return "", reason, false
}

func displayVersion(v string) string {
	if v == "" {
		return "missing"
	}
	return v
}

// runtimeAttr is the nixpkgs attribute for a runtime's version
// (guest-conventions.md "Tools carry").
func runtimeAttr(lang, version string) string {
	switch lang {
	case "node":
		return "nodejs_" + version
	case "ruby":
		return "ruby_" + strings.ReplaceAll(version, ".", "_")
	case "java":
		return "jdk" + version + "_headless"
	}
	return ""
}

// runtimeVersion is the version of a runtime the project's environment
// provides now, "" for none: node's major, ruby's series, and the major
// of java's quoted version ("1.8.0_462" is 8).
func (e *Env) runtimeVersion(ctx context.Context, profile, lang string) string {
	script := lang + " --version 2>/dev/null"
	if lang == "java" {
		script = "java -version 2>&1"
	}
	res, err := e.login(ctx, profile, script)
	if err != nil {
		return ""
	}
	return parseRuntimeVersion(lang, string(res.Stdout))
}

var (
	nodeVersionRe = regexp.MustCompile(`v?([0-9]+)`)
	rubyVersionRe = regexp.MustCompile(`ruby ([0-9]+\.[0-9]+)`)
	javaVersionRe = regexp.MustCompile(`version "([^"]+)"`)
)

func parseRuntimeVersion(lang, out string) string {
	switch lang {
	case "node":
		if m := nodeVersionRe.FindStringSubmatch(firstLine(out)); m != nil {
			return m[1]
		}
	case "ruby":
		if m := rubyVersionRe.FindStringSubmatch(out); m != nil {
			return m[1]
		}
	case "java":
		m := javaVersionRe.FindStringSubmatch(out)
		if m == nil {
			return ""
		}
		v := strings.TrimPrefix(m[1], "1.")
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == '.' || r == '_' || r == '+' || r == '-'
		})
		if len(parts) == 0 {
			return ""
		}
		return parts[0]
	}
	return ""
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line)
}

// pin is the version the list asks for, "" for none or for anything but
// digits and dots (a base older than I-265 ignored these keys the same
// way).
func (w *toolsWanted) pin(lang string) string {
	v := ""
	switch lang {
	case "node":
		v = w.Node
	case "ruby":
		v = w.Ruby
	case "java":
		v = w.Java
	}
	if !pinRe.MatchString(v) {
		return ""
	}
	return v
}

// readWanted is the carry's list, nil when the laptop has not written one
// or wrote an empty file.
func readWanted(home string) (*toolsWanted, error) {
	if home == "" {
		return nil, errf(codeInternal, "the login user has no home directory")
	}
	b, err := os.ReadFile(filepath.Join(home, ".repose", "tools-wanted.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errf(codeInternal, "read the carry's tool list: %v", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	var w toolsWanted
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, errf(codeInvalidArgument, "the carry's tool list is not JSON")
	}
	return &w, nil
}

// itemOK: an entry the agent will act on. The laptop bounds these
// already; one outside the shapes is left alone rather than run.
func itemOK(item toolItem) bool {
	if !toolNameRe.MatchString(item.Name) || len(item.Bins) == 0 {
		return false
	}
	for _, bin := range item.Bins {
		if !toolBinRe.MatchString(bin) {
			return false
		}
	}
	return item.Version == "" || toolVerRe.MatchString(item.Version)
}

// itemKey identifies one entry of the list, so a failure is forgotten as
// soon as the laptop's entry for it changes (the guest's item_key).
func itemKey(item toolItem) string {
	return strings.Join([]string{item.Name, item.Manager, item.Pkg, item.Version}, "|")
}

// The carry's records, inside the live subvolume so a rewind drops them
// with the tools they describe: the marker the laptop compares its hash
// against, and the items a pass could not install, each with the line
// that said why.
func markerPath(live string) string { return filepath.Join(live, ".repose", "carry", "tools") }
func failedPath(live string) string { return filepath.Join(live, ".repose", "tools", "failed") }

func markerHas(live, hash string) bool {
	b, err := os.ReadFile(markerPath(live))
	return err == nil && strings.TrimSpace(string(b)) == hash && hash != ""
}

// writeMarker is best effort: a marker that was not written costs the
// next run another pass, which installs nothing it already has.
func writeMarker(live, hash string) {
	if err := os.MkdirAll(filepath.Dir(markerPath(live)), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(markerPath(live), []byte(hash+"\n"), 0o644)
}

func readFailed(live string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(failedPath(live))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		key, reason, ok := strings.Cut(line, "\t")
		if !ok || key == "" {
			continue
		}
		out[key] = reason
	}
	return out
}

func appendFailed(live, key, reason string) {
	f, err := os.OpenFile(failedPath(live), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s\t%s\n", key, oneLine(reason))
}
