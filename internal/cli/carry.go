package cli

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The carry is what follows the user from the laptop into the guest on
// `run` and `attach` besides the tool logins: the clock (I-198), the git
// config (I-195), the Claude Code config (I-196) and, on `run`, the
// gitignored .env files (I-197). docs/workstreams/15-dev-ergonomics.md.
//
// It never adds a round trip to `run`: its parts ride the credentials ssh
// (I-149's single multiplexed connection), and the markers that let the
// laptop skip what has not changed come back in the sync's first ssh.
// On `attach` it runs in the session helper, beside tmux, never before it.

// carryMarkerDir is where the guest remembers, per carried item, the hash
// of the laptop input it last applied. The guest is the truth: a restore,
// a second laptop or an edit made in the guest all change what it holds,
// which a hash kept on one laptop could not know.
const carryMarkerDir = "~/.repose/carry"

// carryVersion is folded into every hash, so a change to what a part
// writes re-sends it to every guest once.
const carryVersion = "1"

// carryOptions says which parts one carry sends.
type carryOptions struct {
	// TZ is the laptop's IANA zone; "" leaves the guest's alone.
	TZ string
	// Git is the laptop's git config for the checkout (I-195); nil when
	// the command is not run from the project's checkout, whose includeIf
	// rules and identity it needs.
	Git *gitCarry
	// Claude is the laptop's Claude Code config (I-196).
	Claude *claudeCarry
	// Tools is the laptop's global tools and the project's commands
	// (I-221, I-222); `run` only.
	Tools *toolsCarry
	// MCP is the laptop's Claude Code MCP servers, templated (I-556).
	MCP *mcpCarry
	// Markers is the guest's marker set (item -> hash), from the sync's
	// probe or the helper's own. Nil sends every part; a part whose hash
	// matches its marker is left out.
	Markers map[string]string
}

// carryOutcome is what the guest said back.
type carryOutcome struct {
	// TZ is the zone the guest's environment file was moved to, when it
	// changed.
	TZ string
	// Kept names what the guest kept because its copy was newer.
	Kept []string
	// Dropped names what was left out because it would not work in the
	// guest (a missing command, a laptop path). Shown once per change,
	// since an unchanged part is not sent again.
	Dropped []string
	// Warnings are one-line problems that did not stop anything else.
	Warnings []string
	// Installing names the tools the guest lacked and is installing in
	// the background (I-221).
	Installing []string
	// Failed names the parts whose script failed; the previous state of
	// that part is still in place.
	Failed []string
	// MCPLeft is "NAME (reason)" per MCP server left on the laptop, and
	// MCPForward the names among them `repose mcp forward` can run;
	// MCPSecrets the secrets those carried need and the machine lacks;
	// MCPMissing the [server, command] pairs the machine lacks; MCPOld a
	// base without repose-mcp (I-556).
	MCPLeft    []string
	MCPForward []string
	MCPSecrets []mcpNeed
	MCPMissing [][2]string
	MCPOld     bool
	// Sent is the parts that travelled, for tests and -v.
	Sent []string
}

// Lines is the outcome as the user sees it: one line per fact, none when
// nothing worth saying happened.
func (o *carryOutcome) Lines() []string {
	var out []string
	if o.TZ != "" {
		out = append(out, "Time zone set to "+o.TZ+".")
	}
	for _, k := range o.Kept {
		out = append(out, "Kept the machine's "+k+": it is newer than the laptop's.")
	}
	if len(o.Dropped) > 0 {
		out = append(out, "Not carried (would not work in the guest): "+strings.Join(o.Dropped, ", ")+".")
	}
	if n := len(o.Installing); n > 0 {
		what := "tool"
		if n > 1 {
			what = "tools"
		}
		out = append(out, fmt.Sprintf("Installing %d of your %s in the background: %s", n, what, strings.Join(o.Installing, ", ")))
	}
	out = append(out, o.mcpLines()...)
	out = append(out, o.Warnings...)
	for _, f := range o.Failed {
		out = append(out, "Could not carry your "+f+" config; the guest keeps its previous one.")
	}
	return out
}

