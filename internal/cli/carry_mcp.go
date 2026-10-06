package cli

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The MCP part of the carry (DECISIONS I-556, amending I-196): the MCP
// servers the user added to Claude Code on the laptop, at user scope
// (`mcpServers` in ~/.claude.json) and at local scope for this checkout
// (`projects[<main worktree root>].mcpServers`). Nothing else in
// ~/.claude.json is read into the payload.
//
// Every literal credential is replaced by a `${NAME}` reference to a
// repose secret before anything is hashed or packed (I-211), so a value
// never leaves the laptop and a rotated token sends nothing. Servers that
// need the laptop (an Apple app, a program or files there, a server on
// its loopback or LAN) are left behind and named once. Paths under the
// repository become @@REPOSE_CHECKOUT@@, which the guest replaces with the
// checkout's real path.
//
// The guest keeps the list in ~/.repose/mcp/laptop.json (the schema in
// docs/interfaces/guest-conventions.md); `repose-mcp sync`, run at each
// agent start, renders it into every agent's own config. The carry never
// writes an agent file.

//go:embed mcp_carry.sh
var mcpCarryScript string

//go:embed mcp_laptop.jq
var mcpLaptopJQ []byte

// carryMCPVersion is folded into the MCP hashes: a change to what the
// part writes sends it to every guest once.
const carryMCPVersion = "mcp-1"

// mcpLogin is the row in `repose secrets choose` and logins.skip that
// turns the MCP carry off.
const mcpLogin = "mcp"

// The two markers: user scope, and this checkout's local scope.
const (
	mcpUserMarker    = "claude-mcp"
	mcpProjectMarker = "claude-mcp-project"
)

// mcpSkip is one server left on the laptop, as laptop.json lists it.
type mcpSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// mcpCarry is what the MCP part sends.
type mcpCarry struct {
	// User and Project are the carried servers, templated. Project is nil
	// when the run is not from a repository, which leaves the guest's
	// local-scope entries as they are.
	User    map[string]map[string]any
	Project map[string]map[string]any
	Skipped []mcpSkip
	// Templated names the secrets the carry made from a laptop value;
	// `repose secrets import --mcp` sets exactly these.
	Templated []string
	// Cmds is "SERVER BIN" for each bare command the guest checks.
	// It does not depend on the tools carry, so run and attach hash the
	// same list; ToolBins only filters what the guest is asked to check.
	Cmds []string
	// ToolBins are the commands the tools carry installs, which the
	// guest does not report missing. Not hashed.
	ToolBins map[string]bool `json:"-"`
	// Slug and Checkout find the checkout in the guest (checkoutVar).
	Slug, Checkout string
	// HashUser and HashProject are the two markers' values.
	HashUser, HashProject string
	// Approvals are the laptop's .mcp.json answers for this checkout,
	// which the agent window's trust write copies (I-486).
	Approvals mcpApprovals
}

// mcpApprovals is a project's enabledMcpjsonServers and
// disabledMcpjsonServers in the laptop's ~/.claude.json.
type mcpApprovals struct {
	Enabled  []string `json:"enabledMcpjsonServers,omitempty"`
	Disabled []string `json:"disabledMcpjsonServers,omitempty"`
}

func (a mcpApprovals) empty() bool { return len(a.Enabled) == 0 && len(a.Disabled) == 0 }

// claudeJSONPath is where the laptop's Claude Code keeps ~/.claude.json:
// inside $CLAUDE_CONFIG_DIR when set, else the home.
func claudeJSONPath(homeDir string) string {
	if d := os.Getenv(envClaudeConfigDir); d != "" {
		return filepath.Join(d, ".claude.json")
	}
	return filepath.Join(homeDir, ".claude.json")
}

// claudeJSONMCP is the part of ~/.claude.json the carry decodes; every
// other key is skipped by the decoder, never kept.
type claudeJSONMCP struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	// Projects stays raw: only this repository's entry is decoded.
	Projects map[string]json.RawMessage `json:"projects"`
}

// claudeJSONProject is one projects entry's MCP keys.
type claudeJSONProject struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	mcpApprovals
}

