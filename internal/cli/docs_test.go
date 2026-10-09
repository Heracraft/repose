package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// A feature without user docs is not done (DECISIONS I-242). These tests
// hold the CLI to the public CLI reference, apps/web/src/content/docs/cli.md
// (served at /docs/cli): every command, flag, config.toml key, environment
// variable and exit code the CLI has must be named there, in backticks, and
// nothing may be named there that the CLI does not have. A change that adds
// one without the docs fails here; so does a docs line left behind by a
// removal.

const cliDocPath = "../../apps/web/src/content/docs/cli.md"

// hiddenCommands is every hidden command, with why it stays out of the
// docs. Hiding a command is not a way around this test: a hidden command
// missing here fails it.
var hiddenCommands = map[string]string{
	// The session helper the CLI starts beside an attach (session.go). It
	// reads its options from REPOSE_SESSION and does nothing useful when
	// typed by hand.
	"repose __session": "internal helper process",
	// Run by ssh itself from the Match line in ~/.ssh/repose/config
	// (I-281), before every ssh to <project>.repose; typing it does what
	// that ssh would have done, and nobody needs to.
	"repose ssh-prepare": "run by ssh from ~/.ssh/repose/config",
	// What a git or npm user types for the CLI's settings; each only
	// says those are config.toml keys (I-622).
	"repose config set": "answers that the CLI's settings are config.toml keys",
	"repose config get": "answers that the CLI's settings are config.toml keys",
}

// undocumentedFlags is "<command path> --<flag>" for any flag that is
// intentionally left out of the docs, with why. Keep it to old names.
var undocumentedFlags = map[string]string{
	// The old name of `repose browser` (I-292): kept working, hidden from
	// help; cli.md names it once as the old name under repose browser.
	"repose open --desktop": "old name of repose browser",
	"repose open --stop":    "old name of repose browser stop",
	// One spelling per idea (I-619): the old names, hidden for a release.
	"repose open --local-port":           "old form of repose open LOCAL:PORT",
	"repose browser --stop":              "old name of repose browser stop",
	"repose browser --no-open":           "old name of repose browser --no-browser",
	"repose browser bridge --no-browser": "old name of --no-inspect",
	"repose snapshots restore --as-new":  "old name of --as",
	"repose mcp forward --remove":        "old name of repose mcp rm",
	"repose config show --revisions":     "old name of repose config revisions",
	// Moved to `repose sync` (I-367): hidden on run for a release, and
	// they only print where they went.
	"repose run --stash-remote":   "moved to repose sync --stash-remote",
	"repose run --discard-remote": "moved to repose sync --discard-remote",
	// PROJECT's old spelling (I-603): hidden for a release.
	"repose run --name":  "old spelling of repose run PROJECT",
	"repose sync --name": "old spelling of repose sync PROJECT",
}

// internalEnvVars are REPOSE_* names the package reads that no user sets.
var internalEnvVars = map[string]string{
	"REPOSE_SESSION":         "the session helper's options, set by the CLI for its own child (session.go)",
	"REPOSE_SSH_PREPARED":    "set by the CLI for its own ssh, so ssh-prepare skips work already done (sshprepare.go)",
	"REPOSE_DEVSHELL_QUIET":  "set in the guest by the exec script for the dev-shell loader, never read on the laptop (exec.go)",
	"REPOSE_TEST_GOOS":       "tests only: pretend to be another OS (goos())",
	"REPOSE_CLAUDE_PLATFORM": "tests only: the guest's platform settings path in the Claude merge script",
	"REPOSE_ENV_GEN":         "never read: a guest variable `secrets set` refuses as a name (secrets.go, DECISIONS I-475)",
	"REPOSE_CHECKOUT":        "never read: part of @@REPOSE_CHECKOUT@@, the placeholder the MCP carry's guest script replaces (carry_mcp.go, DECISIONS I-556)",
}

func readCLIDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(cliDocPath)
	if err != nil {
		t.Fatalf("read the CLI reference: %v", err)
	}
	return string(b)
}

// backtickSpans returns the text inside every `...` span of s.
var backtickRE = regexp.MustCompile("`([^`\n]+)`")

func backtickSpans(s string) []string {
	var out []string
	for _, m := range backtickRE.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// spanTokens splits a backticked span into words: `--kind console|build|ops`
// gives --kind, console, build, ops; `REPOSE_TIMING=1` gives REPOSE_TIMING, 1.
func spanTokens(span string) []string {
	return strings.FieldsFunc(span, func(r rune) bool {
		return r == ' ' || r == '|' || r == '[' || r == ']' || r == '\\' || r == '=' || r == ','
	})
}

func tokenSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, sp := range backtickSpans(text) {
		for _, tok := range spanTokens(sp) {
			set[tok] = true
		}
	}
	return set
}