// parse reads the guest's reply lines into the outcome.
func (o *carryOutcome) parse(out string) {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		tag, rest, _ := strings.Cut(l, " ")
		if o.parseMCP(tag, rest) {
			continue
		}
		switch tag {
		case "#tz":
			o.TZ = rest
		case "#kept":
			o.Kept = append(o.Kept, rest)
		case "#dropped":
			o.Dropped = append(o.Dropped, rest)
		case "#warn":
			o.Warnings = append(o.Warnings, rest)
		case "#failed":
			o.Failed = append(o.Failed, rest)
		case "#installing":
			o.Installing = append(o.Installing, strings.Fields(rest)...)
		}
	}
}

// guestPayload is one tar and one script for one ssh: the script unpacks
// the tar into a temporary directory and runs each part from it. A part
// runs as its own `sh -e`, so it stops at its own first failure, reports
// `#failed <label>`, and never stops the parts after it.
type guestPayload struct {
	buf    bytes.Buffer
	tw     *tar.Writer
	script strings.Builder
	parts  int
	lines  int // top-level lines added after the preamble
}

func newGuestPayload() *guestPayload {
	p := &guestPayload{}
	p.tw = tar.NewWriter(&p.buf)
	p.script.WriteString("set -e\nt=$(mktemp -d)\ntrap 'rm -rf \"$t\"' EXIT\ntar -x -C \"$t\"\n")
	return p
}

// file adds one file to the tar, 0600.
func (p *guestPayload) file(name string, b []byte) error { return tarAddBytes(p.tw, name, b) }

// fileMeta adds one file with its mode and mtime kept.
func (p *guestPayload) fileMeta(name string, b []byte, mode int64, mtime time.Time) error {
	if err := p.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(b)), ModTime: mtime}); err != nil {
		return err
	}
	_, err := p.tw.Write(b)
	return err
}

// line appends a line to the top-level script (runs under its set -e).
func (p *guestPayload) line(s string) { p.script.WriteString(s + "\n"); p.lines++ }

// empty reports whether the payload has nothing to do in the guest.
func (p *guestPayload) empty() bool { return p.parts == 0 && p.lines == 0 }

// part adds a script that runs on its own with the unpack directory as
// $1. label names it in `#failed <label>`.
func (p *guestPayload) part(label, script string) error {
	p.parts++
	name := fmt.Sprintf("part-%d.sh", p.parts)
	if err := p.file(name, []byte(script)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(&p.script, "sh -e \"$t/%s\" \"$t\" || echo '#failed %s'\n", name, label)
	return nil
}

// observePayload, when set (tests only), sees every payload before it is
// sent: the never-carried test reads the whole stream through it.
var observePayload func(script string, tarball []byte)

func (p *guestPayload) run(ctx context.Context, t sshTarget) ([]byte, error) {
	if err := p.tw.Close(); err != nil {
		return nil, err
	}
	if observePayload != nil {
		observePayload(p.script.String(), p.buf.Bytes())
	}
	return runSSH(ctx, t, p.script.String(), &p.buf)
}

// addCarry adds every part opts asks for to p and returns the labels of
// the parts it added.
func addCarry(p *guestPayload, opts carryOptions) ([]string, error) {
	var sent []string
	// The zone is sent when it differs from the one the guest last took;
	// a reboot keeps it (repose-tmux-session reads /etc/repose/env, and
	// the next start's SetupProject writes the project's tz, which the CLI
	// moved to the same zone).
	if tzHash := carryHash([]byte(opts.TZ)); opts.TZ != "" && !opts.unchanged("tz", tzHash) {
		if err := p.part("time zone", tzPart(opts.TZ)+setMarker("tz", tzHash)); err != nil {
			return nil, err
		}
		sent = append(sent, "tz")
	}
	if v := cliVersion; v != "" && !opts.unchanged("cli-version", carryHash([]byte(v))) {
		if err := p.part("cli version", cliVersionPart(v)+setMarker("cli-version", carryHash([]byte(v)))); err != nil {
			return nil, err
		}
		sent = append(sent, "cli-version")
	}
	if ok, err := addGitPart(p, opts.Git, opts); err != nil {
		return nil, err
	} else if ok {
		sent = append(sent, "git")
	}
	cs, err := addClaudeParts(p, opts.Claude, opts)
	if err != nil {
		return nil, err
	}
	sent = append(sent, cs...)
	ms, err := addMCPParts(p, opts.MCP, opts)
	if err != nil {
		return nil, err
	}
	sent = append(sent, ms...)
	ts, err := addToolsPart(p, opts.Tools, opts)
	if err != nil {
		return nil, err
	}
	sent = append(sent, ts...)
	return sent, nil
}

// markerScript is the shell that prints the guest's markers as
// `#marker <item> <hash>` lines.
// A waiting ~/.repose/tools-notices (what the tools installer could not
// install, I-221) shows as the tools-notices marker, so the carry prints
// it once.
func markerScript() string {
	return `for f in ` + carryMarkerDir + `/*; do [ -f "$f" ] && printf '#marker %s %s\n' "${f##*/}" "$(cat "$f")"; done
[ -s ~/.repose/tools-notices ] && echo '#marker tools-notices waiting'; true
`
}

// parseMarkers picks the `#marker` lines out of a reply.
func parseMarkers(out string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 3 && f[0] == "#marker" {
			m[f[1]] = f[2]
		}
	}
	return m
}