// claudeProjectKeys is the keys Claude Code may have filed this
// repository's local scope under: the main worktree's root (a linked
// worktree and a subdirectory share it), as git spells it and with its
// symlinks resolved.
func claudeProjectKeys(repoRoot string) []string {
	if repoRoot == "" {
		return nil
	}
	main := repoRoot
	if common, err := gitCmd(repoRoot, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil && filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	keys := []string{main}
	if r, err := filepath.EvalSymlinks(main); err == nil && r != main {
		keys = append(keys, r)
	}
	if repoRoot != main {
		keys = append(keys, repoRoot)
	}
	return keys
}

// readClaudeMCP reads the user-scope servers and, when repoRoot is set,
// this repository's local-scope servers and .mcp.json answers. project is
// nil when repoRoot is "".
func readClaudeMCP(homeDir, repoRoot string) (user, project map[string]json.RawMessage, appr mcpApprovals, err error) {
	if repoRoot != "" {
		project = map[string]json.RawMessage{}
	}
	b, err := os.ReadFile(claudeJSONPath(homeDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, project, appr, nil
		}
		return nil, project, appr, err
	}
	keys := claudeProjectKeys(repoRoot)
	var cj claudeJSONMCP
	top, err := jsonFields(b, func(k string) bool { return k == "mcpServers" || k == "projects" })
	if err != nil {
		return nil, project, appr, fmt.Errorf("~/.claude.json is not valid JSON")
	}
	if raw := top["mcpServers"]; raw != nil && json.Unmarshal(raw, &cj.MCPServers) != nil {
		cj.MCPServers = nil
	}
	if raw := top["projects"]; raw != nil {
		cj.Projects, _ = jsonFields(raw, func(k string) bool { return slices.Contains(keys, k) })
	}
	for _, k := range keys {
		if raw, ok := cj.Projects[k]; ok {
			var p claudeJSONProject
			if json.Unmarshal(raw, &p) != nil {
				break
			}
			if p.MCPServers != nil {
				project = p.MCPServers
			}
			appr = p.mcpApprovals
			break
		}
	}
	return cj.MCPServers, project, appr, nil
}

// jsonFields returns the raw values of an object's members whose keys
// want accepts, skipping every other value with a scanner that only
// matches brackets and strings. ~/.claude.json grows to megabytes of
// per-project history, and a full decode of it would cost a run tens of
// milliseconds for the few keys the carry reads. The values it returns
// are checked by the decode that follows.
func jsonFields(b []byte, want func(string) bool) (map[string]json.RawMessage, error) {
	bad := fmt.Errorf("not a JSON object")
	i := skipSpace(b, 0)
	if i >= len(b) || b[i] != '{' {
		return nil, bad
	}
	out := map[string]json.RawMessage{}
	i = skipSpace(b, i+1)
	if i < len(b) && b[i] == '}' {
		return out, nil
	}
	for {
		i = skipSpace(b, i)
		if i >= len(b) || b[i] != '"' {
			return nil, bad
		}
		end, err := skipString(b, i)
		if err != nil {
			return nil, err
		}
		var key string
		if err := json.Unmarshal(b[i:end], &key); err != nil {
			return nil, err
		}
		i = skipSpace(b, end)
		if i >= len(b) || b[i] != ':' {
			return nil, bad
		}
		i = skipSpace(b, i+1)
		vend, err := skipValue(b, i)
		if err != nil {
			return nil, err
		}
		if want(key) {
			out[key] = json.RawMessage(b[i:vend])
		}
		i = skipSpace(b, vend)
		if i >= len(b) {
			return nil, bad
		}
		if b[i] == '}' {
			return out, nil
		}
		if b[i] != ',' {
			return nil, bad
		}
		i++
	}
}

func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

// skipString returns the index after the string that starts at b[i].
func skipString(b []byte, i int) (int, error) {
	for j := i + 1; j < len(b); j++ {
		switch b[j] {
		case '\\':
			j++
		case '"':
			return j + 1, nil
		}
	}
	return 0, fmt.Errorf("unterminated string")
}

// skipValue returns the index after the value that starts at b[i].
func skipValue(b []byte, i int) (int, error) {
	if i >= len(b) {
		return 0, fmt.Errorf("missing value")
	}
	switch b[i] {
	case '"':
		return skipString(b, i)
	case '{', '[':
		depth := 0
		for j := i; j < len(b); j++ {
			switch b[j] {
			case '"':
				end, err := skipString(b, j)
				if err != nil {
					return 0, err
				}
				j = end - 1
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return j + 1, nil
				}
			}
		}
		return 0, fmt.Errorf("unbalanced brackets")
	}
	j := i
	for j < len(b) && b[j] != ',' && b[j] != '}' && b[j] != ']' && b[j] != ' ' && b[j] != '\n' && b[j] != '\t' && b[j] != '\r' {
		j++
	}
	if j == i {
		return 0, fmt.Errorf("missing value")
	}
	return j, nil
}

// buildMCPCarry reads and classifies the laptop's servers. repoRoot is ""
// outside a repository; checkout is the extra checkout's name (I-480) or
// "" for the machine's own; toolBins are the commands the tools carry
// installs, which the guest does not report missing. It returns nil only
// when there is nothing to say: no ~/.claude.json and no repository.
func buildMCPCarry(homeDir, repoRoot, slug, checkout string, toolBins map[string]bool) (*mcpCarry, []string) {
	mc, notes := collectMCP(homeDir, repoRoot, toolBins, nil)
	if mc == nil {
		return nil, notes
	}
	mc.Slug, mc.Checkout = slug, checkout
	mc.hash()
	return mc, notes
}

