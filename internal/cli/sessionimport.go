package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Bringing one Claude Code conversation from the laptop to the machine
// (DECISIONS I-1328). The carry (I-196) never copies `~/.claude/projects`,
// and that stays: a transcript holds every prompt, every file read and
// every tool result of a conversation, and copying all of them to every
// machine on every run is both a lot of bytes and a lot of the user's
// work in a place they did not ask for it. `--session` is the one way in,
// per run, for one named conversation, chosen by the user.
//
// What travels: the conversation's own `<id>.jsonl`, the files Claude Code
// keeps beside it under `<id>/` (its subagents' transcripts, the tool
// results the transcript points at, a title the user set), and the
// conversation's todos. What never travels: `.credentials.json`,
// `history.jsonl` (every shell command, from every project) and any other
// conversation.

// claudeProjectsDir is where Claude Code keeps one directory per working
// directory it has run in, under the config directory.
const claudeProjectsDir = "projects"

// claudeProjectDir is Claude Code's own name for a working directory
// under `projects/`: every character that is neither a letter nor a digit
// becomes "-". Read off a laptop on 2.1.272, where
// `/tmp/claude-1000/sess.imp_test` is `-tmp-claude-1000-sess-imp-test`.
// The machine's side is always a POSIX path under /home/dev, so the
// machine's directory name is exact whatever the laptop runs.
func claudeProjectDir(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// laptopSession is one conversation on the laptop.
type laptopSession struct {
	ID string `json:"id"`
	// Cwd is the directory Claude Code ran in, read from the transcript
	// rather than from the directory's name, which is not reversible.
	Cwd string `json:"cwd"`
	// Title is what names the conversation to a human: the title the
	// user set, else the one Claude Code wrote, else the first prompt.
	Title string `json:"title"`
	// At is when the transcript was last written.
	At time.Time `json:"at"`
	// Bytes is the transcript alone, which is what the list shows; the
	// files beside it are counted when it travels.
	Bytes int64 `json:"bytes"`

	dir         string // the laptop's projects/<name> directory
	path        string // the transcript
	aiTitle     string // the title Claude Code wrote, from the head scan
	firstPrompt string // the conversation's first prompt, from the head scan
}

// sessionHeadCap bounds how much of a transcript the listing reads to
// find the working directory and the title: Claude Code writes the first
// title around line 30, and a 4 MB transcript must not be read whole to
// print one row.
const sessionHeadCap = 512 << 10

// sessionLineCap bounds one transcript line. A tool result lands in the
// transcript whole, so a line of a few megabytes is normal.
const sessionLineCap = 8 << 20

// listLaptopSessions is every conversation of the laptop that ran in root
// or under it, newest first. root is the checkout (or the folder, when it
// is no repository). A laptop with no Claude Code config has none.
func listLaptopSessions(homeDir, root string) ([]laptopSession, error) {
	if root == "" {
		return nil, nil
	}
	root = filepath.Clean(root)
	base := filepath.Join(claudeConfigDir(homeDir), claudeProjectsDir)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []laptopSession
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		dir := filepath.Join(base, de.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			id, ok := strings.CutSuffix(name, ".jsonl")
			if !ok || !looksLikeUUID(id) || !f.Type().IsRegular() {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			s := laptopSession{ID: id, At: info.ModTime(), Bytes: info.Size(), dir: dir, path: filepath.Join(dir, name)}
			if err := s.readHead(root); err != nil || s.Cwd == "" || !pathWithin(s.Cwd, root) {
				continue
			}
			s.Title = s.titleFrom(dir)
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].At.Equal(out[j].At) {
			return out[i].ID < out[j].ID
		}
		return out[i].At.After(out[j].At)
	})
	return out, nil
}

