package cli

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/heracraft/repose/internal/multiplexer"
)

// unixEpoch is the mtime carried files are packed with, so the payload's
// bytes, like the hashes, depend on content only.
var unixEpoch = time.Unix(0, 0)

// The Claude Code part of the carry (I-196): the user's own Claude
// configuration, never its credentials or its state. Carried:
// ~/.claude/CLAUDE.md, settings.json (merged in the guest, never
// overwritten), skills/, agents/, commands/, output-styles/,
// keybindings.json, and the scripts under ~/.claude that settings.json
// runs (hooks, statusLine). Everything else in ~/.claude stays on the
// laptop: .credentials.json, projects/ (transcripts), history.jsonl,
// todos/, shell-snapshots/, file-history/, paste-cache/, sessions/,
// plugins/, statsig/, and ~/.claude.json.

//go:embed claude_merge.jq
var claudeMergeJQ []byte

// claudeFiles and claudeDirs are the allowlist; nothing outside it is
// ever read.
var (
	claudeFiles = []string{"CLAUDE.md", "keybindings.json"}
	claudeDirs  = []string{"skills", "agents", "commands", "output-styles"}
)

// claudeNeverDirs are state directories a settings.json script path is
// never taken from, whatever settings.json says.
var claudeNeverDirs = map[string]bool{
	"projects": true, "todos": true, "shell-snapshots": true, "file-history": true, "paste-cache": true,
	"sessions": true, "plugins": true, "statsig": true, "ide": true, "debug": true, "logs": true,
}

// claudeDirCap bounds one carried directory; skills/ was 4.3 MB on the dev
// box (the outlier the proposal measured), so this leaves room and still
// keeps a stray dataset out of the payload.
const claudeDirCap = 32 << 20

// claudeFileCap bounds one carried file.
const claudeFileCap = 4 << 20

// claudeItem is one carried piece: a file or a directory's files, keyed by
// the path relative to ~/.claude.
type claudeItem struct {
	Marker string
	Files  map[string]claudeFile // rel path -> the file on the laptop
	// Skipped are files under the item's directory left behind because
	// their names look like credentials (claudeSecretFileName). Their
	// names, never their contents, are said when the item is sent, so
	// once per change.
	Skipped []string
	// Hash is the item's marker value, from each file's content hash
	// (claudeHashes), so an unchanged item costs a stat per file.
	Hash string
}

// claudeFile is one laptop file. Its bytes are read only when its item is
// sent (or its hash is not cached): skills/ alone can be megabytes, and
// most runs change nothing.
type claudeFile struct {
	Path  string // absolute, on the laptop
	Mode  os.FileMode
	Size  int64
	MTime int64 // ns
}

func (f claudeFile) read() ([]byte, error) {
	b, err := os.ReadFile(f.Path)
	if err == nil {
		claudeReads.Add(1)
	}
	return b, err
}

// claudeReads counts laptop file reads, for the test that an unchanged
// carry reads nothing.
var claudeReads atomic.Int64

func claudeFileOf(p string, info os.FileInfo) claudeFile {
	return claudeFile{Path: p, Mode: info.Mode().Perm(), Size: info.Size(), MTime: info.ModTime().UnixNano()}
}

// claudeCarry is what the Claude part sends.
type claudeCarry struct {
	Items []claudeItem
	// Settings is the laptop's settings.json, already checked to be JSON;
	// nil when absent or invalid.
	Settings []byte
	Home     string // the laptop's home, rewritten to /home/dev in the guest
	// CfgDir is $CLAUDE_CONFIG_DIR when it moved the config off
	// ~/.claude; paths under it are rewritten to the guest's ~/.claude.
	CfgDir string
	// Plugins are enabledPlugins ids from a marketplace the guest can
	// reach; Marketplaces maps their names to an `add` source.
	Plugins      []string
	Marketplaces map[string]string
	// Notes are said once, on the laptop: an invalid settings.json, a
	// directory over the cap, a plugin from a laptop directory.
	Notes []string
}

// claudeConfigDir is where the laptop keeps its Claude config:
// $CLAUDE_CONFIG_DIR when set, else ~/.claude.
func claudeConfigDir(homeDir string) string {
	if d := os.Getenv(envClaudeConfigDir); d != "" {
		return d
	}
	return filepath.Join(homeDir, ".claude")
}