// collectMCP is buildMCPCarry without the guest's coordinates. values,
// when not nil, receives each templated secret's laptop value: only
// `repose secrets import --mcp` passes one, and the carry never does.
func collectMCP(homeDir, repoRoot string, toolBins map[string]bool, values map[string]string) (*mcpCarry, []string) {
	user, project, appr, err := readClaudeMCP(homeDir, repoRoot)
	if err != nil {
		return nil, []string{"Could not read your Claude Code MCP servers (" + err.Error() + "); the machine keeps the ones it has."}
	}
	if user == nil && project == nil {
		return nil, nil
	}
	c := newMCPClassifier(homeDir, claudeProjectKeys(repoRoot))
	mc := &mcpCarry{User: map[string]map[string]any{}, Approvals: appr, ToolBins: toolBins}
	mc.User = c.scope(user, &mc.Skipped, &mc.Cmds)
	if project != nil {
		mc.Project = c.scope(project, &mc.Skipped, &mc.Cmds)
	}
	for n := range c.values {
		mc.Templated = append(mc.Templated, n)
	}
	sort.Strings(mc.Templated)
	if values != nil {
		for n, v := range c.values {
			values[n] = v
		}
	}
	return mc, nil
}

// off is the carry with nothing in it: `logins.skip = ["mcp"]`. It still
// travels, so the guest empties what an earlier run put there.
func (mc *mcpCarry) off() *mcpCarry {
	o := &mcpCarry{User: map[string]map[string]any{}, Slug: mc.Slug, Checkout: mc.Checkout}
	if mc.Project != nil {
		o.Project = map[string]map[string]any{}
	}
	o.hash()
	return o
}

// hash sets the two markers' values from the templated list only: no
// credential is in it, so a rotated laptop token changes nothing.
func (mc *mcpCarry) hash() {
	u, _ := json.Marshal(mc.User)
	sk, _ := json.Marshal(mc.Skipped)
	t, _ := json.Marshal(mc.Templated)
	mc.HashUser = carryHash([]byte(carryMCPVersion), u, sk, t, []byte(strings.Join(mc.Cmds, "\n")), []byte(mcpCarryScript), mcpLaptopJQ)
	p, _ := json.Marshal(mc.Project)
	mc.HashProject = carryHash([]byte(carryMCPVersion), p, []byte(mc.Slug), []byte(mc.Checkout))
}

// mcpUnchanged is opts.unchanged for an MCP marker, which the guest
// suffixes with ":old" on a base without repose-mcp (it printed the base
// line once; the list is there for the base that reads it).
func (o carryOptions) mcpUnchanged(item, hash string) bool {
	return o.Markers != nil && (o.Markers[item] == hash || o.Markers[item] == hash+":old")
}

// addMCPParts adds the MCP part when either marker changed.
func addMCPParts(p *guestPayload, mc *mcpCarry, opts carryOptions) ([]string, error) {
	if mc == nil {
		return nil, nil
	}
	if opts.mcpUnchanged(mcpUserMarker, mc.HashUser) && opts.mcpUnchanged(mcpProjectMarker, mc.HashProject) {
		return nil, nil
	}
	in := map[string]any{"user": mc.User, "project": mc.Project, "skipped": mc.Skipped}
	if mc.Skipped == nil {
		in["skipped"] = []mcpSkip{}
	}
	b, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var cmds []string
	for _, l := range mc.Cmds {
		if _, bin, _ := strings.Cut(l, " "); !mc.ToolBins[bin] {
			cmds = append(cmds, l)
		}
	}
	var left []string
	for _, s := range mc.Skipped {
		left = append(left, printable(s.Name+" ("+s.Reason+")"))
	}
	files := map[string][]byte{
		"mcp/in.json":   b,
		"mcp/laptop.jq": mcpLaptopJQ,
		"mcp/templated": []byte(strings.Join(mc.Templated, "\n") + "\n"),
		"mcp/cmds":      []byte(strings.Join(cmds, "\n") + "\n"),
		"mcp/left":      []byte(strings.Join(left, "\n") + "\n"),
		"mcp/hash-user": []byte(mc.HashUser),
		"mcp/hash-proj": []byte(mc.HashProject),
	}
	for _, name := range []string{"mcp/in.json", "mcp/laptop.jq", "mcp/templated", "mcp/cmds", "mcp/left", "mcp/hash-user", "mcp/hash-proj"} {
		if err := p.file(name, files[name]); err != nil {
			return nil, err
		}
	}
	if err := p.part("MCP servers", checkoutVar(mc.Slug, mc.Checkout)+mcpCarryScript); err != nil {
		return nil, err
	}
	return []string{mcpUserMarker}, nil
}

