package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heracraft/repose/internal/mcpreg"
)

var mcpListRows = []mcpreg.ServerStatus{
	{Name: "playwright", From: "repose", Agents: []string{"claude", "codex", "gemini", "opencode", "pi"}},
	{Name: "linear", From: "laptop", Agents: []string{"claude", "codex", "gemini", "opencode", "pi"}, State: "needs LINEAR_TOKEN", Needs: []string{"LINEAR_TOKEN"}},
	{Name: "sentry", From: "laptop", Agents: []string{"claude", "gemini", "opencode", "pi"}, State: "codex: an SSE server, which Codex does not take"},
	{Name: "xcode", From: "laptop", Agents: []string{}, State: "an Apple app"},
	{Name: "notes-db", From: "project", Agents: []string{"claude"}, Checkout: "/home/dev/todo-app"},
	{Name: "apple-notes", From: "forward", Agents: []string{"claude", "codex", "gemini", "opencode", "pi"}, State: "laptop not connected"},
	{Name: "my-db", From: "machine", Agents: []string{"claude"}},
}

// The table on a terminal (golden), and the piped lines.
func TestMCPListTable(t *testing.T) {
	var b bytes.Buffer
	if err := writeMCPList(&b, mcpListRows, true); err != nil {
		t.Fatal(err)
	}
	want := `NAME         FROM     AGENTS                           STATE
playwright   repose   claude codex gemini opencode pi
linear       laptop   claude codex gemini opencode pi  needs LINEAR_TOKEN
sentry       laptop   claude gemini opencode pi        codex: an SSE server, which Codex does not take
xcode        laptop   none                             an Apple app
notes-db     project  claude                           ~/todo-app
apple-notes  forward  claude codex gemini opencode pi  laptop not connected
my-db        machine  claude
`
	if b.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", b.String(), want)
	}
	b.Reset()
	if err := writeMCPList(&b, mcpListRows, false); err != nil {
		t.Fatal(err)
	}
	wantPiped := "playwright\trepose\tclaude codex gemini opencode pi\t\n" +
		"linear\tlaptop\tclaude codex gemini opencode pi\tneeds LINEAR_TOKEN\n" +
		"sentry\tlaptop\tclaude gemini opencode pi\tcodex: an SSE server, which Codex does not take\n" +
		"xcode\tlaptop\tnone\tan Apple app\n" +
		"notes-db\tproject\tclaude\t~/todo-app\n" +
		"apple-notes\tforward\tclaude codex gemini opencode pi\tlaptop not connected\n" +
		"my-db\tmachine\tclaude\t\n"
	if b.String() != wantPiped {
		t.Errorf("piped:\n%q\nwant:\n%q", b.String(), wantPiped)
	}
	// Names come from any checkout's .mcp.json: escape sequences and tabs
	// never reach the terminal, in either form.
	evil := []mcpreg.ServerStatus{{Name: "x\x1b]0;PWNED\x07\x1b[2J\ty", From: "project", Agents: []string{"claude"}, Checkout: "/home/dev/a\x1b[31m", State: "s\x9b1m"}}
	for _, tty := range []bool{true, false} {
		b.Reset()
		if err := writeMCPList(&b, evil, tty); err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(b.String(), "\x1b\x07\u009b") || strings.Count(b.String(), "\t") > 3*len(evil) {
			t.Errorf("tty=%v: %q", tty, b.String())
		}
		if !strings.Contains(b.String(), "x]0;PWNED[2Jy") {
			t.Errorf("tty=%v: name lost its printable part: %q", tty, b.String())
		}
	}
	// A checkout outside the home keeps its path; a state follows it.
	if got := mcpState(mcpreg.ServerStatus{Checkout: "/srv/app", State: "needs X"}); got != "/srv/app; needs X" {
		t.Errorf("state = %q", got)
	}
}

