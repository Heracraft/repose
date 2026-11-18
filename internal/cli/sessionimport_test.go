package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The directory names are the ones Claude Code 2.1.272 wrote on the dev
// box: every character that is neither a letter nor a digit becomes "-".
// The evidence for the first two rows is `ls ~/.claude/projects` beside
// the folder each session ran in.
func TestClaudeProjectDirIsClaudeCodesOwnName(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"/home/azureuser/kanali-tiny-class", "-home-azureuser-kanali-tiny-class"},
		{"/tmp/claude-1000/sess.imp_test", "-tmp-claude-1000-sess-imp-test"},
		{"/home/dev/si-e2e", "-home-dev-si-e2e"},
		{"/home/dev", "-home-dev"},
	} {
		if got := claudeProjectDir(c.path); got != c.want {
			t.Errorf("claudeProjectDir(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

// sessionLaptop writes a laptop whose Claude Code config holds one
// conversation of the checkout, one of a subdirectory of it, one of
// another folder, and the files that must never travel. It returns the
// home directory and the checkout.
func sessionLaptop(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	root = filepath.Join(home, "app")
	mk := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	line := func(o map[string]any) string {
		b, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		return string(b) + "\n"
	}
	enc := claudeProjectDir(root)
	// The checkout's conversation: paths in a value, inside prose, and at
	// the end of a string; the encoded project directory in a scratchpad
	// path and in a tool-result path.
	mk(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa.jsonl",
		line(map[string]any{"type": "mode", "mode": "normal"})+
			line(map[string]any{"type": "ai-title", "aiTitle": "Payments refactor"})+
			line(map[string]any{
				"type": "user", "cwd": root, "sessionId": "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa",
				"message": map[string]any{"role": "user", "content": "fix " + root + "/pay.go"},
				"notes": []any{
					"ran `go test " + root + "` and read " + root,
					"scratchpad " + filepath.Join(home, ".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/scratchpad"),
					"saved to " + filepath.Join(home, ".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/tool-results/x.txt"),
					"a sibling " + root + "x/other stays",
					"home is " + home,
				},
			}))
	mk(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/custom-title.json", `{"customTitle":"Payments"}`)
	mk(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/subagents/sub.jsonl",
		line(map[string]any{"type": "user", "cwd": root, "note": "subagent in " + root}))
	mk(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/tool-results/x.txt", "ls "+root+"/pay.go\n")
	mk(".claude/todos/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa-agent-1.json", `[{"content":"ship `+root+`/pay.go"}]`)

	// A conversation that ran one folder down.
	sub := filepath.Join(root, "web")
	mk(".claude/projects/"+claudeProjectDir(sub)+"/bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb.jsonl",
		line(map[string]any{"type": "user", "cwd": sub, "message": map[string]any{"role": "user", "content": "build the web"}}))

	// Another folder's conversation, which belongs to no checkout here.
	other := filepath.Join(home, "elsewhere")
	mk(".claude/projects/"+claudeProjectDir(other)+"/cccccccc-3333-4333-8333-cccccccccccc.jsonl",
		line(map[string]any{"type": "user", "cwd": other, "message": map[string]any{"role": "user", "content": "NEVER-OTHER-SESSION"}}))

	// Never brought by anything here.
	mk(".claude/history.jsonl", `{"display":"NEVER-HISTORY"}`)
	mk(".claude/.credentials.json", `{"claudeAiOauth":{"accessToken":"NEVER-CREDS"}}`)
	mk(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/tool-results/aws-credentials", "NEVER-TOOL-CREDS")

	// Newest last, so the order the list prints is not the file order.
	now := time.Now()
	touch := func(rel string, ago time.Duration) {
		p := filepath.Join(home, rel)
		if err := os.Chtimes(p, now.Add(-ago), now.Add(-ago)); err != nil {
			t.Fatal(err)
		}
	}
	touch(".claude/projects/"+enc+"/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa.jsonl", time.Minute)
	touch(".claude/projects/"+claudeProjectDir(sub)+"/bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb.jsonl", time.Hour)
	return home, root
}

func TestListLaptopSessionsIsThisCheckoutsNewestFirst(t *testing.T) {
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range ss {
		ids = append(ids, s.ID)
	}
	want := []string{"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa", "bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("listLaptopSessions = %v, want %v (newest first, no other folder's)", ids, want)
	}
	// The title the user set wins over the one Claude Code wrote.
	if ss[0].Title != "Payments" {
		t.Errorf("title = %q, want the custom title Payments", ss[0].Title)
	}
	// The second has neither, so its first prompt names it.
	if ss[1].Title != "build the web" {
		t.Errorf("title = %q, want the first prompt", ss[1].Title)
	}
	if ss[0].Cwd != root {
		t.Errorf("cwd = %q, want %q", ss[0].Cwd, root)
	}
}

func TestListLaptopSessionsWithoutAClaudeConfig(t *testing.T) {
	home := t.TempDir()
	ss, err := listLaptopSessions(home, filepath.Join(home, "app"))
	if err != nil || ss != nil {
		t.Fatalf("listLaptopSessions on a laptop with no ~/.claude = %v, %v; want none and no error", ss, err)
	}
}

// buildSessionCarry rewrites every laptop path to the machine's, leaves a
// sibling folder's name alone, and brings nothing but the conversation.
func TestSessionCarryRewritesPathsAndBringsOnlyTheConversation(t *testing.T) {
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := buildSessionCarry(ss[0], home, root, "/home/dev/app")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Dir != "-home-dev-app" {
		t.Fatalf("machine project directory = %q, want -home-dev-app", sc.Dir)
	}
	all := ""
	for _, rel := range sortedByteKeys(sc.Files) {
		all += rel + "\n" + string(sc.Files[rel]) + "\n"
	}
	for _, rel := range sortedByteKeys(sc.Todos) {
		all += rel + "\n" + string(sc.Todos[rel]) + "\n"
	}
	if strings.Contains(all, home) {
		t.Errorf("the laptop's home %q is still in what travels", home)
	}
	if strings.Contains(all, claudeProjectDir(root)) {
		t.Errorf("the laptop's projects directory name %q is still in what travels", claudeProjectDir(root))
	}
	for _, never := range []string{"NEVER-HISTORY", "NEVER-CREDS", "NEVER-OTHER-SESSION", "NEVER-TOOL-CREDS"} {
		if strings.Contains(all, never) {
			t.Errorf("%s travelled; it never may", never)
		}
	}
	for _, want := range []string{
		"/home/dev/app/pay.go",
		"go test /home/dev/app",
		"/home/dev/.claude/projects/-home-dev-app/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/scratchpad",
		"/home/dev/.claude/projects/-home-dev-app/aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/tool-results/x.txt",
		"home is /home/dev",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("%q is not in what travels", want)
		}
	}
	// A folder beside the checkout keeps its own name under the
	// machine's home; TestSessionSwapStopsAtTheEndOfTheName is where
	// each boundary is read one by one.
	if !strings.Contains(all, "/home/dev/appx/other") {
		t.Errorf("the folder beside the checkout did not land under the machine's home:\n%s", all)
	}
	// Everything the conversation owns, and nothing else.
	got := sortedByteKeys(sc.Files)
	want := []string{
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa.jsonl",
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/custom-title.json",
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/subagents/sub.jsonl",
		"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa/tool-results/x.txt",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("files = %v, want %v", got, want)
	}
	if len(sc.Todos) != 1 {
		t.Errorf("todos = %v, want the conversation's one", sc.Todos)
	}
	// Every line is still one JSON object.
	for _, l := range strings.Split(string(sc.Files["aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa.jsonl"]), "\n") {
		if l == "" {
			continue
		}
		if !json.Valid([]byte(l)) {
			t.Fatalf("a rewritten transcript line is not JSON: %.120q", l)
		}
	}
}

// A conversation from a folder under the checkout lands in the machine's
// folder under its checkout, which is the directory `claude --resume`
// looks in when it runs there.
func TestSessionCarryKeepsTheFolderUnderTheCheckout(t *testing.T) {
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := buildSessionCarry(ss[1], home, root, "/home/dev/app")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Dir != claudeProjectDir("/home/dev/app/web") {
		t.Fatalf("machine project directory = %q, want %q", sc.Dir, claudeProjectDir("/home/dev/app/web"))
	}
}

// The machine's half of the import, run as the machine runs it: the
// payload's tar unpacked and its part script executed by sh.
func TestSessionPartWritesTheMachinesProjectDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the part is shell the machine runs")
	}
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	guestHomeDir := t.TempDir()
	// The machine's checkout under a scratch home, so the part's mkdir
	// of the conversation's folder lands where a test may write.
	sc, err := buildSessionCarry(ss[0], home, root, guestHomeDir+"/app")
	if err != nil {
		t.Fatal(err)
	}
	// A copy the machine already has, which the part keeps as
	// .repose-prev before writing the new one.
	dir := filepath.Join(guestHomeDir, ".claude/projects", sc.Dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sc.ID+".jsonl"), []byte("older\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runSessionPart(t, sc, guestHomeDir)
	for _, rel := range []string{
		sc.ID + ".jsonl",
		sc.ID + "/custom-title.json",
		sc.ID + "/subagents/sub.jsonl",
		sc.ID + "/tool-results/x.txt",
	} {
		st, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Errorf("%s is not on the machine: %v", rel, err)
			continue
		}
		if st.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v on the machine, want 0600", rel, st.Mode().Perm())
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, sc.ID+".jsonl.repose-prev")); err != nil || string(b) != "older\n" {
		t.Errorf("the machine's previous copy was not kept as .repose-prev: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(guestHomeDir, ".claude/todos", sc.ID+"-agent-1.json")); err != nil {
		t.Errorf("the conversation's todos are not on the machine: %v", err)
	}
	// The marker the next run reads, so the same conversation is not
	// sent twice.
	m, err := os.ReadFile(filepath.Join(guestHomeDir, ".repose/carry", sessionMarker(sc.ID)))
	if err != nil || strings.TrimSpace(string(m)) != sc.Hash {
		t.Errorf("marker = %q, %v; want %s", m, err, sc.Hash)
	}
}

// runSessionPart unpacks the payload and runs its part the way the
// machine does: sh, with HOME pointed at a scratch directory.
func runSessionPart(t *testing.T, sc *sessionCarry, guestHomeDir string) {
	t.Helper()
	p := newGuestPayload()
	if err := addSessionPart(p, sc); err != nil {
		t.Fatal(err)
	}
	script := p.script.String()
	if err := p.tw.Close(); err != nil {
		t.Fatal(err)
	}
	unpack := t.TempDir()
	untar(t, p.buf.Bytes(), unpack)
	var part string
	for _, l := range strings.Split(script, "\n") {
		if strings.HasPrefix(l, "sh -e ") {
			part = strings.Fields(l)[2]
		}
	}
	part = strings.Trim(strings.ReplaceAll(part, `"$t/`, ""), `"`)
	cmd := exec.Command("sh", "-e", filepath.Join(unpack, part), unpack)
	cmd.Env = append(os.Environ(), "HOME="+guestHomeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the part failed: %v\n%s", err, out)
	}
}

func untar(t *testing.T, b []byte, dir string) {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, h.Name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSessionProbeIsRead(t *testing.T) {
	p := parseSessionProbe("#have 150878\n#mark d35d351962507c473202d43d744955e9\n")
	if p.Have != 150878 || !p.HasIt || p.Mark != "d35d351962507c473202d43d744955e9" {
		t.Fatalf("parseSessionProbe = %+v", p)
	}
	if p := parseSessionProbe("\n"); p.HasIt || p.Mark != "" {
		t.Fatalf("a machine with none = %+v", p)
	}
}

func TestResolveLaptopSessionAnswers(t *testing.T) {
	ss := []laptopSession{
		{ID: "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"},
		{ID: "aaaaaaaa-2222-4222-8222-bbbbbbbbbbbb"},
	}
	if s, err := resolveLaptopSession(ss, sessionLast, "/app"); err != nil || s.ID != ss[0].ID {
		t.Errorf("the bare flag takes the newest: %v, %v", s.ID, err)
	}
	if s, err := resolveLaptopSession(ss, "bbbbbbbbbbbb", "/app"); err != nil || s.ID != ss[1].ID {
		t.Errorf("an id's end names it: %v, %v", s.ID, err)
	}
	if _, err := resolveLaptopSession(ss, "aaaaaaaa", "/app"); err == nil || !strings.Contains(err.Error(), "give more of the id") {
		t.Errorf("a start that names both must say so: %v", err)
	}
	if _, err := resolveLaptopSession(ss, "deadbeef", "/app"); err == nil || !strings.Contains(err.Error(), "repose sessions") {
		t.Errorf("an unknown id must point at the list: %v", err)
	}
	if _, err := resolveLaptopSession(nil, sessionLast, "/app"); err == nil || !strings.Contains(err.Error(), "none to bring") {
		t.Errorf("a folder with no conversation must say so: %v", err)
	}
}

func TestSessionArgsTakesTheIDAfterABareFlag(t *testing.T) {
	ids := []string{"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"}
	// The laptop is read only for a bare flag with an argument beside
	// it, so a plain `repose run` never looks at ~/.claude.
	reads := 0
	// `repose run --session aaaaaaaa-...`: the id, not the project.
	idsOf := func() []string { reads++; return ids }
	id, rest := sessionArgs(sessionLast, []string{"aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"}, idsOf)
	if id != ids[0] || len(rest) != 0 {
		t.Errorf("sessionArgs = %q, %v", id, rest)
	}
	// `repose run myproj --session`: the project stays the project.
	id, rest = sessionArgs(sessionLast, []string{"myproj"}, idsOf)
	if id != sessionLast || len(rest) != 1 || rest[0] != "myproj" {
		t.Errorf("sessionArgs = %q, %v", id, rest)
	}
	// `--session=ID` never looks at the arguments.
	id, rest = sessionArgs("deadbeef", []string{"myproj"}, idsOf)
	if id != "deadbeef" || len(rest) != 1 {
		t.Errorf("sessionArgs = %q, %v", id, rest)
	}
	// No flag at all, and a bare flag with no argument beside it: the
	// laptop is not read.
	before := reads
	if id, _ := sessionArgs("", []string{"myproj"}, idsOf); id != "" {
		t.Errorf("sessionArgs with no flag = %q", id)
	}
	if id, _ := sessionArgs(sessionLast, nil, idsOf); id != sessionLast {
		t.Errorf("a bare flag with no arguments = %q", id)
	}
	if reads != before {
		t.Errorf("the laptop was read %d times with nothing to resolve", reads-before)
	}
}

func TestCheckSessionFlagsRefusals(t *testing.T) {
	for _, c := range []struct {
		opts RunOptions
		want string
	}{
		{RunOptions{Session: "x", Agent: "codex"}, "starts another agent"},
		{RunOptions{Session: "x", Window: "claude"}, "already there"},
		{RunOptions{Session: "x", Worktree: true}, "would not find it"},
	} {
		err := checkSessionFlags(c.opts)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("checkSessionFlags(%+v) = %v, want one naming %q", c.opts, err, c.want)
		}
	}
	if err := checkSessionFlags(RunOptions{Session: "x", Agent: "claude"}); err != nil {
		t.Errorf("--session --agent claude is the same thing twice, not an error: %v", err)
	}
	if err := checkSessionFlags(RunOptions{Worktree: true, Prompt: "p"}); err != nil {
		t.Errorf("without --session nothing is refused: %v", err)
	}
}

// The window's command line, read off a real tmux window by a stand-in
// claude that records its own argv: `--resume <id>` opens the
// conversation the import brought, and the prompt is its next turn.
func TestAgentStartCommandResumes(t *testing.T) {
	id := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	if got := startAgentInTmux(t, "claude", "", id); strings.Join(got, "|") != "--resume|"+id {
		t.Errorf("claude was started with %q, want --resume %s", got, id)
	}
	if got := startAgentInTmux(t, "claude", "add tests", id); strings.Join(got, "|") != "--resume|"+id+"|--|add tests" {
		t.Errorf("claude was started with %q, want --resume %s -- \"add tests\"", got, id)
	}
	// Without a conversation the line is what it always was (I-780).
	if got, want := agentStartCommand("claude", "", ""), shQuote("claude"); got != want {
		t.Errorf("agentStartCommand = %s, want %s", got, want)
	}
}

// The swap stops where the name does, so a folder whose name begins with
// the checkout's keeps its own.
func TestSessionSwapStopsAtTheEndOfTheName(t *testing.T) {
	swaps := sessionRewrite("/Users/jane", "/Users/jane/app", "/home/dev/app-m")
	for _, c := range []struct{ in, want string }{
		{"/Users/jane/app", "/home/dev/app-m"},
		{"/Users/jane/app/pay.go", "/home/dev/app-m/pay.go"},
		{"cd /Users/jane/app && go test ./...", "cd /home/dev/app-m && go test ./..."},
		{"read /Users/jane/app, then stop", "read /home/dev/app-m, then stop"},
		// A different folder, under the home but not the checkout: only
		// the home is rewritten.
		{"/Users/jane/appx/other", "/home/dev/appx/other"},
		{"/Users/jane/app.bak/x", "/home/dev/app.bak/x"},
		// A different user whose name begins with this one's.
		{"/Users/janet/app", "/Users/janet/app"},
		// The encoded directory names.
		{"-Users-jane-app-abc", "-home-dev-app-m-abc"},
		{"-Users-jane2-app", "-Users-jane2-app"},
		{"-Users-jane-elsewhere", "-home-dev-elsewhere"},
	} {
		if got := applySwaps(c.in, swaps); got != c.want {
			t.Errorf("applySwaps(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSessionCarryRefusesATranscriptOverTheCap(t *testing.T) {
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	s := ss[0]
	s.Bytes = sessionCap + 1
	if _, err := buildSessionCarry(s, home, root, "/home/dev/app"); err == nil || !strings.Contains(err.Error(), "one run brings") {
		t.Fatalf("a transcript over the cap must be refused by its size: %v", err)
	}
}

func TestSessionsCmdPrintsTheList(t *testing.T) {
	home, root := sessionLaptop(t)
	var out bytes.Buffer
	e := &Env{HomeDir: home, Cwd: root, Out: &out, ErrOut: io.Discard}
	if err := SessionsCmd(e); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"ID", "LAST USED", "FOLDER", "TITLE", "aaaaaaaa", "Payments", "web"} {
		if !strings.Contains(got, want) {
			t.Errorf("`repose sessions` did not print %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "cccccccc") {
		t.Errorf("another folder's conversation is listed:\n%s", got)
	}
	out.Reset()
	e.Quiet = true
	if err := SessionsCmd(e); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(out.String()); len(got) != 2 || got[0] != "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa" {
		t.Errorf("-q gives the whole ids, one per line: %v", got)
	}
	out.Reset()
	e.Quiet, e.JSON = false, true
	if err := SessionsCmd(e); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 2 || rows[0]["title"] != "Payments" {
		t.Errorf("--json = %s (%v)", out.String(), err)
	}
}

func TestSessionsCmdOnAFolderWithNone(t *testing.T) {
	home := t.TempDir()
	var out bytes.Buffer
	e := &Env{HomeDir: home, Cwd: home, Out: &out, ErrOut: io.Discard}
	if err := SessionsCmd(e); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No Claude Code conversation") {
		t.Errorf("a folder with none must say so: %q", out.String())
	}
}

// The home folder is never a conversation's (I-601): the swaps there
// would be the same path twice.
func TestSessionCommandsRefuseTheHomeFolder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	e := &Env{HomeDir: home, Cwd: home, Out: io.Discard, ErrOut: io.Discard}
	err = SessionsCmd(e)
	if err == nil || !strings.Contains(err.Error(), "cd into your project") {
		t.Fatalf("`repose sessions` in the home folder = %v, want the home refusal", err)
	}
	if _, err := e.bringSession(context.Background(), sshTarget{}, sessionLast, home, "/home/dev/app"); err == nil ||
		!strings.Contains(err.Error(), "cd into your project") {
		t.Fatalf("--session in the home folder = %v, want the home refusal", err)
	}
}

// A laptop with a conversation of another folder costs a few lines of
// that transcript, not sessionHeadCap of it: the working directory is on
// one of the first lines and the read stops there.
func TestListLaptopSessionsStopsAtAnotherFoldersCwd(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "app")
	other := filepath.Join(home, "elsewhere")
	dir := filepath.Join(home, ".claude", claudeProjectsDir, claudeProjectDir(other))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The working directory on the first line, then more than the cap of
	// text that must not be read.
	var b strings.Builder
	b.WriteString(`{"type":"user","cwd":` + quoteJSON(t, other) + "}\n")
	for b.Len() < sessionHeadCap*2 {
		b.WriteString(`{"type":"user","text":"` + strings.Repeat("x", 4096) + "\"}\n")
	}
	p := filepath.Join(dir, "dddddddd-4444-4444-8444-dddddddddddd.jsonl")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s := laptopSession{path: p}
	if err := s.readHead(root); err != nil {
		t.Fatal(err)
	}
	if s.Cwd != other {
		t.Fatalf("cwd = %q, want %q", s.Cwd, other)
	}
	// Nothing after the working directory was kept, which is what says
	// the read stopped there.
	if s.firstPrompt != "" || s.aiTitle != "" {
		t.Errorf("the read went on past another folder's working directory: %q %q", s.firstPrompt, s.aiTitle)
	}
	ss, err := listLaptopSessions(home, root)
	if err != nil || len(ss) != 0 {
		t.Fatalf("listLaptopSessions = %v, %v; want none", ss, err)
	}
}

func quoteJSON(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A prompt with HTML characters in it keeps its own bytes: the rewrite
// re-encodes every line, and Go's default encoder would turn `<` into an
// escape the laptop's file never held.
func TestRewriteKeepsTheBytesOfAPrompt(t *testing.T) {
	in := []byte(`{"cwd":"/Users/jane/app","text":"if a < b && c > d then /Users/jane/app/x"}` + "\n")
	out, err := rewriteJSONLines(in, sessionRewrite("/Users/jane", "/Users/jane/app", "/home/dev/app"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "a < b && c > d") {
		t.Errorf("the prompt's characters were escaped: %s", out)
	}
	if !strings.Contains(string(out), "/home/dev/app/x") {
		t.Errorf("the path was not rewritten: %s", out)
	}
}

// file-history-snapshot keys its backups by the file's path, so a map's
// keys are rewritten too.
func TestRewriteRewritesMapKeys(t *testing.T) {
	in := []byte(`{"snapshot":{"trackedFileBackups":{"/Users/jane/app/pay.go":{"at":1}}}}` + "\n")
	out, err := rewriteJSONLines(in, sessionRewrite("/Users/jane", "/Users/jane/app", "/home/dev/app"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"/home/dev/app/pay.go"`) {
		t.Errorf("the key was not rewritten: %s", out)
	}
	if strings.Contains(string(out), "/Users/jane") {
		t.Errorf("a laptop path is left: %s", out)
	}
}

// The window must open where the conversation ran, and the part makes
// that folder when the sync has not (an empty or ignored folder).
func TestSessionPartMakesTheConversationsFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the part is shell the machine runs")
	}
	home, root := sessionLaptop(t)
	ss, err := listLaptopSessions(home, root)
	if err != nil {
		t.Fatal(err)
	}
	guestHomeDir := t.TempDir()
	sc, err := buildSessionCarry(ss[1], home, root, guestHomeDir+"/app")
	if err != nil {
		t.Fatal(err)
	}
	if sc.Cwd != guestHomeDir+"/app/web" {
		t.Fatalf("the conversation's folder on the machine = %q", sc.Cwd)
	}
	// The probe, not the part: a conversation the machine already has
	// sends no part and still needs the folder.
	cmd := exec.Command("sh", "-e", "-c", sessionProbeScript(sc.Dir, sc.ID, sc.Cwd))
	cmd.Env = append(os.Environ(), "HOME="+guestHomeDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the probe failed: %v\n%s", err, out)
	}
	if st, err := os.Stat(sc.Cwd); err != nil || !st.IsDir() {
		t.Errorf("the conversation's folder was not made: %v", err)
	}
}

// tildeGuestPath is what the window is told, so a conversation of the
// checkout opens in the checkout and one of a folder under it opens
// there.
func TestResumeWindowFolder(t *testing.T) {
	if got := tildeGuestPath("/home/dev/app"); got != "~/app" {
		t.Errorf("tildeGuestPath = %q", got)
	}
	if got := tildeGuestPath("/home/dev/app/web"); got != "~/app/web" {
		t.Errorf("tildeGuestPath = %q", got)
	}
}

// The line the run prints about the conversation: which one, when the
// user did not choose; that it arrived, when no window follows to show
// it; and nothing when the user named the id and a window is opening.
func TestSessionBroughtLine(t *testing.T) {
	b := &sessionBrought{ID: "aaaaaaaa-1111-4111-8111-bbbbccccdddd", Title: "Payments refactor"}
	if got, want := b.line(true, false), "Conversation ccccdddd: Payments refactor"; got != want {
		t.Errorf("a bare flag = %q, want %q", got, want)
	}
	if got := b.line(false, false); got != "" {
		t.Errorf("a named id with a window opening = %q, want nothing", got)
	}
	if got, want := b.line(false, true), "Conversation ccccdddd is on the machine."; got != want {
		t.Errorf("a named id with no window = %q, want %q", got, want)
	}
	b.Kept = true
	if got := b.line(false, true); got != "" {
		t.Errorf("a kept copy has said so already = %q, want nothing", got)
	}
}

// A window on the machine already resumed on the conversation is found
// by its tmux window option, so no second claude starts on one
// transcript.
func TestSessionProbeFindsTheWindow(t *testing.T) {
	p := parseSessionProbe("#have 10\n#mark abc\n#window claude-2\n")
	if p.Window != "claude-2" {
		t.Fatalf("parseSessionProbe = %+v", p)
	}
	if !strings.Contains(sessionProbeScript("-home-dev-app", "the-id", "/home/dev/app"), "@"+sessionWindowOption) {
		t.Error("the probe does not read the window option")
	}
}

// The window a resumed claude opens in carries the conversation's id, so
// the next --session finds it.
func TestResumedWindowCarriesTheConversation(t *testing.T) {
	id := "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	c := agentWindowCommand("proj", "", "claude", "~/proj", "claude", "", id, mcpApprovals{})
	if !strings.Contains(c, "@"+sessionWindowOption) || !strings.Contains(c, id) {
		t.Errorf("the window does not record the conversation:\n%s", c)
	}
	// No conversation, no option.
	if c := agentWindowCommand("proj", "", "claude", "~/proj", "claude", "", "", mcpApprovals{}); strings.Contains(c, sessionWindowOption) {
		t.Errorf("a window with no conversation records one:\n%s", c)
	}
}