// buildClaudeCarry reads the allowlisted parts of the laptop's Claude
// config. A laptop with no ~/.claude carries nothing.
func buildClaudeCarry(homeDir string) (*claudeCarry, error) {
	dir := claudeConfigDir(homeDir)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, nil
	}
	cc := &claudeCarry{Home: homeDir, Marketplaces: map[string]string{}}
	if filepath.Clean(dir) != filepath.Join(homeDir, ".claude") {
		cc.CfgDir = filepath.Clean(dir)
	}
	for _, name := range claudeFiles {
		f, err := statSmallFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		cc.Items = append(cc.Items, claudeItem{Marker: "claude-" + markerName(name), Files: map[string]claudeFile{name: f}})
	}
	for _, d := range claudeDirs {
		files, skipped, over, err := readClaudeDir(dir, d)
		if err != nil {
			continue
		}
		if over {
			cc.Notes = append(cc.Notes, fmt.Sprintf("~/.claude/%s is over %d MB, so it was not carried.", d, claudeDirCap>>20))
			continue
		}
		if len(files) > 0 || len(skipped) > 0 {
			cc.Items = append(cc.Items, claudeItem{Marker: "claude-" + d, Files: files, Skipped: skipped})
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	switch {
	case err != nil:
	case !json.Valid(raw):
		cc.Notes = append(cc.Notes, "Your ~/.claude/settings.json is not valid JSON, so it was not carried; the guest keeps its own.")
	default:
		var s map[string]any
		_ = json.Unmarshal(raw, &s)
		if s == nil {
			// Valid JSON, not an object: nothing to merge.
			cc.Notes = append(cc.Notes, "Your ~/.claude/settings.json is not a JSON object, so it was not carried; the guest keeps its own.")
			break
		}
		var dropped int
		cc.Settings, dropped, err = claudeSettingsWithoutSecrets(raw)
		if err != nil {
			return nil, err
		}
		if dropped > 0 {
			cc.Notes = append(cc.Notes, fmt.Sprintf("Left out %d %s of your ~/.claude/settings.json that %s a credential (a token, or a password in a URL); set %s in the guest instead.", dropped, plural(dropped, "entry", "entries"), plural(dropped, "holds", "hold"), plural(dropped, "it", "them")))
		}
		// Scripts and plugins come from what travels, not the raw file.
		s = nil
		_ = json.Unmarshal(cc.Settings, &s)
		scripts := claudeScripts(s, homeDir, dir)
		if len(scripts) > 0 {
			cc.Items = append(cc.Items, claudeItem{Marker: "claude-scripts", Files: scripts})
		}
		cc.Plugins, cc.Marketplaces, cc.Notes = claudePlugins(s, cc.Notes)
	}
	hc := loadClaudeHashes()
	items := cc.Items[:0]
	for _, it := range cc.Items {
		if it.Hash = claudeItemHash(it, hc); it.Hash != "" {
			items = append(items, it)
		}
	}
	cc.Items = items
	hc.save()
	if len(cc.Items) == 0 && cc.Settings == nil {
		return nil, nil
	}
	return cc, nil
}

// claudeSecretKeys are settings.json keys that hold a credential or run
// one, and so never leave the laptop (I-211; "Claude Code credentials
// are never copied anywhere"): env (ANTHROPIC_API_KEY, MCP tokens),
// apiKeyHelper and the cloud-auth helpers (a helper carried to the guest
// would also flip it to API-key auth), the OpenTelemetry headers helper,
// and the forced login method, which is the laptop's account choice.
// Any other key starting with "aws" or "gcp" goes too: those are all
// cloud credential plumbing.
var claudeSecretKeys = map[string]bool{
	"env": true, "apiKeyHelper": true, "otelHeadersHelper": true,
	"forceLoginMethod": true, "forceLoginOrgUUID": true,
}

func claudeSecretKey(k string) bool {
	return claudeSecretKeys[k] || strings.HasPrefix(k, "aws") || strings.HasPrefix(k, "gcp")
}

// claudeSettingsWithoutSecrets is the laptop's settings.json, a JSON
// object, with claudeSecretKeys removed and every entry whose strings
// hold a credential (secretIn) left out: a hook or the statusLine whose
// command carries a token, a permission rule naming one, a marketplace
// whose URL has a password (and the plugins enabled from it). dropped
// counts those entries; their values are never said. What remains is
// re-encoded, so key order is Go's (sorted); the merge in the guest does
// not depend on it.
func claudeSettingsWithoutSecrets(raw []byte) (out []byte, dropped int, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, 0, err
	}
	for k := range m {
		if claudeSecretKey(k) {
			delete(m, k)
		}
	}
	for k, v := range m {
		if s, ok := v.(string); ok && secretIn(s) {
			delete(m, k)
			dropped++
			continue
		}
		nv, drop := scrubSecrets(v, &dropped)
		if drop {
			delete(m, k)
		} else {
			m[k] = nv
		}
	}
	// A marketplace left without its source cannot be added; neither can
	// the plugins enabled from it.
	if km, ok := m["extraKnownMarketplaces"].(map[string]any); ok {
		ep, _ := m["enabledPlugins"].(map[string]any)
		for name, v := range km {
			if vm, ok := v.(map[string]any); ok && vm["source"] != nil {
				continue
			}
			delete(km, name)
			for id := range ep {
				if strings.HasSuffix(id, "@"+name) {
					delete(ep, id)
				}
			}
		}
	}
	out, err = json.Marshal(m)
	return out, dropped, err
}

