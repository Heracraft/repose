package mcpreg

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// applyCodex merges want into ~/.codex/config.toml. Values are judged from
// the parsed file; the text is edited by whole tables, from a
// `[mcp_servers.NAME]` header to the line before the next header outside
// `mcp_servers.NAME.*`, so comments and everything else stay as written.
// A name the file holds in another form (dotted keys, an inline table) is
// left alone with a warning. The edited text must parse to exactly the
// intended servers, or nothing is written.
func applyCodex(p Paths, want *Rendered, prev *agentRecord, warn func(string)) *agentRecord {
	path := filepath.Join(p.Home, ".codex", "config.toml")
	// A link (home-manager, a dotfiles repo) belongs to whatever made it:
	// the rename below would replace it with a file. agent-setup says so
	// once per start; agents/codex.json still records what Codex would get.
	if st, err := os.Lstat(path); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return prev
	}
	b, _, err := readFile(path)
	if err != nil {
		warn("cannot read ~/.codex/config.toml: " + err.Error())
		return prev
	}
	text := string(b)
	cur, err := codexServers(text)
	if err != nil {
		warn("~/.codex/config.toml does not parse; leaving it alone")
		return prev
	}
	changes, owned := plan(cur, want.User, prev.User, want.Retired)
	held := map[string]any{}
	expect := map[string]any{}
	for k, v := range cur {
		expect[k] = v
	}
	for _, c := range changes {
		var ok bool
		switch c.act {
		case actAdd:
			text = appendTable(text, c.name, c.want)
			ok = true
		case actReplace:
			text, ok = replaceTable(text, c.name, tomlTable(c.name, c.want))
		case actRemove:
			text, ok = replaceTable(text, c.name, "")
		}
		if !ok {
			// Warned once per value: agent-setup syncs before every start.
			if h, seen := prev.Held[c.name]; !seen || !equal(h, c.want) {
				warn(fmt.Sprintf("~/.codex/config.toml holds mcp_servers.%s in a form repose does not edit; leaving it alone", c.name))
			}
			held[c.name] = c.want
			if c.act != actAdd {
				// The old value stays, and stays repose's.
				owned[c.name] = cur[c.name]
			}
			continue
		}
		if c.act == actRemove {
			delete(expect, c.name)
		} else {
			expect[c.name] = c.want
		}
	}
	rec := &agentRecord{}
	if len(owned) > 0 {
		rec.User = owned
	}
	if len(held) > 0 {
		rec.Held = held
	}
	if text == string(b) {
		return rec
	}
	got, err := codexServers(text)
	if err != nil || !equal(got, expect) {
		warn("~/.codex/config.toml would not parse as intended after the edit; leaving it alone")
		return prev
	}
	if err := writeAtomic(path, []byte(text), 0o600); err != nil {
		warn("cannot write ~/.codex/config.toml: " + err.Error())
		return prev
	}
	return rec
}

// codexServers is the mcp_servers table of a Codex config.
func codexServers(text string) (map[string]any, error) {
	var doc map[string]any
	if _, err := toml.Decode(text, &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	switch ms := doc["mcp_servers"].(type) {
	case nil:
	case map[string]any:
		for k, v := range ms {
			out[k] = v
		}
	default:
		return nil, fmt.Errorf("mcp_servers is not a table")
	}
	return out, nil
}

var bareKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKeyRe.MatchString(k) {
		return k
	}
	return tomlString(k)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func tomlValue(v any) string {
	switch x := v.(type) {
	case string:
		return tomlString(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		return strconv.FormatBool(x)
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = tomlValue(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]any:
		keys := sortedKeys(x)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = tomlKey(k) + " = " + tomlValue(x[k])
		}
		return "{ " + strings.Join(parts, ", ") + " }"
	}
	return tomlString(fmt.Sprint(v))
}

// codexKeyOrder puts the keys a reader looks for first.
var codexKeyOrder = map[string]int{"command": 0, "args": 1, "url": 2, "bearer_token_env_var": 3, "http_headers": 4, "env_http_headers": 5, "startup_timeout_sec": 6}

func tomlTable(name string, v any) string {
	m, _ := v.(map[string]any)
	keys := sortedKeys(m)
	sort.SliceStable(keys, func(i, j int) bool {
		oi, iok := codexKeyOrder[keys[i]]
		oj, jok := codexKeyOrder[keys[j]]
		if !iok {
			oi = 100
		}
		if !jok {
			oj = 100
		}
		return oi < oj
	})
	var b strings.Builder
	fmt.Fprintf(&b, "[mcp_servers.%s]\n", tomlKey(name))
	for _, k := range keys {
		fmt.Fprintf(&b, "%s = %s\n", tomlKey(k), tomlValue(m[k]))
	}
	return b.String()
}

func appendTable(text, name string, v any) string {
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + tomlTable(name, v)
}

var headerRe = regexp.MustCompile(`^\s*\[`)

// tableHeaderRe matches `[mcp_servers.NAME]` and, with sub, a header of a
// subtable `[mcp_servers.NAME.x]`.
func tableHeaderRe(name string, sub bool) *regexp.Regexp {
	key := `(?:` + regexp.QuoteMeta(name) + `|"` + regexp.QuoteMeta(name) + `")`
	if sub {
		return regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*` + key + `\s*\.`)
	}
	return regexp.MustCompile(`^\s*\[\s*mcp_servers\s*\.\s*` + key + `\s*\]\s*(#.*)?$`)
}

// replaceTable replaces the table of name (header through its last
// non-blank, non-comment line) with repl; an empty repl removes it with
// the blank line before it. ok is false when no such header is found.
func replaceTable(text, name, repl string) (string, bool) {
	lines := strings.SplitAfter(text, "\n")
	head, sub := tableHeaderRe(name, false), tableHeaderRe(name, true)
	start := -1
	for i, l := range lines {
		if head.MatchString(strings.TrimRight(l, "\r\n")) {
			start = i
			break
		}
	}
	if start < 0 {
		return text, false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		l := strings.TrimRight(lines[i], "\r\n")
		if headerRe.MatchString(l) && !sub.MatchString(l) {
			end = i
			break
		}
	}
	// Blank lines and comments at the end belong to what follows.
	for end > start+1 {
		t := strings.TrimSpace(lines[end-1])
		if t == "" || strings.HasPrefix(t, "#") {
			end--
			continue
		}
		break
	}
	before := strings.Join(lines[:start], "")
	after := strings.Join(lines[end:], "")
	if repl == "" {
		if strings.HasSuffix(before, "\n\n") {
			before = before[:len(before)-1]
		} else if before == "" {
			after = strings.TrimLeft(after, "\n")
		}
		return before + after, true
	}
	if after != "" && !strings.HasSuffix(repl, "\n") {
		repl += "\n"
	}
	return before + repl + after, true
}