// printable drops control characters from a name the classifier refused,
// so it cannot break the line it is named on.
func printable(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// The classifier (design 3.2, critic item 6).

var (
	mcpServerName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	// mcpSecretWord is an env key that names a credential.
	mcpSecretWord = regexp.MustCompile(`(?i)key|token|secret|passw|auth|credential|cookie|session|private`)
	// mcpLongRun is a 20+ character run that reads like a token.
	mcpLongRun = regexp.MustCompile(`[A-Za-z0-9_-]{20,}`)
	// mcpAuthScheme is an Authorization value with a literal credential.
	mcpAuthScheme = regexp.MustCompile(`(?i)^(bearer|basic|token)\s+(\S+)$`)
	// mcpQuerySecret is a query parameter whose name says it holds one.
	mcpQuerySecret = regexp.MustCompile(`(?i)key|token|auth|secret|passw|sig`)
	// mcpTokenShape is a whole argument, path segment or query value that
	// reads like a token: 20 or more token characters with a letter and a
	// digit among them (checked apart), or a provider's key prefix.
	mcpTokenShape  = regexp.MustCompile(`^[A-Za-z0-9_+=~-]{20,}$`)
	mcpTokenPrefix = regexp.MustCompile(`^(sk-|sk_|pk_|rk_|AIza|ntn_|secret_|lin_api_|xai-|gsk_|hf_|r8_|dop_v1_|npm_|pypi-|shpat_|SG\.)[A-Za-z0-9_.+=-]{8,}$`)
	// mcpHeaderFlags take an HTTP header, "Name: value", as their value
	// (mcp-remote, supergateway, curl-style servers).
	mcpHeaderFlags = setOf("--header", "-H", "--headers")
	// mcpPlainHeaders hold no credential; every other header value is
	// templated, since a server's headers are where its key goes.
	mcpPlainHeaders = setOf("accept", "accept-encoding", "accept-language", "content-type", "user-agent", "mcp-protocol-version", "cache-control")
)

// mcpTokenLike is a value that reads like a credential on its own.
func mcpTokenLike(v string) bool {
	if mcpTokenPrefix.MatchString(v) || secretIn(v) {
		return true
	}
	return mcpTokenShape.MatchString(v) && strings.ContainsAny(v, "0123456789") && strings.IndexFunc(v, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') }) >= 0
}

// mcpSecretFlag is a flag whose value is a credential: one with token,
// secret, password or apikey in its name, or that ends in key, auth or
// pass. --auth-type, --session-name and --keyboard take plain values.
func mcpSecretFlag(f string) bool {
	f = strings.ToLower(strings.TrimLeft(f, "-"))
	for _, w := range []string{"token", "secret", "passw", "apikey", "api-key", "api_key", "credential", "cookie", "bearer"} {
		if strings.Contains(f, w) {
			return true
		}
	}
	words := strings.FieldsFunc(f, func(r rune) bool { return r == '-' || r == '_' || r == '.' })
	if len(words) == 0 {
		return false
	}
	last := words[len(words)-1]
	return strings.HasSuffix(last, "key") || last == "auth" || last == "pass" || last == "pat" || last == "pwd"
}

// mcpPlatformNames are the machine's own servers (I-246): a laptop entry
// by that name or for that package is dropped without a word, since the
// machine's is wired to the shared browser.
var (
	mcpPlatformNames    = setOf("playwright", "chrome-devtools")
	mcpPlatformPackages = setOf("@playwright/mcp", "playwright-mcp", "chrome-devtools-mcp")
	mcpAppleWords       = []string{"applescript", "apple-", "imessage", "xcode", "iterm", "macos", "osascript", "shortcuts"}
	mcpLaunchers        = setOf("npx", "node", "uvx", "uv", "python3", "python", "docker", "bunx", "bun", "deno", "pipx")
	mcpLaptopRoots      = []string{"/Users/", "/Applications/", "/Library/", "/opt/homebrew/", "/usr/local/", "/Volumes/", "/private/", "/var/folders/"}
)

// mcpAgentSecret is a name an agent on the machine reads for its own
// login or that the machine's shell uses: a secret by that name would
// change the agent's account or break the shell, so a server's value is
// filed under its own prefix instead (critic item 6).
func mcpAgentSecret(n string) bool {
	switch n {
	case "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS",
		"GITHUB_TOKEN", "GH_TOKEN", "VERCEL_TOKEN":
		return true
	}
	for _, p := range []string{"ANTHROPIC_", "CLAUDE_CODE_", "CODEX_", "OPENCODE_", "REPOSE"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return shellSecretNames[n] || reservedSecretNames[n]
}

// mcpSecretName is a secret name for server's suffix: upper case, every
// other character _, MCP_ in front when it would not start with a
// letter, at most 64 (secrets.md).
func mcpSecretName(server, suffix string) string {
	s := strings.ToUpper(server)
	if suffix != "" {
		s += "_" + strings.ToUpper(suffix)
	}
	s = strings.Map(func(r rune) rune {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, s)
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		s = "MCP_" + s
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

type mcpClassifier struct {
	home   string
	repos  []string          // the repository's roots, rewritten to the checkout
	values map[string]string // secret -> laptop value; memory only
}

func newMCPClassifier(home string, repos []string) *mcpClassifier {
	return &mcpClassifier{home: home, repos: repos, values: map[string]string{}}
}

// scope classifies one scope's servers in name order, appending skips
// and command checks.
func (c *mcpClassifier) scope(raw map[string]json.RawMessage, skipped *[]mcpSkip, cmds *[]string) map[string]map[string]any {
	out := map[string]map[string]any{}
	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		var s map[string]any
		if err := json.Unmarshal(raw[n], &s); err != nil || s == nil {
			*skipped = append(*skipped, mcpSkip{n, "not a server entry"})
			continue
		}
		v := c.classify(n, s)
		switch {
		case v.drop:
		case v.skip != "":
			*skipped = append(*skipped, mcpSkip{n, v.skip})
		default:
			out[n] = v.server
			if v.cmd != "" {
				*cmds = append(*cmds, n+" "+v.cmd)
			}
		}
	}
	return out
}

// mcpVerdict is one server's classification.
type mcpVerdict struct {
	server map[string]any
	skip   string
	drop   bool
	cmd    string
}

// classifyMCPServer is the classifier on its own, for tests: the verdict
// and the secrets it templated with their laptop values.
func classifyMCPServer(home, repo, name string, s map[string]any) (mcpVerdict, map[string]string) {
	c := newMCPClassifier(home, []string{repo})
	v := c.classify(name, s)
	return v, c.values
}

func (c *mcpClassifier) classify(name string, s map[string]any) mcpVerdict {
	if !mcpServerName.MatchString(name) {
		return mcpVerdict{skip: "name"}
	}
	kind, _ := s["type"].(string)
	command, _ := s["command"].(string)
	rawURL, _ := s["url"].(string)
	if kind == "" {
		kind = "stdio"
		if command == "" && rawURL != "" {
			kind = "http"
		}
	}
	switch kind {
	case "stdio", "http", "sse", "ws":
	default:
		return mcpVerdict{skip: "type"}
	}
	args := stringList(s["args"])
	if mcpPlatformNames[name] || (kind == "stdio" && mcpIsPlatform(command, args)) {
		return mcpVerdict{drop: true}
	}
	if kind == "stdio" {
		hay := strings.ToLower(cmdBase(command) + " " + strings.Join(args, " "))
		for _, w := range mcpAppleWords {
			if strings.Contains(hay, w) {
				return mcpVerdict{skip: "an Apple app"}
			}
		}
	} else if mcpLaptopHost(rawURL) {
		return mcpVerdict{skip: "runs on your laptop"}
	}
	if _, ok := s["headersHelper"]; ok {
		return mcpVerdict{skip: "gets its headers from a laptop command"}
	}
	oauth, _ := s["oauth"].(map[string]any)
	if _, ok := oauth["clientSecretHelper"]; ok {
		return mcpVerdict{skip: "gets its OAuth secret from a laptop command"}
	}
	env, _ := s["env"].(map[string]any)
	if kind == "stdio" {
		if command == "" {
			return mcpVerdict{skip: "no command"}
		}
		if !c.underRepo(command) && c.laptopPath(command) && !mcpLaunchers[cmdBase(command)] {
			return mcpVerdict{skip: "a program on your laptop"}
		}
		for _, a := range args {
			if p := argPath(a); p != "" && !c.underRepo(p) && c.laptopPath(p) {
				return mcpVerdict{skip: "files on your laptop"}
			}
		}
		for _, k := range sortedKeys(env) {
			if v, ok := env[k].(string); ok && !c.underRepo(v) && c.laptopPath(v) {
				return mcpVerdict{skip: "reads " + printable(k) + " from your laptop"}
			}
		}
	}

	// Carried: copy and template.
	out := map[string]any{}
	for k, v := range s {
		out[k] = v
	}
	var cmd string
	if kind == "stdio" {
		switch {
		case c.underRepo(command):
			out["command"] = c.repoRewrite(command)
		case strings.ContainsAny(command, "/\\") && mcpLaunchers[cmdBase(command)]:
			out["command"] = cmdBase(command)
		case !strings.ContainsAny(command, "/\\"):
			cmd = c.checkCmd(command)
		}
		if _, ok := s["args"]; ok {
			out["args"] = c.templateArgs(name, args)
		}
		if env != nil {
			ne := map[string]any{}
			for _, k := range sortedKeys(env) {
				v, ok := env[k].(string)
				if !ok {
					ne[k] = env[k]
					continue
				}
				ne[k] = c.templateEnv(name, k, v)
			}
			out["env"] = ne
		}
	} else {
		out["url"] = c.templateURL(name, rawURL)
		if h, ok := s["headers"].(map[string]any); ok {
			nh := map[string]any{}
			for _, k := range sortedKeys(h) {
				v, ok := h[k].(string)
				if !ok {
					nh[k] = h[k]
					continue
				}
				nh[k] = c.templateHeader(name, k, v)
			}
			out["headers"] = nh
		}
	}
	if oauth != nil {
		no := map[string]any{}
		for k, v := range oauth {
			no[k] = v
		}
		if v, ok := oauth["clientSecret"].(string); ok && !strings.Contains(v, "${") {
			no["clientSecret"] = "${" + c.assign(name, "CLIENT_SECRET", "", v) + "}"
		}
		out["oauth"] = no
	}
	return mcpVerdict{server: out, cmd: cmd}
}

// checkCmd is bin when the guest should check it: not one every machine
// has, and a safe word. addMCPParts leaves out the tools carry's bins.
func (c *mcpClassifier) checkCmd(bin string) string {
	if baseCommands[bin] || mcpLaunchers[bin] || !safeBin.MatchString(bin) {
		return ""
	}
	return bin
}

func mcpIsPlatform(command string, args []string) bool {
	if mcpPlatformPackages[cmdBase(command)] {
		return true
	}
	for _, a := range args {
		if mcpPlatformPackages[packageName(a)] {
			return true
		}
	}
	return false
}

// cmdBase is a command's file name on either system, without a Windows
// .exe or .cmd: "C:\\nvm\\npx.cmd" and "/Users/me/.nvm/.../npx" are "npx".
func cmdBase(command string) string {
	if i := strings.LastIndexAny(command, "/\\"); i >= 0 {
		command = command[i+1:]
	}
	for _, ext := range []string{".exe", ".cmd", ".EXE", ".CMD"} {
		command = strings.TrimSuffix(command, ext)
	}
	return command
}

// packageName is an npm spec without its version: "@scope/name@1" is
// "@scope/name", "name@latest" is "name".
func packageName(a string) string {
	if strings.HasPrefix(a, "@") {
		if i := strings.Index(a[1:], "@"); i >= 0 {
			return a[:i+1]
		}
		return a
	}
	if i := strings.Index(a, "@"); i > 0 {
		return a[:i]
	}
	return a
}

// mcpLaptopHost is a URL whose host is the laptop or its network:
// localhost, *.local, a loopback, private, link-local or CGNAT (Tailscale)
// address, or a tailnet name.
func mcpLaptopHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h == "localhost" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".ts.net") {
		return true
	}
	ip := net.ParseIP(h)
	if ip == nil {
		return false
	}
	_, cgnat, _ := net.ParseCIDR("100.64.0.0/10")
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || cgnat.Contains(ip)
}

// expandHome turns ~/, $HOME/ and ${HOME}/ into the laptop's home.
func (c *mcpClassifier) expandHome(p string) string {
	for _, pre := range []string{"~/", "$HOME/", "${HOME}/"} {
		if strings.HasPrefix(p, pre) {
			return filepath.Join(c.home, p[len(pre):])
		}
	}
	if p == "~" || p == "$HOME" || p == "${HOME}" {
		return c.home
	}
	return p
}

// laptopPath is a path that only exists on the laptop: under its home,
// a macOS or Homebrew location, or a Windows drive.
func (c *mcpClassifier) laptopPath(p string) bool {
	p = c.expandHome(p)
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') && ((p[0] >= 'A' && p[0] <= 'Z') || (p[0] >= 'a' && p[0] <= 'z')) {
		return true
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if c.home != "" && (p == c.home || strings.HasPrefix(p, strings.TrimSuffix(c.home, "/")+"/")) {
		return true
	}
	for _, r := range mcpLaptopRoots {
		if strings.HasPrefix(p, r) {
			return true
		}
	}
	return false
}

func (c *mcpClassifier) underRepo(p string) bool {
	p = c.expandHome(p)
	for _, r := range c.repos {
		if r != "" && (p == r || strings.HasPrefix(p, r+"/") || strings.Contains(p, "="+r) || strings.Contains(p, ":"+r) || strings.HasPrefix(p, r+":")) {
			return true
		}
	}
	return false
}

// repoRewrite replaces every repository root in s with the placeholder
// the guest fills in.
func (c *mcpClassifier) repoRewrite(s string) string {
	s2 := c.expandHome(s)
	// Longest root first, so a nested worktree is not half replaced.
	roots := append([]string(nil), c.repos...)
	sort.Slice(roots, func(i, j int) bool { return len(roots[i]) > len(roots[j]) })
	for _, r := range roots {
		if r == "" {
			continue
		}
		s2 = replaceRoot(s2, r)
	}
	return s2
}

// replaceRoot replaces root where it is a whole path prefix in s: at the
// start, after = or :, and followed by the end, / or :.
func replaceRoot(s, root string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], root) && (i == 0 || s[i-1] == '=' || s[i-1] == ':') {
			end := i + len(root)
			if end == len(s) || s[end] == '/' || s[end] == ':' {
				b.WriteString("@@REPOSE_CHECKOUT@@")
				i = end
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// argPath is the path an argument names, if any: the whole argument,
// the value of --flag=VALUE, or the host side of a docker -v HOST:GUEST.
func argPath(a string) string {
	if strings.HasPrefix(a, "-") {
		_, v, ok := strings.Cut(a, "=")
		if !ok {
			return ""
		}
		a = v
	}
	if !(strings.HasPrefix(a, "/") || strings.HasPrefix(a, "~") || strings.HasPrefix(a, "$HOME") || strings.HasPrefix(a, "${HOME}") || (len(a) >= 3 && a[1] == ':' && (a[2] == '\\' || a[2] == '/'))) {
		return ""
	}
	if i := strings.Index(a, ":/"); i > 1 {
		a = a[:i]
	}
	return a
}

// assign picks the secret name for a laptop value: want when it is free
// or already holds the same value, else the server's own prefix, then a
// number. Values are compared in memory and kept only there.
func (c *mcpClassifier) assign(server, suffix, want, value string) string {
	if want == "" || !secretNameRe.MatchString(want) || mcpAgentSecret(want) {
		want = mcpSecretName(server, suffix)
	}
	cands := []string{want, mcpSecretName(server, suffix)}
	for _, n := range cands {
		if old, ok := c.values[n]; !ok || old == value {
			c.values[n] = value
			return n
		}
	}
	// The number goes after the cut to 64 characters, so a long base
	// still gives a new name each time.
	base := mcpSecretName(server, suffix)
	for i := 2; ; i++ {
		tail := fmt.Sprintf("_%d", i)
		n := base
		if len(n) > 64-len(tail) {
			n = n[:64-len(tail)]
		}
		n += tail
		if old, ok := c.values[n]; !ok || old == value {
			c.values[n] = value
			return n
		}
	}
}

// templateURL replaces a password in the user info, user info that looks
// like a token, and the value of a query parameter whose name says it is
// a credential.
func (c *mcpClassifier) templateURL(server, raw string) string {
	if raw == "" {
		return raw
	}
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return raw
	}
	auth, path := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		auth, path = rest[:i], rest[i:]
	}
	if at := strings.LastIndex(auth, "@"); at >= 0 {
		ui, host := auth[:at], auth[at+1:]
		if u, p, hasPass := strings.Cut(ui, ":"); hasPass && p != "" && !strings.Contains(p, "${") {
			ui = u + ":${" + c.assign(server, "PASSWORD", "", p) + "}"
		} else if !hasPass && !strings.Contains(ui, "${") && (mcpLongRun.MatchString(ui) || secretIn(ui)) {
			ui = "${" + c.assign(server, "TOKEN", "", ui) + "}"
		}
		auth = ui + "@" + host
	}
	p, tail := path, ""
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		p, tail = path[:i], path[i:]
	}
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		if seg != "" && !strings.Contains(seg, "${") && mcpTokenLike(seg) {
			segs[i] = "${" + c.assign(server, "TOKEN", "", seg) + "}"
		}
	}
	path = strings.Join(segs, "/") + tail
	if q := strings.Index(path, "?"); q >= 0 {
		query, frag := path[q+1:], ""
		if h := strings.Index(query, "#"); h >= 0 {
			query, frag = query[:h], query[h:]
		}
		parts := strings.Split(query, "&")
		for i, kv := range parts {
			k, v, ok := strings.Cut(kv, "=")
			if !ok || v == "" || strings.Contains(v, "${") || !(mcpQuerySecret.MatchString(k) || mcpTokenLike(v)) {
				continue
			}
			parts[i] = k + "=${" + c.assign(server, k, "", v) + "}"
		}
		path = path[:q+1] + strings.Join(parts, "&") + frag
	}
	return scheme + "://" + auth + path
}