// section returns the text under the "## <title>" heading, up to the next
// "## " heading.
func section(t *testing.T, doc, title string) string {
	t.Helper()
	start := strings.Index(doc, "\n## "+title+"\n")
	if start < 0 {
		t.Fatalf("cli.md has no %q section", "## "+title)
	}
	rest := doc[start+len(title)+5:]
	if end := strings.Index(rest, "\n## "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// commandPath is the command a span names ("repose config add PACKAGE..."
// names "repose config add"), and whether the span names one at all.
func commandPath(root *cobra.Command, span string) (path string, ok bool, ghost string) {
	toks := strings.Fields(span)
	if len(toks) == 0 || toks[0] != "repose" {
		return "", false, ""
	}
	cur := root
	for _, tok := range toks[1:] {
		if tok == "" || tok[0] < 'a' || tok[0] > 'z' || strings.ContainsAny(tok, "|") {
			break
		}
		if !cur.HasSubCommands() {
			break // an argument of a leaf, such as `repose completion bash`
		}
		next := findChild(cur, tok)
		if next == nil {
			return "", true, cur.CommandPath() + " " + tok
		}
		cur = next
	}
	return cur.CommandPath(), true, ""
}

func findChild(c *cobra.Command, name string) *cobra.Command {
	for _, s := range c.Commands() {
		if s.Name() == name || s.HasAlias(name) {
			return s
		}
	}
	return nil
}

// commandText maps each command path to the docs text that belongs to it:
// a "### `repose X`" heading owns the lines up to the next heading, a table
// row whose first cell is "`repose X`" owns its own line.
func commandText(root *cobra.Command, doc string) map[string]string {
	owned := map[string]string{}
	owner := ""
	for _, line := range strings.Split(doc, "\n") {
		switch {
		case strings.HasPrefix(line, "#"):
			owner = ""
			if strings.HasPrefix(line, "### ") {
				if spans := backtickSpans(line); len(spans) > 0 {
					if p, ok, _ := commandPath(root, spans[0]); ok {
						owner = p
					}
				}
			}
		case strings.HasPrefix(line, "| `repose "):
			spans := backtickSpans(line)
			if p, ok, _ := commandPath(root, spans[0]); ok {
				owned[p] += line + "\n"
			}
			continue
		}
		if owner != "" {
			owned[owner] += line + "\n"
		}
	}
	return owned
}

func walkCommands(c *cobra.Command, fn func(*cobra.Command)) {
	fn(c)
	for _, s := range c.Commands() {
		walkCommands(s, fn)
	}
}

func docsRoot() *cobra.Command {
	root := newRootCmd("dev")
	root.InitDefaultHelpCmd()
	root.InitDefaultVersionFlag()
	return root
}

func TestDocsNameEveryCommandAndFlag(t *testing.T) {
	doc := readCLIDoc(t)
	root := docsRoot()
	owned := commandText(root, doc)
	allTokens := tokenSet(doc)

	var missing []string
	flagOf := map[string]bool{}
	walkCommands(root, func(c *cobra.Command) {
		path := c.CommandPath()
		if c.Hidden {
			if _, ok := hiddenCommands[path]; !ok {
				missing = append(missing, path+": hidden, and not in hiddenCommands with a reason")
			}
			return
		}
		if c != root && !c.HasSubCommands() {
			if _, ok := owned[path]; !ok {
				missing = append(missing, path+": no `"+path+"` heading or table row")
			}
		}
		// Flags go with their command: under its heading or on its row.
		// Root flags (global) go anywhere.
		text := owned[path]
		fs := c.LocalNonPersistentFlags()
		if c == root {
			fs = c.Flags()
		}
		have := tokenSet(text)
		fs.VisitAll(func(f *pflag.Flag) {
			flagOf["--"+f.Name] = true
			if f.Shorthand != "" {
				flagOf["-"+f.Shorthand] = true
			}
			key := path + " --" + f.Name
			if f.Hidden {
				if _, ok := undocumentedFlags[key]; !ok {
					missing = append(missing, key+": hidden, and not in undocumentedFlags with a reason")
				}
				return
			}
			if _, ok := undocumentedFlags[key]; ok {
				return
			}
			in := have
			if c == root {
				in = allTokens
			}
			if !in["--"+f.Name] {
				missing = append(missing, key+": flag not documented with its command")
			}
			if f.Shorthand != "" && !in["-"+f.Shorthand] {
				missing = append(missing, path+" -"+f.Shorthand+": short flag not documented with its command")
			}
		})
		// A parent's persistent flags other than root's (`repose config
		// --global`, DECISIONS I-490) are checked on the parent.
		if c != root {
			c.PersistentFlags().VisitAll(func(f *pflag.Flag) {
				flagOf["--"+f.Name] = true
				if !allTokens["--"+f.Name] {
					missing = append(missing, path+" --"+f.Name+": persistent flag not documented")
				}
			})
		}
	})

	// The other way round: a command or flag the docs name must exist.
	var ghosts []string
	for _, sp := range backtickSpans(doc) {
		if _, ok, ghost := commandPath(root, sp); ok && ghost != "" {
			ghosts = append(ghosts, "`"+sp+"` names "+ghost+", which is not a command")
		}
		for _, tok := range spanTokens(sp) {
			if strings.HasPrefix(tok, "-") && len(tok) > 1 && !flagOf[tok] && tok != "--help" && tok != "-h" && tok != "--" {
				ghosts = append(ghosts, "`"+sp+"` names "+tok+", which no command has")
			}
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("undocumented in cli.md: %s", m)
	}
	for _, g := range ghosts {
		t.Errorf("ghost in cli.md: %s", g)
	}
}

// configKeys is every config.toml key the Config struct reads, dotted for
// tables ("sync.exclude").
func configKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("toml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			keys = append(keys, configKeys(f.Type, prefix+tag+".")...)
			continue
		}
		keys = append(keys, prefix+tag)
	}
	return keys
}

func TestDocsNameEveryConfigKey(t *testing.T) {
	doc := section(t, readCLIDoc(t), "config.toml")
	have := tokenSet(doc)
	keys := map[string]bool{}
	for _, k := range configKeys(reflect.TypeOf(Config{}), "") {
		keys[k] = true
		if !have[k] {
			t.Errorf("config.toml key %q is not in cli.md's config.toml section", k)
		}
	}
	// A key-shaped token in the table that the CLI does not read.
	for _, line := range strings.Split(doc, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		k := backtickSpans(line)[0]
		if !keys[k] {
			t.Errorf("ghost in cli.md: config.toml key %q is not read by the CLI", k)
		}
	}
}

var reposeEnvRE = regexp.MustCompile(`\bREPOSE_[A-Z0-9_]+\b`)

func TestDocsNameEveryEnvVar(t *testing.T) {
	doc := section(t, readCLIDoc(t), "Environment variables")
	have := tokenSet(doc)
	registered := map[string]bool{}
	for _, v := range userEnvVars {
		registered[v] = true
		if !have[v] {
			t.Errorf("environment variable %s is in userEnvVars but not in cli.md's Environment variables section", v)
		}
	}
	for tok := range have {
		if reposeEnvRE.MatchString(tok) && !registered[tok] {
			t.Errorf("ghost in cli.md: %s is documented but the CLI does not read it", tok)
		}
	}
	// Every REPOSE_* name in the package's code is registered or internal,
	// so a new os.Getenv("REPOSE_X") can't skip the registry.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range reposeEnvRE.FindAllString(string(b), -1) {
			if !registered[name] && internalEnvVars[name] == "" {
				t.Errorf("%s reads %s: add it to userEnvVars (env.go) and cli.md, or to internalEnvVars here with why", f, name)
			}
		}
	}
}

func TestDocsListEveryExitCode(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "exitcode.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[int]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if !strings.HasPrefix(name.Name, "Exit") || i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok {
				t.Fatalf("%s is not a literal", name.Name)
			}
			v, _ := strconv.Atoi(lit.Value)
			codes[v] = name.Name
		}
		return true
	})
	doc := section(t, readCLIDoc(t), "Exit codes")
	rows := map[int]bool{}
	for _, line := range strings.Split(doc, "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 3 {
			continue
		}
		if v, err := strconv.Atoi(strings.TrimSpace(cells[1])); err == nil {
			rows[v] = true
		}
	}
	for v, name := range codes {
		if !rows[v] {
			t.Errorf("exit code %d (%s) is not in cli.md's Exit codes table", v, name)
		}
	}
	for v := range rows {
		if _, ok := codes[v]; !ok {
			t.Errorf("ghost in cli.md: exit code %d is not an Exit constant in exitcode.go", v)
		}
	}
}