// scrubSecrets walks a settings value. An object with a string field that
// holds a secret is dropped whole (a hook's {type, command}, a
// marketplace's source); an array loses the elements that drop; an
// object loses the fields that drop. Only a string or an object can drop.
func scrubSecrets(v any, dropped *int) (any, bool) {
	switch t := v.(type) {
	case string:
		return t, secretIn(t)
	case []any:
		out := t[:0]
		for _, e := range t {
			ne, drop := scrubSecrets(e, dropped)
			if drop {
				if _, isStr := e.(string); isStr {
					*dropped++
				}
				continue
			}
			out = append(out, ne)
		}
		return out, false
	case map[string]any:
		for k, e := range t {
			if s, ok := e.(string); ok && (secretIn(s) || secretIn(k)) {
				*dropped++
				return nil, true
			}
		}
		for k, e := range t {
			ne, drop := scrubSecrets(e, dropped)
			if drop {
				delete(t, k)
				continue
			}
			t[k] = ne
		}
		return t, false
	}
	return v, false
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// markerName turns a relative path into a marker file name.
func markerName(rel string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(strings.ToLower(rel))
}

func statSmallFile(p string) (claudeFile, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return claudeFile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > claudeFileCap {
		return claudeFile{}, fmt.Errorf("%s: not a regular file under the cap", p)
	}
	return claudeFileOf(p, info), nil
}

// readClaudeDir reads the regular files under dir/d (symlinks and
// anything else are left behind: a link to a laptop path means nothing in
// the guest). over reports the directory past claudeDirCap.
func readClaudeDir(dir, d string) (files map[string]claudeFile, skipped []string, over bool, err error) {
	root := filepath.Join(dir, d)
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, nil, false, fmt.Errorf("no %s", d)
	}
	files = map[string]claudeFile{}
	var total int64
	err = filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			if de.Name() == ".git" || de.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !de.Type().IsRegular() {
			return nil
		}
		if claudeSecretFileName(de.Name()) {
			rel, _ := filepath.Rel(dir, p)
			skipped = append(skipped, filepath.ToSlash(rel))
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		if total > claudeDirCap {
			over = true
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, p)
		files[filepath.ToSlash(rel)] = claudeFileOf(p, info)
		return nil
	})
	sort.Strings(skipped)
	return files, skipped, over, err
}

// sshIdentityName is an SSH key pair's default file name (id_ed25519,
// id_rsa.pub, id_ecdsa_sk), not every id_ file (id_generator.py).
var sshIdentityName = regexp.MustCompile(`^id_(rsa|dsa|ecdsa|ed25519)(_sk)?(\.pub)?$`)

