package main

// `env apply` and `env show`: the project's environment on a machine the
// user owns. The project's `repose.nix` at the checkout root is its
// environment here as it is on a hosted machine (DECISIONS I-832), and
// the profile it makes lives in the project's live subvolume, so a rewind
// undoes an environment change.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// repoConfigName is the project's configuration at the checkout root, the
// file the sync delivers (internal/cli/repoconfig.go). A box in
// single-user mode has no home-manager to evaluate it against, so the
// file is read for the packages it asks for and everything else it
// declares is named in `skipped`. Half an environment the user is told
// about beats half an environment they find out about from `npm run dev`.
const repoConfigName = "repose.nix"

// maxElement bounds how much of a list element a `skipped` entry names.
const maxElement = 40

func cmdEnv(ctx context.Context, env *Env, args []string) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(env.Stderr, usage)
		return errf(codeInvalidArgument, "no env command given")
	}
	switch args[0] {
	case "apply":
		return cmdEnvApply(ctx, env, args[1:])
	case "show":
		return cmdEnvShow(ctx, env, args[1:])
	}
	_, _ = fmt.Fprint(env.Stderr, usage)
	return errf(codeInvalidArgument, "unknown env command %q", args[0])
}

// cmdEnvApply builds the project's environment: every package its
// repose.nix asks for, and every attribute `--add` names beside them, in
// the profile inside the live subvolume. `repose config add` on the
// laptop is what passes `--add`: the menu is the same menu, and on a box
// the user owns no api renders a fragment, so the name travels as a name.
//
// It is idempotent — a package already there is reported, not
// reinstalled — and it never removes anything, because the carried tools
// land in the same profile.
func cmdEnvApply(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "env apply")
	project := fs.String("project", "", "the project's slug")
	add := fs.String("add", "", "nixpkgs attribute paths to install beside the project's own, comma-separated")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	extra, err := parseAdd(*add)
	if err != nil {
		return err
	}
	live, err := requireProject(env, *project)
	if err != nil {
		return err
	}
	wanted, skipped, err := wantedPackages(live)
	if err != nil {
		return err
	}
	wanted = mergeAttrs(wanted, extra)
	if err := env.nixReady(ctx); err != nil {
		return err
	}
	profile := profileDir(env.Store, *project)
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		return errf(codeInternal, "make the project's profile directory: %v", err)
	}
	held, err := env.profilePackages(ctx, profile)
	if err != nil {
		return err
	}
	there := make(map[string]bool, len(held))
	for _, p := range held {
		there[p.Attr] = true
	}

	start := time.Now()
	verb := env.profileVerb(ctx)
	added, present := []string{}, []string{}
	firstFail := ""
	for _, attr := range wanted {
		if there[attr] {
			present = append(present, attr)
			continue
		}
		_, _ = fmt.Fprintf(env.Stderr, "Installing %s\n", attr)
		reason, ok := env.profileAdd(ctx, profile, verb, attr)
		if !ok {
			if firstFail == "" {
				firstFail = reason
			}
			skipped = append(skipped, wireSkip{Name: attr, Reason: reason})
			continue
		}
		added = append(added, attr)
	}
	// An apply that installed nothing and found nothing is a failure, not
	// an answer: there is no environment, and the reason the first package
	// would not build is what the user has to act on. One package of five
	// failing is reported in `skipped`, where the other four are still
	// installed (`point take` of every project answers the same way).
	if len(added) == 0 && len(present) == 0 && firstFail != "" {
		return errf(codeInternal, "install the project's packages: %s", firstFail)
	}
	return emit(env.Stdout, wireEnvApply{
		Profile: profile,
		Added:   added,
		Present: present,
		Skipped: skipped,
		MS:      time.Since(start).Milliseconds(),
	})
}

// cmdEnvShow is what the project's profile holds.
func cmdEnvShow(ctx context.Context, env *Env, args []string) error {
	fs := flagSet(env, "env show")
	project := fs.String("project", "", "the project's slug")
	if err := fs.Parse(args); err != nil {
		return parseErr(err)
	}
	if err := checkSlug("project", *project); err != nil {
		return err
	}
	if _, err := requireProject(env, *project); err != nil {
		return err
	}
	profile := profileDir(env.Store, *project)
	out := wireEnvShow{Profile: profile, Packages: []wirePackage{}}
	// A project whose environment was never applied holds nothing, and
	// says so on a box where nix is not installed yet.
	if _, err := os.Lstat(profile); err != nil {
		return emit(env.Stdout, out)
	}
	if err := env.nixReady(ctx); err != nil {
		return err
	}
	held, err := env.profilePackages(ctx, profile)
	if err != nil {
		return err
	}
	for _, p := range held {
		out.Packages = append(out.Packages, wirePackage(p))
	}
	return emit(env.Stdout, out)
}

