package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// quietAllowed are the strings outside a failure path that may still name
// a repose command, each with why (DECISIONS I-484). The key is
// file:function:the string's first 40 characters.
var quietAllowed = map[string]string{
	"repoconfig.go:lastRepoConfigStands:%s did not build last time (revision %s:": "a failure: the last build of this repose.nix failed",
	"repoconfig.go:applyRepoConfig:%s was not applied (%s). `repose config ":      "a refusal: the api refused the repose.nix",
	"repoconfig.go:applyRepoConfig:Could not send %s (%s); the machine keep":      "a failure: the repose.nix could not be sent",
	"mux_herdr.go:herdrFocusScript:%s has no checkout %s. `repose run --on ": "a refusal: exit 2, the machine has no such checkout (herdr)",
	"run.go:attachCommand:%s has no checkout %s. `repose run --on ":               "a refusal: exit 2, the machine has no such checkout",
	"buildprogress.go:printApplied:Built revision %s. It changes the kernel":      "the change does nothing until a restart the user times",
	"restore.go:writeDestroyedTable:A live project is called %s; this one co":     "the plain `repose restore NAME` is wrong for this row",
	"status.go:writeStatusLinesMux:  the environment's agent (guestd) is no":         "a failure that status reports",
	"lifecycle.go:DestroyCmd:`repose rm %s` tries again.":                         "the failed destroy's next step, passed to opFailed",
	"run.go:laptopAheadLine:Not synced: your laptop has work the mac":             "a warning: the laptop's work did not go",
	"inputproxy.go:files:%s is %s; dropped files are copied up to":                "a refusal: the dropped file is too large",
	"login.go:runLogin:No plan yet. Choose one at https://repos":                  "blocked: nothing runs without a plan",
	"sync.go:String:Nothing new to sync. The machine has cha":                     "a refusal: the sync did nothing",
	"sync.go:Warnings:The guest's %s has commits your laptop d":                   "a warning: the guest's commits were left detached",
	"creds.go:skippedCredNotice: login an earlier repose run copied to t":         "says why a copy was removed",
	"sync.go:Warnings:Removed the .env file an earlier repose ":                   "says why a copy was removed",
	"sync.go:Warnings:Removed the %d .env files an earlier rep":                   "says why a copy was removed",
	"logins.go:loginsHeader:Copied at each repose run, for every pro":             "names when, not what to type",
	"logins.go:loginsHeader: at each repose run (its own list)":                   "names when, not what to type",
	"logins.go:loginsHeader: at each repose run (the shared list)":                "names when, not what to type",
	"logins.go:saveLoginSkip:Saved: %s %s on your laptop, for %s. The":            "names when, not what to type",
	"secrets.go:SecretsListCmd:Copied from this laptop at each repose r":          "names when, not what to type",
	"scan.go:printScan:\n%d to check in the guest; each one it l":                 "names when, not what to type",
	"questions.go:QuestionsCmd:Waiting at a prompt in their terminal, w":          "says what reply can't do",
	"cert.go:refreshSSHAccess:Could not renew your SSH certificate for":           "a failure",
	"fork.go:ForkCmd:Could not start the agent in %s: %s. `re":                    "a failure",
	"login.go:setUpPlainSSH:warning: could not write ~/.ssh/repose/c":             "a failure",
	"run.go:failedStart:Fix it with `repose config edit --projec":                 "a failure: the config did not build",
	"temp.go:tempSessionEndedWith:Could not destroy %s (%s). It goes at it":           "a failure",
	"run.go:runRun:Another %s %s is open; two agents share ":                      "a warning: two agents are about to edit one tree",
}

// quietFailureCalls are the calls whose string arguments are a failure or
// a refusal, where the next command belongs (I-153).
var quietFailureCalls = map[string]bool{
	"exitf": true, "opFailed": true, "withNext": true, "Errorf": true, "New": true,
}

// quietFailureFuncs are functions whose every string is a failure, a
// refusal or help text: error builders, cobra command definitions.
var quietFailureFuncs = map[string]bool{
	"notRunningMessage": true, "nextAfterFailedStart": true, "again": true,
	"waitForSSH": true, "briefErr": true, "Error": true, "ReadPNG": true,
	"exitCodeFor": true, // the login and rate-limit refusals
	"applyScript": true, // a shell script run on the guest, not output
}