// claudeSecretFileName is a file name that holds a secret by its look:
// .env files, keys and certificates, anything named credentials, SSH
// identities. Never carried from skills/, agents/, commands/,
// output-styles/ or as a hook script, whatever else is beside them.
func claudeSecretFileName(name string) bool {
	n := strings.ToLower(name)
	switch {
	case n == ".env", strings.HasPrefix(n, ".env."), sshIdentityName.MatchString(n),
		strings.Contains(n, "credentials"),
		strings.HasSuffix(n, ".pem"), strings.HasSuffix(n, ".key"), strings.HasSuffix(n, ".p12"), strings.HasSuffix(n, ".pfx"):
		return true
	}
	return false
}

// claudeScripts finds the files under the laptop's ~/.claude that a hook
// or the statusLine runs, by the words of each command that name a path
// there (~/, $HOME/, ${HOME}/ or the home itself), and reads them.
func claudeScripts(s map[string]any, homeDir, dir string) map[string]claudeFile {
	out := map[string]claudeFile{}
	for _, cmd := range claudeCommands(s) {
		for _, w := range strings.Fields(cmd) {
			w = strings.Trim(w, `"'`)
			for _, pre := range []string{"~/", "$HOME/", "${HOME}/"} {
				if strings.HasPrefix(w, pre) {
					w = filepath.Join(homeDir, w[len(pre):])
				}
			}
			if !filepath.IsAbs(w) {
				continue
			}
			rel, err := filepath.Rel(dir, filepath.Clean(w))
			if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
				continue
			}
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if claudeNeverDirs[parts[0]] || claudeSecretFileName(filepath.Base(rel)) || rel == "settings.json" {
				continue
			}
			f, err := statSmallFile(filepath.Join(dir, rel))
			if err != nil {
				continue
			}
			out[filepath.ToSlash(rel)] = f
		}
	}
	return out
}

// claudeCommands lists every hook and statusLine command in a settings
// object, the same shapes claude_merge.jq walks.
func claudeCommands(s map[string]any) []string {
	var cmds []string
	if hooks, ok := s["hooks"].(map[string]any); ok {
		for _, groups := range hooks {
			gs, _ := groups.([]any)
			for _, g := range gs {
				gm, _ := g.(map[string]any)
				hs, _ := gm["hooks"].([]any)
				for _, h := range hs {
					hm, _ := h.(map[string]any)
					if c, ok := hm["command"].(string); ok {
						cmds = append(cmds, c)
					}
				}
			}
		}
	}
	if sl, ok := s["statusLine"].(map[string]any); ok {
		if c, ok := sl["command"].(string); ok {
			cmds = append(cmds, c)
		}
	}
	sort.Strings(cmds)
	return cmds
}

// claudePlugins reads enabledPlugins and extraKnownMarketplaces. Claude
// Code records only names there and does not reinstall on a fresh
// machine, so the guest installs what is missing. A marketplace that is
// a directory on the laptop cannot be reached from the guest; its
// plugins are named once instead.
func claudePlugins(s map[string]any, notes []string) (plugins []string, markets map[string]string, _ []string) {
	markets = map[string]string{}
	local := map[string]bool{}
	if km, ok := s["extraKnownMarketplaces"].(map[string]any); ok {
		for name, v := range km {
			vm, _ := v.(map[string]any)
			src, _ := vm["source"].(map[string]any)
			kind, _ := src["source"].(string)
			switch kind {
			case "github":
				if r, ok := src["repo"].(string); ok {
					markets[name] = r
				}
			case "git", "url":
				if u, ok := src["url"].(string); ok {
					markets[name] = u
				}
			default:
				local[name] = true
			}
		}
	}
	if ep, ok := s["enabledPlugins"].(map[string]any); ok {
		for id, on := range ep {
			if b, ok := on.(bool); !ok || !b {
				continue
			}
			_, m, ok := strings.Cut(id, "@")
			if !ok {
				continue
			}
			if local[m] {
				notes = append(notes, fmt.Sprintf("Plugin %s comes from a marketplace on your laptop, so the guest cannot install it.", id))
				continue
			}
			plugins = append(plugins, id)
		}
	}
	sort.Strings(plugins)
	return plugins, markets, notes
}