// pathWithin reports whether p is root or sits under it. Both are
// cleaned absolute paths from this laptop, so a plain prefix with the
// separator is the whole test. On Windows it ignores case, where
// `C:\Users\x\app` and `c:\users\x\app` are one folder and a
// difference would read as "no conversation here".
func pathWithin(p, root string) bool {
	p = filepath.Clean(p)
	if goos() == "windows" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator)) ||
		// A transcript written on this laptop always uses the laptop's
		// separator, but a checkout copied between machines may not.
		strings.HasPrefix(p, root+"/")
}

// readHead reads the start of the transcript for the working directory,
// and keeps the title Claude Code wrote and the first prompt on the way.
// It stops at the working directory when that is another folder's, so a
// laptop with hundreds of conversations costs a few lines each and not
// sessionHeadCap each.
func (s *laptopSession) readHead(root string) error {
	f, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), sessionLineCap)
	var read int
	for sc.Scan() {
		read += len(sc.Bytes()) + 1
		var l struct {
			Type    string `json:"type"`
			Cwd     string `json:"cwd"`
			AITitle string `json:"aiTitle"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			continue
		}
		if s.Cwd == "" && l.Cwd != "" {
			s.Cwd = l.Cwd
			if !pathWithin(s.Cwd, root) {
				return nil
			}
		}
		if s.aiTitle == "" && l.AITitle != "" {
			s.aiTitle = l.AITitle
		}
		if s.firstPrompt == "" && l.Type == "user" && l.Message.Role == "user" {
			var text string
			if json.Unmarshal(l.Message.Content, &text) == nil {
				s.firstPrompt = text
			}
		}
		if s.Cwd != "" && s.aiTitle != "" {
			return nil
		}
		if read >= sessionHeadCap {
			break
		}
	}
	return sc.Err()
}

// titleFrom is the conversation's name: the title the user set in Claude
// Code, else the one Claude Code wrote for it, else its first prompt.
func (s *laptopSession) titleFrom(dir string) string {
	if b, err := os.ReadFile(filepath.Join(dir, s.ID, "custom-title.json")); err == nil {
		var t struct {
			CustomTitle string `json:"customTitle"`
		}
		if json.Unmarshal(b, &t) == nil && t.CustomTitle != "" {
			return oneLine(t.CustomTitle)
		}
	}
	if s.aiTitle != "" {
		return oneLine(s.aiTitle)
	}
	return oneLine(s.firstPrompt)
}

// sessionTitleCap is how much of a title a row shows.
const sessionTitleCap = 52

// shortTitle is the title cut to one terminal column's worth.
func shortTitle(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	r := []rune(t)
	if len(r) <= sessionTitleCap {
		return t
	}
	return strings.TrimSpace(string(r[:sessionTitleCap-1])) + "…"
}

// resolveLaptopSession is the conversation idArg names among those of
// this checkout: sessionLast is the newest, and anything else is a whole
// id, its start or its end, as every id the CLI takes (I-619).
func resolveLaptopSession(sessions []laptopSession, idArg, where string) (laptopSession, error) {
	if len(sessions) == 0 {
		return laptopSession{}, exitf(ExitUsage, "This laptop has no Claude Code conversation in %s, so there is none to bring.", laptopPath(where))
	}
	if idArg == sessionLast {
		return sessions[0], nil
	}
	ids := make([]string, len(sessions))
	for i, s := range sessions {
		ids[i] = s.ID
	}
	hits := matchID(ids, idArg)
	if len(hits) > 1 {
		return laptopSession{}, exitf(ExitUsage, "%s names %d of this laptop's conversations; give more of the id.", idArg, len(hits))
	}
	for _, s := range sessions {
		if len(hits) == 1 && s.ID == hits[0] {
			return s, nil
		}
	}
	return laptopSession{}, exitf(ExitUsage, "%s names no Claude Code conversation in %s. `repose sessions` lists them.", idArg, laptopPath(where))
}

// laptopSessionIDs is the ids alone, for the flag parser (sessionArgs).
func laptopSessionIDs(homeDir, root string) []string {
	ss, err := listLaptopSessions(homeDir, root)
	if err != nil {
		return nil
	}
	ids := make([]string, len(ss))
	for i, s := range ss {
		ids[i] = s.ID
	}
	return ids
}

// sessionSwap is one literal path replacement the import makes inside the
// conversation's files, as the Claude settings merge rewrites the laptop's
// home (claude_merge.jq's swap). A transcript holds paths in prose, in
// shell command lines and in tool output, so the swap is not anchored to
// the start of a value; it applies wherever the path ends, which is where
// the character after it cannot go on spelling the same name.
type sessionSwap struct {
	From, To string
	// cont reports whether a character after From keeps the name going,
	// so the swap must not apply: "/Users/jane" is not "/Users/janet",
	// and "-home-jane" is not "-home-jane2".
	cont func(byte) bool
}

// pathNameChar is a character that can go on spelling a path component:
// after it, the path has not ended.
func pathNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '-' || c == '.'
}

// encodedNameChar is the same for the names Claude Code gives its
// `projects/` directories, where the separator is "-" and so "-" ends a
// component rather than continuing it.
func encodedNameChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func (s sessionSwap) apply(v string) string {
	if s.From == "" || s.From == s.To || !strings.Contains(v, s.From) {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); {
		if strings.HasPrefix(v[i:], s.From) {
			j := i + len(s.From)
			if j == len(v) || !s.cont(v[j]) {
				b.WriteString(s.To)
				i = j
				continue
			}
		}
		b.WriteByte(v[i])
		i++
	}
	return b.String()
}

// sessionRewrite is the swaps for one import, in the order they apply.
// The checkout comes before the home, since the checkout usually sits
// inside it, and the encoded forms come before the plain ones so that
// `projects/<encoded>` paths inside the transcript land in the machine's
// directory and not in a name built from the laptop's.
func sessionRewrite(homeDir, laptopRoot, machineRoot string) []sessionSwap {
	return []sessionSwap{
		{From: claudeProjectDir(laptopRoot), To: claudeProjectDir(machineRoot), cont: encodedNameChar},
		{From: claudeProjectDir(homeDir), To: claudeProjectDir(guestHome), cont: encodedNameChar},
		{From: laptopRoot, To: machineRoot, cont: pathNameChar},
		{From: homeDir, To: guestHome, cont: pathNameChar},
	}
}

func applySwaps(v string, swaps []sessionSwap) string {
	for _, s := range swaps {
		v = s.apply(v)
	}
	return v
}

// rewriteJSONValue walks a decoded JSON value and rewrites every string
// in it, which is what keeps the result valid JSON whatever a laptop path
// holds (a Windows backslash is escaped by the encoder, not by us).
func rewriteJSONValue(v any, swaps []sessionSwap) any {
	switch t := v.(type) {
	case string:
		return applySwaps(t, swaps)
	case []any:
		for i, e := range t {
			t[i] = rewriteJSONValue(e, swaps)
		}
		return t
	case map[string]any:
		// The keys first: `file-history-snapshot` keys its backups by the
		// file's path, so a key is rewritten too, and a map must not be
		// changed while it is being walked.
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		for _, k := range keys {
			nv := rewriteJSONValue(t[k], swaps)
			if nk := applySwaps(k, swaps); nk != k {
				delete(t, k)
				t[nk] = nv
				continue
			}
			t[k] = nv
		}
		return t
	}
	return v
}

// marshalLine encodes one rewritten value the way the transcript held it:
// no HTML escaping, so a prompt with `<` or `&` in it keeps the bytes
// Claude Code wrote, and no trailing newline of the encoder's own.
func marshalLine(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// rewriteJSONLines is a transcript with its laptop paths rewritten. A
// line that is not JSON travels as it is: the file is the user's
// conversation, and dropping part of it would be worse than leaving one
// line's paths alone.
func rewriteJSONLines(b []byte, swaps []sessionSwap) ([]byte, error) {
	var out []byte
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), sessionLineCap)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			out = append(out, line...)
			out = append(out, '\n')
			continue
		}
		enc, err := marshalLine(rewriteJSONValue(v, swaps))
		if err != nil {
			return nil, err
		}
		out = append(out, enc...)
		out = append(out, '\n')
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// sessionExtraDirs are the directories Claude Code keeps beside a
// transcript that belong to that conversation and nothing else: its
// subagents' own transcripts, and the tool results the transcript points
// at by path. An allowlist, so a directory Claude Code adds later stays
// on the laptop until someone decides it should travel.
var sessionExtraDirs = []string{"subagents", "tool-results"}

// sessionExtraFiles are the files beside a transcript that travel with it.
var sessionExtraFiles = []string{"custom-title.json"}

// sessionExtrasCap bounds the files beside the transcript. Over it they
// stay behind, named once, and the conversation still resumes: a tool
// result the machine lacks reads as one that was cleaned up.
const sessionExtrasCap = 32 << 20

// sessionCap bounds the transcript itself. Over it nothing is brought,
// because without the transcript there is no conversation.
const sessionCap = 64 << 20

// sessionCarry is one conversation as it travels.
type sessionCarry struct {
	ID string
	// Dir is the machine's projects/<name> directory.
	Dir string
	// Cwd is the conversation's folder on the machine, absolute: the
	// checkout, or the folder under it the conversation ran in. The
	// window opens there, and the part makes it when the sync has not
	// (an empty or ignored folder).
	Cwd string
	// Files are the machine-relative paths under Dir, with their bytes
	// already rewritten.
	Files map[string][]byte
	// Todos are the conversation's todo files, keyed by their name under
	// the Claude config directory's `todos/`.
	Todos map[string][]byte
	// Hash is what the machine records once the import has landed, so a
	// second run with the same conversation sends nothing.
	Hash string
	// Notes are said once: what was left behind and why.
	Notes []string
	// Bytes is everything that travels, for the line the run prints.
	Bytes int64
}

// buildSessionCarry reads the conversation and rewrites it for the
// machine. machineRoot is the checkout's absolute path on the machine
// (/home/dev/<name>), which the conversation's own working directory is
// mapped onto, so a conversation that ran in a subdirectory of the
// checkout lands in the machine's subdirectory of it.
func buildSessionCarry(s laptopSession, homeDir, laptopRoot, machineRoot string) (*sessionCarry, error) {
	if s.Bytes > sessionCap {
		return nil, fmt.Errorf("the conversation is %s, more than the %s one run brings", humanBytes(s.Bytes), humanBytes(sessionCap))
	}
	laptopRoot = filepath.Clean(laptopRoot)
	swaps := sessionRewrite(homeDir, laptopRoot, machineRoot)
	machineCwd := machineRoot
	if rel, err := filepath.Rel(laptopRoot, filepath.Clean(s.Cwd)); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		machineCwd = machineRoot + "/" + filepath.ToSlash(rel)
	}
	sc := &sessionCarry{ID: s.ID, Dir: claudeProjectDir(machineCwd), Cwd: machineCwd, Files: map[string][]byte{}, Todos: map[string][]byte{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	body, err := rewriteJSONLines(raw, swaps)
	if err != nil {
		return nil, err
	}
	sc.Files[s.ID+".jsonl"] = body
	sc.Bytes = int64(len(body))

	extras, over, err := readSessionExtras(filepath.Join(s.dir, s.ID), swaps)
	if err == nil {
		for rel, b := range extras {
			sc.Files[s.ID+"/"+rel] = b
			sc.Bytes += int64(len(b))
		}
	}
	if over {
		sc.Notes = append(sc.Notes, fmt.Sprintf("The files beside the conversation are over %s, so they stayed on this laptop; the conversation itself came.", humanBytes(sessionExtrasCap)))
	}
	for name, b := range readSessionTodos(claudeConfigDir(homeDir), s.ID, swaps) {
		sc.Todos[name] = b
		sc.Bytes += int64(len(b))
	}
	parts := [][]byte{[]byte(sc.Dir)}
	for _, rel := range sortedByteKeys(sc.Files) {
		parts = append(parts, []byte(rel), sc.Files[rel])
	}
	for _, name := range sortedByteKeys(sc.Todos) {
		parts = append(parts, []byte(name), sc.Todos[name])
	}
	sc.Hash = carryHash(parts...)
	return sc, nil
}

// readSessionExtras reads the allowlisted files under the conversation's
// own directory, rewritten. JSON files are rewritten as JSON; anything
// else (a saved tool result) has its laptop paths replaced in its bytes.
func readSessionExtras(dir string, swaps []sessionSwap) (map[string][]byte, bool, error) {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return nil, false, fmt.Errorf("no %s", dir)
	}
	out := map[string][]byte{}
	var total int64
	var over bool
	add := func(rel, path string, size int64) error {
		if total+size > sessionExtrasCap {
			over = true
			return filepath.SkipAll
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		out[rel] = rewriteSessionFile(rel, b, swaps)
		total += int64(len(b))
		return nil
	}
	for _, name := range sessionExtraFiles {
		p := filepath.Join(dir, name)
		if st, err := os.Lstat(p); err == nil && st.Mode().IsRegular() {
			if err := add(name, p, st.Size()); err != nil {
				return out, over, nil
			}
		}
	}
	for _, d := range sessionExtraDirs {
		root := filepath.Join(dir, d)
		if st, err := os.Lstat(root); err != nil || !st.IsDir() {
			continue
		}
		err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
			if err != nil || de.IsDir() || !de.Type().IsRegular() {
				return nil
			}
			if claudeSecretFileName(de.Name()) {
				return nil
			}
			st, err := de.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return nil
			}
			return add(filepath.ToSlash(rel), p, st.Size())
		})
		if err != nil {
			break
		}
	}
	return out, over, nil
}

// readSessionTodos reads the conversation's todo files. Claude Code names
// them after the conversation under the config directory's `todos/`, so
// the conversation's own are exactly the ones whose name starts with its
// id; no other conversation's travels.
func readSessionTodos(cfgDir, id string, swaps []sessionSwap) map[string][]byte {
	entries, err := os.ReadDir(filepath.Join(cfgDir, "todos"))
	if err != nil {
		return nil
	}
	out := map[string][]byte{}
	for _, de := range entries {
		if !de.Type().IsRegular() || !strings.HasPrefix(de.Name(), id) {
			continue
		}
		st, err := de.Info()
		if err != nil || st.Size() > claudeFileCap {
			continue
		}
		b, err := os.ReadFile(filepath.Join(cfgDir, "todos", de.Name()))
		if err != nil {
			continue
		}
		out[de.Name()] = rewriteSessionFile(de.Name(), b, swaps)
	}
	return out
}

// rewriteSessionFile rewrites one file's laptop paths: through JSON when
// the name says it is JSON (so an escaped backslash path stays escaped),
// else in its bytes.
func rewriteSessionFile(name string, b []byte, swaps []sessionSwap) []byte {
	switch {
	case strings.HasSuffix(name, ".jsonl"):
		if out, err := rewriteJSONLines(b, swaps); err == nil {
			return out
		}
	case strings.HasSuffix(name, ".json"):
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		var v any
		if dec.Decode(&v) == nil {
			if out, err := marshalLine(rewriteJSONValue(v, swaps)); err == nil {
				return out
			}
		}
	}
	return []byte(applySwaps(string(b), swaps))
}

func sortedByteKeys(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// sessionMarker is what the machine records once a conversation has
// landed, beside the carry's markers (carryMarkerDir), so the next run
// with the same conversation sends none of it again.
func sessionMarker(id string) string { return "session-" + id }

// sessionProbeScript asks the machine what it already has of this
// conversation: the marker the last import wrote, and the size of the
// transcript it holds. Both, because the file is the truth — the user may
// have gone on with the conversation on the machine — and the marker says
// whether the files beside it landed too.
//
// It also makes the conversation's folder, which is where the window
// opens: the sync does not bring a folder that holds nothing it tracks,
// and tmux hands a window with a `-c` that does not exist the home
// directory instead. Here and not in the part, because a conversation
// the machine already has sends no part and still needs the folder.
func sessionProbeScript(dir, id, cwd string) string {
	return fmt.Sprintf(`mkdir -p %[5]s
f=$HOME/.claude/projects/%[1]s/%[2]s.jsonl
[ -f "$f" ] && printf '#have %%s\n' "$(wc -c < "$f" | tr -d ' ')"
m=%[3]s/%[4]s
[ -f "$m" ] && printf '#mark %%s\n' "$(cat "$m")"
tmux list-windows -a -F '#{@`+sessionWindowOption+`} #{window_name}' 2>/dev/null | awk -v i=%[2]s '$1 == i { printf "#window %%s\n", $2; exit }'
true
`, shQuote(dir), shQuote(id), carryMarkerDir, shQuote(sessionMarker(id)), shQuote(cwd))
}

// sessionWindowOption is the tmux window option a resumed window carries:
// the conversation it was started on. The next `--session` for that
// conversation finds the window instead of opening a second claude on one
// transcript, which would interleave two conversations into one file
// (DECISIONS I-1328). herdr has no window options, so a herdr machine
// answers nothing here and opens a second tab, which its own
// "another agent is open in the same working tree" line still names.
const sessionWindowOption = "repose-session"

// sessionProbe is the machine's answer.
type sessionProbe struct {
	Have   int64  // the machine's transcript, 0 when it has none
	Mark   string // the hash the last import recorded, "" when none
	Window string // a window already resumed on the conversation
	HasIt  bool
}

func parseSessionProbe(out string) sessionProbe {
	var p sessionProbe
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if v, ok := strings.CutPrefix(l, "#have "); ok {
			p.HasIt = true
			var n int64
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				p.Have = n
			}
		}
		if v, ok := strings.CutPrefix(l, "#mark "); ok {
			p.Mark = v
		}
		if v, ok := strings.CutPrefix(l, "#window "); ok {
			p.Window = v
		}
	}
	return p
}

// addSessionPart writes the conversation into the machine's Claude
// projects directory. A transcript the machine already holds and has gone
// further with is kept, and the laptop's is not sent at all: that
// decision is made before the upload, from the probe.
func addSessionPart(p *guestPayload, sc *sessionCarry) error {
	for _, rel := range sortedByteKeys(sc.Files) {
		if err := p.fileMeta("session/"+rel, sc.Files[rel], 0o600, unixEpoch); err != nil {
			return err
		}
	}
	for _, name := range sortedByteKeys(sc.Todos) {
		if err := p.fileMeta("session-todos/"+name, sc.Todos[name], 0o600, unixEpoch); err != nil {
			return err
		}
	}
	script := fmt.Sprintf(`d=$HOME/.claude/projects/%s
mkdir -p "$d"
f=$d/%s.jsonl
if [ -f "$f" ]; then cp -p "$f" "$f.repose-prev"; fi
cp -R "$1/session/." "$d/"
chmod -R u+rw,go-rwx "$d/%s.jsonl" "$d/%s" 2>/dev/null || true
`, shQuote(sc.Dir), shQuote(sc.ID), shQuote(sc.ID), shQuote(sc.ID))
	if len(sc.Todos) > 0 {
		script += `mkdir -p "$HOME/.claude/todos" && cp -R "$1/session-todos/." "$HOME/.claude/todos/"
`
	}
	script += setMarker(sessionMarker(sc.ID), sc.Hash)
	return p.part("conversation", script)
}
