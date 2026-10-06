package mcpreg

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Status is `repose-mcp status --json` (docs/interfaces/guest-conventions.md
// "MCP registry"): every MCP server an agent on the machine has, or that
// the registry holds and no agent got, read from the files alone.
type Status struct {
	Version int            `json:"version"`
	Agents  []string       `json:"agents"`
	Servers []ServerStatus `json:"servers"`
}

// ServerStatus is one row: a server name from one source.
type ServerStatus struct {
	Name     string            `json:"name"`
	From     string            `json:"from"`
	Agents   []string          `json:"agents"`
	State    string            `json:"state"`
	Needs    []string          `json:"needs,omitempty"`
	Missing  []string          `json:"missing,omitempty"`
	Skipped  map[string]string `json:"skipped,omitempty"`
	Checkout string            `json:"checkout,omitempty"`
}

// layer is where an agent's entry was found.
type found struct {
	name     string
	from     string
	checkout string
}

// ReadStatus reads every agent's config and the registry. It starts no
// server and writes nothing.
func ReadStatus(p Paths) (*Status, error) {
	reg, err := Load(p)
	if err != nil {
		return nil, err
	}
	rendered := readRendered(p)
	checkouts := Checkouts(p.Home)
	st := &Status{Version: 1, Agents: Agents}
	type key struct{ name, from, checkout string }
	rows := map[key]*ServerStatus{}
	row := func(k key) *ServerStatus {
		r := rows[k]
		if r == nil {
			r = &ServerStatus{Name: k.name, From: k.from, Checkout: k.checkout, Agents: []string{}}
			rows[k] = r
		}
		return r
	}
	wants := map[string]*Rendered{}
	for _, agent := range Agents {
		want := reg.Render(agent)
		wants[agent] = want
		prev := rendered.Agents[agent]
		if prev == nil {
			prev = &agentRecord{}
		}
		// fromOf attributes an entry of the agent's own files: repose's
		// while it equals what repose renders or rendered, else the user's.
		fromOf := func(name string, v any, checkout string) string {
			if checkout != "" {
				if w, ok := want.Projects[checkout][name]; ok && equal(v, w) {
					return FromLaptop
				}
				if w, ok := prev.Projects[checkout][name]; ok && equal(v, w) {
					return FromLaptop
				}
				return FromMachine
			}
			if w, ok := want.User[name]; ok && equal(v, w) {
				return want.From[name]
			}
			if w, ok := prev.User[name]; ok && equal(v, w) {
				if f, ok := want.From[name]; ok {
					return f
				}
				return FromLaptop
			}
			for _, r := range want.Retired[name] {
				if equal(v, r) {
					return FromRepose
				}
			}
			return FromMachine
		}
		for _, f := range agentEntries(p, agent, checkouts, fromOf) {
			r := row(key(f))
			if !contains(r.Agents, agent) {
				r.Agents = append(r.Agents, agent)
			}
		}
	}
	// Registry servers no agent has still get a row, so the reason shows.
	all, _ := reg.list(true)
	for _, l := range all {
		k := key{l.Name, l.From, ""}
		if l.Project != "" {
			k.checkout = l.Project
		}
		if _, ok := rows[k]; ok {
			continue
		}
		if l.From == FromRepose {
			// pi before its MCP release, or an agent the user turned it
			// off in: no row of its own unless someone has it.
			continue
		}
		row(k)
	}
	for _, s := range reg.Laptop.Skipped {
		if _, ok := reg.Forward[s.Name]; ok {
			// Left on the laptop and forwarded from there: the forward's
			// row says all of it.
			continue
		}
		r := row(key{s.Name, FromLaptop, ""})
		if r.State == "" {
			r.State = s.Reason
		}
	}
	laptopServer := func(name, checkout string) Server {
		if checkout != "" {
			return reg.Laptop.Projects[checkout][name]
		}
		s, _ := reg.LaptopServer(name)
		return s
	}
	for k, r := range rows {
		var parts []string
		if r.State != "" {
			parts = append(parts, r.State)
		}
		switch k.from {
		case FromLaptop:
			if s := laptopServer(k.name, k.checkout); s != nil {
				r.Needs = Needs(s, p.SecretsDir)
				if transport(s) == "stdio" {
					// Missing names the command as written: the expanded
					// form may hold a secret's value.
					raw := str(s, "command")
					cmd := Expand(raw, p.SecretsDir)
					if _, err := exec.LookPath(cmd); err != nil && cmd != "" {
						r.Missing = []string{raw}
					}
				}
			}
		case FromForward:
			if _, err := os.Stat(filepath.Join(p.SocketDir, k.name+".sock")); err != nil {
				parts = append(parts, "laptop not connected")
			}
		}
		if len(r.Needs) > 0 {
			parts = append(parts, "needs "+strings.Join(r.Needs, ", "))
		}
		for _, m := range r.Missing {
			parts = append(parts, m+" missing")
		}
		if k.from != FromMachine && k.from != FromProject {
			for _, agent := range Agents {
				if contains(r.Agents, agent) {
					continue
				}
				for _, s := range wants[agent].Skipped {
					if s.Name == k.name {
						if r.Skipped == nil {
							r.Skipped = map[string]string{}
						}
						r.Skipped[agent] = s.Reason
						parts = append(parts, agent+": "+s.Reason)
						break
					}
				}
			}
		}
		r.State = strings.Join(parts, "; ")
		sort.Slice(r.Agents, func(i, j int) bool { return agentRank(r.Agents[i]) < agentRank(r.Agents[j]) })
		st.Servers = append(st.Servers, *r)
	}
	sort.Slice(st.Servers, func(i, j int) bool {
		a, b := st.Servers[i], st.Servers[j]
		if fromRank(a.From) != fromRank(b.From) {
			return fromRank(a.From) < fromRank(b.From)
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Checkout < b.Checkout
	})
	if st.Servers == nil {
		st.Servers = []ServerStatus{}
	}
	return st, nil
}

func fromRank(f string) int {
	for i, x := range []string{FromRepose, FromLaptop, FromProject, FromForward, FromMachine} {
		if x == f {
			return i
		}
	}
	return 99
}

func agentRank(a string) int {
	for i, x := range Agents {
		if x == a {
			return i
		}
	}
	return 99
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// jsonServers reads key (a JSON object of servers) from a JSON file;
// anything unreadable is empty.
func jsonServers(path, key string) map[string]any {
	b, ok, _ := readFile(path)
	if !ok {
		return nil
	}
	if strings.HasSuffix(path, ".jsonc") {
		b = stripJSONC(b)
	}
	doc, err := parseObject(b)
	if err != nil {
		return nil
	}
	o, err := doc.child(key)
	if err != nil {
		return nil
	}
	return rawMap(o)
}

func decoded(v any) map[string]any {
	var m map[string]any
	switch x := v.(type) {
	case json.RawMessage:
		_ = json.Unmarshal(x, &m) // not an object: no fields
	case map[string]any:
		m = x
	}
	return m
}

func disabled(v any) bool {
	e, ok := decoded(v)["enabled"].(bool)
	return ok && !e
}

// agentEntries are the servers agent has, with where each came from. The
// precedence between layers is the agent's own (DECISIONS I-553, I-555).
func agentEntries(p Paths, agent string, checkouts []string, fromOf func(string, any, string) string) []found {
	var out []found
	home := p.Home
	switch agent {
	case "claude":
		for n, v := range jsonServers(filepath.Join(home, ".claude.json"), "mcpServers") {
			out = append(out, found{n, fromOf(n, v, ""), ""})
		}
		b, ok, _ := readFile(filepath.Join(home, ".claude.json"))
		var doc struct {
			Projects map[string]struct {
				MCPServers map[string]json.RawMessage `json:"mcpServers"`
			} `json:"projects"`
		}
		if ok && json.Unmarshal(b, &doc) == nil {
			for _, co := range checkouts {
				for n, v := range doc.Projects[co].MCPServers {
					out = append(out, found{n, fromOf(n, v, co), co})
				}
			}
		}
		for _, co := range checkouts {
			for n := range jsonServers(filepath.Join(co, ".mcp.json"), "mcpServers") {
				out = append(out, found{n, FromProject, co})
			}
		}
	case "codex":
		final := map[string]found{}
		if b, ok, _ := readFile(p.etc("etc/codex/config.toml")); ok {
			if sys, err := codexServers(string(b)); err == nil {
				for n := range sys {
					final[n] = found{n, FromRepose, ""}
				}
			}
		}
		if b, ok, _ := readFile(filepath.Join(home, ".codex", "config.toml")); ok {
			if user, err := codexServers(string(b)); err == nil {
				for n, v := range user {
					if disabled(v) {
						delete(final, n)
						continue
					}
					if _, sys := final[n]; sys && !hasAny(v, "command", "url") {
						continue // a field override of the platform entry
					}
					final[n] = found{n, fromOf(n, v, ""), ""}
				}
			}
		}
		out = appendFound(out, final)
	case "gemini":
		final := map[string]found{}
		var sys struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		_ = readJSON(p.etc("etc/gemini-cli/system-defaults.json"), &sys) // unreadable: no system servers
		for n := range sys.MCPServers {
			final[n] = found{n, FromRepose, ""}
		}
		exts, _ := os.ReadDir(filepath.Join(home, ".gemini", "extensions"))
		for _, e := range exts {
			for n, v := range jsonServers(filepath.Join(home, ".gemini", "extensions", e.Name(), "gemini-extension.json"), "mcpServers") {
				if e.Name() == GeminiExtension {
					final[n] = found{n, fromOf(n, v, ""), ""}
				} else {
					final[n] = found{n, FromMachine, ""}
				}
			}
		}
		settings := filepath.Join(home, ".gemini", "settings.json")
		for n := range jsonServers(settings, "mcpServers") {
			final[n] = found{n, FromMachine, ""}
		}
		var s struct {
			MCP struct {
				Excluded []string `json:"excluded"`
			} `json:"mcp"`
		}
		_ = readJSON(settings, &s) // unreadable: nothing excluded
		for _, n := range s.MCP.Excluded {
			delete(final, n)
		}
		out = appendFound(out, final)
	case "opencode":
		final := map[string]found{}
		off := map[string]bool{}
		for n, v := range jsonServers(p.etc("etc/opencode/opencode.json"), "mcp") {
			final[n] = found{n, FromRepose, ""}
			off[n] = off[n] || disabled(v)
		}
		dir := filepath.Join(home, ".config", "opencode")
		for n, v := range jsonServers(filepath.Join(dir, "config.json"), "mcp") {
			if disabled(v) {
				off[n] = true
			}
			if hasAny(v, "command", "url") {
				final[n] = found{n, fromOf(n, v, ""), ""}
			}
		}
		for _, f := range []string{"opencode.json", "opencode.jsonc"} {
			for n, v := range jsonServers(filepath.Join(dir, f), "mcp") {
				if disabled(v) {
					off[n] = true
				}
				if hasAny(v, "command", "url") {
					final[n] = found{n, FromMachine, ""}
				}
			}
		}
		for n := range off {
			if off[n] {
				delete(final, n)
			}
		}
		out = appendFound(out, final)
	case "pi":
		final := map[string]found{}
		if b, ok, _ := readFile(p.etc("etc/repose/pi-extension.js")); ok && strings.Contains(string(b), "registerMcpServer") {
			for n := range jsonServers(p.Platform, "mcpServers") {
				final[n] = found{n, FromRepose, ""}
			}
			for n, v := range jsonServers(p.agentFile("pi"), "mcpServers") {
				final[n] = found{n, fromOf(n, v, ""), ""}
			}
		}
		piDir := os.Getenv("PI_CODING_AGENT_DIR")
		if piDir == "" {
			piDir = filepath.Join(home, ".pi", "agent")
		}
		for n, v := range jsonServers(filepath.Join(piDir, "mcp.json"), "mcpServers") {
			if disabled(v) {
				delete(final, n)
				continue
			}
			final[n] = found{n, FromMachine, ""}
		}
		out = appendFound(out, final)
	}
	return out
}

func hasAny(v any, keys ...string) bool {
	m := decoded(v)
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func appendFound(out []found, m map[string]found) []found {
	for _, n := range sortedKeys(m) {
		out = append(out, m[n])
	}
	return out
}
