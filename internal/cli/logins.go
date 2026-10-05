package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"golang.org/x/term"
)

// Choosing which of the laptop's logins `run` copies (DECISIONS I-422).
// The choice lives in the laptop's config.toml, never in the api: the
// logins travel laptop to guest over SSH and the api never sees them, so
// a setting about them has no business there either (R2-8). A [logins]
// table holds the list for every project; [projects.NAME.logins] replaces
// it for one. A login left out is not sent, and a copy an earlier run left
// on the machine is removed when it is byte for byte the laptop's (the
// I-298 rule), so a login made on the machine stays.

// loginItem is one row of `repose secrets choose`.
type loginItem struct {
	Name string // what config.toml and `repose secrets choose` call it
	What string
}

// envLogin is the .env files' name in the list: they hold more live keys
// than the tool logins do, so they are chosen in the same place (I-197).
const envLogin = "env"

// loginItems is every row, in the order `repose secrets choose` shows them: the
// credRows, then the .env files.
func loginItems() []loginItem {
	what := map[string]string{
		"gh":       "GitHub CLI login: every repo you can reach",
		"codex":    "Codex CLI login",
		"opencode": "opencode login",
	}
	var items []loginItem
	for _, r := range credRows {
		items = append(items, loginItem{Name: r.Label, What: what[r.Label]})
	}
	return append(items, loginItem{Name: envLogin, What: "gitignored .env files"})
}

func loginNames() []string {
	var names []string
	for _, it := range loginItems() {
		names = append(names, it.Name)
	}
	return names
}

func isLoginName(n string) bool {
	for _, it := range loginItems() {
		if it.Name == n {
			return true
		}
	}
	return false
}

// loginSkip is the set `run` leaves on the laptop for the project named
// slug: its own list when config.toml has one, else the global list.
// chosen is false when neither exists (run then names `repose secrets choose`
// once per copy). unknown are names in the list that are not logins.
func (c Config) loginSkip(slug string) (skip map[string]bool, chosen bool, unknown []string) {
	var list *[]string
	if p, ok := c.Projects[slug]; ok && slug != "" && p.Logins.Skip != nil {
		list = p.Logins.Skip
	} else if c.Logins.Skip != nil {
		list = c.Logins.Skip
	}
	skip = map[string]bool{}
	if list == nil {
		return skip, false, nil
	}
	for _, n := range *list {
		if isLoginName(n) {
			skip[n] = true
		} else {
			unknown = append(unknown, n)
		}
	}
	return skip, true, unknown
}

// loginSkip is Config.loginSkip for `run`, which warns about a name in
// the list that is not a login and carries on without it.
func (e *Env) loginSkip(slug string) (map[string]bool, bool) {
	skip, chosen, unknown := e.Cfg.loginSkip(slug)
	for _, n := range unknown {
		e.warn("config.toml lists %q under logins, which is not a login repose copies (%s); it was ignored.", n, strings.Join(loginNames(), ", "))
	}
	return skip, chosen
}

// loginsScope is what `repose secrets choose` reads and writes: the
// global list, or one project's.
type loginsScope struct {
	Slug string // "" for every project
}

func (s loginsScope) path() []string {
	if s.Slug == "" {
		return []string{"logins"}
	}
	return []string{"projects", s.Slug, "logins"}
}

// current is the scope's effective list and whether the scope has a list
// of its own (a project without one follows the global list).
func (s loginsScope) current(c Config) (skip map[string]bool, own bool) {
	skip, _, _ = c.loginSkip(s.Slug)
	if s.Slug == "" {
		return skip, c.Logins.Skip != nil
	}
	p, ok := c.Projects[s.Slug]
	return skip, ok && p.Logins.Skip != nil
}

func (e *Env) loginsScope(ctx context.Context, projectArg string) (loginsScope, error) {
	if e.resolveArg(projectArg) == "" {
		return loginsScope{}, nil
	}
	p, err := requireProject(ctx, e, projectArg)
	if err != nil {
		return loginsScope{}, err
	}
	return loginsScope{Slug: p.Slug}, nil
}