// reposeCommand finds "repose NAME" past the string's start; at its start
// it is a message's own prefix ("repose paste needs xclip").
var reposeCommand = regexp.MustCompile("(.)repose ([a-z][a-z-]*)")

// namesCommand is whether s names a repose command to run.
func namesCommand(s string, commands map[string]bool) bool {
	for _, m := range reposeCommand.FindAllStringSubmatch(s, -1) {
		if commands[m[2]] {
			return true
		}
	}
	return false
}

// TestSuccessOutputNamesNoCommand holds what a command prints when it
// worked, and what a listing prints, to what happened (DECISIONS I-484).
// A string naming a `repose ...` command fails here unless it is an
// argument of a failure call (quietFailureCalls), cobra help text, or in
// quietAllowed with why. Stderr is not exempt: run and attach print notes
// there too, and a note for something that worked is held to the same rule. The next command belongs on
// failures and refusals, where the user is stuck (I-153); on a success
// line it is a lesson the user did not ask for, printed every time.
func TestSuccessOutputNamesNoCommand(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	for _, c := range docsRoot().Commands() {
		commands[c.Name()] = true
		for _, a := range c.Aliases {
			commands[a] = true
		}
	}
	fset := token.NewFileSet()
	seen := map[string]bool{}
	var bad []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil || quietFailureFuncs[fn.Name.Name] || isCobraCmd(fn.Name.Name) {
				continue
			}
			var stack []ast.Node
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if n == nil {
					stack = stack[:len(stack)-1]
					return true
				}
				stack = append(stack, n)
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil || !namesCommand(s, commands) || onFailurePath(stack) {
					return true
				}
				key := name + ":" + fn.Name.Name + ":" + firstN(s, 40)
				seen[key] = true
				if _, ok := quietAllowed[key]; !ok {
					bad = append(bad, fset.Position(lit.Pos()).String()+" "+strconv.Quote(s)+"\n    key "+strconv.Quote(key))
				}
				return true
			})
		}
	}
	sort.Strings(bad)
	for _, b := range bad {
		t.Errorf("names a repose command outside a failure: %s", b)
	}
	if len(bad) > 0 {
		t.Log("Say what happened and stop: a next command goes on a failure or refusal only (DECISIONS I-484).\n" +
			"If the user cannot get the result without that command (a kernel change that waits for a restart),\n" +
			"add the key to quietAllowed with why.")
	}
	for key := range quietAllowed {
		if !seen[key] {
			t.Errorf("quietAllowed has %q, which no longer matches a string; remove it", key)
		}
	}
}

// onFailurePath is whether the literal at the top of stack is an argument
// of a failure call, a write to stderr, or cobra help text.
func onFailurePath(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		switch v := stack[i].(type) {
		case *ast.CallExpr:
			name := callName(v.Fun)
			if quietFailureCalls[name] {
				return true
			}
		case *ast.KeyValueExpr:
			if k, ok := v.Key.(*ast.Ident); ok {
				switch k.Name {
				case "Use", "Short", "Long", "Example":
					return true
				}
			}
		}
	}
	return false
}

// isCobraCmd is a newXCmd function: its strings are help and flag text.
func isCobraCmd(name string) bool {
	return strings.HasPrefix(name, "new") && strings.HasSuffix(name, "Cmd")
}

func callName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func firstN(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// cliUnasked is the CLI's twin of apps/web/src/lib/unasked.ts: phrasings
// of help nobody asked for (DECISIONS I-485).
var cliUnasked = regexp.MustCompile(`(?i)\b(nothing to type|don'?t worry|no need to worry|that'?s it|that'?s all|you'?re all set|you can now|simply|just (run|type|open|click)|in other words|happy (coding|hacking))\b`)

// TestCLIReassures fails on a CLI string that reassures or narrates
// instead of stating what happened (DECISIONS I-485).
func TestCLIReassures(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				if m := cliUnasked.FindString(s); m != "" {
					t.Errorf("%s: %q says %q (DECISIONS I-485)", fset.Position(lit.Pos()), s, m)
				}
			}
			return true
		})
	}
}