func (c *mcpClassifier) templateHeader(server, k, v string) string {
	if strings.Contains(v, "${") {
		return v
	}
	if strings.EqualFold(k, "Authorization") {
		if m := mcpAuthScheme.FindStringSubmatch(v); m != nil {
			return m[1] + " ${" + c.assign(server, "TOKEN", "", m[2]) + "}"
		}
	}
	if v == "" || (mcpPlainHeaders[strings.ToLower(k)] && !secretIn(v) && !mcpLongRun.MatchString(v)) {
		return v
	}
	return "${" + c.assign(server, k, "", v) + "}"
}

// templateHeaderArg templates "Name: value", a header flag's value.
func (c *mcpClassifier) templateHeaderArg(server, a string) string {
	k, v, ok := strings.Cut(a, ":")
	if !ok {
		return a
	}
	k = strings.TrimSpace(k)
	v = strings.TrimSpace(v)
	if k == "" || strings.ContainsAny(k, " \t") {
		return a
	}
	return k + ": " + c.templateHeader(server, k, v)
}

// templateArgs replaces --flag=VALUE and --flag VALUE when the flag names a
// credential, the value of a header flag (--header "X-Key: VALUE"), an
// argument that reads like a token, and the credential parts of a URL;
// paths under the repository become the checkout's placeholder.
func (c *mcpClassifier) templateArgs(server string, args []string) []any {
	out := make([]any, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.Contains(a, "${"):
		case strings.HasPrefix(a, "-") && strings.Contains(a, "="):
			f, v, _ := strings.Cut(a, "=")
			switch {
			case v == "":
			case mcpHeaderFlags[f]:
				a = f + "=" + c.templateHeaderArg(server, v)
			case mcpSecretFlag(f) || mcpTokenLike(v):
				a = f + "=${" + c.assign(server, strings.TrimLeft(f, "-"), "", v) + "}"
			case strings.Contains(v, "://"):
				a = f + "=" + c.templateURL(server, v)
			}
		case mcpHeaderFlags[a] && i+1 < len(args) && !strings.Contains(args[i+1], "${"):
			out = append(out, a)
			i++
			a = c.templateHeaderArg(server, args[i])
		case strings.HasPrefix(a, "-") && mcpSecretFlag(a) && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") && !strings.Contains(args[i+1], "${"):
			out = append(out, a)
			i++
			a = "${" + c.assign(server, strings.TrimLeft(a, "-"), "", args[i]) + "}"
		case strings.Contains(a, "://"):
			a = c.templateURL(server, a)
			if secretIn(a) {
				a = "${" + c.assign(server, "TOKEN", "", args[i]) + "}"
			}
		case mcpTokenLike(a):
			a = "${" + c.assign(server, "TOKEN", "", a) + "}"
		}
		if c.underRepo(a) {
			a = c.repoRewrite(a)
		}
		out = append(out, a)
	}
	return out
}