// SecretsChooseCmd implements `repose secrets choose`: with --on or --off
// it sets the NAMEs, with --reset it drops the scope's list, and with
// none it shows the toggle list on a terminal and the list otherwise.
func SecretsChooseCmd(ctx context.Context, e *Env, projectArg string, on, off, reset bool, names []string) error {
	n := 0
	for _, b := range []bool{on, off, reset} {
		if b {
			n++
		}
	}
	switch {
	case n > 1:
		return exitf(ExitUsage, "Give one of --on, --off and --reset.")
	case (on || off) && len(names) == 0:
		return exitf(ExitUsage, "Name what to turn %s: %s.", map[bool]string{true: "on", false: "off"}[on], strings.Join(loginNames(), ", "))
	case !on && !off && len(names) > 0:
		return exitf(ExitUsage, "Say --on or --off before the names: `repose secrets choose --off %s`.", strings.Join(names, " "))
	case reset:
		return chooseReset(ctx, e, projectArg)
	case on || off:
		return chooseSet(ctx, e, projectArg, on, names)
	}
	return chooseList(ctx, e, projectArg)
}

func chooseList(ctx context.Context, e *Env, projectArg string) error {
	scope, err := e.loginsScope(ctx, projectArg)
	if err != nil {
		return err
	}
	skip, own := scope.current(e.Cfg)
	found := e.loginsFound()
	if !e.JSON && term.IsTerminal(int(os.Stdin.Fd())) && writerIsTerminal(e.ErrOut) {
		on, saved, err := pickLoginsTTY(loginsHeader(scope, own), loginItems(), skip, found)
		if err != nil {
			return err
		}
		if !saved {
			_, _ = fmt.Fprintln(e.Out, "Nothing changed.")
			return nil
		}
		return e.saveLoginSkip(scope, offOf(on))
	}
	_, _ = fmt.Fprintln(e.Out, loginsHeader(scope, own)+":")
	writeLoginRows(e.Out, skip, found)
	return nil
}

func writeLoginRows(w io.Writer, skip map[string]bool, found map[string]string) {
	for _, it := range loginItems() {
		state := "on "
		if skip[it.Name] {
			state = "off"
		}
		_, _ = fmt.Fprintf(w, "  %s  %-9s %s%s\n", state, it.Name, it.What, foundNote(found, it.Name))
	}
}

func loginsHeader(s loginsScope, own bool) string {
	switch {
	case s.Slug == "":
		return "Copied at each repose run, for every project"
	case own:
		return "Copied to " + s.Slug + " at each repose run (its own list)"
	default:
		return "Copied to " + s.Slug + " at each repose run (the shared list)"
	}
}

// loginsFound says, per row, what this laptop has: "" when it has the
// login, else a note. The .env count is the current checkout's.
func (e *Env) loginsFound() map[string]string {
	found := map[string]string{}
	for _, r := range credRows {
		if _, err := os.Stat(filepath.Join(e.HomeDir, r.laptopRel())); err != nil {
			found[r.Label] = "not logged in on this laptop"
		}
	}
	if root := gitRepoRoot(e.Cwd); root != "" {
		if files, err := buildEnvCarry(root); err == nil {
			switch len(files) {
			case 0:
				found[envLogin] = "none in this checkout"
			case 1:
				found[envLogin] = "1 in this checkout: " + files[0].Rel
			default:
				found[envLogin] = fmt.Sprintf("%d in this checkout", len(files))
			}
		}
	}
	return found
}

// writerIsTerminal is whether w is a terminal (a test's buffer is not).
func writerIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func foundNote(found map[string]string, name string) string {
	if n := found[name]; n != "" {
		return " (" + n + ")"
	}
	return ""
}

func offOf(on map[string]bool) []string {
	var off []string
	for _, n := range loginNames() {
		if !on[n] {
			off = append(off, n)
		}
	}
	return off
}

func chooseSet(ctx context.Context, e *Env, projectArg string, turnOn bool, names []string) error {
	var bad []string
	for _, n := range names {
		if !isLoginName(n) {
			bad = append(bad, n)
		}
	}
	if len(bad) > 0 {
		return exitf(ExitUsage, "Not a login repose copies: %s. The names are %s.", strings.Join(bad, ", "), strings.Join(loginNames(), ", "))
	}
	scope, err := e.loginsScope(ctx, projectArg)
	if err != nil {
		return err
	}
	skip, _ := scope.current(e.Cfg)
	for _, n := range names {
		skip[n] = !turnOn
	}
	var off []string
	for _, n := range loginNames() {
		if skip[n] {
			off = append(off, n)
		}
	}
	return e.saveLoginSkip(scope, off)
}

// chooseReset is --reset: a project goes back to the list for every
// project; without one, every login is copied again.
func chooseReset(ctx context.Context, e *Env, projectArg string) error {
	scope, err := e.loginsScope(ctx, projectArg)
	if err != nil {
		return err
	}
	if err := writeLoginsTable(e.Dir, scope, nil, true); err != nil {
		return err
	}
	if scope.Slug == "" {
		_, _ = fmt.Fprintln(e.Out, "Every login is copied again, except where a project has its own list.")
	} else {
		_, _ = fmt.Fprintf(e.Out, "%s follows the list for every project again.\n", scope.Slug)
	}
	return nil
}

