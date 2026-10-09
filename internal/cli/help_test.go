package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// The help's shape (review C6, C9, theme 7; DECISIONS I-630).

func TestRootHelpIsGrouped(t *testing.T) {
	root := docsRoot()
	for _, c := range root.Commands() {
		if !c.IsAvailableCommand() || c.Name() == "help" || c.Name() == "version" || c.Name() == "completion" {
			continue
		}
		if c.GroupID == "" {
			t.Errorf("repose %s is in no group of root help; add it to commandGroups (help.go)", c.Name())
		}
	}
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out.String(), "\nDocs: "+docsURL+"\n") {
		t.Errorf("root help does not end with the docs line:\n%s", out.String())
	}
	if strings.Index(out.String(), "  run ") > strings.Index(out.String(), "  ls ") {
		t.Error("root help lists ls before run")
	}
}

// Every Short is one clause that starts with a verb, under 70 columns:
// root help prints them in a column after the name.
func TestShortLinesAreOneClause(t *testing.T) {
	walkCommands(docsRoot(), func(c *cobra.Command) {
		if !c.IsAvailableCommand() || !c.HasParent() {
			return
		}
		s := c.Short
		switch {
		case s == "":
			t.Errorf("%s has no Short", c.CommandPath())
		case utf8.RuneCountInString(s) >= 70:
			t.Errorf("%s: Short is %d columns: %q", c.CommandPath(), utf8.RuneCountInString(s), s)
		case strings.Contains(s, ";"):
			t.Errorf("%s: Short joins two ideas with a semicolon: %q", c.CommandPath(), s)
		case strings.HasSuffix(s, "."):
			t.Errorf("%s: Short ends with a period: %q", c.CommandPath(), s)
		case s[0] < 'A' || s[0] > 'Z':
			t.Errorf("%s: Short does not start with a capital verb: %q", c.CommandPath(), s)
		}
		// A Long starts with the verb too (Run, not Runs).
		if first, _, _ := strings.Cut(c.Long, " "); strings.HasSuffix(first, "s") && first != "Alias" && c.Long != "" {
			t.Errorf("%s: Long starts with %q; start it with the verb as a command", c.CommandPath(), first)
		}
	})
}

// Help fits an 80-column terminal: Long, Example and flag lines.
func TestHelpFitsEightyColumns(t *testing.T) {
	walkCommands(docsRoot(), func(c *cobra.Command) {
		if !c.IsAvailableCommand() {
			return
		}
		check := func(what, text string) {
			for _, line := range strings.Split(text, "\n") {
				if n := utf8.RuneCountInString(line); n > helpWidth {
					t.Errorf("%s: %s line is %d columns: %q", c.CommandPath(), what, n, line)
				}
			}
		}
		check("Long", c.Long)
		check("Example", c.Example)
		check("flag", flagUsages(c.LocalFlags()))
	})
}

// machineWordRE is the words the CLI does not use for the machine, its
// agent or its Nix file (review C4): "machine" is the name.
var machineWordRE = regexp.MustCompile(`(?i)\b(guest|guests|guestd|fragment)\b|\benvironment\b`)

// machineWordOK is text with one of those words that is not about the
// machine: an environment variable, a command's name, a file name.
var machineWordOK = []*regexp.Regexp{
	regexp.MustCompile(`(?i)environment variables?`),
	regexp.MustCompile(`repose-guest-[a-z-]+`),
	regexp.MustCompile(`fragment\.nix`),
	regexp.MustCompile(`\bset-environment\b`),
}

func machineWordsIn(s string) []string {
	for _, ok := range machineWordOK {
		s = ok.ReplaceAllString(s, "")
	}
	return machineWordRE.FindAllString(s, -1)
}

func TestHelpSaysMachine(t *testing.T) {
	walkCommands(docsRoot(), func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		var out bytes.Buffer
		c.SetOut(&out)
		if err := c.Help(); err != nil {
			t.Fatal(err)
		}
		if w := machineWordsIn(out.String()); len(w) > 0 {
			t.Errorf("%s --help says %q; the word is machine", c.CommandPath(), w)
		}
	})
}

// TestMessagesSayMachine reads every string in the package's code: a
// message, a progress line or an error the CLI can print. Strings that
// never reach a user are skipped: struct tags, regexps, timing lines and
// the api's field names.
func TestMessagesSayMachine(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		skip := map[*ast.BasicLit]bool{}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.Field:
				if n.Tag != nil {
					skip[n.Tag] = true
				}
			case *ast.CallExpr:
				name := ""
				switch fn := n.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				if name == "timingf" || name == "MustCompile" || name == "Contains" || name == "TrimSuffix" || name == "ReplaceAll" || name == "NewReplacer" {
					for _, a := range n.Args {
						if lit, ok := a.(*ast.BasicLit); ok {
							skip[lit] = true
						}
					}
				}
			case *ast.KeyValueExpr:
				if lit, ok := n.Key.(*ast.BasicLit); ok {
					skip[lit] = true
				}
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || skip[lit] {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			// A shell script's own comments never print.
			var lines []string
			for _, l := range strings.Split(s, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(l), "#") || strings.HasPrefix(strings.TrimSpace(l), "#warn") {
					lines = append(lines, l)
				}
			}
			if w := machineWordsIn(strings.Join(lines, "\n")); len(w) > 0 {
				t.Errorf("%s: %q says %q; the word is machine", fset.Position(lit.Pos()), s, w)
			}
			return true
		})
	}
}
