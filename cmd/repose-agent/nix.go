package main

// The project's environment on a machine the user owns is Nix in
// single-user mode (DECISIONS I-832): the store at /nix is shared between
// projects, because the shared downloads are what make a second project
// cheap, and the profile is inside the project's live subvolume, so a
// rewind takes an environment change back with the files.
//
// Every process these commands start goes through Env.Runner, which is
// undo.Runner — the seam internal/undo's backend uses for btrfs. The real
// one is undo.Exec; a test gives the agent a fake.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/heracraft/repose/internal/undo"
)

// nixExperimental are the features `nix profile` and a `nixpkgs#<attr>`
// installable need. A single-user install enables neither in its
// nix.conf, so every call carries them; a box that enables them already
// is unaffected.
var nixExperimental = []string{"--extra-experimental-features", "nix-command flakes"}

// maxReason bounds the one line of a failed command that reaches the
// user, and maxAttrLen a nixpkgs attribute path (internal/menu's
// MaxPackageLen).
const (
	maxReason   = 160
	maxAttrLen  = 200
	profileName = "profile"
)

// profileDir is the project's profile. `.repose` is inside the live area,
// so `point rewind` restores the environment the project had with the
// files it had.
func profileDir(store, slug string) string {
	return filepath.Join(liveDir(store, slug), ".repose", profileName)
}

// pkgAttrRe is a nixpkgs attribute path, the shape internal/menu accepts
// for a `{package}` item (its packageRe, behind ValidPackage). The agent
// checks it itself because it builds an installable and a shell word out
// of it: nothing that matches can end a Nix string, start an
// interpolation, or be read as an option.
var pkgAttrRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_+-]*(\.[A-Za-z_][A-Za-z0-9_+-]*)*$`)

func validAttr(s string) bool { return len(s) <= maxAttrLen && pkgAttrRe.MatchString(s) }

// exec runs one process through the Env's runner.
func (e *Env) exec(ctx context.Context, argv ...string) (undo.Result, error) {
	return e.Runner.Run(ctx, argv...)
}

// nixArgs is a `nix` command line with the experimental features on it.
func nixArgs(rest ...string) []string {
	argv := make([]string, 0, len(rest)+3)
	argv = append(argv, "nix")
	argv = append(argv, nixExperimental...)
	return append(argv, rest...)
}

// nixReady refuses a box with no nix by naming what is absent, rather
// than failing inside the first install with nix's own wording. The CLI
// reads the message, not only the code, and turns it into the install
// that puts Nix there (internal/cli agentNoNix), so the first words of
// it are part of the contract.
func (e *Env) nixReady(ctx context.Context) error {
	if _, err := e.exec(ctx, "nix", "--version"); err != nil {
		var ee *undo.ExitError
		// How a shell says it cannot find a command: exit 127, and
		// "command not found" on stderr. The agent runs through a shell,
		// so an absent `nix` arrives as one of those rather than as a Go
		// lookup error, and shells differ on which.
		if errors.As(err, &ee) && ee.Result.ExitCode != 127 &&
			!strings.Contains(string(ee.Result.Stderr), "not found") {
			return errf(codeInternal, "nix is on the box but `nix --version` exited %d", ee.Result.ExitCode)
		}
		return errf(codeNotFound, "nix is not installed on this box")
	}
	return nil
}

// nixThere is the same question where the answer is not a refusal: the
// tools carry installs with the laptop's package manager when the box has
// no nix, and reports per tool what it then could not do.
func (e *Env) nixThere(ctx context.Context) bool {
	_, err := e.exec(ctx, "nix", "--version")
	return err == nil
}

// profileVerb is `add` or `install`. `nix profile add` is the name since
// Nix 2.24 and `install` the one before it, and both exist on the
// versions in between, so the box's own nix says which it has rather than
// the agent assuming (nix/guest/base/tools-carry.sh probes the same way).
func (e *Env) profileVerb(ctx context.Context) string {
	if _, err := e.exec(ctx, nixArgs("profile", "add", "--help")...); err == nil {
		return "add"
	}
	return "install"
}

// profileAdd installs one nixpkgs attribute into the project's profile.
// It answers the one line a user reads when it fails.
func (e *Env) profileAdd(ctx context.Context, profile, verb, attr string) (string, bool) {
	res, err := e.exec(ctx, nixArgs("profile", verb, "--profile", profile, "nixpkgs#"+attr)...)
	if err == nil {
		return "", true
	}
	return failReason(res, err), false
}

// profileRemove drops an attribute the project no longer pins. A failure
// is not reported: what the profile holds is read back, and a remove that
// did nothing leaves the next install to decide.
func (e *Env) profileRemove(ctx context.Context, profile, attr string) {
	_, _ = e.exec(ctx, nixArgs("profile", "remove", "--profile", profile, attr)...)
}

// nixPackage is one element of a profile, as `env show` prints it.
type nixPackage struct {
	Name string
	Attr string
}

// profilePackages is what the project's profile holds. A project whose
// profile was never made holds nothing, and answers so without nix.
func (e *Env) profilePackages(ctx context.Context, profile string) ([]nixPackage, error) {
	if _, err := os.Lstat(profile); err != nil {
		return nil, nil
	}
	res, err := e.exec(ctx, nixArgs("profile", "list", "--profile", profile, "--json")...)
	if err != nil {
		return nil, errf(codeInternal, "read the project's profile: %s", failReason(res, err))
	}
	return parseProfileList(res.Stdout)
}

// profileElement is one element of `nix profile list --json`.
type profileElement struct {
	AttrPath string `json:"attrPath"`
}

// parseProfileList reads both shapes nix has printed: an object keyed by
// the element's name, and the array of the versions before it. Either way
// the answer is sorted by name, so `env show` is the same listing twice.
func parseProfileList(b []byte) ([]nixPackage, error) {
	var doc struct {
		Elements json.RawMessage `json:"elements"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, errf(codeInternal, "parse the profile listing")
	}
	out := []nixPackage{}
	if len(doc.Elements) == 0 {
		return out, nil
	}
	var byName map[string]profileElement
	if err := json.Unmarshal(doc.Elements, &byName); err == nil {
		for name, el := range byName {
			out = append(out, nixPackage{Name: name, Attr: attrOf(el.AttrPath)})
		}
	} else {
		var list []profileElement
		if err := json.Unmarshal(doc.Elements, &list); err != nil {
			return nil, errf(codeInternal, "parse the profile listing")
		}
		for _, el := range list {
			attr := attrOf(el.AttrPath)
			out = append(out, nixPackage{Name: attr, Attr: attr})
		}
	}
	slices.SortFunc(out, func(a, b nixPackage) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// attrOf is the nixpkgs attribute path of an element's attrPath, which
// carries the flake's output and system in front of it
// (`legacyPackages.x86_64-linux.python312Packages.black`).
func attrOf(attrPath string) string {
	parts := strings.Split(attrPath, ".")
	if len(parts) > 2 && (parts[0] == "legacyPackages" || parts[0] == "packages") {
		parts = parts[2:]
	}
	return strings.Join(parts, ".")
}

// resolveAttr is the nixpkgs attribute whose bin/<cmd> it is, "" for
// none: nix-locate's index when the box has it, else the attribute named
// like the command when nixpkgs has one. The attribute named like the
// command wins, then one outside a package set, then the shortest, then
// the first by name — the order nix/guest/base/tools-carry.sh picks in.
func (e *Env) resolveAttr(ctx context.Context, cmd string) string {
	res, err := e.exec(ctx, "nix-locate", "--minimal", "--no-group",
		"--type", "x", "--type", "s", "--whole-name", "--at-root", "/bin/"+cmd)
	if err == nil {
		best := ""
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			attr := strings.TrimSpace(line)
			if attr == "" {
				continue
			}
			// Each line is `<attr>.<output>`.
			if i := strings.LastIndex(attr, "."); i > 0 {
				attr = attr[:i]
			}
			if !validAttr(attr) {
				continue
			}
			if attr == cmd {
				return cmd
			}
			if betterAttr(attr, best) {
				best = attr
			}
		}
		if best != "" {
			return best
		}
	}
	if _, err := e.exec(ctx, nixArgs("eval", "--raw", "nixpkgs#"+cmd+".name")...); err == nil {
		return cmd
	}
	return ""
}