// addClaudeParts adds one part per carried item that changed, then the
// settings merge, then the plugin install. It returns the labels added.
func addClaudeParts(p *guestPayload, cc *claudeCarry, opts carryOptions) ([]string, error) {
	if cc == nil {
		return nil, nil
	}
	var sent []string
	for i, it := range cc.Items {
		hash := it.Hash
		if opts.unchanged(it.Marker, hash) {
			continue
		}
		base := fmt.Sprintf("claude/i%d", i)
		for _, rel := range sortedFileKeys(it.Files) {
			f := it.Files[rel]
			b, err := f.read()
			if err != nil {
				continue // gone since the listing; the next carry sees it
			}
			if err := p.fileMeta(base+"/"+rel, b, int64(f.Mode), unixEpoch); err != nil {
				return nil, err
			}
		}
		// Onto what the guest has: a skill made in the guest stays.
		script := fmt.Sprintf("mkdir -p ~/.claude\nif [ -d \"$1/%[1]s\" ]; then cp -R \"$1/%[1]s/.\" ~/.claude/; fi\n", base)
		if len(it.Skipped) > 0 {
			msg := "Not carried (the names look like credentials): ~/.claude/" + strings.Join(it.Skipped, ", ~/.claude/") + "."
			if err := p.file(base+".skipped", []byte(msg)); err != nil {
				return nil, err
			}
			script += fmt.Sprintf("printf '#warn %%s\\n' \"$(cat \"$1/%s.skipped\")\"\n", base)
		}
		script += setMarker(it.Marker, hash)
		if err := p.part("Claude "+strings.TrimPrefix(it.Marker, "claude-"), script); err != nil {
			return nil, err
		}
		sent = append(sent, it.Marker)
	}
	if cc.Settings != nil {
		hash := carryHash(cc.Settings, []byte(cc.Home), []byte(cc.CfgDir), claudeMergeJQ)
		if !opts.unchanged("claude-settings", hash) {
			for name, b := range map[string][]byte{"claude/settings.json": cc.Settings, "claude/home": []byte(cc.Home), "claude/cfg": []byte(cc.CfgDir), "claude/merge.jq": claudeMergeJQ} {
				if err := p.file(name, b); err != nil {
					return nil, err
				}
			}
			if err := p.part("Claude settings", claudeSettingsScript()+setMarker("claude-settings", hash)); err != nil {
				return nil, err
			}
			sent = append(sent, "claude-settings")
		}
	}
	if len(cc.Plugins) > 0 {
		list, _ := json.Marshal(map[string]any{"plugins": cc.Plugins, "marketplaces": cc.Marketplaces})
		hash := carryHash(list)
		if !opts.unchanged("claude-plugins", hash) {
			if err := p.file("claude/plugins.json", list); err != nil {
				return nil, err
			}
			if err := p.file("claude/plugins.sh", []byte(claudePluginsScript(hash))); err != nil {
				return nil, err
			}
			if err := p.part("Claude plugins", `mkdir -p ~/.repose
cp "$1/claude/plugins.json" ~/.repose/claude-plugins.json
cp "$1/claude/plugins.sh" ~/.repose/claude-plugins.sh
setsid -f sh ~/.repose/claude-plugins.sh </dev/null >/dev/null 2>&1
`); err != nil {
				return nil, err
			}
			sent = append(sent, "claude-plugins")
		}
	}
	return sent, nil
}

// claudeItemHash is the item's marker value: each file's path, mode and
// content hash. A file that cannot be read is left out of the item; ""
// means none could be.
func claudeItemHash(it claudeItem, hc *claudeHashes) string {
	var parts [][]byte
	for _, rel := range sortedFileKeys(it.Files) {
		f := it.Files[rel]
		sum, err := hc.sum(f)
		if err != nil {
			delete(it.Files, rel)
			continue
		}
		parts = append(parts, []byte(rel), []byte(fmt.Sprint(f.Mode)), []byte(sum))
	}
	for _, rel := range it.Skipped {
		parts = append(parts, []byte("skipped"), []byte(rel))
	}
	if len(parts) == 0 {
		return ""
	}
	return carryHash(parts...)
}

// claudeHashes caches each carried file's content hash by path, size and
// mtime in the CLI's carry-hashes.json (cli-config.md), so a run whose
// Claude config did not change stats its files and reads none. A missing
// or unreadable cache is an empty one.
type claudeHashes struct {
	path    string
	entries map[string]claudeHashEntry
	used    map[string]bool
	changed bool
}

type claudeHashEntry struct {
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime_ns"`
	Sum   string `json:"sha256"`
}