// requireProject is the live area of a project the store holds; its
// absence is not_found, as it is for quiesce.
func requireProject(env *Env, slug string) (string, error) {
	live := liveDir(env.Store, slug)
	if _, err := os.Stat(live); err != nil {
		return "", errf(codeNotFound, "the store has no project %s", slug)
	}
	return live, nil
}

// parseAdd reads `--add`: the packages the menu named, which on a box
// the user owns reach the environment as names, because no api renders a
// fragment on this path.
func parseAdd(list string) ([]string, error) {
	out := []string{}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !validAttr(name) {
			return nil, errf(codeInvalidArgument, "--add takes nixpkgs attribute paths; %q is not one", name)
		}
		out = append(out, name)
	}
	return out, nil
}

// mergeAttrs is the file's packages and `--add`'s in one list, in order,
// without a duplicate: installing the same attribute twice costs a nix
// call and says "added" about something that was already going in.
func mergeAttrs(lists ...[]string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, list := range lists {
		for _, attr := range list {
			if seen[attr] {
				continue
			}
			seen[attr] = true
			out = append(out, attr)
		}
	}
	return out
}

// wantedPackages reads the project's repose.nix for the packages its
// profile is to hold: every plain nixpkgs name in a `home.packages` list.
// One reader answers for both shapes of the file, because the fragment
// the menu generates renders each selection as `home.packages = [
// pkgs.<name> ];` (internal/menu's catalog) and a hand-written one is
// usually one `with pkgs; [ ... ]`.
//
// Everything else the file declares comes back as skipped, named. A
// machine the user owns has Nix and no home-manager, so a `programs.*`
// module or a system service is not applied, and the user reads that here
// rather than discovering it later.
func wantedPackages(live string) ([]string, []wireSkip, error) {
	b, err := os.ReadFile(filepath.Join(live, repoConfigName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, []wireSkip{}, nil
	}
	if err != nil {
		return nil, nil, errf(codeInternal, "read the project's %s: %v", repoConfigName, err)
	}
	clean := cleanNix(string(b))
	attrs := []string{}
	skipped := []wireSkip{}
	seen := map[string]bool{}
	for _, list := range packageLists(clean) {
		for _, el := range listElements(list.body) {
			attr, ok := attrOfElement(el, list.withPkgs)
			if !ok {
				skipped = append(skipped, wireSkip{Name: shorten(el), Reason: "not a plain nixpkgs package name"})
				continue
			}
			if seen[attr] {
				continue
			}
			seen[attr] = true
			attrs = append(attrs, attr)
		}
	}
	return attrs, append(skipped, outsideAProfile(clean)...), nil
}

// attrOfElement is the nixpkgs attribute a list element names, if it
// names one at all: `pkgs.bun`, or `bun` under a `with pkgs;`.
func attrOfElement(el string, withPkgs bool) (string, bool) {
	if rest, ok := strings.CutPrefix(el, "pkgs."); ok {
		if validAttr(rest) {
			return rest, true
		}
		return "", false
	}
	if withPkgs && validAttr(el) {
		return el, true
	}
	return "", false
}

// notInAProfile is what a fragment may declare that a single-user profile
// cannot hold. Each one found is named once.
var notInAProfile = []struct{ key, name, reason string }{
	{"repose.system", "repose.system", "a system service is the box's own to run"},
	{"repose.overlays", "repose.overlays", "an overlay needs a build of the whole environment"},
	{"programs.", "programs.*", "a home-manager module, which a profile has not"},
	{"systemd.user.", "systemd.user.*", "a user unit, which a profile has not"},
	{"home.file", "home.file", "a home-manager file, which a profile has not"},
	{"xdg.configFile", "xdg.configFile", "a home-manager file, which a profile has not"},
	{"home.sessionVariables", "home.sessionVariables", "a home-manager variable, which a profile has not"},
	{"home.sessionPath", "home.sessionPath", "a home-manager variable, which a profile has not"},
}

func outsideAProfile(clean string) []wireSkip {
	out := []wireSkip{}
	for _, item := range notInAProfile {
		if strings.Contains(clean, item.key) {
			out = append(out, wireSkip{Name: item.name, Reason: item.reason})
		}
	}
	return out
}

// cleanNix blanks what a reader must not read as code: comments, and the
// body of every string. Lengths and newlines are kept, so nothing else
// moves.
func cleanNix(src string) string {
	b := []byte(src)
	n := len(b)
	for i := 0; i < n; {
		switch {
		case b[i] == '#':
			j := i
			for j < n && b[j] != '\n' {
				j++
			}
			blank(b, i, j)
			i = j
		case b[i] == '/' && i+1 < n && b[i+1] == '*':
			j := i + 2
			for j+1 < n && (b[j] != '*' || b[j+1] != '/') {
				j++
			}
			j = min(j+2, n)
			blank(b, i, j)
			i = j
		case b[i] == '\'' && i+1 < n && b[i+1] == '\'':
			// A Nix indented string: '' ... ''.
			j := i + 2
			for j+1 < n && (b[j] != '\'' || b[j+1] != '\'') {
				j++
			}
			j = min(j+2, n)
			blank(b, i, j)
			i = j
		case b[i] == '"':
			j := i + 1
			for j < n && b[j] != '"' {
				if b[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, n)
			blank(b, i, j)
			i = j
		default:
			i++
		}
	}
	return string(b)
}

func blank(b []byte, from, to int) {
	for i := from; i < to && i < len(b); i++ {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
}

// nixList is one `home.packages` list of a fragment.
type nixList struct {
	withPkgs bool
	body     string
}

// packageLists are the fragment's `home.packages` lists, in the order the
// file writes them. An assignment this reader cannot see a list in (a
// variable, a `++` of one) is left out, and `outsideAProfile` is what
// tells the user the file says more than the profile holds.
func packageLists(clean string) []nixList {
	out := []nixList{}
	const key = "home.packages"
	for i := 0; ; {
		k := strings.Index(clean[i:], key)
		if k < 0 {
			return out
		}
		rest := clean[i+k+len(key):]
		i += k + len(key)
		j := skipSpace(rest, 0)
		if j >= len(rest) || rest[j] != '=' {
			continue
		}
		j = skipSpace(rest, j+1)
		withPkgs := false
		if w, ok := cutWord(rest, j, "with"); ok {
			if w, ok := cutWord(rest, w, "pkgs"); ok && w < len(rest) && rest[w] == ';' {
				withPkgs = true
				j = skipSpace(rest, w+1)
			}
		}
		if j >= len(rest) || rest[j] != '[' {
			continue
		}
		end := matchBracket(rest, j)
		if end < 0 {
			continue
		}
		out = append(out, nixList{withPkgs: withPkgs, body: rest[j+1 : end]})
		i += end
	}
}

// cutWord steps over one word and the space after it.
func cutWord(s string, i int, word string) (int, bool) {
	if !strings.HasPrefix(s[i:], word) {
		return i, false
	}
	return skipSpace(s, i+len(word)), true
}

func skipSpace(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	return i
}

// matchBracket is the index of the `]` closing the `[` at from, -1 for
// none.
func matchBracket(s string, from int) int {
	depth := 0
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// listElements splits a list body into its top-level elements: a bare
// token, or a group (a parenthesised call, a nested list, an attribute
// set) which names no package on its own.
func listElements(body string) []string {
	out := []string{}
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case isSpace(c):
			i++
		case c == '(' || c == '[' || c == '{':
			end := matchGroup(body, i)
			if end < 0 {
				end = len(body) - 1
			}
			out = append(out, strings.Join(strings.Fields(body[i:end+1]), " "))
			i = end + 1
		default:
			j := i
			for j < len(body) && !isSep(body[j]) {
				j++
			}
			out = append(out, body[i:j])
			i = j
		}
	}
	return out
}

// matchGroup is the index of the bracket closing the one at from, -1 for
// none. Every kind counts towards the depth, which is enough once the
// strings are blanked.
func matchGroup(s string, from int) int {
	depth := 0
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isSep(c byte) bool {
	switch c {
	case '(', ')', '[', ']', '{', '}', ';', ',':
		return true
	}
	return isSpace(c)
}

// shorten names a list element in a `skipped` entry without printing a
// paragraph of the user's own file back at them.
func shorten(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxElement {
		return string(r[:maxElement])
	}
	return s
}