// templateEnv makes a credential-looking value `${KEY}` (or the server's
// own name for it), rewrites a repository path, and keeps the rest.
func (c *mcpClassifier) templateEnv(server, k, v string) string {
	switch {
	case strings.Contains(v, "${"):
		return v
	case c.underRepo(v):
		return c.repoRewrite(v)
	case mcpSecretWord.MatchString(k) || mcpTokenLike(v) || mcpLongRun.MatchString(v) || urlHasCredential(v):
		return "${" + c.assign(server, k, k, v) + "}"
	}
	return v
}

// urlHasCredential is a URL with user info or a credential-named query
// parameter (DATABASE_URL=postgres://u:p@host is short enough to miss the
// 20-character rule).
func urlHasCredential(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	if _, ok := u.User.Password(); ok {
		return true
	}
	for k := range u.Query() {
		if mcpQuerySecret.MatchString(k) {
			return true
		}
	}
	return false
}

func stringList(v any) []string {
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, e := range l {
		if s, ok := e.(string); ok {
			out = append(out, s)
		} else {
			out = append(out, fmt.Sprint(e))
		}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// toolBinsOf is the commands the tools carry installs.
func toolBinsOf(tc *toolsCarry) map[string]bool {
	bins := map[string]bool{}
	if tc == nil {
		return bins
	}
	for _, it := range tc.Wanted.Items {
		for _, b := range it.Bins {
			bins[b] = true
		}
	}
	return bins
}

// mcpOutcomeLines are the MCP part's lines in carryOutcome.Lines.
func (o *carryOutcome) mcpLines() []string {
	var out []string
	if len(o.MCPLeft) > 0 {
		what := "MCP server "
		if len(o.MCPLeft) > 1 {
			what = "MCP servers "
		}
		out = append(out, "Left on your laptop: "+what+strings.Join(o.MCPLeft, ", ")+". repose mcp forward NAME runs one from here.")
	}
	if n := len(o.MCPSecrets); n > 0 {
		var all, fromLaptop []string
		for _, s := range o.MCPSecrets {
			all = append(all, s.Name+" ("+strings.Join(s.Servers, ", ")+")")
			if s.FromLaptop {
				fromLaptop = append(fromLaptop, s.Name)
			}
		}
		l := "MCP servers need secrets the machine lacks: " + strings.Join(all, ", ") + "."
		if n == 1 {
			l = "An MCP server needs a secret the machine lacks: " + all[0] + "."
		}
		switch {
		case len(fromLaptop) == n && n == 1:
			l += " Set it from your laptop's value with repose secrets import --mcp."
		case len(fromLaptop) == n:
			l += " Set them from your laptop's values with repose secrets import --mcp."
		case len(fromLaptop) == 0:
			l += " Set each with repose secrets set NAME."
		default:
			l += " Set " + strings.Join(fromLaptop, ", ") + " from your laptop's values with repose secrets import --mcp, and the rest with repose secrets set NAME."
		}
		out = append(out, l)
	}
	for _, m := range o.MCPMissing {
		out = append(out, "MCP server "+m[0]+" needs "+m[1]+", which the machine lacks.")
	}
	if o.MCPOld {
		out = append(out, "This machine's base predates MCP servers from your laptop; they arrive after its next update.")
	}
	return out
}

// mcpNeed is one `#mcpsecret` line: a secret the guest lacks, the
// servers that name it, and whether the laptop had its value.
type mcpNeed struct {
	Name       string
	Servers    []string
	FromLaptop bool
}

// parseMCP reads one of the MCP part's reply lines; false when tag is not
// one of them.
func (o *carryOutcome) parseMCP(tag, rest string) bool {
	switch tag {
	case "#mcpleft":
		o.MCPLeft = append(o.MCPLeft, rest)
	case "#mcpsecret":
		f := strings.Fields(rest)
		if len(f) < 2 {
			return true
		}
		o.MCPSecrets = append(o.MCPSecrets, mcpNeed{Name: f[0], Servers: strings.Split(f[1], ","), FromLaptop: len(f) > 2 && f[2] == "laptop"})
	case "#mcpcmd":
		f := strings.Fields(rest)
		if len(f) == 2 {
			o.MCPMissing = append(o.MCPMissing, [2]string{f[0], f[1]})
		}
	case "#mcpold":
		o.MCPOld = true
	default:
		return false
	}
	return true
}