// fakeMCPStatus puts a repose-mcp on the test guest's PATH that runs body.
func fakeMCPStatus(t *testing.T, body string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "repose-mcp"), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const mcpStatusDoc = `{"version": 1,
  "agents": ["claude", "codex", "gemini", "opencode", "pi"],
  "servers": [
    {"name": "playwright", "from": "repose", "agents": ["claude", "codex"], "state": ""},
    {"name": "apple-notes", "from": "forward", "agents": ["claude"], "state": "laptop not connected"},
    {"name": "xcode", "from": "laptop", "agents": [], "state": "an Apple app", "future": true}
  ]}
`

// --json prints the machine's document byte for byte, keys the CLI does
// not know included; without it the rows come from the same document.
func TestMCPListOverSSH(t *testing.T) {
	target, _ := mcpGuest(t)
	fakeMCPStatus(t, "[ \"$*\" = 'status --json' ] || exit 2\ncat <<'EOF'\n"+mcpStatusDoc+"EOF\n")
	var out bytes.Buffer
	e := &Env{Out: &out, ErrOut: &bytes.Buffer{}, JSON: true}
	if err := mcpListOn(context.Background(), e, target, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if out.String() != mcpStatusDoc {
		t.Errorf("--json:\n%s\nwant:\n%s", out.String(), mcpStatusDoc)
	}
	out.Reset()
	e.JSON = false
	if err := mcpListOn(context.Background(), e, target, "todo-app"); err != nil {
		t.Fatal(err)
	}
	want := "playwright\trepose\tclaude codex\t\napple-notes\tforward\tclaude\tlaptop not connected\nxcode\tlaptop\tnone\tan Apple app\n"
	if out.String() != want {
		t.Errorf("rows:\n%q\nwant:\n%q", out.String(), want)
	}
	// A file the machine left out is a line on stderr after the rows.
	fakeMCPStatus(t, "cat <<'EOF'\n{\"version\":1,\"servers\":[],\"problems\":[\"~/.repose/mcp/laptop.json does not parse (x\\u001b[2J); servers from your laptop are left out\"]}\nEOF\n")
	var errb bytes.Buffer
	out.Reset()
	e.ErrOut = &errb
	if err := mcpListOn(context.Background(), e, target, "todo-app"); err != nil {
		t.Fatal(err)
	}
	if errb.String() != "On todo-app: ~/.repose/mcp/laptop.json does not parse (x[2J); servers from your laptop are left out.\n" {
		t.Errorf("problems: %q", errb.String())
	}
}

// A base without repose-mcp is one line naming the update; a registry
// that does not parse passes the machine's reason on.
func TestMCPListOldBase(t *testing.T) {
	target, _ := mcpGuest(t)
	hideCommand(t, "repose-mcp") // the shell's own 127
	var out bytes.Buffer
	e := &Env{Out: &out, ErrOut: &bytes.Buffer{}}
	err := mcpListOn(context.Background(), e, target, "todo-app")
	want := "The base of todo-app predates repose mcp list; it works after the machine's next update."
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if exitCodeOf(err) != ExitGeneric {
		t.Errorf("exit = %d", exitCodeOf(err))
	}
	if strings.Contains(err.Error(), "\n") || out.Len() != 0 {
		t.Errorf("more than one line: %q, out %q", err, out.String())
	}

	fakeMCPStatus(t, "echo 'repose-mcp: ~/.repose/mcp/laptop.json: unexpected end of JSON input' >&2\nexit 1\n")
	err = mcpListOn(context.Background(), e, target, "todo-app")
	if err == nil || !strings.Contains(err.Error(), "Could not read the MCP servers of todo-app") || !strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Errorf("registry error: %v", err)
	}
	fakeMCPStatus(t, "echo not json\n")
	if err := mcpListOn(context.Background(), e, target, "todo-app"); err == nil || !strings.Contains(err.Error(), "not JSON") {
		t.Errorf("not JSON: %v", err)
	}
}