// setMarker is the shell line a part ends with once it has applied item.
func setMarker(item, hash string) string {
	return fmt.Sprintf("mkdir -p %s && printf '%%s\\n' %s > %s/%s\n", carryMarkerDir, hash, carryMarkerDir, item)
}

// carryHash is the marker value for a part's input: its version and every
// byte that decides what the part writes, in a fixed order.
// secretPattern matches a credential inside a carried value or key (I-211):
// a URL with a password or token in its user info, and the prefixes of
// the tokens a laptop config most often holds (Anthropic, GitHub classic,
// fine-grained and OAuth, GitLab, Slack, AWS access keys) or a bearer
// header.
var secretPattern = regexp.MustCompile(`://[^/\s:@]+:[^/\s@]+@|sk-ant-|gh[pousr]_[A-Za-z0-9]|github_pat_|glpat-|xox[abposr]-|AKIA[0-9A-Z]{16}|(?i:bearer)\s+[A-Za-z0-9._~+/=-]{20,}`)

// secretIn reports whether s holds a credential by secretPattern. The
// carry drops the entry; the value is never printed.
func secretIn(s string) bool { return secretPattern.MatchString(s) }

func carryHash(parts ...[]byte) string {
	h := sha256.New()
	h.Write([]byte(carryVersion))
	for _, b := range parts {
		_, _ = fmt.Fprintf(h, "\x00%d\x00", len(b))
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// unchanged reports whether the guest already holds hash for item.
func (o carryOptions) unchanged(item, hash string) bool {
	return o.Markers != nil && o.Markers[item] == hash
}

// ianaZone matches what a zone name may contain, which is also what makes
// it safe inside a shell word: letters, digits and _+- in slash-separated
// parts ("America/Argentina/Buenos_Aires", "Etc/GMT+3").
var ianaZone = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)

// tzPart moves the guest to zone (I-198): /etc/repose/env, which every
// login shell sources and guestd rewrites at the next start from the
// project's tz (the CLI sends that to the api too), and tmux's global
// and per-session environment, which every new window and agent is
// started with. Shells already running keep the zone they started with.
// sudo is the guest's passwordless one (dev is in wheel); the file stays
// root's, 0644, replaced by a rename.
// cliVersion is this binary's version, set by Execute; "" in tests,
// which then send no cli-version part.
var cliVersion string

// cliVersionPart writes the laptop's repose version where the machine
// guide tells an agent to look (I-412): the public docs describe the
// latest release, and an agent that suggests a command should know when
// the user's CLI is older.
func cliVersionPart(v string) string {
	return fmt.Sprintf("mkdir -p ~/.repose && printf '%%s\\n' %s > ~/.repose/cli-version.new && mv -f ~/.repose/cli-version.new ~/.repose/cli-version\n", shQuote(v))
}

func tzPart(zone string) string {
	return fmt.Sprintf(`z=%s
f=/etc/repose/env
if [ -f "$f" ] && ! grep -qxF "TZ=$z" "$f"; then
  { grep -v '^TZ=' "$f" || true; printf 'TZ=%%s\n' "$z"; } > "$1/env.new"
  sudo -n sh -c 'cat > /etc/repose/env.repose-new && chmod 0644 /etc/repose/env.repose-new && mv -f /etc/repose/env.repose-new /etc/repose/env' < "$1/env.new"
  echo "#tz $z"
fi
if tmux list-sessions >/dev/null 2>&1; then
  tmux set-environment -g TZ "$z"
  tmux list-sessions -F '#{session_name}' | while IFS= read -r s; do tmux set-environment -t "=$s" TZ "$z"; done
fi
`, shQuote(zone))
}

var (
	laptopTZOnce sync.Once
	laptopTZVal  string
)

// laptopTZ is localTZ, read once per process, and only ever a name that
// passes ianaZone (anything else is treated as unknown). A variable so
// tests can be in a zone of their choosing.
var laptopTZ = func() string {
	laptopTZOnce.Do(func() {
		if z := localTZ(); ianaZone.MatchString(z) {
			laptopTZVal = z
		}
	})
	return laptopTZVal
}

// carryNotedName is the laptop file that remembers, for each project, the
// carry lines `run` printed last (DECISIONS I-618): "Logins copied: gh,
// codex", a login or file the machine kept, what was not carried. A line
// prints again only when its content changes, so the one new line is not
// lost among ten that every run repeats.
const carryNotedName = "carry-noted.json"

// carryNoter decides which of a run's carry lines print. Lines are kept
// as hashes, never as text.
type carryNoter struct {
	path    string
	project string
	all     map[string][]string // the file: project id -> line hashes
	before  map[string]bool     // this project's hashes from the last run
	now     []string
}

// newCarryNoter reads the file in dir for project. A file that cannot be
// read costs one repeat of each line.
func newCarryNoter(dir, project string) *carryNoter {
	n := &carryNoter{project: project, all: map[string][]string{}, before: map[string]bool{}}
	if dir == "" || project == "" {
		return n
	}
	n.path = filepath.Join(dir, carryNotedName)
	if b, err := os.ReadFile(n.path); err == nil {
		_ = json.Unmarshal(b, &n.all) // a damaged cache only repeats lines
	}
	for _, h := range n.all[project] {
		n.before[h] = true
	}
	return n
}

// fresh records line and reports whether it prints: one the last run of
// this project did not print. A failure ("Could not ...") always prints
// and is never recorded, since it is this run's news.
func (n *carryNoter) fresh(line string) bool {
	if n == nil {
		return true
	}
	if strings.HasPrefix(line, "Could not ") {
		return true
	}
	sum := sha256.Sum256([]byte(line))
	h := hex.EncodeToString(sum[:8])
	n.now = append(n.now, h)
	return !n.before[h]
}

// save replaces the project's lines with this run's, so a line that
// stops and later comes back prints again.
func (n *carryNoter) save() {
	if n == nil || n.path == "" {
		return
	}
	if len(n.now) == 0 {
		if _, ok := n.all[n.project]; !ok {
			return
		}
		delete(n.all, n.project)
	} else {
		n.all[n.project] = n.now
	}
	if b, err := json.Marshal(n.all); err == nil {
		_ = writeFileAtomic(n.path, b, 0o600) // best effort, see newCarryNoter
	}
}

// keptLoginLine is the line for a login the machine kept because its
// copy is newer than the laptop's.
func keptLoginLine(label string) string {
	return "Kept the machine's " + label + " login: it is newer than the laptop's."
}

// warn prints line on stderr when it is fresh.
func (n *carryNoter) warn(e *Env, line string) {
	if n.fresh(line) {
		e.warn("%s", line)
	}
}

// print is the end of a run's carry: the logins copied, the logins left
// on the laptop and the carry's own lines, each when it is fresh; then
// the record of what this run had to say.
func (n *carryNoter) print(e *Env, copied []string, skip map[string]bool, chosen bool, carried *carryOutcome) {
	if len(copied) > 0 {
		if l := "Logins copied: " + strings.Join(copied, ", "); n.fresh(l) {
			_, _ = fmt.Fprintln(e.Out, l)
		}
		if l := loginsLine(copied, skip, chosen); l != "" && n.fresh(l) {
			_, _ = fmt.Fprintln(e.Out, l)
		}
	}
	if carried != nil {
		for _, l := range carried.Lines() {
			if n.fresh(l) {
				_, _ = fmt.Fprintln(e.ErrOut, l)
			}
		}
	}
	n.save()
}