func loadClaudeHashes() *claudeHashes {
	hc := &claudeHashes{entries: map[string]claudeHashEntry{}, used: map[string]bool{}}
	dir, err := configDir()
	if err != nil {
		return hc
	}
	hc.path = filepath.Join(dir, "carry-hashes.json")
	if b, err := os.ReadFile(hc.path); err == nil {
		_ = json.Unmarshal(b, &hc.entries)
		if hc.entries == nil {
			hc.entries = map[string]claudeHashEntry{}
		}
	}
	return hc
}

func (hc *claudeHashes) sum(f claudeFile) (string, error) {
	hc.used[f.Path] = true
	if e, ok := hc.entries[f.Path]; ok && e.Size == f.Size && e.MTime == f.MTime {
		return e.Sum, nil
	}
	b, err := f.read()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	e := claudeHashEntry{Size: f.Size, MTime: f.MTime, Sum: hex.EncodeToString(h[:])}
	hc.entries[f.Path] = e
	hc.changed = true
	return e.Sum, nil
}

// save writes the entries this carry used, dropping the rest, when
// anything changed. Best effort: a failed write costs a re-read next time.
func (hc *claudeHashes) save() {
	for p := range hc.entries {
		if !hc.used[p] {
			delete(hc.entries, p)
			hc.changed = true
		}
	}
	if !hc.changed || hc.path == "" {
		return
	}
	b, err := json.Marshal(hc.entries)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(hc.path), 0o700); err != nil {
		return
	}
	// A temp file of its own: `run` and a session helper may save at once.
	f, err := os.CreateTemp(filepath.Dir(hc.path), "carry-hashes-*.tmp")
	if err != nil {
		return
	}
	_, werr := f.Write(b)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(f.Name())
		return
	}
	if err := os.Rename(f.Name(), hc.path); err != nil {
		_ = os.Remove(f.Name())
	}
}

func sortedFileKeys(m map[string]claudeFile) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// claudeSettingsScript is the guest half of the merge: rewrite the
// laptop's home, list the commands the result would run, drop the ones
// the guest cannot run (named once), merge, check the result with
// `jq empty`, keep the previous file as settings.json.repose-prev and
// rename the new one into place, then keep the hooks this laptop file
// added in ~/.repose/claude-laptop-hooks.json, which the next merge takes
// out of the guest's before the union (DECISIONS I-499). An invalid guest
// file is left alone.
func claudeSettingsScript() string {
	return `t=$1
c="$t/claude"
s="$HOME/.claude/settings.json"
mkdir -p "$HOME/.claude"
echo '{}' > "$t/empty.json"
g="$t/empty.json"
if [ -s "$s" ]; then
  if ! jq empty "$s" 2>/dev/null; then
    echo "#warn The guest's ~/.claude/settings.json is not valid JSON, so your laptop's settings were not merged into it."
    exit 0
  fi
  g="$s"
fi
pf=${REPOSE_CLAUDE_PLATFORM:-/etc/repose/claude-settings.json}
[ -r "$pf" ] || pf="$t/empty.json"
ph="$HOME/.repose/claude-laptop-hooks.json"
pv="$t/empty.json"
if [ -s "$ph" ] && jq empty "$ph" 2>/dev/null; then pv="$ph"; fi
: > "$t/missing"
q() {
  qm=$1 qh=$2 qg=$3 ql=$4
  shift 4
  jq -n "$@" --arg mode "$qm" --arg home "$qh" --arg cfg "$(cat "$c/cfg" 2>/dev/null || true)" --arg dest "$HOME" --slurpfile g "$qg" --slurpfile l "$ql" --slurpfile p "$pf" --slurpfile prev "$pv" --rawfile missing "$t/missing" -f "$c/merge.jq"
}
q rewrite "$(cat "$c/home")" "$g" "$c/settings.json" > "$t/laptop.json"
cmd_ok() {
  set -f
  first=1
  for w in $1; do
    w=${w#\"}; w=${w%\"}; w=${w#\'}; w=${w%\'}
    case $w in
      "~/"*) w="$HOME/${w#"~/"}" ;;
      '$HOME/'*) w="$HOME/${w#'$HOME/'}" ;;
      '${HOME}/'*) w="$HOME/${w#'${HOME}/'}" ;;
    esac
    case $w in *'$'*|*'` + "`" + `'*) first=; continue ;; esac
    if [ -n "$first" ]; then
      case $w in *=*) continue ;; esac
      first=
      case $w in
        /*) [ -x "$w" ] || { set +f; return 1; } ;;
        *) command -v "$w" >/dev/null 2>&1 || { set +f; return 1; } ;;
      esac
    else
      case $w in /*) [ -e "$w" ] || { set +f; return 1; } ;; esac
    fi
  done
  set +f
  return 0
}
q commands "" "$g" "$t/laptop.json" -r > "$t/commands"
while IFS= read -r b; do
  [ -n "$b" ] || continue
  cmd=$(printf '%s' "$b" | base64 -d)
  if ! cmd_ok "$cmd"; then
    printf '%s\n' "$b" >> "$t/missing"
    printf '#dropped Claude hook "%s"\n' "$(printf '%s' "$cmd" | tr '\n' ' ' | cut -c1-100)"
  fi
done < "$t/commands"
q merge "" "$g" "$t/laptop.json" > "$t/merged.json"
jq empty "$t/merged.json"
q laptophooks "" "$g" "$t/laptop.json" > "$t/laptop-hooks.json"
save_hooks() {
  cmp -s "$t/laptop-hooks.json" "$ph" && return 0
  mkdir -p "$HOME/.repose"
  cp "$t/laptop-hooks.json" "$ph.tmp"
  mv -f "$ph.tmp" "$ph"
}
if [ -f "$s" ] && cmp -s "$t/merged.json" "$s"; then save_hooks; exit 0; fi
cp "$t/merged.json" "$s.tmp"
chmod 600 "$s.tmp"
jq empty "$s.tmp"
if [ -f "$s" ]; then cp -p "$s" "$s.repose-prev"; fi
mv -f "$s.tmp" "$s"
save_hooks
`
}