func (e *Env) saveLoginSkip(scope loginsScope, off []string) error {
	before, _ := scope.current(e.Cfg)
	if err := writeLoginsTable(e.Dir, scope, off, false); err != nil {
		return err
	}
	where := "every project"
	if scope.Slug != "" {
		where = scope.Slug
	}
	if len(off) == 0 {
		_, _ = fmt.Fprintf(e.Out, "Saved: every login is copied, for %s.\n", where)
		return nil
	}
	verb := "stay"
	if len(off) == 1 {
		verb = "stays"
	}
	_, _ = fmt.Fprintf(e.Out, "Saved: %s %s on your laptop, for %s. The next repose run removes copies an earlier run left on the machine.\n", strings.Join(off, ", "), verb, where)
	for _, n := range off {
		if n == "gh" && !before["gh"] {
			_, _ = fmt.Fprintln(e.Out, "Without gh, git on the machine cannot push to GitHub until you run `gh auth login` there or store a token as a secret (see /docs/secrets).")
		}
	}
	return nil
}

// writeLoginsTable sets the scope's skip list in config.toml, or removes
// its table. Only that table's lines change: the rest of the file, its
// comments included, stays byte for byte. The result must decode to the
// list asked for, or the file is left alone (a hand-written dotted key
// such as `logins.skip = [...]` at the top would otherwise be duplicated).
func writeLoginsTable(dir string, scope loginsScope, off []string, remove bool) error {
	path := configPath(dir)
	if t, err := filepath.EvalSymlinks(path); err == nil {
		path = t // a dotfiles symlink: write its target
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	var body []string
	if !remove {
		q := make([]string, len(off))
		for i, n := range off {
			q[i] = fmt.Sprintf("%q", n)
		}
		body = []string{"skip = [" + strings.Join(q, ", ") + "]"}
	}
	out := replaceTOMLTable(string(b), scope.path(), body, remove)

	var cfg Config
	if _, err := toml.Decode(out, &cfg); err != nil {
		return exitf(ExitGeneric, "Could not update %s (%v); it was left as it was. Set the list there by hand: see /docs/cli#configtoml.", path, err)
	}
	got, own := scope.current(cfg)
	want := map[string]bool{}
	for _, n := range off {
		want[n] = true
	}
	if remove && own || !remove && (!own || !sameSet(got, want)) {
		return exitf(ExitGeneric, "Could not update %s: it sets the logins list in a way repose does not edit. It was left as it was; change it by hand.", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func sameSet(a, b map[string]bool) bool {
	for k, v := range a {
		if v && !b[k] {
			return false
		}
	}
	for k, v := range b {
		if v && !a[k] {
			return false
		}
	}
	return true
}

// replaceTOMLTable puts body in the table at path: in place of the
// table's keys when the file has the table (its comment lines stay), at
// the end otherwise. remove drops the table, header and all.
func replaceTOMLTable(src string, path, body []string, remove bool) string {
	lines := strings.Split(src, "\n")
	start, end := -1, len(lines)
	for i, l := range lines {
		p, ok := tomlHeader(l)
		if !ok {
			continue
		}
		if start >= 0 {
			end = i
			break
		}
		if equalPath(p, path) {
			start = i
		}
	}
	if start < 0 {
		if remove {
			return src
		}
		out := strings.TrimRight(src, "\n")
		if out != "" {
			out += "\n\n"
		}
		return out + "[" + tomlPath(path) + "]\n" + strings.Join(body, "\n") + "\n"
	}
	// Trailing blank lines belong to the gap before the next table.
	for end > start+1 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	var keep []string
	if !remove {
		keep = append(keep, lines[start])
		for _, l := range lines[start+1 : end] {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				keep = append(keep, l)
			}
		}
		keep = append(keep, body...)
	}
	out := append(append(append([]string{}, lines[:start]...), keep...), lines[end:]...)
	s := strings.Join(out, "\n")
	if remove {
		for strings.Contains(s, "\n\n\n") {
			s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
		}
		s = strings.TrimLeft(s, "\n")
		if strings.HasSuffix(s, "\n\n") {
			s = strings.TrimRight(s, "\n") + "\n"
		}
	}
	return s
}

// tomlHeader reads a `[a.b."c"]` table header line into its keys.
// Array-of-tables headers (`[[x]]`) are headers too, with ok, and never
// equal a path this file writes.
func tomlHeader(line string) ([]string, bool) {
	l := strings.TrimSpace(line)
	if !strings.HasPrefix(l, "[") {
		return nil, false
	}
	if strings.HasPrefix(l, "[[") {
		return []string{"[["}, true
	}
	var keys []string
	var cur strings.Builder
	quote := byte(0)
	for i := 1; i < len(l); i++ {
		c := l[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '.':
			keys = append(keys, strings.TrimSpace(cur.String()))
			cur.Reset()
		case c == ']':
			return append(keys, strings.TrimSpace(cur.String())), true
		default:
			cur.WriteByte(c)
		}
	}
	return nil, false
}

func equalPath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// tomlPath writes keys as a header: bare where TOML allows, quoted
// otherwise.
func tomlPath(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		bare := k != ""
		for _, c := range k {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
				bare = false
			}
		}
		if bare {
			out[i] = k
		} else {
			out[i] = fmt.Sprintf("%q", k)
		}
	}
	return strings.Join(out, ".")
}

// errPickCancelled is q or Ctrl-C in the toggle list.
var errPickCancelled = errors.New("cancelled")

// pickLogins runs the toggle list on r (a terminal in raw mode) and w:
// up and down (arrows, k, j) move, space toggles, Enter saves, q or
// Ctrl-C leaves without saving. It returns the rows left on.
func pickLogins(r io.Reader, w io.Writer, header string, items []loginItem, skip map[string]bool, found map[string]string) (map[string]bool, error) {
	on := map[string]bool{}
	for _, it := range items {
		on[it.Name] = !skip[it.Name]
	}
	cur := 0
	drawn := 0
	draw := func() {
		var b strings.Builder
		if drawn > 0 {
			fmt.Fprintf(&b, "\x1b[%dA\r\x1b[J", drawn)
		}
		fmt.Fprintf(&b, "%s:\r\nspace toggles, enter saves, q leaves\r\n\r\n", header)
		for i, it := range items {
			mark, ptr := " ", "  "
			if on[it.Name] {
				mark = "x"
			}
			if i == cur {
				ptr = "> "
			}
			fmt.Fprintf(&b, "%s[%s] %-9s %s%s\r\n", ptr, mark, it.Name, it.What, foundNote(found, it.Name))
		}
		_, _ = io.WriteString(w, b.String())
		drawn = len(items) + 3
	}
	draw()
	br := bufio.NewReader(r)
	for {
		c, err := br.ReadByte()
		if err != nil {
			return nil, err
		}
		switch c {
		case '\r', '\n':
			return on, nil
		case 0x03, 'q':
			return nil, errPickCancelled
		case ' ', 'x':
			on[items[cur].Name] = !on[items[cur].Name]
		case 'k':
			cur = (cur + len(items) - 1) % len(items)
		case 'j':
			cur = (cur + 1) % len(items)
		case 0x1b:
			if b, _ := br.ReadByte(); b != '[' {
				return nil, errPickCancelled
			}
			switch b, _ := br.ReadByte(); b {
			case 'A':
				cur = (cur + len(items) - 1) % len(items)
			case 'B':
				cur = (cur + 1) % len(items)
			}
		default:
			continue
		}
		draw()
	}
}

// pickLoginsTTY is pickLogins on the real terminal, which goes back to
// how it was however the list ends.
func pickLoginsTTY(header string, items []loginItem, skip map[string]bool, found map[string]string) (map[string]bool, bool, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false, err
	}
	restore := func() { _ = term.Restore(fd, state) }
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-sig:
			restore()
			os.Exit(ExitInterrupted)
		case <-done:
		}
	}()
	on, err := pickLogins(os.Stdin, os.Stderr, header, items, skip, found)
	close(done)
	signal.Stop(sig)
	restore()
	if errors.Is(err, errPickCancelled) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return on, true, nil
}

// loginsLine is `run`'s note under "Credentials:": the logins left on
// the laptop, once a choice was made (I-484: nothing before one). It is said only
// when the logins' part went (copied names a login, not just "git", which
// is named on every run), so it comes once per change, not every run.
func loginsLine(copied []string, skip map[string]bool, chosen bool) string {
	went := false
	for _, c := range copied {
		went = went || isLoginName(c)
	}
	if !went {
		return ""
	}
	if !chosen {
		return ""
	}
	var off []string
	for n := range skip {
		off = append(off, n)
	}
	sort.Strings(off)
	if len(off) == 0 {
		return ""
	}
	return "Left on your laptop: " + strings.Join(off, ", ") + "."
}