// betterAttr: is a a better pick than b, where "" is the worst?
func betterAttr(a, b string) bool {
	if b == "" {
		return true
	}
	if inSet, bInSet := strings.Contains(a, "."), strings.Contains(b, "."); inSet != bInSet {
		return !inSet
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// loginPath is the PATH every probe runs with: the project's profile
// first, then the directories a tool lands in outside it, then the login
// shell's own (nix/guest/base/tools-carry.sh, search_path). So `command
// -v` answers for the shell the user and the agents get, and a tool the
// user installed with `go install` or `cargo install` is not installed
// again.
func loginPath(profile string) string {
	dirs := []string{
		shellQuote(filepath.Join(profile, "bin")),
		`"$HOME/.local/bin"`, `"$HOME/.local/share/pnpm"`, `"$HOME/.npm-global/bin"`,
		`"$HOME/go/bin"`, `"$HOME/.cargo/bin"`, `"$HOME/.bun/bin"`, `"$HOME/.deno/bin"`,
		`"$HOME/.nix-profile/bin"`, `"$PATH"`,
	}
	return "export PATH=" + strings.Join(dirs, ":") + "; "
}

// login runs one command in a login shell with that PATH.
func (e *Env) login(ctx context.Context, profile, script string) (undo.Result, error) {
	return e.exec(ctx, "bash", "-lc", loginPath(profile)+script)
}

// anyBin reports whether the project's environment already provides any
// of a tool's commands.
func (e *Env) anyBin(ctx context.Context, profile string, bins []string) bool {
	for _, bin := range bins {
		if !toolBinRe.MatchString(bin) {
			continue
		}
		if _, err := e.login(ctx, profile, "command -v "+shellQuote(bin)+" >/dev/null 2>&1"); err == nil {
			return true
		}
	}
	return false
}

// shellQuote makes one shell word of s, which is how a validated name
// reaches a login shell.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// failReason is the one line of a failed command a user reads: its last
// non-empty output line, bounded. Nothing else of the output travels.
func failReason(res undo.Result, err error) string {
	for _, b := range [][]byte{res.Stderr, res.Stdout} {
		lines := strings.Split(string(b), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			if line := strings.TrimSpace(lines[i]); line != "" {
				return oneLine(line)
			}
		}
	}
	if err != nil {
		return oneLine(err.Error())
	}
	return "the command failed"
}

// oneLine is a message that fits on a line of a user's terminal and
// cannot break the records the agent writes a reason into.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxReason {
		return string(r[:maxReason])
	}
	return s
}