// claudePluginsScript installs, in the background, the marketplace
// plugins the laptop has enabled and the guest lacks, with Claude Code's
// own commands (checked against 2.1.278, the base's version on
// 2026-09-23: `claude plugin list --json`, `claude plugin marketplace
// list --json`, `claude plugin marketplace add <source>`, `claude plugin
// install <name>@<marketplace>`). It says what happened in tmux (or
// herdr, I-509) and
// writes its marker only when every install worked, so a failure is
// tried again on the next carry.
func claudePluginsScript(hash string) string {
	return `f="$HOME/.repose/claude-plugins.json"
command -v claude >/dev/null 2>&1 || exit 0
installed=$(claude plugin list --json 2>/dev/null | jq -r '.[].id' 2>/dev/null || true)
known=$(claude plugin marketplace list --json 2>/dev/null | jq -r '.[].name' 2>/dev/null || true)
ok=""
bad=""
for id in $(jq -r '.plugins[]' "$f"); do
  if printf '%s\n' "$installed" | grep -qxF "$id"; then continue; fi
  m=${id#*@}
  if ! printf '%s\n' "$known" | grep -qxF "$m"; then
    src=$(jq -r --arg m "$m" '.marketplaces[$m] // empty' "$f")
    if [ -z "$src" ] && [ "$m" = claude-plugins-official ]; then src=anthropics/claude-plugins-official; fi
    if [ -n "$src" ] && claude plugin marketplace add "$src" >/dev/null 2>&1; then known="$known
$m"; fi
  fi
  if claude plugin install "$id" >/dev/null 2>&1; then ok="$ok $id"; else bad="$bad $id"; fi
done
msg=""
[ -n "$ok" ] && msg="Installed Claude plugins:$ok."
[ -n "$bad" ] && msg="$msg Could not install:$bad (claude plugin install in the guest says why)."
if [ -n "$msg" ] && tmux list-sessions >/dev/null 2>&1; then
  s=$(tmux list-sessions -F '#{session_name}' | head -n 1)
  tmux display-message -d 6000 -t "=$s:" "$msg" 2>/dev/null || true
elif [ -n "$msg" ] && [ -S ` + multiplexer.HerdrSocket + ` ]; then
  herdr notification show repose --body "$msg" >/dev/null 2>&1 || true
fi
if [ -z "$bad" ]; then
` + setMarker("claude-plugins", hash) + `fi
`
}
